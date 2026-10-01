package whatsapp_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/whatsapp"
)

func fixedNow() time.Time { return time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC) }

type unexpectedLog struct {
	mu    sync.Mutex
	calls []string
}

func (u *unexpectedLog) fn() apperror.UnexpectedFunc {
	return func(_ context.Context, message string, cause error, _ ...any) *apperror.AppError {
		u.mu.Lock()
		u.calls = append(u.calls, message)
		u.mu.Unlock()
		return apperror.New("GEN900", "unexpected", codes.Internal).WithCause(cause)
	}
}

func (u *unexpectedLog) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.calls)
}

type fakeNames map[uuid.UUID]string

const testDevicePublicID = "dev_V1StGXR8Z5jdHi6B"

func (f fakeNames) Describe(_ context.Context, id uuid.UUID) (string, string, error) {
	if n, ok := f[id]; ok {
		return testDevicePublicID, n, nil
	}
	return "", "", errors.New("fake: no device")
}

type countingUOW struct {
	mu       sync.Mutex
	n        int
	snapshot func() func()
}

func (c *countingUOW) run(ctx context.Context, fn func(ctx context.Context) error) error {
	c.mu.Lock()
	c.n++
	snap := c.snapshot
	c.mu.Unlock()
	var restore func()
	if snap != nil {
		restore = snap()
	}
	err := fakes.UnitOfWork(ctx, fn)
	if err != nil && restore != nil {
		restore()
	}
	return err
}

func (c *countingUOW) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

type sinkFixture struct {
	svc        *whatsapp.Service
	store      *fakes.WhatsAppSessions
	hooks      *fakes.Webhooks
	rt         *fakes.Runtime
	uow        *countingUOW
	unexpected *unexpectedLog
	ref        whatsapp.SessionRef
}

func newSinkFixture(t *testing.T) *sinkFixture {
	t.Helper()
	return newSinkFixtureWith(t, nil, nil)
}

func newSinkFixtureWith(t *testing.T, names fakeNames, wrap func(*fakes.WhatsAppSessions) whatsapp.Store) *sinkFixture {
	t.Helper()
	ref := whatsapp.SessionRef{DeviceID: uuid.New(), OrgID: uuid.New(), ProjectID: uuid.New(), JID: "628111:1@s.whatsapp.net"}
	if names == nil {
		names = fakeNames{ref.DeviceID: "sales-01"}
	}
	f := &sinkFixture{
		store:      fakes.NewWhatsAppSessions(),
		hooks:      &fakes.Webhooks{},
		rt:         fakes.NewRuntime(),
		uow:        &countingUOW{},
		unexpected: &unexpectedLog{},
		ref:        ref,
	}
	f.uow.snapshot = func() func() {
		row, ok := f.store.Row(ref.DeviceID)
		return func() {
			if ok {
				f.store.Seed(&row)
			}
		}
	}
	var store whatsapp.Store = f.store
	if wrap != nil {
		store = wrap(f.store)
	}
	f.svc = whatsapp.NewService(store, slog.New(slog.NewTextHandler(io.Discard, nil)), f.unexpected.fn(),
		f.uow.run, f.rt, f.hooks, names, fixedNow)
	return f
}

func (f *sinkFixture) seed(state whatsapp.State, jid string) {
	s := whatsapp.NewSession(f.ref.DeviceID, f.ref.OrgID, f.ref.ProjectID, whatsapp.EngineWhatsmeow)
	s.State = state
	s.JID = jid
	s.Phone = whatsapp.PhoneFromJID(jid)
	f.store.Seed(s)
}

func TestSink_ConnectedEmitsOnceInsideOneUnitOfWork(t *testing.T) {
	f := newSinkFixture(t)
	f.seed(whatsapp.StateDisconnected, f.ref.JID)

	f.svc.OnState(f.ref, whatsapp.StateConnected, "")

	require.Equal(t, 1, f.uow.count())
	row, ok := f.store.Row(f.ref.DeviceID)
	require.True(t, ok)
	require.Equal(t, whatsapp.StateConnected, row.State)
	calls := f.hooks.Recorded()
	require.Len(t, calls, 1)
	require.Equal(t, events.DeviceConnected, calls[0].Type)
	require.True(t, calls[0].InTx, "the event must be enqueued inside the transition's unit of work")
	require.Equal(t, events.DeviceConnectedV1{
		Device: events.DeviceRefV1{ID: testDevicePublicID, Name: "sales-01", Phone: "628111"},
		State:  "connected",
		At:     fixedNow(),
	}, calls[0].Data)
	require.Zero(t, f.unexpected.count())
}

