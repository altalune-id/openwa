package message_test

import (
	"context"
	"errors"
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
	"altalune.id/openwa/internal/testutil/fakes"
)

func TestRecordInbound_RetriesOnceOnAPublicIDCollision(t *testing.T) {
	e := newEnv(t)
	var calls atomic.Int32
	var ids []string
	e.store.InsertInboundFn = func(_ context.Context, m *message.Message) (bool, error) {
		ids = append(ids, m.PublicID)
		if calls.Add(1) == 1 {
			return false, &message.PublicIDTakenError{PublicID: m.PublicID}
		}
		return true, nil
	}
	require.NoError(t, e.svc.Recorder().RecordInbound(t.Context(), e.ref(), dm("WA1")))
	require.Equal(t, int32(2), calls.Load())
	require.Len(t, ids, 2)
	require.NotEqual(t, ids[0], ids[1], "the retry mints a fresh public id")
	require.Len(t, e.hooks.Recorded(), 1, "the failed first attempt emitted nothing that survived")
	require.Zero(t, e.unexpected.Load())
}

func TestRecordInbound_ASecondCollisionSurfaces(t *testing.T) {
	e := newEnv(t)
	var calls atomic.Int32
	e.store.InsertInboundFn = func(_ context.Context, m *message.Message) (bool, error) {
		calls.Add(1)
		return false, &message.PublicIDTakenError{PublicID: m.PublicID}
	}
	err := e.svc.Recorder().RecordInbound(t.Context(), e.ref(), dm("WA1"))
	require.True(t, message.IsPublicIDTakenError(err), "got %v", err)
	require.Equal(t, int32(2), calls.Load(), "exactly one retry, never a loop")
	require.Empty(t, e.store.All())
	require.Empty(t, e.hooks.Recorded())
	require.Zero(t, e.unexpected.Load())
}

func TestRecordInbound_WebhookFailureIsReported(t *testing.T) {
	e := newEnv(t)
	e.hooks.Err = errors.New("outbox down")
	require.Error(t, e.svc.Recorder().RecordInbound(t.Context(), e.ref(), dm("WA1")))
	require.Equal(t, int32(1), e.unexpected.Load())
}

func TestRecordInbound_LIDOnlyChatIsKeyedByTheLID(t *testing.T) {
	e := newEnv(t)
	in := message.InboundInput{WAID: "L1", ChatJID: "77@lid", SenderJID: "77@lid", Type: "text", Body: "x"}
	require.NoError(t, e.svc.Recorder().RecordInbound(t.Context(), e.ref(), in))
	require.Equal(t, "77@lid", e.chats.Ensured[0].JID)
	require.Equal(t, "77@lid", e.contacts.Calls[0].JID)
}

func TestSend_RetriesOnceOnAPublicIDCollision(t *testing.T) {
	e := newEnv(t)
	var calls atomic.Int32
	e.store.SaveFn = func(_ context.Context, m *message.Message, _ int) error {
		if calls.Add(1) == 1 {
			return &message.PublicIDTakenError{PublicID: m.PublicID}
		}
		return nil
	}
	_, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Text: "x"})
	require.NoError(t, err)
	require.Equal(t, int32(2), calls.Load())
	require.Zero(t, e.unexpected.Load())
}

func TestBareIDMethods_RefuseASiblingProject(t *testing.T) {
	e := newEnv(t)
	m, err := message.NewOutbound(e.tc.OrgID, uuid.New(), e.device, uuid.New(), fakes.MessagePublicID(), message.SendInput{Text: "x"}, 1)
	require.NoError(t, err)
	e.store.Seed(m)
	ctx := e.ctx(t)
	_, err = e.svc.React(ctx, m.ID, "x")
	require.True(t, message.IsNotFoundError(err))
	_, err = e.svc.Revoke(ctx, m.ID)
	require.True(t, message.IsNotFoundError(err))
	_, err = e.svc.Edit(ctx, m.ID, "x")
	require.True(t, message.IsNotFoundError(err))
	require.True(t, message.IsNotFoundError(e.svc.MarkSent(ctx, m.ID, e.now, nil)))
	require.True(t, message.IsNotFoundError(e.svc.MarkFailed(ctx, m.ID, "", false)))
	require.True(t, message.IsNotFoundError(e.svc.Requeue(ctx, m.ID)))
	_, _, _, err = e.svc.OpenMedia(ctx, m.ID)
	require.True(t, message.IsNotFoundError(err))
	_, err = e.svc.Resolve(ctx, m.PublicID)
	require.True(t, message.IsNotFoundError(err))
}

