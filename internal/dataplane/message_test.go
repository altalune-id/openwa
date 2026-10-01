package dataplane_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/dataplane"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/testutil/fakes"
)

const project = "/api/v1/orgs/acme/projects/main"

// NOTE: adapts the package's shared send (write_test.go) to binary bodies and a content type.
func do(t *testing.T, h http.Handler, method, path, contentType string, body []byte, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	header := http.Header{}
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	for k, v := range hdr {
		header.Set(k, v)
	}
	return send(t, h, method, path, string(body), header)
}

func TestSendMessage_Returns202WithTheResource(t *testing.T) {
	e := newEnv(true)
	rec := do(t, e.handler(), http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", "application/json",
		[]byte(`{"to":"628111222333","text":"halo","mentions":["628111"],"reply_to":"3A5F"}`), nil)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "queued", got["status"])
	require.Equal(t, project+"/messages/"+got["id"].(string), got["url"], "R8: an id, a status and a URL")
	require.Equal(t, "halo", e.messages.sends[0].Text)
	require.Equal(t, "3A5F", e.messages.sends[0].ReplyTo)
	require.Regexp(t, `^msg_[A-Za-z0-9_-]{16}$`, got["id"], "the id is the public id, never the row UUID")
	require.Regexp(t, `^cht_`, got["chat_id"])

	rec = do(t, e.handler(), http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", "application/json",
		[]byte(`{"to":"628111222333","text":"quoted","reply_to":"`+got["id"].(string)+`"}`), nil)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	require.Equal(t, got["id"], e.messages.sends[1].ReplyTo, "a msg_ reply_to reaches the service, which resolves it in the project")
}