func TestSink_SameStateCallsWriteNothingAndEmitNothing(t *testing.T) {
	f := newSinkFixture(t)
	f.seed(whatsapp.StateConnected, f.ref.JID)
	before := f.store.SaveCount()

	f.svc.OnState(f.ref, whatsapp.StateConnected, "")

	require.Equal(t, before, f.store.SaveCount())
	require.Empty(t, f.hooks.Recorded())
}

func TestSink_DisconnectedEmitsOnlyFromConnected(t *testing.T) {
	f := newSinkFixture(t)
	f.seed(whatsapp.StateConnected, f.ref.JID)
	f.svc.OnState(f.ref, whatsapp.StateDisconnected, "network")
	calls := f.hooks.Recorded()
	require.Len(t, calls, 1)
	require.Equal(t, events.DeviceDisconnected, calls[0].Type)
	data, ok := calls[0].Data.(events.DeviceDisconnectedV1)
	require.True(t, ok)
	require.Equal(t, "network", data.Reason)

	g := newSinkFixture(t)
	g.seed(whatsapp.StateLinking, "")
	g.svc.OnState(g.ref, whatsapp.StateDisconnected, "lease_lost")
	require.Empty(t, g.hooks.Recorded())
}

func TestSink_LoggedOutPayloadKeepsThePhoneItClears(t *testing.T) {
	f := newSinkFixture(t)
	f.seed(whatsapp.StateConnected, f.ref.JID)
	f.svc.OnState(f.ref, whatsapp.StateLoggedOut, "logged_out_by_phone")
	calls := f.hooks.Recorded()
	require.Len(t, calls, 1)
	data, ok := calls[0].Data.(events.DeviceLoggedOutV1)
	require.True(t, ok)
	require.Equal(t, "628111", data.Device.Phone)
	row, _ := f.store.Row(f.ref.DeviceID)
	require.Empty(t, row.Phone)
}

func TestSink_DegradedOnADisconnectedRowIsAQuietNoOp(t *testing.T) {
	f := newSinkFixture(t)
	f.seed(whatsapp.StateDisconnected, f.ref.JID)
	before := f.store.SaveCount()
	f.svc.OnDegraded(f.ref, errors.New("keepalive timeout"))
	require.Equal(t, before, f.store.SaveCount())
	require.Zero(t, f.unexpected.count(), "a reordered engine event is not an unexpected failure")
}

func TestSink_ConnectedRepairsAMissingIdentityFromTheRef(t *testing.T) {
	f := newSinkFixture(t)
	f.seed(whatsapp.StateLinking, "")

	f.svc.OnState(f.ref, whatsapp.StateConnected, "")

	row, _ := f.store.Row(f.ref.DeviceID)
	require.Equal(t, f.ref.JID, row.JID)
	require.Equal(t, "628111", row.Phone)
	require.Equal(t, whatsapp.StateConnected, row.State)
	calls := f.hooks.Recorded()
	require.Len(t, calls, 1)
	require.Equal(t, events.DeviceConnected, calls[0].Type)
}

func TestSink_OnLinkedSavesIdentityOnly(t *testing.T) {
	f := newSinkFixture(t)
	f.seed(whatsapp.StateLinking, "")
	f.svc.OnLinked(f.ref, whatsapp.Identity{JID: f.ref.JID, LID: "9@lid", PushName: "Ops", Platform: "android"})
	row, _ := f.store.Row(f.ref.DeviceID)
	require.Equal(t, whatsapp.StateDisconnected, row.State)
	require.Equal(t, "9@lid", row.LID)
	require.Empty(t, f.hooks.Recorded())
}

func TestSink_MissingRowIsQuietForEngineEventsButUnexpectedForOnLinked(t *testing.T) {
	f := newSinkFixture(t)
	f.svc.OnState(f.ref, whatsapp.StateConnected, "")
	require.Zero(t, f.unexpected.count())
	f.svc.OnLinked(f.ref, whatsapp.Identity{JID: f.ref.JID})
	require.Equal(t, 1, f.unexpected.count())
}

func TestSink_EnqueueFailureIsReported(t *testing.T) {
	f := newSinkFixture(t)
	f.seed(whatsapp.StateDisconnected, f.ref.JID)
	f.hooks.Err = errors.New("outbox down")
	f.svc.OnState(f.ref, whatsapp.StateConnected, "")
	require.Equal(t, 1, f.unexpected.count())
}

type recordingInbound struct {
	mu       sync.Mutex
	messages []whatsapp.InboundMessage
}

