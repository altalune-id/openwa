package message_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
)

type env struct {
	unexpected atomic.Int32
	store      *fakes.Message
	devices    *fakes.MessageDevices
	chats      *fakes.MessageChats
	contacts   *fakes.MessageContacts
	transport  *fakes.Transport
	fetcher    *fakes.MediaFetcher
	waker      *fakes.Waker
	hooks      *fakes.Webhooks
	tenants    *fakes.MessageTenants
	now        time.Time
	svc        *message.Service
	tc         tenant.Context
	device     uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{
		store: fakes.NewMessage(), chats: fakes.NewMessageChats(), contacts: &fakes.MessageContacts{},
		transport: &fakes.Transport{FetchBody: []byte("jpeg")}, fetcher: &fakes.MediaFetcher{Data: []byte("fetched"), Mime: "image/png"},
		waker: &fakes.Waker{}, hooks: &fakes.Webhooks{}, tenants: &fakes.MessageTenants{Org: "acme", Project: "main"},
		now: time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC),
		tc:  tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()}, device: uuid.New(),
	}
	e.tenants.Projects = []uuid.UUID{e.tc.ProjectID}
	e.devices = &fakes.MessageDevices{Refs: map[uuid.UUID]message.DeviceRef{e.device: {ID: e.device, Name: "sales-01", Phone: "628123456789", Linked: true}}}
	unexpected := func(_ context.Context, msg string, cause error, _ ...any) *apperror.AppError {
		e.unexpected.Add(1)
		return apperror.New("GEN900", msg, codes.Internal).WithCause(cause)
	}
	e.svc = message.NewService(e.store, slog.New(slog.NewTextHandler(io.Discard, nil)), unexpected, fakes.UnitOfWork, message.Deps{
		Devices: e.devices, Chats: e.chats, Contacts: e.contacts, Transport: e.transport,
		Media: fakes.MediaStore{Transport: e.transport}, Fetcher: e.fetcher, Waker: e.waker, Webhooks: e.hooks, Tenants: e.tenants,
	}, message.Options{BaseURL: "https://wa.example.com", MaxMediaBytes: 1 << 20, RetentionDays: 30, StaleAfter: 78 * time.Second, Clock: func() time.Time { return e.now }})
	return e
}

func (e *env) ctx(t *testing.T) context.Context { return tenant.Into(t.Context(), e.tc) }

func TestSend_RefusesAnUnlinkedDevice(t *testing.T) {
	e := newEnv(t)
	e.devices.Refs[e.device] = message.DeviceRef{ID: e.device, Linked: false}
	_, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Text: "hi"})
	require.True(t, message.IsDeviceNotLinkedError(err), "got %v", err)
	require.Empty(t, e.waker.Woken)
}

func TestSend_QueuesTextAndWakesTheSender(t *testing.T) {
	e := newEnv(t)
	m, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "+62 811-1222-333", Text: "halo"})
	require.NoError(t, err)
	require.Equal(t, message.StatusQueued, m.Status)
	require.Equal(t, e.now.UTC(), m.CreatedAt, "persisted times follow the injected clock")
	require.Equal(t, e.now.UTC(), m.WATimestamp)
	require.Equal(t, "628111222333@s.whatsapp.net", e.chats.Ensured[0].JID)
	require.Equal(t, []uuid.UUID{e.device}, e.waker.Woken)
	require.Len(t, e.chats.Touches, 1)
	require.False(t, e.chats.Touches[0].Inbound)
	stored, err := e.store.ByID(e.ctx(t), m.ID)
	require.NoError(t, err)
	require.Equal(t, "halo", stored.Body)
}

func TestSend_RecipientRules(t *testing.T) {
	e := newEnv(t)
	for _, to := range []string{"0811222333", "12", "abc@example.com", ""} {
		_, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: to, Text: "x"})
		require.True(t, message.IsInvalidInputError(err), "%q: got %v", to, err)
	}
	_, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "120363012345678901@g.us", Text: "x"})
	require.NoError(t, err)
	require.Equal(t, "group", e.chats.Ensured[len(e.chats.Ensured)-1].Kind)
}

func TestSend_FetchesURLMediaThroughTheFetcher(t *testing.T) {
	e := newEnv(t)
	m, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Media: &message.MediaInput{URL: "https://cdn.example.com/a.png"}})
	require.NoError(t, err)
	require.Equal(t, []string{"https://cdn.example.com/a.png"}, e.fetcher.URLs)
	require.Equal(t, message.TypeImage, m.Type, "the fetched mime decides the type")
	require.Equal(t, []byte("fetched"), m.Raw)

	e.fetcher.Err = errors.New("dial tcp 10.0.0.1:443: private address")
	_, err = e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Media: &message.MediaInput{URL: "https://internal/a.png"}})
	require.True(t, message.IsMediaFetchError(err), "got %v", err)
}

