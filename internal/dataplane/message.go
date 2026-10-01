package dataplane

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/platform/authn"
	"altalune.id/openwa/internal/platform/publicid"
	"altalune.id/openwa/internal/platform/session"
)

type sendRequest struct {
	To    string `json:"to"`
	Text  string `json:"text"`
	Media *struct {
		URL      string `json:"url"`
		Base64   string `json:"base64"`
		Mime     string `json:"mime"`
		Filename string `json:"filename"`
		Caption  string `json:"caption"`
		Voice    bool   `json:"voice"`
	} `json:"media"`
	Location *struct {
		Lat     float64 `json:"lat"`
		Lng     float64 `json:"lng"`
		Name    string  `json:"name"`
		Address string  `json:"address"`
	} `json:"location"`
	ReplyTo       string   `json:"reply_to"`
	Mentions      []string `json:"mentions"`
	MarkReadFirst bool     `json:"mark_read_first"`
}

// SECURITY: plan 03's beginDevice (credential, public-id shape, scope, project-scoped Resolve, resource list) runs before the body is read, so a bad key or a foreign device costs one 404 and no upload.
func (h *Handler) sendMessage(w http.ResponseWriter, r *http.Request) {
	req, dev, ok := h.beginDevice(w, r, authn.ScopeMessagesWrite)
	if !ok {
		return
	}
	deviceID := dev.ID
	// SECURITY: a bounded number of sends hold their decoded media at once, so concurrent large uploads cannot exhaust memory.
	select {
	case h.sendSlot <- struct{}{}:
		defer func() { <-h.sendSlot }()
	case <-r.Context().Done():
		h.fail(w, r, &UnavailableError{})
		return
	}
	in, err := h.decodeSend(w, r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// SECURITY: the recipient is validated here, so a bare string ParseJID would accept never reaches the engine.
	if err = message.ValidateRecipient(in.To); err != nil {
		h.fail(w, r, err)
		return
	}
	fp, err := fingerprint(in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// NOTE: namespaced per device (D25).
	key := idempotencyKeyOf(r, "messages:"+dev.PublicID+":")
	if key != "" {
		rec, reserveErr := h.idem.reserve(req.scope.projectID, key, fp)
		if reserveErr != nil {
			h.fail(w, r, reserveErr)
			return
		}
		if rec != nil {
			writeBytes(w, rec.resp.status, rec.resp.body)
			return
		}
		// NOTE: releases the reservation unless store() completed it, so a failed send is retryable.
		defer h.idem.release(req.scope.projectID, key)
	}
	m, err := h.messages.Send(req.ctx, deviceID, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	body, err := json.Marshal(h.messageViewOf(req, m))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if key != "" {
		h.idem.store(req.scope.projectID, key, fp, idempotencyResponse{status: http.StatusAccepted, body: body})
	}
	writeBytes(w, http.StatusAccepted, body)
}

const multipartMemory = 1 << 20

func (h *Handler) decodeSend(w http.ResponseWriter, r *http.Request) (SendInput, error) {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxSend)
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType == "multipart/form-data" {
		return h.decodeMultipart(r)
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return SendInput{}, bodyError(err)
	}
	var body sendRequest
	if unmarshalErr := json.Unmarshal(raw, &body); unmarshalErr != nil {
		return SendInput{}, &BadRequestError{}
	}
	in := SendInput{To: body.To, Text: body.Text, ReplyTo: body.ReplyTo, Mentions: body.Mentions, MarkReadFirst: body.MarkReadFirst}
	if body.Media != nil {
		media := &SendMedia{URL: body.Media.URL, Mime: body.Media.Mime, Filename: body.Media.Filename, Caption: body.Media.Caption, Voice: body.Media.Voice}
		if body.Media.Base64 != "" {
			decoded, decErr := base64.StdEncoding.DecodeString(body.Media.Base64)
			if decErr != nil {
				return SendInput{}, &BadRequestError{}
			}
			media.Bytes = decoded
		}
		in.Media = media
	}
	if body.Location != nil {
		in.Location = &LocationRef{Lat: body.Location.Lat, Lng: body.Location.Lng, Name: body.Location.Name, Address: body.Location.Address}
	}
	return in, nil
}

// NOTE: only a body over the limit is 413; every other read or parse failure is a malformed request.
func bodyError(err error) error {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return &PayloadTooLargeError{}
	}
	return &BadRequestError{}
}

func (h *Handler) decodeMultipart(r *http.Request) (SendInput, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return SendInput{}, &BadRequestError{}
	}
	form, err := mr.ReadForm(multipartMemory)
	if err != nil {
		return SendInput{}, bodyError(err)
	}
	defer func() { _ = form.RemoveAll() }()
	first := func(k string) string {
		if v := form.Value[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	in := SendInput{To: first("to"), Text: first("text"), ReplyTo: first("reply_to"), Mentions: form.Value["mentions"], MarkReadFirst: first("mark_read_first") == "true"}
	files := form.File["file"]
	if len(files) == 0 {
		return in, nil
	}
	fh := files[0]
	f, err := fh.Open()
	if err != nil {
		return SendInput{}, &BadRequestError{}
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		return SendInput{}, &BadRequestError{}
	}
	partMime, _, mimeErr := mime.ParseMediaType(fh.Header.Get("Content-Type"))
	if mimeErr != nil {
		partMime = ""
	}
	in.Media = &SendMedia{Bytes: data, Mime: partMime, Filename: fh.Filename, Caption: first("caption"), Voice: first("voice") == "true"}
	return in, nil
}

type sendFingerprint struct {
	To            string       `json:"to"`
	Text          string       `json:"text"`
	ReplyTo       string       `json:"reply_to"`
	Mentions      []string     `json:"mentions"`
	MarkReadFirst bool         `json:"mark_read_first"`
	Location      *LocationRef `json:"location"`
	MediaURL      string       `json:"media_url"`
	MediaMime     string       `json:"media_mime"`
	MediaName     string       `json:"media_filename"`
	MediaCaption  string       `json:"media_caption"`
	MediaVoice    bool         `json:"media_voice"`
	MediaSHA256   string       `json:"media_sha256"`
}

// NOTE: compared instead of the raw body, so JSON and multipart retries match (D25).
func fingerprint(in SendInput) ([]byte, error) {
	fp := sendFingerprint{To: in.To, Text: in.Text, ReplyTo: in.ReplyTo, Mentions: in.Mentions, MarkReadFirst: in.MarkReadFirst, Location: in.Location}
	if in.Media != nil {
		sum := sha256.Sum256(in.Media.Bytes)
		fp.MediaURL, fp.MediaMime, fp.MediaName, fp.MediaCaption, fp.MediaVoice = in.Media.URL, in.Media.Mime, in.Media.Filename, in.Media.Caption, in.Media.Voice
		fp.MediaSHA256 = hex.EncodeToString(sum[:])
	}
	return json.Marshal(fp)
}

func (h *Handler) listDeviceMessages(w http.ResponseWriter, r *http.Request) {
	req, dev, ok := h.beginDevice(w, r, authn.ScopeMessagesRead)
	if !ok {
		return
	}
	deviceID := dev.ID
	p, err := h.authorizeScope(req, authn.ScopeMessagesRead)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	opts, err := h.messageListOpts(req, r, p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	opts.DeviceID = &deviceID
	h.writeMessages(w, r, req, opts)
}

func (h *Handler) listChatMessages(w http.ResponseWriter, r *http.Request) {
	req, c, ok := h.chatFor(w, r, authn.ScopeMessagesRead, false)
	if !ok {
		return
	}
	p, err := h.authorizeScope(req, authn.ScopeMessagesRead)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	opts, err := h.messageListOpts(req, r, p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	opts.ChatID = &c.ID
	h.writeMessages(w, r, req, opts)
}

func (h *Handler) writeMessages(w http.ResponseWriter, r *http.Request, req request, opts MessageListOpts) {
	items, next, err := h.messages.List(req.ctx, opts)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := listEnvelope[messageView]{Data: make([]messageView, 0, len(items)), NextCursor: next}
	for _, m := range items {
		out.Data = append(out.Data, h.messageViewOf(req, m))
	}
	h.writeJSON(w, r, out)
}

// SECURITY: chat_id and since_id are resolved in the project and then reach-checked on their owning device; an unknown id and an unreachable one are the same masked 404. A malformed id is a 400.
func (h *Handler) messageListOpts(req request, r *http.Request, p session.Principal) (MessageListOpts, error) {
	q := r.URL.Query()
	opts := MessageListOpts{Cursor: q.Get("cursor")}
	if v := q.Get("chat_id"); v != "" {
		if !publicid.Valid(chat.PublicIDPrefix, v) {
			return MessageListOpts{}, &BadRequestError{}
		}
		c, err := h.chats.Resolve(req.ctx, v)
		if err != nil {
			return MessageListOpts{}, err
		}
		if !p.ReachesResource(req.scope.orgID, req.scope.projectID, c.DeviceID) {
			return MessageListOpts{}, &NotFoundError{}
		}
		opts.ChatID = &c.ID
	}
	if v := q.Get("since_id"); v != "" {
		if !publicid.Valid(message.PublicIDPrefix, v) {
			return MessageListOpts{}, &BadRequestError{}
		}
		m, err := h.messages.Resolve(req.ctx, v)
		if err != nil {
			return MessageListOpts{}, err
		}
		if !p.ReachesResource(req.scope.orgID, req.scope.projectID, m.DeviceID) {
			return MessageListOpts{}, &NotFoundError{}
		}
		opts.SinceID = &m.ID
	}
	limit, err := limitOf(q.Get("limit"))
	if err != nil {
		return MessageListOpts{}, err
	}
	opts.Limit = limit
	return opts, nil
}

func limitOf(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, &BadRequestError{}
	}
	return n, nil
}

// SECURITY: the same order as beginDevice: shape, then the key's scope, then the project-scoped read, then the reach check on the owning device; every refusal is the masked 404.
func (h *Handler) messageFor(w http.ResponseWriter, r *http.Request, scopeName string, write bool) (request, MessageRef, bool) {
	begin := h.begin
	if write {
		begin = h.beginWrite
	}
	req, ok := begin(w, r)
	if !ok {
		return request{}, MessageRef{}, false
	}
	raw := r.PathValue("message")
	if !publicid.Valid(message.PublicIDPrefix, raw) {
		h.fail(w, r, &NotFoundError{})
		return request{}, MessageRef{}, false
	}
	p, err := h.authorizeScope(req, scopeName)
	if err != nil {
		h.fail(w, r, err)
		return request{}, MessageRef{}, false
	}
	m, err := h.messages.Resolve(req.ctx, raw)
	if err != nil {
		h.fail(w, r, err)
		return request{}, MessageRef{}, false
	}
	// SECURITY: reach is checked on the OWNING device id (S2), never the message's own id, so a device-bound key sees only its devices' messages.
	if !p.ReachesResource(req.scope.orgID, req.scope.projectID, m.DeviceID) {
		h.fail(w, r, &NotFoundError{})
		return request{}, MessageRef{}, false
	}
	return req, m, true
}

func (h *Handler) getMessage(w http.ResponseWriter, r *http.Request) {
	req, m, ok := h.messageFor(w, r, authn.ScopeMessagesRead, false)
	if !ok {
		return
	}
	h.writeJSON(w, r, h.messageViewOf(req, m))
}

func (h *Handler) getMedia(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	req, m, ok := h.messageFor(w, r, authn.ScopeMessagesRead, false)
	if !ok {
		return
	}
	f, mimeType, name, err := h.messages.OpenMedia(req.ctx, m.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer func() { _ = f.Close() }()
	// SECURITY: the sender chose the mime type; only raster images render inline, nothing is sniffed, and a sandbox CSP keeps any active content inert.
	disposition := "attachment"
	if message.InlineSafe(mimeType) {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, name, time.Time{}, f)
}

func (h *Handler) reactMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Emoji string `json:"emoji"`
	}
	h.childWrite(w, r, &body, func(req request, m MessageRef) (MessageRef, error) {
		return h.messages.React(req.ctx, m.ID, body.Emoji)
	})
}

func (h *Handler) revokeMessage(w http.ResponseWriter, r *http.Request) {
	h.childWrite(w, r, nil, func(req request, m MessageRef) (MessageRef, error) { return h.messages.Revoke(req.ctx, m.ID) })
}

func (h *Handler) editMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	h.childWrite(w, r, &body, func(req request, m MessageRef) (MessageRef, error) { return h.messages.Edit(req.ctx, m.ID, body.Text) })
}

func (h *Handler) childWrite(w http.ResponseWriter, r *http.Request, body any, do func(request, MessageRef) (MessageRef, error)) {
	req, m, ok := h.messageFor(w, r, authn.ScopeMessagesWrite, true)
	if !ok {
		return
	}
	if body != nil {
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
		if err != nil || len(raw) > maxRequestBytes || json.Unmarshal(raw, body) != nil {
			h.fail(w, r, &BadRequestError{})
			return
		}
	}
	child, err := do(req, m)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out, err := json.Marshal(h.messageViewOf(req, child))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeBytes(w, http.StatusAccepted, out)
}

func (h *Handler) readMessage(w http.ResponseWriter, r *http.Request) {
	req, m, ok := h.messageFor(w, r, authn.ScopeMessagesWrite, true)
	if !ok {
		return
	}
	if err := h.messages.MarkRead(req.ctx, m.ChatID); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
