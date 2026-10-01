package handlers

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/platform/keyset"
	"altalune.id/openwa/internal/project"
	"altalune.id/openwa/internal/web"
	"altalune.id/openwa/internal/web/templates"
)

const (
	threadPage      = 50
	multipartMemory = 1 << 20
	pollOverlap     = 5 * time.Second
)

// InboxHandler owns the project-scoped inbox pages and fragments.
type InboxHandler struct {
	Deps
	Chats    *chat.Service
	Messages *message.Service
	Devices  *device.Service
	MaxMedia int64
	// Clock stamps the poll watermark; NewInboxHandler sets time.Now.
	Clock func() time.Time
}

// NewInboxHandler wires the handler.
func NewInboxHandler(d Deps, projects *project.Service, chats *chat.Service, messages *message.Service, devices *device.Service, maxMedia int64) *InboxHandler {
	d.Projects = projects
	return &InboxHandler{Deps: d, Chats: chats, Messages: messages, Devices: devices, MaxMedia: maxMedia, Clock: time.Now}
}

// Register wires the inbox routes onto mux.
func (h *InboxHandler) Register(mux web.Mux) {
	const base = "/orgs/{org}/projects/{project}/inbox"
	mux.HandleFunc("GET "+base, h.GetInbox)
	mux.HandleFunc("GET "+base+"/chats", h.GetChatList)
	mux.HandleFunc("GET "+base+"/chats/{chat}", h.GetThread)
	mux.HandleFunc("GET "+base+"/chats/{chat}/messages", h.GetThreadMessages)
	mux.HandleFunc("POST "+base+"/chats/{chat}/messages", h.PostMessage)
	mux.HandleFunc("POST "+base+"/chats/{chat}/read", h.PostRead)
	mux.HandleFunc("GET "+base+"/messages/{message}/media", h.GetMedia)
	mux.HandleFunc("POST "+base+"/messages/{message}/reactions", h.PostReaction)
	mux.HandleFunc("POST "+base+"/messages/{message}/revoke", h.PostRevoke)
	mux.HandleFunc("GET "+base+"/new", h.GetNewChat)
	mux.HandleFunc("POST "+base+"/new", h.PostNewChat)
}

// GetInbox renders the inbox page with the chat list and an empty detail pane.
func (h *InboxHandler) GetInbox(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	v, err := h.inboxView(sc, r, nil)
	if err != nil {
		h.LogErr("web inbox: list", err)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return
	}
	Render(w, sc.req, templates.InboxLayout(h.layout(sc), v))
}

// GetChatList returns the chat list panel, or only its rows when rows=1 (the morphing poll).
func (h *InboxHandler) GetChatList(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	list, _, err := h.chatList(sc, r)
	if err != nil {
		h.LogErr("web inbox: list", err)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return
	}
	base := h.ProjectFragmentBase(sc)
	if r.URL.Query().Get("rows") == "1" {
		Render(w, sc.req, templates.ChatListRows(base, list))
		return
	}
	Render(w, sc.req, templates.ChatListPanel(base, list))
}

// GetThread returns the thread pane for htmx, or the whole inbox with the thread open.
func (h *InboxHandler) GetThread(w http.ResponseWriter, r *http.Request) {
	sc, c, ok := h.requireChat(w, r)
	if !ok {
		return
	}
	tv, err := h.threadView(sc, c)
	if err != nil {
		h.LogErr("web inbox: thread", err)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return
	}
	if web.IsHTMXRequest(r) {
		list, _, listErr := h.chatList(sc, r)
		if listErr != nil {
			h.LogErr("web inbox: list", listErr)
			Render(w, sc.req, templates.ThreadPane(h.ProjectFragmentBase(sc), tv))
			return
		}
		Render(w, sc.req, templates.ThreadOpened(h.ProjectFragmentBase(sc), tv, markActive(list, c.PublicID)))
		return
	}
	v, err := h.inboxView(sc, r, &tv)
	if err != nil {
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return
	}
	v.List = markActive(v.List, c.PublicID)
	Render(w, sc.req, templates.InboxLayout(h.layout(sc), v))
}