func TestSend_ReplyToOurIDResolvesItsWhatsAppID(t *testing.T) {
	e := newEnv(t)
	first, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Text: "one"})
	require.NoError(t, err)
	reply, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Text: "two", ReplyTo: first.PublicID})
	require.NoError(t, err)
	require.Equal(t, first.WAMessageID, reply.QuotedWAMessageID)
	raw, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Text: "three", ReplyTo: "3A5F0C1D2E"})
	require.NoError(t, err)
	require.Equal(t, "3A5F0C1D2E", raw.QuotedWAMessageID, "a WhatsApp id passes through")
}

func TestReactRevokeEdit_QueueChildRows(t *testing.T) {
	e := newEnv(t)
	sent, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Text: "hi"})
	require.NoError(t, err)
	sent.MarkSending(time.Now())
	sent.MarkSent(e.now, nil, e.now)
	require.NoError(t, e.store.Save(e.ctx(t), sent, 0))
	e.waker.Woken = nil

	r, err := e.svc.React(e.ctx(t), sent.ID, "👍")
	require.NoError(t, err)
	require.Equal(t, message.TypeReaction, r.Type)
	require.Equal(t, sent.WAMessageID, r.TargetWAMessageID)
	require.Equal(t, sent.ChatID, r.ChatID)

	_, err = e.svc.Edit(e.ctx(t), sent.ID, "hello")
	require.NoError(t, err)
	e.now = e.now.Add(21 * time.Minute)
	_, err = e.svc.Edit(e.ctx(t), sent.ID, "too late")
	require.True(t, message.IsEditWindowClosedError(err))

	_, err = e.svc.Revoke(e.ctx(t), sent.ID)
	require.NoError(t, err)
	require.Len(t, e.waker.Woken, 3, "every child row wakes the sender")

	in := message.NewInbound(message.Ref{DeviceID: e.device, OrgID: e.tc.OrgID, ProjectID: e.tc.ProjectID}, sent.ChatID, fakes.MessagePublicID(), message.InboundInput{WAID: "IN1", Type: "text"})
	e.store.Seed(in)
	_, err = e.svc.Revoke(e.ctx(t), in.ID)
	require.True(t, message.IsNotOwnMessageError(err))
}

func TestGet_SiblingProjectIsNotFound(t *testing.T) {
	e := newEnv(t)
	m, err := message.NewOutbound(e.tc.OrgID, uuid.New(), e.device, uuid.New(), fakes.MessagePublicID(), message.SendInput{Text: "x"}, 1)
	require.NoError(t, err)
	e.store.Seed(m)
	_, err = e.svc.Get(e.ctx(t), m.ID)
	require.True(t, message.IsNotFoundError(err))
}

func TestClaimNext_BuildsTheOutboundWithTargetSender(t *testing.T) {
	e := newEnv(t)
	chat := message.ChatRef{ID: uuid.New(), DeviceID: e.device, JID: "628111@s.whatsapp.net", Kind: "dm"}
	e.chats.Seed(chat)
	in := message.NewInbound(message.Ref{DeviceID: e.device, OrgID: e.tc.OrgID, ProjectID: e.tc.ProjectID}, chat.ID, fakes.MessagePublicID(),
		message.InboundInput{WAID: "IN1", Type: "text", SenderJID: "628111@s.whatsapp.net", Body: "pesan"})
	e.store.Seed(in)
	r, err := message.NewReaction(e.tc.OrgID, e.tc.ProjectID, e.device, chat.ID, fakes.MessagePublicID(), "IN1", "❤️")
	require.NoError(t, err)
	e.store.Seed(r)

	got, err := e.svc.ClaimNext(e.ctx(t), e.device)
	require.NoError(t, err)
	require.Equal(t, r.ID, got.ID)
	require.Equal(t, message.TypeReaction, got.Kind)
	require.Equal(t, "628111@s.whatsapp.net", got.ChatJID)
	require.Equal(t, "628111@s.whatsapp.net", got.TargetSender)
	require.False(t, got.TargetFromMe)
	require.Equal(t, "❤️", got.Text)

	none, err := e.svc.ClaimNext(e.ctx(t), e.device)
	require.NoError(t, err)
	require.Nil(t, none)
}