func TestSendMessage_Base64AndMultipartMedia(t *testing.T) {
	e := newEnv(true)
	h := e.handler()
	b64 := base64.StdEncoding.EncodeToString([]byte("jpeg"))
	rec := do(t, h, http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", "application/json",
		[]byte(`{"to":"628111222333","media":{"base64":"`+b64+`","mime":"image/jpeg","caption":"c"}}`), nil)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	require.Equal(t, []byte("jpeg"), e.messages.sends[0].Media.Bytes)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("to", "628111222333"))
	require.NoError(t, mw.WriteField("caption", "from form"))
	part, err := mw.CreatePart(map[string][]string{
		"Content-Disposition": {`form-data; name="file"; filename="a.pdf"`},
		"Content-Type":        {"application/pdf"},
	})
	require.NoError(t, err)
	_, _ = part.Write([]byte("%PDF"))
	require.NoError(t, mw.Close())
	rec = do(t, h, http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", mw.FormDataContentType(), buf.Bytes(), nil)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	sent := e.messages.sends[1].Media
	require.Equal(t, "a.pdf", sent.Filename)
	require.Equal(t, "application/pdf", sent.Mime)
	require.Equal(t, "from form", sent.Caption)
	require.Equal(t, []byte("%PDF"), sent.Bytes)

	rec = do(t, h, http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", "application/json",
		[]byte(`{"to":"628111222333","media":{"base64":"@@@"}}`), nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSendMessage_IdempotencyKeyReplaysTheFirstResponse(t *testing.T) {
	e := newEnv(true)
	h := e.handler()
	body := []byte(`{"to":"628111222333","text":"once"}`)
	hdr := map[string]string{"Idempotency-Key": "k1"}
	first := do(t, h, http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", "application/json", body, hdr)
	second := do(t, h, http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", "application/json", body, hdr)
	require.Equal(t, http.StatusAccepted, second.Code)
	require.Equal(t, first.Body.String(), second.Body.String())
	require.Len(t, e.messages.sends, 1, "the replay never reaches the service")
	conflict := do(t, h, http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", "application/json", []byte(`{"to":"628111222333","text":"other"}`), hdr)
	require.Equal(t, http.StatusConflict, conflict.Code)
}

func TestSendMessage_IdempotencyKeyIsPerDeviceAndSurvivesANewBoundary(t *testing.T) {
	e := newEnv(true)
	h := e.handler()
	hdr := map[string]string{"Idempotency-Key": "k2"}
	form := func() ([]byte, string) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		require.NoError(t, mw.WriteField("to", "628111222333"))
		part, err := mw.CreatePart(map[string][]string{
			"Content-Disposition": {`form-data; name="file"; filename="a.pdf"`},
			"Content-Type":        {"application/pdf"},
		})
		require.NoError(t, err)
		_, _ = part.Write([]byte("%PDF"))
		require.NoError(t, mw.Close())
		return buf.Bytes(), mw.FormDataContentType()
	}
	b1, ct1 := form()
	b2, ct2 := form()
	require.NotEqual(t, ct1, ct2, "each writer picks a random boundary")
	first := do(t, h, http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", ct1, b1, hdr)
	second := do(t, h, http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", ct2, b2, hdr)
	require.Equal(t, http.StatusAccepted, second.Code, second.Body.String())
	require.Equal(t, first.Body.String(), second.Body.String(), "the retry replays although its raw bytes differ")
	other := do(t, h, http.MethodPost, project+"/devices/"+e.deviceBPub+"/messages", ct1, b1, hdr)
	require.Equal(t, http.StatusAccepted, other.Code)
	require.NotEqual(t, first.Body.String(), other.Body.String(), "the same key on another device is another send")
	require.Len(t, e.messages.sends, 2)
}

func TestSendMessage_OversizeIs413AndMalformedIs400(t *testing.T) {
	e := newEnv(true)
	h := e.handler()
	big := `{"to":"628111222333","media":{"base64":"` + strings.Repeat("A", 4<<20) + `"}}`
	rec := do(t, h, http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", "application/json", []byte(big), nil)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	rec = do(t, h, http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", "multipart/form-data; boundary=x", []byte("not a form"), nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, "a broken form is malformed, not too large")
}

// SECURITY: a project-wide key reaches only its project's devices; a device id from another project is the masked 404 and never reaches a service.
func TestProjectWideKey_CannotUseAnotherProjectsDevice(t *testing.T) {
	e := newEnv(true)
	h := e.handler()
	foreign := e.scoped.sibling.PublicID
	for _, rt := range []struct{ method, path, body string }{
		{http.MethodPost, "/devices/" + foreign + "/messages", `{"to":"628111222333","text":"x"}`},
		{http.MethodGet, "/devices/" + foreign + "/messages", ""},
		{http.MethodPost, "/devices/" + uuid.NewString() + "/messages", `{"to":"628111222333","text":"x"}`},
	} {
		rec := do(t, h, rt.method, project+rt.path, "application/json", []byte(rt.body), nil)
		require.Equal(t, http.StatusNotFound, rec.Code, "%s %s", rt.method, rt.path)
	}
	require.Equal(t, 2, e.scoped.siblingHits, "the sibling's well-formed id reached the project-scoped lookup and was refused there")
	require.Empty(t, e.messages.sends)
}

func TestMalformedPublicIDs_Are404WithoutALookup(t *testing.T) {
	e := newEnv(true)
	h := e.handler()
	for _, path := range []string{
		"/messages/" + uuid.NewString(), "/messages/cht_Bd7Kq2Wm9Xp4Lz3a", "/messages/msg_short",
		"/chats/" + uuid.NewString(), "/chats/msg_Hy3Rk8Pw2Nq5Tv7a",
	} {
		rec := do(t, h, http.MethodGet, project+path, "", nil, nil)
		require.Equal(t, http.StatusNotFound, rec.Code, path)
		require.NotRegexp(t, `[0-9a-f]{8}-[0-9a-f]{4}-`, rec.Body.String(), "SECURITY: a 404 body names no UUID")
	}
	require.Zero(t, e.messages.resolves, "a malformed id never reaches the store")
}

// SECURITY: a key bound to device B cannot send through, read from or fetch media of device A; each denial is the masked 404.
func TestDeviceBoundKey_IsConfinedToItsDevice(t *testing.T) {
	e := newEnv(true)
	msg := dataplane.MessageRef{ID: uuid.New(), PublicID: fakes.MessagePublicID(), DeviceID: e.deviceA, Type: "image", Status: "received", Mentions: []string{}, Media: &dataplane.MediaRef{Mime: "image/jpeg"}}
	e.messages.seed(msg)
	e.authz.resources = []uuid.UUID{e.deviceB}
	h := e.handler()
	for _, rt := range []struct{ method, path, body string }{
		{http.MethodPost, "/devices/" + e.deviceAPub + "/messages", `{"to":"628111222333","text":"x"}`},
		{http.MethodGet, "/devices/" + e.deviceAPub + "/messages", ""},
		{http.MethodGet, "/messages/" + msg.PublicID, ""},
		{http.MethodGet, "/messages/" + msg.PublicID + "/media", ""},
		{http.MethodPost, "/messages/" + msg.PublicID + "/reactions", `{"emoji":"👍"}`},
		{http.MethodPost, "/messages/" + msg.PublicID + "/read", ""},
		{http.MethodPost, "/messages/" + msg.PublicID + "/revoke", ""},
		{http.MethodPatch, "/messages/" + msg.PublicID, `{"text":"edited"}`},
	} {
		rec := do(t, h, rt.method, project+rt.path, "application/json", []byte(rt.body), nil)
		require.Equal(t, http.StatusNotFound, rec.Code, "%s %s", rt.method, rt.path)
	}
	require.Empty(t, e.messages.sends)

	e.authz.resources = []uuid.UUID{e.deviceA}
	rec := do(t, e.handler(), http.MethodGet, project+"/messages/"+msg.PublicID, "", nil, nil)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestGetMedia_ServesHeadersAnd410(t *testing.T) {
	e := newEnv(true)
	e.messages.media = []byte("jpeg-bytes")
	msg := dataplane.MessageRef{ID: uuid.New(), PublicID: fakes.MessagePublicID(), DeviceID: e.deviceA, Type: "image", Mentions: []string{}, Media: &dataplane.MediaRef{Mime: "image/jpeg"}}
	e.messages.seed(msg)
	h := e.handler()
	rec := do(t, h, http.MethodGet, project+"/messages/"+msg.PublicID+"/media", "", nil, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "image/jpeg", rec.Header().Get("Content-Type"))
	require.Equal(t, "10", rec.Header().Get("Content-Length"))
	require.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
	require.True(t, strings.HasPrefix(rec.Header().Get("Content-Disposition"), "inline"))
	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "sandbox", rec.Header().Get("Content-Security-Policy"))
	require.Equal(t, "jpeg-bytes", rec.Body.String())

	svg := dataplane.MessageRef{ID: uuid.New(), PublicID: fakes.MessagePublicID(), DeviceID: e.deviceA, Type: "image", Mentions: []string{}, Media: &dataplane.MediaRef{Mime: "image/svg+xml"}}
	e.messages.seed(svg)
	rec = do(t, e.handler(), http.MethodGet, project+"/messages/"+svg.PublicID+"/media", "", nil, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment"), "SECURITY: an SVG never renders inline")

	e.messages.gone = true
	rec = do(t, e.handler(), http.MethodGet, project+"/messages/"+msg.PublicID+"/media", "", nil, nil)
	require.Equal(t, http.StatusGone, rec.Code)
	require.Contains(t, rec.Body.String(), `"code":"gone"`)
}

func TestReactRevokeEdit_AnswerAccepted(t *testing.T) {
	e := newEnv(true)
	msg := dataplane.MessageRef{ID: uuid.New(), PublicID: fakes.MessagePublicID(), DeviceID: e.deviceA, WAID: "3EB0A", Mentions: []string{}}
	e.messages.seed(msg)
	h := e.handler()
	for _, rt := range []struct{ method, suffix, body, typ string }{
		{http.MethodPost, "/reactions", `{"emoji":"👍"}`, "reaction"},
		{http.MethodPost, "/revoke", ``, "revoke"},
		{http.MethodPatch, "", `{"text":"fixed"}`, "edit"},
	} {
		rec := do(t, h, rt.method, project+"/messages/"+msg.PublicID+rt.suffix, "application/json", []byte(rt.body), nil)
		require.Equal(t, http.StatusAccepted, rec.Code, rt.typ)
		require.Contains(t, rec.Body.String(), `"type":"`+rt.typ+`"`)
	}
	rec := do(t, h, http.MethodPost, project+"/messages/"+msg.PublicID+"/read", "", nil, nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, []uuid.UUID{msg.ChatID}, e.messages.reads)
}

func TestListMessages_EnvelopeAndBadCursor(t *testing.T) {
	e := newEnv(true)
	e.messages.seed(dataplane.MessageRef{ID: uuid.New(), PublicID: fakes.MessagePublicID(), DeviceID: e.deviceA, Mentions: []string{}})
	h := e.handler()
	rec := do(t, h, http.MethodGet, project+"/devices/"+e.deviceAPub+"/messages", "", nil, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var env struct {
		Data       []map[string]any `json:"data"`
		NextCursor string           `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	require.Len(t, env.Data, 1)
	rec = do(t, h, http.MethodGet, project+"/devices/"+e.deviceAPub+"/messages?cursor=garbage", "", nil, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	rec = do(t, h, http.MethodGet, project+"/devices/"+e.deviceAPub+"/messages?since_id=nope", "", nil, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// SECURITY: a malformed recipient is refused at the S3 edge and never reaches the engine.
func TestSendMessage_MalformedRecipientIs400BeforeTheService(t *testing.T) {
	e := newEnv(true)
	h := e.handler()
	for _, to := range []string{"x", "", "0812345", "12@evil.example", "@s.whatsapp.net", "62811 abc"} {
		body, err := json.Marshal(map[string]string{"to": to, "text": "hi"})
		require.NoError(t, err)
		rec := do(t, h, http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", "application/json", body, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code, "to=%q", to)
	}
	require.Empty(t, e.messages.sends)
	for _, to := range []string{"628111222333", "+62 811-1222-333", "628111@s.whatsapp.net", "1203@g.us", "99@lid"} {
		body, err := json.Marshal(map[string]string{"to": to, "text": "hi"})
		require.NoError(t, err)
		rec := do(t, h, http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", "application/json", body, nil)
		require.Equal(t, http.StatusAccepted, rec.Code, "to=%q", to)
	}
}

func TestSendMessage_MalformedRecipientInMultipartIs400(t *testing.T) {
	e := newEnv(true)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("to", "x"))
	require.NoError(t, mw.Close())
	rec := do(t, e.handler(), http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", mw.FormDataContentType(), buf.Bytes(), nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Empty(t, e.messages.sends)
}

func TestGetMedia_OversizeIs413AndStillCarriesNoSniffHeaders(t *testing.T) {
	e := newEnv(true)
	msg := dataplane.MessageRef{ID: uuid.New(), PublicID: fakes.MessagePublicID(), DeviceID: e.deviceA, Type: "image", Mentions: []string{}, Media: &dataplane.MediaRef{Mime: "image/jpeg"}}
	e.messages.seed(msg)
	e.messages.mediaErr = &message.MediaTooLargeError{Size: 9 << 20, Max: 1 << 20}
	rec := do(t, e.handler(), http.MethodGet, project+"/messages/"+msg.PublicID+"/media", "", nil, nil)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
}

func TestGetMedia_InlineOnlyForRasterImages(t *testing.T) {
	e := newEnv(true)
	e.messages.media = []byte("x")
	h := e.handler()
	for mimeType, inline := range map[string]bool{"image/png": true, "image/webp": true, "image/gif": true, "text/html": false, "application/pdf": false, "image/svg+xml": false} {
		m := dataplane.MessageRef{ID: uuid.New(), PublicID: fakes.MessagePublicID(), DeviceID: e.deviceA, Mentions: []string{}, Media: &dataplane.MediaRef{Mime: mimeType}}
		e.messages.seed(m)
		rec := do(t, h, http.MethodGet, project+"/messages/"+m.PublicID+"/media", "", nil, nil)
		require.Equal(t, http.StatusOK, rec.Code, mimeType)
		require.Equal(t, inline, strings.HasPrefix(rec.Header().Get("Content-Disposition"), "inline"), mimeType)
		require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"), mimeType)
		require.Equal(t, "sandbox", rec.Header().Get("Content-Security-Policy"), mimeType)
	}
}

// SECURITY: a device-bound key naming another device's chat or message gets the same 404 as an id that does not exist.
func TestListMessages_BoundKeyCannotProbeSiblingDeviceIDs(t *testing.T) {
	e := newEnv(true)
	sibChat := dataplane.ChatRef{ID: uuid.New(), PublicID: fakes.ChatPublicID(), DeviceID: e.deviceB, JID: "b@s.whatsapp.net", Kind: "dm"}
	e.chats.rows[sibChat.ID] = sibChat
	sibMsg := dataplane.MessageRef{ID: uuid.New(), PublicID: fakes.MessagePublicID(), DeviceID: e.deviceB, Mentions: []string{}}
	e.messages.seed(sibMsg)
	e.authz.resources = []uuid.UUID{e.deviceA}
	h := e.handler()
	list := project + "/devices/" + e.deviceAPub + "/messages"
	for _, q := range []string{
		"chat_id=" + sibChat.PublicID, "chat_id=" + fakes.ChatPublicID(),
		"since_id=" + sibMsg.PublicID, "since_id=" + fakes.MessagePublicID(),
	} {
		rec := do(t, h, http.MethodGet, list+"?"+q, "", nil, nil)
		require.Equal(t, http.StatusNotFound, rec.Code, q)
		require.JSONEq(t, `{"code":"not_found","message":"not_found"}`, rec.Body.String(), q)
	}
	rec := do(t, h, http.MethodGet, project+"/chats/"+sibChat.PublicID+"/messages", "", nil, nil)
	require.Equal(t, http.StatusNotFound, rec.Code)

	own := dataplane.MessageRef{ID: uuid.New(), PublicID: fakes.MessagePublicID(), DeviceID: e.deviceA, Mentions: []string{}}
	e.messages.seed(own)
	rec = do(t, h, http.MethodGet, list+"?since_id="+own.PublicID, "", nil, nil)
	require.Equal(t, http.StatusOK, rec.Code, "an id on the key's own device still resolves")
}

func TestSendMessage_FailedSendReleasesTheKeyForARetry(t *testing.T) {
	e := newEnv(true)
	h := e.handler()
	e.messages.sendErr = &message.DeviceNotLinkedError{DeviceID: "x"}
	hdr := map[string]string{"Idempotency-Key": "k3"}
	body := []byte(`{"to":"628111222333","text":"retry"}`)
	first := do(t, h, http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", "application/json", body, hdr)
	require.Equal(t, http.StatusConflict, first.Code)
	second := do(t, h, http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", "application/json", body, hdr)
	require.Equal(t, http.StatusAccepted, second.Code, "a failed send is not remembered, so the retry runs")
}

func TestSendMessage_ConcurrentSameKeyIsInProgress(t *testing.T) {
	e := newEnv(true)
	e.messages.gate, e.messages.entered = make(chan struct{}), make(chan struct{}, 1)
	h := e.handler()
	hdr := map[string]string{"Idempotency-Key": "k4"}
	body := []byte(`{"to":"628111222333","text":"slow"}`)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- do(t, h, http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", "application/json", body, hdr)
	}()
	<-e.messages.entered
	rec := do(t, h, http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", "application/json", body, hdr)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), `"code":"in_progress"`)
	close(e.messages.gate)
	require.Equal(t, http.StatusAccepted, (<-done).Code)
}

func TestSendMessage_ConcurrencyIsBounded(t *testing.T) {
	e := newEnv(true)
	e.messages.gate, e.messages.entered = make(chan struct{}), make(chan struct{}, 8)
	const total, limit = 6, 2
	e.sendLimit = limit
	h := e.handler()
	var wg sync.WaitGroup
	codes := make(chan int, total)
	for i := range total {
		wg.Go(func() {
			b := []byte(fmt.Sprintf(`{"to":"628111222333","text":"n%d"}`, i))
			codes <- do(t, h, http.MethodPost, project+"/devices/"+e.deviceAPub+"/messages", "application/json", b, nil).Code
		})
	}
	for range limit {
		<-e.messages.entered
	}
	require.Never(t, func() bool { return len(e.messages.entered) > 0 }, 150*time.Millisecond, 10*time.Millisecond, "a send beyond the bound waits for a slot")
	close(e.messages.gate)
	wg.Wait()
	close(codes)
	for c := range codes {
		require.Equal(t, http.StatusAccepted, c)
	}
	require.LessOrEqual(t, e.messages.maxActive, limit)
	require.Len(t, e.messages.sends, total)
}

// NOTE: a read without a credential is the masked 404 (the route needs a key), while device and write routes answer 401.
func TestReads_WithoutACredentialAreMasked404(t *testing.T) {
	e := newEnv(true)
	m := dataplane.MessageRef{ID: uuid.New(), PublicID: fakes.MessagePublicID(), DeviceID: e.deviceA, Mentions: []string{}}
	e.messages.seed(m)
	h := e.handler()
	for _, path := range []string{"/messages/" + m.PublicID, "/messages/" + m.PublicID + "/media", "/chats", "/contacts"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, project+path, nil))
		require.Equal(t, http.StatusNotFound, rec.Code, path)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, project+"/devices/"+e.deviceAPub+"/messages", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
