package handlers_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/platform/keyset"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/web/handlers"
)

const inboxBase = "/orgs/acme/projects/alpha/inbox"

// NOTE: adapts the real chat service for the message service, exactly as boot's chatsForMessage does.
type chatsPort struct{ svc *chat.Service }

func (p chatsPort) EnsureForJID(ctx context.Context, deviceID uuid.UUID, jid, lid, kind, name string) (message.ChatRef, error) {
	c, err := p.svc.EnsureForJID(ctx, deviceID, jid, lid, chat.Kind(kind), name)
	if err != nil {
		return message.ChatRef{}, err
	}
	return message.ChatRef{ID: c.ID, PublicID: c.PublicID, DeviceID: c.DeviceID, JID: c.JID, LID: c.LID, Kind: string(c.Kind), Name: c.Name}, nil
}

func (p chatsPort) Get(ctx context.Context, id uuid.UUID) (message.ChatRef, error) {
	c, err := p.svc.Get(ctx, id)
	if err != nil {
		return message.ChatRef{}, err
	}
	return message.ChatRef{ID: c.ID, PublicID: c.PublicID, DeviceID: c.DeviceID, JID: c.JID, Kind: string(c.Kind), Name: c.Name}, nil
}

func (p chatsPort) Touch(ctx context.Context, id uuid.UUID, at time.Time, preview string, inbound bool) error {
	return p.svc.Touch(ctx, id, at, preview, inbound)
}

func (p chatsPort) MarkRead(ctx context.Context, id uuid.UUID) error { return p.svc.MarkRead(ctx, id) }

func (p chatsPort) Repair(ctx context.Context, id uuid.UUID, last *time.Time, preview string, unread int) error {
	return p.svc.Repair(ctx, id, last, preview, unread)
}

type inboxFixture struct {
	*handlerFixture
	Mux       *http.ServeMux
	Inbox     *handlers.InboxHandler
	Chats     *chat.Service
	ChatStore *fakes.Chat
	Messages  *message.Service
	Store     *fakes.Message
	Transport *fakes.Transport
	uid, org  uuid.UUID
	tc        tenant.Context
	device    uuid.UUID
	devicePub string
}

func newInboxFixture(t *testing.T) *inboxFixture {
	t.Helper()
	f := newFixture(t)
	uid := uuid.New()
	o := f.seedOrg(t, "acme", uid)
	proj, err := f.Projects.Create(setTenant(context.Background(), o.ID, uid), o.ID, "alpha", "Alpha")
	require.NoError(t, err)
	tc := tenant.Context{OrgID: o.ID, ProjectID: proj.ID, UserID: uid}

	devStore := fakes.NewDevice()
	d, err := device.New(o.ID, proj.ID, fakes.DevicePublicID(), "sales-01")
	require.NoError(t, err)
	require.NoError(t, devStore.Save(tenant.Into(context.Background(), tc), d, 0))
	devices := device.NewService(devStore, discardLogger(), passthroughUnexpected(), fakes.UnitOfWork, &fakes.DeviceSessions{})

	chatStore := fakes.NewChat()
	chats := chat.NewService(chatStore, discardLogger(), passthroughUnexpected(), fakes.UnitOfWork, &fakes.Groups{})
	store := fakes.NewMessage()
	transport := &fakes.Transport{FetchBody: []byte("img")}
	msgs := message.NewService(store, discardLogger(), passthroughUnexpected(), fakes.UnitOfWork, message.Deps{
		Devices:   &fakes.MessageDevices{Refs: map[uuid.UUID]message.DeviceRef{d.ID: {ID: d.ID, Name: "sales-01", Linked: true}}},
		Chats:     chatsPort{svc: chats},
		Contacts:  &fakes.MessageContacts{},
		Transport: transport,
		Media:     fakes.MediaStore{Transport: transport},
		Fetcher:   &fakes.MediaFetcher{},
		Waker:     &fakes.Waker{},
		Webhooks:  &fakes.Webhooks{},
		Tenants:   &fakes.MessageTenants{Org: "acme", Project: "alpha"},
	}, message.Options{BaseURL: "http://localhost", MaxMediaBytes: 1 << 20, StaleAfter: time.Minute})

	mux := http.NewServeMux()
	inbox := handlers.NewInboxHandler(f.Deps, f.Projects, chats, msgs, devices, 1<<20)
	inbox.Register(mux)
	return &inboxFixture{handlerFixture: f, Mux: mux, Inbox: inbox, Chats: chats, ChatStore: chatStore, Messages: msgs, Store: store, Transport: transport, uid: uid, org: o.ID, tc: tc, device: d.ID, devicePub: d.PublicID}
}