// NOTE: the opened chat's row is highlighted, and the panel's poll URL carries it, so the next morph keeps it.
func markActive(list templates.ChatListView, id string) templates.ChatListView {
	list.ActiveID = id
	for i := range list.Rows {
		list.Rows[i].Active = list.Rows[i].ID == id
	}
	return list
}

// GetThreadMessages serves the poll (since=) and the history page (before=).
func (h *InboxHandler) GetThreadMessages(w http.ResponseWriter, r *http.Request) {
	sc, c, ok := h.requireChat(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	base := h.ProjectFragmentBase(sc)
	tv := h.threadShell(sc, c)
	if raw := q.Get("since"); raw != "" {
		_, sinceID, err := keyset.Decode(keyset.Cursor(raw))
		if err != nil {
			h.ErrorPageKey(w, sc.req, http.StatusBadRequest, "error.bad_request", err)
			return
		}
		wm, err := time.Parse(time.RFC3339Nano, q.Get("updated_after"))
		if err != nil {
			h.ErrorPageKey(w, sc.req, http.StatusBadRequest, "error.bad_request", err)
			return
		}
		queriedAt := h.Clock()
		ctx := sc.req.Context()
		batch, err := h.freshAfter(ctx, c.ID, sinceID)
		if err != nil {
			h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
			return
		}
		more := len(batch) > threadPage
		batch = batch[:min(len(batch), threadPage)]
		newSince := raw
		upTo := sinceID
		if len(batch) > 0 {
			newSince = newestID(batch, raw)
			upTo = batch[len(batch)-1].ID
		}
		changed, _, err := h.Messages.List(ctx, message.ListOpts{ChatID: &c.ID, UpdatedAfter: &wm, Limit: message.MaxListLimit})
		if err != nil {
			h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
			return
		}
		touched, targets := splitTouched(changed, batch, upTo)
		if len(batch) == 0 && len(touched) == 0 && len(targets) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		recent, err := h.recentWindow(ctx, c.ID)
		if err != nil {
			h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
			return
		}
		around := slices.Concat(recent, touched)
		tv.Bubbles = bubbles(batch, around, h.mediaURL(sc))
		tv.LastID = newSince
		tv.Watermark = nextWatermark(queriedAt)
		tv.Refresh = refreshed(bubbles(refreshRows(recent, touched, batch, targets), around, h.mediaURL(sc)), tv.Bubbles)
		if more {
			w.Header().Set("HX-Trigger", "thread-poll-now")
		}
		Render(w, sc.req, templates.ThreadAppend(base, tv))
		return
	}
	items, next, err := h.Messages.List(sc.req.Context(), message.ListOpts{ChatID: &c.ID, Cursor: q.Get("before"), Limit: threadPage})
	if err != nil {
		h.ErrorPageKey(w, sc.req, http.StatusBadRequest, "error.bad_request", err)
		return
	}
	recent, err := h.recentWindow(sc.req.Context(), c.ID)
	if err != nil {
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return
	}
	slices.Reverse(items)
	tv.Bubbles, tv.OlderCursor = bubbles(items, recent, h.mediaURL(sc)), next
	Render(w, sc.req, templates.ThreadPrepend(base, tv))
}

// PostMessage queues the composer's text or file and asks the thread poll to fetch it at once.
func (h *InboxHandler) PostMessage(w http.ResponseWriter, r *http.Request) {
	sc, c, ok := h.requireChat(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.MaxMedia+1<<20)
	if err := r.ParseMultipartForm(multipartMemory); err != nil { //nolint:gosec // G120: the body is capped by MaxBytesReader above.
		status := http.StatusBadRequest
		if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
			status = http.StatusRequestEntityTooLarge
		}
		h.ErrorPageKey(w, sc.req, status, "error.bad_request", err)
		return
	}
	text := strings.TrimSpace(r.PostFormValue("text"))
	in := message.SendInput{To: c.JID, Text: text, ReplyTo: r.PostFormValue("reply_to")}
	if file, fh, err := r.FormFile("file"); err == nil {
		defer func() { _ = file.Close() }()
		data, readErr := io.ReadAll(file)
		if readErr != nil {
			h.ErrorPageKey(w, sc.req, http.StatusBadRequest, "error.bad_request", readErr)
			return
		}
		in.Text = ""
		in.Media = &message.MediaInput{Bytes: data, Mime: fh.Header.Get("Content-Type"), Filename: fh.Filename, Caption: text}
	}
	if _, err := h.Messages.Send(sc.req.Context(), c.DeviceID, in); err != nil {
		h.logFailure("web inbox: send", err)
		h.ErrorPageKey(w, sc.req, statusForSend(err), "error.save_failed", err)
		return
	}
	w.Header().Set("HX-Trigger", "thread-poll-now")
	Render(w, sc.req, templates.ComposerReset(h.ProjectFragmentBase(sc), h.threadShell(sc, c)))
}