func TestResolve_MalformedAndOwn(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Resolve(e.ctx(t), "nope")
	require.True(t, message.IsNotFoundError(err))
	m, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Text: "x"})
	require.NoError(t, err)
	got, err := e.svc.Resolve(e.ctx(t), m.PublicID)
	require.NoError(t, err)
	require.Equal(t, m.ID, got.ID)
}

func TestListCountAndLookup(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Text: "x"})
	require.NoError(t, err)
	rows, _, err := e.svc.List(e.ctx(t), message.ListOpts{})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	n, err := e.svc.CountToday(e.ctx(t))
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	e.transport.OnWA = map[string]string{"628111222333": "628111222333@s.whatsapp.net"}
	got, err := e.svc.IsOnWhatsApp(e.ctx(t), e.device, []string{"628111222333"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	_, err = e.svc.IsOnWhatsApp(e.ctx(t), uuid.New(), nil)
	require.True(t, message.IsNotFoundError(err))
}

func TestMarkFailed_BlankReasonBecomesUnknown(t *testing.T) {
	e := newEnv(t)
	m, err := e.svc.Send(e.ctx(t), e.device, message.SendInput{To: "628111222333", Text: "x"})
	require.NoError(t, err)
	_, err = e.svc.ClaimNext(e.ctx(t), e.device)
	require.NoError(t, err)
	require.NoError(t, e.svc.MarkFailed(e.ctx(t), m.ID, "", false))
	got, err := e.store.ByID(e.ctx(t), m.ID)
	require.NoError(t, err)
	require.Equal(t, "unknown", got.Error)
}

func TestMediaHelpers(t *testing.T) {
	require.Equal(t, "https://h/api/v1/orgs/o/projects/p/messages/msg_1/media", message.MediaURL("https://h/", "o", "p", "msg_1"))
	require.Equal(t, "a.pdf", message.MediaFilename(&message.Message{PublicID: "msg_1", Media: &message.Media{Filename: "a.pdf"}}))
	require.Equal(t, "msg_1.ogg", message.MediaFilename(&message.Message{PublicID: "msg_1", Media: &message.Media{Mime: "audio/ogg; codecs=opus"}}))
	require.Equal(t, "msg_1", message.MediaFilename(&message.Message{PublicID: "msg_1"}))
}

func TestWAMedia_OpensThroughTheTransport(t *testing.T) {
	tr := &fakes.Transport{FetchBody: []byte("x")}
	w := message.NewWAMedia(tr, 0, 0)
	f, mime, err := w.Open(t.Context(), mediaRow())
	require.NoError(t, err)
	_ = f.Close()
	require.Equal(t, "image/png", mime)
	_, err = w.Put(t.Context(), mediaRow(), nil)
	require.NoError(t, err)
	tr.Err = errors.New("boom")
	_, _, err = w.Open(t.Context(), mediaRow())
	require.Error(t, err)
}

type failingMatch struct{ *fakes.MessageDevices }

func (failingMatch) Match(context.Context, uuid.UUID, message.MatchInput) (bool, []string, error) {
	return false, nil, errors.New("rules engine down")
}

func TestRecordInbound_RulesErrorKeepsTheMessageAndItsReceivedEvent(t *testing.T) {
	e := newEnv(t)
	svc := message.NewService(e.store, slog.New(slog.NewTextHandler(io.Discard, nil)), func(_ context.Context, msg string, cause error, _ ...any) *apperror.AppError {
		e.unexpected.Add(1)
		return apperror.New("GEN900", msg, codes.Internal).WithCause(cause)
	}, fakes.UnitOfWork, message.Deps{
		Devices: failingMatch{e.devices}, Chats: e.chats, Contacts: e.contacts, Transport: e.transport,
		Media: fakes.MediaStore{Transport: e.transport}, Fetcher: e.fetcher, Waker: e.waker, Webhooks: e.hooks, Tenants: e.tenants,
	}, message.Options{BaseURL: "https://wa.example.com", MaxMediaBytes: 1 << 20, Clock: func() time.Time { return e.now }})
	require.NoError(t, svc.Recorder().RecordInbound(t.Context(), e.ref(), dm("WA1")))
	require.Len(t, e.store.All(), 1, "the delivered message is never lost")
	calls := e.hooks.Recorded()
	require.Len(t, calls, 1)
	require.Equal(t, events.MessageReceived, calls[0].Type)
	require.Nil(t, calls[0].Data.(events.MessageEventV1).Matched)
	require.Zero(t, e.unexpected.Load())
}

func TestClaimNext_StopsWhenTheContextIsDone(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(e.ctx(t))
	cancel()
	_, err := e.svc.ClaimNext(ctx, e.device)
	require.ErrorIs(t, err, context.Canceled)
}