func (r *recordingInbound) RecordInbound(_ context.Context, _ whatsapp.SessionRef, m whatsapp.InboundMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, m)
	return nil
}

func (r *recordingInbound) RecordReceipt(context.Context, whatsapp.SessionRef, whatsapp.Receipt) error {
	return nil
}

func (r *recordingInbound) UpsertContact(context.Context, whatsapp.SessionRef, whatsapp.ContactUpdate) error {
	return nil
}

func TestSink_InboundIsANoOpUntilWired(t *testing.T) {
	f := newSinkFixture(t)
	f.svc.OnMessage(f.ref, whatsapp.InboundMessage{ID: "A"})
	f.svc.OnReceipt(f.ref, whatsapp.Receipt{})
	f.svc.OnContact(f.ref, whatsapp.ContactUpdate{})
	f.svc.OnHistory(f.ref, []whatsapp.InboundMessage{{ID: "B"}})

	in := &recordingInbound{}
	require.NoError(t, f.svc.SetInbound(in))
	f.svc.OnMessage(f.ref, whatsapp.InboundMessage{ID: "C"})
	require.Len(t, in.messages, 1)
	require.Equal(t, "C", in.messages[0].ID)
}

func TestSink_DescribeFailureStillPersistsTheState(t *testing.T) {
	f := newSinkFixtureWith(t, fakeNames{}, nil)
	f.seed(whatsapp.StateDisconnected, f.ref.JID)

	f.svc.OnState(f.ref, whatsapp.StateConnected, "")

	row, _ := f.store.Row(f.ref.DeviceID)
	require.Equal(t, whatsapp.StateConnected, row.State, "naming the device must not discard the transition")
	require.Empty(t, f.hooks.Recorded())
	require.Equal(t, 1, f.unexpected.count(), "the dropped event is reported")
}

type flakyStore struct {
	*fakes.WhatsAppSessions
	mu        sync.Mutex
	staleLeft int
}

func (s *flakyStore) Save(ctx context.Context, sess *whatsapp.Session, ifVersion int) error {
	s.mu.Lock()
	fail := s.staleLeft > 0
	if fail {
		s.staleLeft--
	}
	s.mu.Unlock()
	if fail {
		return &whatsapp.StaleVersionError{Want: ifVersion, Got: ifVersion + 1}
	}
	return s.WhatsAppSessions.Save(ctx, sess, ifVersion)
}

func flaky(n int) func(*fakes.WhatsAppSessions) whatsapp.Store {
	return func(s *fakes.WhatsAppSessions) whatsapp.Store { return &flakyStore{WhatsAppSessions: s, staleLeft: n} }
}

func TestSink_StaleVersionIsRetriedOnce(t *testing.T) {
	f := newSinkFixtureWith(t, nil, flaky(1))
	f.seed(whatsapp.StateConnected, f.ref.JID)

	f.svc.OnState(f.ref, whatsapp.StateDisconnected, "network")

	require.Equal(t, 2, f.uow.count())
	row, _ := f.store.Row(f.ref.DeviceID)
	require.Equal(t, whatsapp.StateDisconnected, row.State)
	require.Len(t, f.hooks.Recorded(), 1, "the failed first attempt must not leave an event behind")
	require.Zero(t, f.unexpected.count())
}

func TestSink_TwoStaleVersionsAreReported(t *testing.T) {
	f := newSinkFixtureWith(t, nil, flaky(2))
	f.seed(whatsapp.StateConnected, f.ref.JID)

	f.svc.OnState(f.ref, whatsapp.StateDisconnected, "network")

	require.Equal(t, 2, f.uow.count())
	require.Equal(t, 1, f.unexpected.count())
}

func TestSink_SameStateEmitsNothing(t *testing.T) {
	for _, st := range []whatsapp.State{whatsapp.StateConnected, whatsapp.StateDisconnected, whatsapp.StateLoggedOut, whatsapp.StateUnlinked, whatsapp.StateLinking} {
		t.Run(string(st), func(t *testing.T) {
			f := newSinkFixture(t)
			jid := f.ref.JID
			if st == whatsapp.StateLoggedOut || st == whatsapp.StateUnlinked || st == whatsapp.StateLinking {
				jid = ""
			}
			f.seed(st, jid)
			before := f.store.SaveCount()

			f.svc.OnState(f.ref, st, "again")

			require.Equal(t, before, f.store.SaveCount())
			require.Empty(t, f.hooks.Recorded())
			require.Zero(t, f.unexpected.count())
		})
	}
}