// PostRead marks the chat read, sending read receipts for its unread messages.
func (h *InboxHandler) PostRead(w http.ResponseWriter, r *http.Request) {
	sc, c, ok := h.requireChat(w, r)
	if !ok {
		return
	}
	if err := h.Messages.MarkRead(sc.req.Context(), c.ID); err != nil {
		h.logFailure("web inbox: read", err)
		h.ErrorPageKey(w, sc.req, http.StatusConflict, "error.save_failed", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetMedia streams a message's attachment; SECURITY: behind RequireProject, never cached.
func (h *InboxHandler) GetMedia(w http.ResponseWriter, r *http.Request) {
	sc, m, ok := h.requireMessage(w, r)
	if !ok {
		return
	}
	f, mimeType, name, err := h.Messages.OpenMedia(sc.req.Context(), m.ID)
	switch {
	case message.IsMediaUnavailableError(err):
		w.WriteHeader(http.StatusGone)
		return
	case message.IsNotFoundError(err):
		http.NotFound(w, sc.req)
		return
	case message.IsMediaTooLargeError(err):
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	case err != nil:
		h.LogErr("web inbox: media", err)
		w.WriteHeader(http.StatusBadGateway)
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
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, sc.req, name, time.Time{}, f)
}

// PostReaction queues one of the picker's reactions and answers with a toast.
func (h *InboxHandler) PostReaction(w http.ResponseWriter, r *http.Request) {
	sc, m, ok := h.requireMessage(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil || !slices.Contains(inboxReactions, r.PostForm.Get("emoji")) {
		h.ErrorPageKey(w, sc.req, http.StatusBadRequest, "error.bad_request", err)
		return
	}
	if _, err := h.Messages.React(sc.req.Context(), m.ID, r.PostForm.Get("emoji")); err != nil {
		h.logFailure("web inbox: react", err)
		h.ErrorPageKey(w, sc.req, statusForSend(err), "error.save_failed", err)
		return
	}
	base := h.ProjectFragmentBase(sc)
	Render(w, sc.req, templates.OOBToast(base, web.FlashOK, base.Tr("inbox.reaction_queued")))
}

// PostRevoke queues a delete-for-everyone from the confirm dialog and answers with a toast.
func (h *InboxHandler) PostRevoke(w http.ResponseWriter, r *http.Request) {
	sc, m, ok := h.requireMessage(w, r)
	if !ok {
		return
	}
	if _, err := h.Messages.Revoke(sc.req.Context(), m.ID); err != nil {
		h.logFailure("web inbox: revoke", err)
		h.ErrorPageKey(w, sc.req, statusForSend(err), "error.save_failed", err)
		return
	}
	base := h.ProjectFragmentBase(sc)
	Render(w, sc.req, templates.OOBToast(base, web.FlashOK, base.Tr("inbox.revoke_queued")))
}

// GetNewChat renders the new-chat form.
func (h *InboxHandler) GetNewChat(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	devices, err := h.devices(sc, r.URL.Query().Get("device"))
	if err != nil {
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return
	}
	Render(w, sc.req, templates.NewChatLayout(h.newLayout(sc), templates.NewChatView{ProjectSlug: sc.project.Slug, Devices: devices, Phone: r.URL.Query().Get("phone")}))
}

// PostNewChat checks the number is on WhatsApp, sends the first message and opens the thread.
func (h *InboxHandler) PostNewChat(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPageKey(w, sc.req, http.StatusBadRequest, "error.bad_request", err)
		return
	}
	devicePub := r.PostForm.Get("device")
	phone := digitsOnly(r.PostForm.Get("phone"))
	text := r.PostForm.Get("text")
	//i18n:use inbox.error.*
	fail := func(key string) {
		devices, _ := h.devices(sc, devicePub)
		RenderStatus(w, sc.req, http.StatusUnprocessableEntity, templates.NewChatLayout(h.newLayout(sc),
			templates.NewChatView{ProjectSlug: sc.project.Slug, Devices: devices, Phone: r.PostForm.Get("phone"), Text: text, Error: key}))
	}
	dev, err := h.Devices.Resolve(sc.req.Context(), devicePub)
	if err != nil {
		h.logFailure("web inbox: device", err)
		fail("inbox.error.send_failed")
		return
	}
	deviceID := dev.ID
	found, err := h.Messages.IsOnWhatsApp(sc.req.Context(), deviceID, []string{phone})
	if err != nil {
		h.logFailure("web inbox: is on whatsapp", err)
		fail("inbox.error.send_failed")
		return
	}
	if _, ok := found[phone]; !ok {
		fail("inbox.error.not_on_whatsapp")
		return
	}
	m, err := h.Messages.Send(sc.req.Context(), deviceID, message.SendInput{To: phone, Text: text})
	if err != nil {
		h.logFailure("web inbox: first message", err)
		fail("inbox.error.send_failed")
		return
	}
	pubs, err := h.Chats.PublicIDs(sc.req.Context(), []uuid.UUID{m.ChatID})
	if err != nil {
		h.logFailure("web inbox: first message chat", err)
		fail("inbox.error.send_failed")
		return
	}
	h.SetFlash(w, sc.req, web.FlashOK, "flash.message_queued")
	http.Redirect(w, sc.req, h.ProjectURL(sc, "/inbox/chats/"+pubs[m.ChatID]), http.StatusSeeOther) //nolint:gosec // G710: both slugs come from resolved rows and the suffix from a minted public id.
}

func (h *InboxHandler) requireChat(w http.ResponseWriter, r *http.Request) (ProjectScope, *chat.Chat, bool) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return ProjectScope{}, nil, false
	}
	c, err := h.Chats.Resolve(sc.req.Context(), r.PathValue("chat"))
	if err != nil {
		if chat.IsNotFoundError(err) {
			h.ErrorPageKey(w, sc.req, http.StatusNotFound, "error.not_found", err)
			return ProjectScope{}, nil, false
		}
		h.LogErr("web inbox: chat", err)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return ProjectScope{}, nil, false
	}
	return sc, c, true
}

// NOTE: Resolve is project-scoped and answers not found for a malformed id without a query, so a UUID or a foreign id is the same 404.
func (h *InboxHandler) requireMessage(w http.ResponseWriter, r *http.Request) (ProjectScope, *message.Message, bool) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return ProjectScope{}, nil, false
	}
	m, err := h.Messages.Resolve(sc.req.Context(), r.PathValue("message"))
	if err != nil {
		if message.IsNotFoundError(err) {
			h.ErrorPageKey(w, sc.req, http.StatusNotFound, "error.not_found", err)
			return ProjectScope{}, nil, false
		}
		h.LogErr("web inbox: message", err)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return ProjectScope{}, nil, false
	}
	return sc, m, true
}