func (x *inboxFixture) ctx() context.Context { return tenant.Into(context.Background(), x.tc) }

func (x *inboxFixture) do(t *testing.T, method, target string, body string, hx bool) *httptest.ResponseRecorder {
	t.Helper()
	r := x.authedRequest(t, method, target, body, session.Principal{UserID: x.uid, ActiveOrgID: x.org})
	if hx {
		r.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, r)
	return rec
}

func (x *inboxFixture) chatWith(t *testing.T, texts ...string) (*chat.Chat, []*message.Message) {
	t.Helper()
	var out []*message.Message
	for _, s := range texts {
		m, err := x.Messages.Send(x.ctx(), x.device, message.SendInput{To: "628111222333", Text: s})
		require.NoError(t, err)
		out = append(out, m)
	}
	c, err := x.Chats.Get(x.ctx(), out[0].ChatID)
	require.NoError(t, err)
	return c, out
}

func TestInbox_PageRendersTheSplitPaneAndAMorphingList(t *testing.T) {
	x := newInboxFixture(t)
	c, _ := x.chatWith(t, "halo")
	rec := x.do(t, http.MethodGet, inboxBase, "", false)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	require.Contains(t, body, `id="inbox-detail"`)
	require.Contains(t, body, `id="chat-list"`)
	require.Contains(t, body, `hx-swap="innerMorph"`)
	require.Contains(t, body, `hx-trigger="every 5s"`)
	require.Contains(t, body, `hx-status:4xx="target:#hx-error swap:innerHTML"`)
	require.Contains(t, body, `id="chat-`+c.PublicID+`"`, "each row keeps a stable id for the morph")
	require.NotContains(t, body, c.ID.String(), "SECURITY: no chat UUID reaches the page")
	require.Regexp(t, regexp.MustCompile(`id="chat-list"[^>]*hx-nonce=`), body)
}

func TestInbox_RowsModeReturnsOnlyRows(t *testing.T) {
	x := newInboxFixture(t)
	x.chatWith(t, "halo")
	rec := x.do(t, http.MethodGet, inboxBase+"/chats?rows=1", "", true)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), `id="chat-list"`, "the poll morphs children, so it must not return the shell")
	require.Contains(t, rec.Body.String(), `<li`)
}

func TestInbox_ThreadPollAdvancesSince(t *testing.T) {
	x := newInboxFixture(t)
	c, msgs := x.chatWith(t, "one", "two")
	thread := x.do(t, http.MethodGet, inboxBase+"/chats/"+c.PublicID, "", true)
	require.Equal(t, http.StatusOK, thread.Code)
	tb := thread.Body.String()
	require.Contains(t, tb, `id="thread-poll"`)
	require.Contains(t, tb, `hx-trigger="every 5s, thread-poll-now from:body"`)
	require.Contains(t, tb, `hx-swap="beforeend scroll:bottom scrollTarget:#thread-scroll"`)
	require.Contains(t, tb, "updated_after=")
	require.NotContains(t, tb, msgs[1].ID.String(), "SECURITY: no internal id reaches the page")
	require.NotContains(t, tb, c.ID.String())
	require.Contains(t, tb, `data-copy="`+c.PublicID+`"`, "the header copies the chat's public id, the one the API takes")
	require.Contains(t, tb, `hx-encoding="multipart/form-data"`)
	require.Contains(t, tb, `hx-disable="find button, find textarea, find input"`)

	since := url.Values{"since": {string(keyset.Encode(msgs[0].WATimestamp, msgs[0].ID))}, "updated_after": {time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)}}
	rec := x.do(t, http.MethodGet, inboxBase+"/chats/"+c.PublicID+"/messages?"+since.Encode(), "", true)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, `id="msg-`+msgs[1].PublicID+`"`, "the newer row is appended")
	appended, _, _ := strings.Cut(body, "<template")
	require.NotContains(t, appended, `id="msg-`+msgs[0].PublicID+`"`, "the older row is only refreshed in a partial, never appended")
	require.Contains(t, body, `hx-target="#msg-`+msgs[0].PublicID+`"`)
	require.Contains(t, body, `<template hx type="partial" hx-target="#thread-poll" hx-swap="outerHTML"`)

	quiet := url.Values{"since": {string(keyset.Encode(msgs[1].WATimestamp, msgs[1].ID))}, "updated_after": {time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}}
	rec = x.do(t, http.MethodGet, inboxBase+"/chats/"+c.PublicID+"/messages?"+quiet.Encode(), "", true)
	require.Equal(t, http.StatusNoContent, rec.Code, "nothing new, nothing swapped")
}