func TestMarkSentAndMarkFailed_EnqueueStatusInsideTheUnitOfWork(t *testing.T) {
	e := newEnv(t)
	m, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Text: "x"})
	require.NoError(t, err)
	claimed, err := e.svc.ClaimNext(e.ctx(t), e.device)
	require.NoError(t, err)
	require.NoError(t, e.svc.MarkSent(e.ctx(t), claimed.ID, e.now, nil))
	calls := e.hooks.Recorded()
	require.Len(t, calls, 1)
	require.Equal(t, events.MessageStatus, calls[0].Type)
	require.True(t, calls[0].InTx)
	st := calls[0].Data.(events.MessageStatusV1)
	require.Equal(t, "sent", st.Status)
	require.Equal(t, m.WAMessageID, st.Message.WAID)
	require.Equal(t, "sales-01", st.Device.Name)

	f, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Text: "y"})
	require.NoError(t, err)
	_, err = e.svc.ClaimNext(e.ctx(t), e.device)
	require.NoError(t, err)
	require.NoError(t, e.svc.MarkFailed(e.ctx(t), f.ID, "ack_timeout", true))
	require.Len(t, e.hooks.Recorded(), 1, "a retryable failure requeues silently")
	_, err = e.svc.ClaimNext(e.ctx(t), e.device)
	require.NoError(t, err)
	require.NoError(t, e.svc.MarkFailed(e.ctx(t), f.ID, "reachout_timelock", false))
	calls = e.hooks.Recorded()
	require.Len(t, calls, 2)
	failed := calls[1].Data.(events.MessageStatusV1)
	require.Equal(t, "failed", failed.Status)
	require.Equal(t, "reachout_timelock", failed.Error)
}

func TestMarkSent_AfterAReceiptEmitsNothing(t *testing.T) {
	e := newEnv(t)
	m, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Text: "x"})
	require.NoError(t, err)
	_, err = e.svc.ClaimNext(e.ctx(t), e.device)
	require.NoError(t, err)
	ref := message.Ref{DeviceID: e.device, OrgID: e.tc.OrgID, ProjectID: e.tc.ProjectID}
	require.NoError(t, e.svc.RecordReceipt(t.Context(), ref, message.ReceiptInput{WAIDs: []string{m.WAMessageID}, Type: "delivered", At: e.now}))
	require.NoError(t, e.svc.MarkSent(e.ctx(t), m.ID, e.now, nil))
	calls := e.hooks.Recorded()
	require.Len(t, calls, 1, "only the delivered event; a sent after delivered would move the status backwards")
	require.Equal(t, "delivered", calls[0].Data.(events.MessageStatusV1).Status)
}

func TestClaimNext_FailsAnExhaustedStaleRowAndReturnsTheNext(t *testing.T) {
	e := newEnv(t)
	stuck, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Text: "stuck"})
	require.NoError(t, err)
	for _, m := range e.store.All() {
		m.Status, m.Attempts, m.UpdatedAt = message.StatusSending, message.MaxAttempts, e.now.Add(-time.Hour)
		e.store.Seed(m)
	}
	next, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Text: "next"})
	require.NoError(t, err)
	got, err := e.svc.ClaimNext(e.ctx(t), e.device)
	require.NoError(t, err)
	require.Equal(t, next.ID, got.ID)
	calls := e.hooks.Recorded()
	require.Len(t, calls, 1)
	require.Equal(t, stuck.PublicID, calls[0].Data.(events.MessageStatusV1).Message.ID)
	require.Equal(t, "failed", calls[0].Data.(events.MessageStatusV1).Status)
}

func TestMarkRead_PagesPastOneListPage(t *testing.T) {
	e := newEnv(t)
	chat := message.ChatRef{ID: uuid.New(), DeviceID: e.device, JID: "628111@s.whatsapp.net", Kind: "dm"}
	e.chats.Seed(chat)
	ref := message.Ref{DeviceID: e.device, OrgID: e.tc.OrgID, ProjectID: e.tc.ProjectID}
	for i := range message.MaxListLimit + 5 {
		e.store.Seed(message.NewInbound(ref, chat.ID, fakes.MessagePublicID(), message.InboundInput{WAID: fmt.Sprintf("W%03d", i), SenderJID: "628111@s.whatsapp.net", Type: "text", Timestamp: e.now}))
	}
	require.NoError(t, e.svc.MarkRead(e.ctx(t), chat.ID))
	for _, m := range e.store.All() {
		require.NotNil(t, m.ReadAt, m.WAMessageID)
	}
}