func (h *InboxHandler) chatList(sc ProjectScope, r *http.Request) (templates.ChatListView, []templates.InboxDevice, error) {
	q := r.URL.Query()
	devices, err := h.devices(sc, q.Get("device"))
	if err != nil {
		return templates.ChatListView{}, nil, err
	}
	limit := templates.ChatPageSize
	if n, convErr := strconv.Atoi(q.Get("limit")); convErr == nil && n > 0 {
		limit = min(n, chat.MaxListLimit)
	}
	opts := chat.ListOpts{Search: q.Get("q"), Limit: limit}
	if pub := q.Get("device"); pub != "" {
		d, resolveErr := h.Devices.Resolve(sc.req.Context(), pub)
		switch {
		case device.IsNotFoundError(resolveErr):
			return templates.ChatListView{ProjectSlug: sc.project.Slug, Query: q.Get("q"), Limit: limit}, devices, nil
		case resolveErr != nil:
			return templates.ChatListView{}, nil, resolveErr
		}
		opts.DeviceID = &d.ID
	}
	items, next, err := h.Chats.List(sc.req.Context(), opts)
	if err != nil {
		return templates.ChatListView{}, nil, err
	}
	active := q.Get("active")
	return templates.ChatListView{
		ProjectSlug: sc.project.Slug, DeviceID: q.Get("device"), Query: q.Get("q"), ActiveID: active,
		Rows: chatRows(items, active), Limit: limit, HasMore: next != "" && limit < chat.MaxListLimit,
		Capped: next != "" && limit >= chat.MaxListLimit,
	}, devices, nil
}