func TestInbox_PollGoesQuietWithTheReturnedWatermark(t *testing.T) {
	x := newInboxFixture(t)
	c, msgs := x.chatWith(t, "one", "two")
	x.Inbox.Clock = func() time.Time { return time.Now().Add(time.Minute) }
	first := url.Values{"since": {string(keyset.Encode(msgs[0].WATimestamp, msgs[0].ID))}, "updated_after": {time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)}}
	rec := x.do(t, http.MethodGet, inboxBase+"/chats/"+c.PublicID+"/messages?"+first.Encode(), "", true)
	require.Equal(t, http.StatusOK, rec.Code)
	next := regexp.MustCompile(`since=([^&"]+)&(?:amp;)?updated_after=([^&"]+)`).FindStringSubmatch(rec.Body.String())
	require.Len(t, next, 3, rec.Body.String())
	since, err := url.QueryUnescape(next[1])
	require.NoError(t, err)
	wm, err := url.QueryUnescape(next[2])
	require.NoError(t, err)
	again := url.Values{"since": {since}, "updated_after": {wm}}
	rec = x.do(t, http.MethodGet, inboxBase+"/chats/"+c.PublicID+"/messages?"+again.Encode(), "", true)
	require.Equal(t, http.StatusNoContent, rec.Code, "with the token and watermark the poll returned, an unchanged thread goes quiet")
}

func TestInbox_PollRefreshesAnUnsettledBubbleInPlace(t *testing.T) {
	x := newInboxFixture(t)
	c, sent := x.chatWith(t, "halo")
	in := message.NewInbound(message.Ref{DeviceID: x.device, OrgID: x.tc.OrgID, ProjectID: x.tc.ProjectID}, c.ID, fakes.MessagePublicID(),
		message.InboundInput{WAID: "IN1", Type: "text", Body: "balas", SenderJID: "628111222333@s.whatsapp.net", Timestamp: time.Now()})
	x.Store.Seed(in)
	since := string(keyset.Encode(in.WATimestamp, in.ID))
	before := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	path := inboxBase + "/chats/" + c.PublicID + "/messages?" + url.Values{"since": {since}, "updated_after": {before}}.Encode()
	rec := x.do(t, http.MethodGet, path, "", true)
	require.Equal(t, http.StatusOK, rec.Code, "nothing new, but the queued bubble changed after the watermark")
	body := rec.Body.String()
	require.Contains(t, body, `hx-target="#msg-`+sent[0].PublicID+`"`)
	require.Contains(t, body, `hx-swap="outerMorph"`)
	require.Contains(t, body, `hx-sync="this:replace"`, "the new poll element keeps the sync guard")

	quiet := inboxBase + "/chats/" + c.PublicID + "/messages?" + url.Values{"since": {since}, "updated_after": {time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)}}.Encode()
	require.Equal(t, http.StatusNoContent, x.do(t, http.MethodGet, quiet, "", true).Code, "a thread with nothing changed since the watermark goes quiet")
}

func TestInbox_ControlsSitOutsideTheSwappedPanel(t *testing.T) {
	x := newInboxFixture(t)
	x.chatWith(t, "halo")
	body := x.do(t, http.MethodGet, inboxBase, "", false).Body.String()
	controls := strings.Index(body, `id="chat-list-controls"`)
	panel := strings.Index(body, `id="chat-list-panel"`)
	require.True(t, controls >= 0 && panel > controls, "controls render before, not inside, the panel")
	require.Less(t, strings.Index(body, `id="inbox-search"`), panel)
	fragment := x.do(t, http.MethodGet, inboxBase+"/chats?q=bu", "", true).Body.String()
	require.NotContains(t, fragment, `id="inbox-search"`, "a panel swap never replaces the search box")
	require.Contains(t, fragment, "q=bu", "query strings are built with url.Values")
}