func TestRequeue_GivesTheAttemptBackSilently(t *testing.T) {
	e := newEnv(t)
	m, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Text: "x"})
	require.NoError(t, err)
	for range message.MaxAttempts + 1 {
		claimed, err := e.svc.ClaimNext(e.ctx(t), e.device)
		require.NoError(t, err)
		require.NoError(t, e.svc.Requeue(e.ctx(t), claimed.ID))
	}
	got, err := e.store.ByID(e.ctx(t), m.ID)
	require.NoError(t, err)
	require.Equal(t, message.StatusQueued, got.Status, "four interrupted claims never fail a message WhatsApp never saw")
	require.Zero(t, got.Attempts)
	require.Empty(t, e.hooks.Recorded())
}

func TestRecordReceipt_EmitsOncePerTransition(t *testing.T) {
	e := newEnv(t)
	m, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Text: "x"})
	require.NoError(t, err)
	ref := message.Ref{DeviceID: e.device, OrgID: e.tc.OrgID, ProjectID: e.tc.ProjectID}
	rec := message.ReceiptInput{WAIDs: []string{m.WAMessageID, "UNKNOWN"}, Type: "read", At: e.now}
	require.NoError(t, e.svc.RecordReceipt(t.Context(), ref, rec))
	require.NoError(t, e.svc.RecordReceipt(t.Context(), ref, rec))
	require.NoError(t, e.svc.RecordReceipt(t.Context(), ref, message.ReceiptInput{WAIDs: []string{m.WAMessageID}, Type: "sender"}))
	calls := e.hooks.Recorded()
	require.Len(t, calls, 1)
	require.Equal(t, "read", calls[0].Data.(events.MessageStatusV1).Status)
}

func TestMarkRead_GroupsBySenderAndStampsReadAt(t *testing.T) {
	e := newEnv(t)
	chat := message.ChatRef{ID: uuid.New(), DeviceID: e.device, JID: "120363@g.us", Kind: "group"}
	e.chats.Seed(chat)
	ref := message.Ref{DeviceID: e.device, OrgID: e.tc.OrgID, ProjectID: e.tc.ProjectID}
	for _, w := range []struct{ id, sender string }{{"A", "1@lid"}, {"B", "1@lid"}, {"C", "2@lid"}} {
		e.store.Seed(message.NewInbound(ref, chat.ID, fakes.MessagePublicID(), message.InboundInput{WAID: w.id, SenderJID: w.sender, Type: "text"}))
	}
	require.NoError(t, e.svc.MarkRead(e.ctx(t), chat.ID))
	require.Len(t, e.transport.Reads, 2)
	require.Equal(t, []uuid.UUID{chat.ID}, e.chats.Reads)
	for _, m := range e.store.All() {
		require.NotNil(t, m.ReadAt, m.WAMessageID)
	}
}

func TestOpenMedia(t *testing.T) {
	e := newEnv(t)
	text, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Text: "x"})
	require.NoError(t, err)
	_, _, _, err = e.svc.OpenMedia(e.ctx(t), text.ID)
	require.True(t, message.IsNotFoundError(err), "a message without media has no media resource")

	ref := message.Ref{DeviceID: e.device, OrgID: e.tc.OrgID, ProjectID: e.tc.ProjectID}
	img := message.NewInbound(ref, uuid.New(), fakes.MessagePublicID(), message.InboundInput{WAID: "IMG", Type: "image",
		Media: &message.InboundMedia{Mime: "image/jpeg", Keys: message.MediaKeys{DirectPath: "/v/t62"}}})
	e.store.Seed(img)
	f, mime, name, err := e.svc.OpenMedia(e.ctx(t), img.ID)
	require.NoError(t, err)
	defer f.Close()
	body, err := io.ReadAll(f)
	require.NoError(t, err)
	require.Equal(t, "jpeg", string(body))
	require.Equal(t, "image/jpeg", mime)
	require.Equal(t, img.PublicID+".jpg", name)

	huge := message.NewInbound(ref, uuid.New(), fakes.MessagePublicID(), message.InboundInput{WAID: "HUGE", Type: "video",
		Media: &message.InboundMedia{Mime: "video/mp4", Size: 1 << 40, Keys: message.MediaKeys{DirectPath: "/v/t62"}}})
	e.store.Seed(huge)
	_, _, _, err = e.svc.OpenMedia(e.ctx(t), huge.ID)
	require.True(t, message.IsMediaTooLargeError(err), "a forged size is refused before any download")

	ctx, cancel := context.WithCancel(e.ctx(t))
	cancel()
	_, _, _, err = e.svc.OpenMedia(ctx, img.ID)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, e.unexpected.Load(), "a caller that went away is not reported as a server fault")
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