func (h *InboxHandler) inboxView(sc ProjectScope, r *http.Request, thread *templates.ThreadView) (templates.InboxView, error) {
	list, devices, err := h.chatList(sc, r)
	if err != nil {
		return templates.InboxView{}, err
	}
	return templates.InboxView{ProjectSlug: sc.project.Slug, Devices: devices, List: list, Thread: thread}, nil
}

func (h *InboxHandler) threadShell(sc ProjectScope, c *chat.Chat) templates.ThreadView {
	name := c.Name
	if name == "" {
		name, _, _ = strings.Cut(c.JID, "@")
	}
	return templates.ThreadView{
		ProjectSlug: sc.project.Slug, ChatID: c.PublicID, Name: name, JID: c.JID, Kind: string(c.Kind),
		LastID: firstPollToken(), MaxMediaMB: int(h.MaxMedia >> 20), Reactions: inboxReactions,
	}
}

func (h *InboxHandler) threadView(sc ProjectScope, c *chat.Chat) (templates.ThreadView, error) {
	tv := h.threadShell(sc, c)
	queriedAt := h.Clock()
	items, next, err := h.Messages.List(sc.req.Context(), message.ListOpts{ChatID: &c.ID, Limit: threadPage})
	if err != nil {
		return tv, err
	}
	tv.LastID = newestID(items, tv.LastID)
	tv.Watermark = nextWatermark(queriedAt)
	slices.Reverse(items)
	tv.Bubbles, tv.OlderCursor = bubbles(items, nil, h.mediaURL(sc)), next
	if c.Kind == chat.KindGroup {
		if info, infoErr := h.Chats.GroupInfo(sc.req.Context(), c.ID); infoErr == nil {
			tv.Group = &templates.GroupSummary{Topic: info.Topic, Participants: info.Participants, Announce: info.Announce, Locked: info.Locked}
		}
	}
	return tv, nil
}

func (h *InboxHandler) devices(sc ProjectScope, selected string) ([]templates.InboxDevice, error) {
	views, err := h.Devices.List(sc.req.Context())
	if err != nil {
		return nil, err
	}
	out := make([]templates.InboxDevice, 0, len(views))
	for _, v := range views {
		out = append(out, templates.InboxDevice{ID: v.Device.PublicID, Name: v.Device.Name, Selected: v.Device.PublicID == selected})
	}
	return out, nil
}

func (h *InboxHandler) mediaURL(sc ProjectScope) func(string) string {
	return func(publicID string) string { return h.ProjectURL(sc, "/inbox/messages/"+publicID+"/media") }
}

func (h *InboxHandler) layout(sc ProjectScope) web.LayoutData {
	return h.LayoutForProject(sc.req, "Inbox", sc.org.Slug, sc.project, "inbox")
}

func (h *InboxHandler) newLayout(sc ProjectScope) web.LayoutData {
	l := h.LayoutForProject(sc.req, "New chat", sc.org.Slug, sc.project, "inbox")
	l.ActiveNav.Parent = "inbox"
	l.Crumbs = append(l.Crumbs, web.Crumb{Label: "Inbox", Href: h.ProjectURL(sc, "/inbox")})
	return l
}