func TestInbox_ComposerQueuesAndTriggersThePoll(t *testing.T) {
	x := newInboxFixture(t)
	c, _ := x.chatWith(t, "first")
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("text", "from the composer"))
	require.NoError(t, mw.Close())
	r := x.authedRequest(t, http.MethodPost, inboxBase+"/chats/"+c.PublicID+"/messages", "", session.Principal{UserID: x.uid, ActiveOrgID: x.org})
	r.Body = httpNopCloser(buf.Bytes())
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, r)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "thread-poll-now", rec.Header().Get("HX-Trigger"))
	require.Contains(t, rec.Body.String(), `hx-target="#composer"`)
	var found bool
	for _, m := range x.Store.All() {
		found = found || (m.Body == "from the composer" && m.Status == message.StatusQueued)
	}
	require.True(t, found)
}

func TestInbox_MediaDoorHeadersAnd410(t *testing.T) {
	x := newInboxFixture(t)
	c, _ := x.chatWith(t, "x")
	img := message.NewInbound(message.Ref{DeviceID: x.device, OrgID: x.tc.OrgID, ProjectID: x.tc.ProjectID}, c.ID, fakes.MessagePublicID(), message.InboundInput{
		WAID: "IMG", Type: "image", Media: &message.InboundMedia{Mime: "image/jpeg", Keys: message.MediaKeys{DirectPath: "/v"}},
	})
	x.Store.Seed(img)
	rec := x.do(t, http.MethodGet, inboxBase+"/messages/"+img.PublicID+"/media", "", false)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
	require.True(t, strings.HasPrefix(rec.Header().Get("Content-Disposition"), "inline"))
	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "sandbox", rec.Header().Get("Content-Security-Policy"))
	require.Equal(t, "img", rec.Body.String())

	svg := message.NewInbound(message.Ref{DeviceID: x.device, OrgID: x.tc.OrgID, ProjectID: x.tc.ProjectID}, c.ID, fakes.MessagePublicID(), message.InboundInput{
		WAID: "SVG", Type: "image", Media: &message.InboundMedia{Mime: "image/svg+xml", Keys: message.MediaKeys{DirectPath: "/v"}},
	})
	x.Store.Seed(svg)
	rec = x.do(t, http.MethodGet, inboxBase+"/messages/"+svg.PublicID+"/media", "", false)
	require.True(t, strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment"), "SECURITY: an SVG never renders inline")

	x.Transport.Err = &message.MediaUnavailableError{ID: img.PublicID}
	rec = x.do(t, http.MethodGet, inboxBase+"/messages/"+img.PublicID+"/media", "", false)
	require.Equal(t, http.StatusGone, rec.Code)
}

func TestInbox_OtherProjectChatIsNotFound(t *testing.T) {
	x := newInboxFixture(t)
	other, err := chat.New(x.tc.OrgID, uuid.New(), x.device, fakes.ChatPublicID(), "628999@s.whatsapp.net", "", chat.KindDM)
	require.NoError(t, err)
	x.ChatStore.Seed(other)
	for _, id := range []string{other.PublicID, other.ID.String()} {
		rec := x.do(t, http.MethodGet, inboxBase+"/chats/"+id, "", true)
		require.Equal(t, http.StatusNotFound, rec.Code, "a sibling project's chat and a raw UUID are the same 404")
	}
	rec := x.do(t, http.MethodGet, inboxBase+"/messages/"+uuid.NewString()+"/media", "", false)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestInbox_NewChatRefusesANumberNotOnWhatsApp(t *testing.T) {
	x := newInboxFixture(t)
	x.Transport.OnWA = map[string]string{}
	form := url.Values{"device": {x.devicePub}, "phone": {"+62 811 1222 333"}, "text": {"halo"}}
	rec := x.do(t, http.MethodPost, inboxBase+"/new", form.Encode(), false)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, rec.Body.String(), "inbox.error.not_on_whatsapp")

	x.Transport.OnWA = map[string]string{"628111222333": "628111222333@s.whatsapp.net"}
	rec = x.do(t, http.MethodPost, inboxBase+"/new", form.Encode(), false)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Regexp(t, `/inbox/chats/cht_[A-Za-z0-9_-]{16}$`, rec.Header().Get("Location"), "the redirect names the chat by its public id")
}

type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }

func httpNopCloser(b []byte) nopCloser { return nopCloser{bytes.NewReader(b)} }

var pollTokens = regexp.MustCompile(`since=([^&"]+)&(?:amp;)?updated_after=([^&"]+)`)
var bubbleIDs = regexp.MustCompile(`id="(msg-msg_[A-Za-z0-9_-]+)"`)

func nextPoll(t *testing.T, body string) url.Values {
	t.Helper()
	m := pollTokens.FindStringSubmatch(body)
	require.Len(t, m, 3, body)
	since, err := url.QueryUnescape(m[1])
	require.NoError(t, err)
	wm, err := url.QueryUnescape(m[2])
	require.NoError(t, err)
	return url.Values{"since": {since}, "updated_after": {wm}}
}

func appendedIDs(body string) []string {
	head, _, _ := strings.Cut(body, "<template")
	var out []string
	for _, m := range bubbleIDs.FindAllStringSubmatch(head, -1) {
		out = append(out, m[1])
	}
	return out
}

func (x *inboxFixture) ref() message.Ref {
	return message.Ref{DeviceID: x.device, OrgID: x.tc.OrgID, ProjectID: x.tc.ProjectID}
}

func (x *inboxFixture) poll(t *testing.T, chatID string, q url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return x.do(t, http.MethodGet, inboxBase+"/chats/"+chatID+"/messages?"+q.Encode(), "", true)
}

func TestInbox_StatusRefreshKeepsTheBubblesReactions(t *testing.T) {
	x := newInboxFixture(t)
	c, sent := x.chatWith(t, "hello")
	react := message.NewInbound(x.ref(), c.ID, fakes.MessagePublicID(), message.InboundInput{WAID: "R1", Type: "text", Body: "👍"})
	react.Type, react.TargetWAMessageID, react.UpdatedAt = message.TypeReaction, sent[0].WAMessageID, time.Now().Add(-2*time.Hour)
	x.Store.Seed(react)
	q := url.Values{"since": {string(keyset.Encode(react.WATimestamp, react.ID))}, "updated_after": {time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)}}
	rec := x.poll(t, c.PublicID, q)
	require.Equal(t, http.StatusOK, rec.Code, "the queued bubble changed after the watermark; the reaction row did not")
	body := rec.Body.String()
	require.Contains(t, body, `hx-target="#msg-`+sent[0].PublicID+`"`)
	require.Contains(t, body, "<span>👍</span>", "a refreshed bubble keeps the reactions its slice no longer carries")
}

func TestInbox_LiveReplyKeepsItsQuotedText(t *testing.T) {
	x := newInboxFixture(t)
	c, sent := x.chatWith(t, "hello")
	reply := message.NewInbound(x.ref(), c.ID, fakes.MessagePublicID(), message.InboundInput{WAID: "IN9", Type: "text", Body: "answer", QuotedWAID: sent[0].WAMessageID})
	x.Store.Seed(reply)
	q := url.Values{"since": {string(keyset.Encode(sent[0].WATimestamp, sent[0].ID))}, "updated_after": {time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}}
	rec := x.poll(t, c.PublicID, q)
	require.Equal(t, http.StatusOK, rec.Code)
	head, _, _ := strings.Cut(rec.Body.String(), "<template")
	require.Contains(t, head, `id="msg-`+reply.PublicID+`"`)
	require.Regexp(t, `border-s-2[^>]*>hello<`, head, "the live reply shows the text it quotes, which is not in the polled slice")
}