// NOTE: the oldest rows after since come first, one more than a page, so the caller can tell a burst from a full page and advance since only to what it returns.
func (h *InboxHandler) freshAfter(ctx context.Context, chatID, since uuid.UUID) ([]*message.Message, error) {
	items, _, err := h.Messages.List(ctx, message.ListOpts{ChatID: &chatID, SinceID: &since, Limit: threadPage + 1})
	return items, err
}

// NOTE: the newest page, as the context a refreshed or newly polled bubble finds its reactions and quoted text in.
func (h *InboxHandler) recentWindow(ctx context.Context, chatID uuid.UUID) ([]*message.Message, error) {
	items, _, err := h.Messages.List(ctx, message.ListOpts{ChatID: &chatID, Limit: message.MaxListLimit})
	return items, err
}

// NOTE: touched is changed rows already on screen (id at or before upTo, not in batch); targets are the WhatsApp ids a reaction, edit or revoke in the batch or in touched points at.
func splitTouched(changed, batch []*message.Message, upTo uuid.UUID) (touched []*message.Message, targets map[string]struct{}) {
	targets = map[string]struct{}{}
	inBatch := make(map[uuid.UUID]struct{}, len(batch))
	for _, m := range batch {
		inBatch[m.ID] = struct{}{}
		if m.TargetWAMessageID != "" {
			targets[m.TargetWAMessageID] = struct{}{}
		}
	}
	for _, m := range changed {
		if _, ok := inBatch[m.ID]; ok || m.ID.String() > upTo.String() {
			continue
		}
		touched = append(touched, m)
		if m.TargetWAMessageID != "" {
			targets[m.TargetWAMessageID] = struct{}{}
		}
	}
	return touched, targets
}

// NOTE: rows to redraw in place, oldest first: every touched row, plus any recent row a new reaction, edit or revoke points at; rows in the batch are appended instead.
func refreshRows(recent, touched, batch []*message.Message, targets map[string]struct{}) []*message.Message {
	skip := make(map[uuid.UUID]struct{}, len(batch))
	for _, m := range batch {
		skip[m.ID] = struct{}{}
	}
	seen := map[uuid.UUID]struct{}{}
	var out []*message.Message
	add := func(m *message.Message) {
		if _, dup := skip[m.ID]; dup {
			return
		}
		if _, dup := seen[m.ID]; dup {
			return
		}
		seen[m.ID] = struct{}{}
		out = append(out, m)
	}
	for _, m := range touched {
		add(m)
	}
	for _, m := range recent {
		if _, ok := targets[m.WAMessageID]; ok {
			add(m)
		}
	}
	slices.SortFunc(out, func(a, b *message.Message) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	return out
}

func refreshed(drawn, appended []templates.BubbleView) []templates.BubbleView {
	skip := make(map[string]struct{}, len(appended))
	for _, b := range appended {
		skip[b.ID] = struct{}{}
	}
	out := make([]templates.BubbleView, 0, len(drawn))
	for _, b := range drawn {
		if _, dup := skip[b.ID]; !dup {
			out = append(out, b)
		}
	}
	return out
}

// NOTE: a refused input is the caller's mistake, not ours, so only the rest is logged.
func (h *InboxHandler) logFailure(msg string, err error) {
	if statusForSend(err) < http.StatusInternalServerError {
		return
	}
	h.LogErr(msg, err)
}

func statusForSend(err error) int {
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge), message.IsMediaTooLargeError(err):
		return http.StatusRequestEntityTooLarge
	case message.IsInvalidInputError(err), message.IsUnsupportedMimeError(err), message.IsMediaFetchError(err), chat.IsInvalidJIDError(err):
		return http.StatusUnprocessableEntity
	case message.IsDeviceNotLinkedError(err), message.IsEditWindowClosedError(err), message.IsNotOwnMessageError(err):
		return http.StatusConflict
	case message.IsNotFoundError(err), chat.IsNotFoundError(err):
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func digitsOnly(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

//i18n:use inbox.status.*