func TestInbox_BurstOverAPageLosesNothingAcrossPolls(t *testing.T) {
	x := newInboxFixture(t)
	c, msgs := x.chatWith(t, "first")
	const burst = 120
	want := map[string]bool{}
	for i := range burst {
		in := message.NewInbound(x.ref(), c.ID, fakes.MessagePublicID(), message.InboundInput{WAID: "B" + strconv.Itoa(i), Type: "text", Body: "b", Timestamp: time.Now()})
		x.Store.Seed(in)
		want["msg-"+in.PublicID] = true
	}
	x.Inbox.Clock = func() time.Time { return time.Now().Add(time.Minute) }
	q := url.Values{"since": {string(keyset.Encode(msgs[0].WATimestamp, msgs[0].ID))}, "updated_after": {time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)}}
	got := map[string]int{}
	for i := range 6 {
		rec := x.poll(t, c.PublicID, q)
		if rec.Code == http.StatusNoContent {
			break
		}
		require.Equal(t, http.StatusOK, rec.Code)
		ids := appendedIDs(rec.Body.String())
		if i == 0 {
			require.Len(t, ids, 50, "a burst is capped to one page")
			require.Equal(t, "thread-poll-now", rec.Header().Get("HX-Trigger"), "the rest is asked for at once")
		}
		for _, id := range ids {
			got[id]++
		}
		q = nextPoll(t, rec.Body.String())
	}
	require.Len(t, got, burst, "every message of the burst arrived")
	for id, n := range got {
		require.True(t, want[id], id)
		require.Equal(t, 1, n, "%s appended once", id)
	}
}

func TestInbox_ComposerThenPollShowsTheBubbleOnce(t *testing.T) {
	x := newInboxFixture(t)
	c, _ := x.chatWith(t, "first")
	thread := x.do(t, http.MethodGet, inboxBase+"/chats/"+c.PublicID, "", true)
	q := nextPoll(t, thread.Body.String())

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("text", "typed once"))
	require.NoError(t, mw.Close())
	r := x.authedRequest(t, http.MethodPost, inboxBase+"/chats/"+c.PublicID+"/messages", "", session.Principal{UserID: x.uid, ActiveOrgID: x.org})
	r.Body = httpNopCloser(buf.Bytes())
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("HX-Request", "true")
	post := httptest.NewRecorder()
	x.Mux.ServeHTTP(post, r)
	require.Equal(t, http.StatusOK, post.Code)
	require.Empty(t, bubbleIDs.FindAllString(post.Body.String(), -1), "the composer response carries no bubble")

	x.Inbox.Clock = func() time.Time { return time.Now().Add(time.Minute) }
	rec := x.poll(t, c.PublicID, q)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, appendedIDs(rec.Body.String()), 1, "the poll appends the queued bubble")
	require.Contains(t, rec.Body.String(), "typed once")
	require.Equal(t, http.StatusNoContent, x.poll(t, c.PublicID, nextPoll(t, rec.Body.String())).Code, "the next poll does not append it again")
}

func TestInbox_OtherProjectIsMasked404OnEveryPost(t *testing.T) {
	x := newInboxFixture(t)
	otherProject := uuid.New()
	other, err := chat.New(x.tc.OrgID, otherProject, x.device, fakes.ChatPublicID(), "628999@s.whatsapp.net", "", chat.KindDM)
	require.NoError(t, err)
	x.ChatStore.Seed(other)
	foreign := message.NewInbound(message.Ref{DeviceID: x.device, OrgID: x.tc.OrgID, ProjectID: otherProject}, other.ID, fakes.MessagePublicID(), message.InboundInput{WAID: "F1", Type: "text", Body: "x"})
	x.Store.Seed(foreign)
	before := len(x.Store.All())
	for _, id := range []string{other.PublicID, other.ID.String()} {
		for _, suffix := range []string{"/messages", "/read"} {
			rec := x.do(t, http.MethodPost, inboxBase+"/chats/"+id+suffix, url.Values{"text": {"x"}}.Encode(), true)
			require.Equal(t, http.StatusNotFound, rec.Code, "chat %s POST %s", id, suffix)
		}
	}
	for _, id := range []string{foreign.PublicID, foreign.ID.String()} {
		for _, suffix := range []string{"/reactions", "/revoke"} {
			rec := x.do(t, http.MethodPost, inboxBase+"/messages/"+id+suffix, url.Values{"emoji": {"👍"}}.Encode(), true)
			require.Equal(t, http.StatusNotFound, rec.Code, "message %s POST %s", id, suffix)
		}
	}
	require.Len(t, x.Store.All(), before, "nothing was queued against the other project")
}
