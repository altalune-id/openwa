package whatsapp_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/whatsapp"
)

func (f *sinkFixture) ctx(t *testing.T) context.Context {
	t.Helper()
	return tenant.Into(t.Context(), tenant.Context{OrgID: f.ref.OrgID, ProjectID: f.ref.ProjectID})
}

func TestService_LinkInsertsALinkingRowAfterTheRuntimeAccepts(t *testing.T) {
	f := newSinkFixture(t)
	st, err := f.svc.Link(f.ctx(t), f.ref.DeviceID)
	require.NoError(t, err)
	require.Equal(t, whatsapp.OutcomePending, st.Outcome)
	require.Len(t, f.rt.Links, 1)
	require.Equal(t, f.ref.OrgID, f.rt.Links[0].OrgID)
	require.Empty(t, f.rt.Links[0].JID)
	row, ok := f.store.Row(f.ref.DeviceID)
	require.True(t, ok)
	require.Equal(t, whatsapp.StateLinking, row.State)
}

func TestService_LinkRefusesALinkedDeviceWithoutCallingTheRuntime(t *testing.T) {
	f := newSinkFixture(t)
	f.seed(whatsapp.StateDisconnected, f.ref.JID)
	_, err := f.svc.Link(f.ctx(t), f.ref.DeviceID)
	require.True(t, whatsapp.IsAlreadyLinkedError(err), "got %v", err)
	require.Empty(t, f.rt.Links)
}

func TestService_LinkRollsTheRowBackWhenTheRuntimeRefuses(t *testing.T) {
	f := newSinkFixture(t)
	f.rt.LinkFn = func(context.Context, whatsapp.SessionRef) (whatsapp.LinkState, error) {
		return whatsapp.LinkState{}, &whatsapp.NotOwnedError{ID: "x"}
	}
	_, err := f.svc.Link(f.ctx(t), f.ref.DeviceID)
	require.True(t, whatsapp.IsNotOwnedError(err))
	_, ok := f.store.Row(f.ref.DeviceID)
	require.False(t, ok, "a device with no row before the refusal has none after it")

	f.seed(whatsapp.StateLoggedOut, "")
	_, err = f.svc.Link(f.ctx(t), f.ref.DeviceID)
	require.True(t, whatsapp.IsNotOwnedError(err))
	row, _ := f.store.Row(f.ref.DeviceID)
	require.Equal(t, whatsapp.StateLoggedOut, row.State, "a refused link restores the prior state")
}

func TestService_LinkWritesTheRowBeforeTheRuntimeStarts(t *testing.T) {
	f := newSinkFixture(t)
	f.rt.LinkFn = func(context.Context, whatsapp.SessionRef) (whatsapp.LinkState, error) {
		row, ok := f.store.Row(f.ref.DeviceID)
		require.True(t, ok, "the attempt may report OnState(unlinked) at once, so its row must already exist")
		require.Equal(t, whatsapp.StateLinking, row.State)
		return whatsapp.LinkState{Outcome: whatsapp.OutcomePending}, nil
	}
	_, err := f.svc.Link(f.ctx(t), f.ref.DeviceID)
	require.NoError(t, err)
}

func TestService_LinkWithPhoneReachesTheRuntime(t *testing.T) {
	f := newSinkFixture(t)
	st, err := f.svc.LinkWithPhone(f.ctx(t), f.ref.DeviceID, "+62 812-3456")
	require.NoError(t, err)
	require.Equal(t, "ABCD-EFGH", st.PairingCode)
	require.Equal(t, []string{"+62 812-3456"}, f.rt.Phones)
}

func TestService_StatusByDevicesFillsUnlinkedAndLive(t *testing.T) {
	f := newSinkFixture(t)
	f.seed(whatsapp.StateConnected, f.ref.JID)
	f.rt.LiveSet[f.ref.DeviceID] = true
	missing := uuid.New()
	got, err := f.svc.StatusByDevices(f.ctx(t), []uuid.UUID{f.ref.DeviceID, missing})
	require.NoError(t, err)
	require.Equal(t, whatsapp.StateConnected, got[f.ref.DeviceID].State)
	require.True(t, got[f.ref.DeviceID].Live)
	require.Equal(t, "628111", got[f.ref.DeviceID].Phone)
	require.Equal(t, whatsapp.StateUnlinked, got[missing].State)
	require.False(t, got[missing].Live)
}

func TestService_UnlinkIsIdempotentOnAnUnlinkedDevice(t *testing.T) {
	f := newSinkFixture(t)
	f.rt.UnlinkErr = &whatsapp.NotOwnedError{ID: "x"}
	require.NoError(t, f.svc.Unlink(f.ctx(t), f.ref.DeviceID))

	f.seed(whatsapp.StateDisconnected, f.ref.JID)
	require.True(t, whatsapp.IsNotOwnedError(f.svc.Unlink(f.ctx(t), f.ref.DeviceID)),
		"a linked device held by no process here must not report success")
}

func TestService_ForgetPassesTheJIDThenDeletesTheRow(t *testing.T) {
	f := newSinkFixture(t)
	f.seed(whatsapp.StateConnected, f.ref.JID)
	require.NoError(t, f.svc.Forget(f.ctx(t), f.ref.DeviceID))
	require.Len(t, f.rt.Forgets, 1)
	require.Equal(t, f.ref.JID, f.rt.Forgets[0].JID)
	_, ok := f.store.Row(f.ref.DeviceID)
	require.False(t, ok)
}

func TestService_ForgetFailureKeepsTheRow(t *testing.T) {
	f := newSinkFixture(t)
	f.seed(whatsapp.StateConnected, f.ref.JID)
	f.rt.ForgetErr = &whatsapp.EngineError{Op: "purge", Err: errors.New("db down")}
	require.True(t, whatsapp.IsEngineError(f.svc.Forget(f.ctx(t), f.ref.DeviceID)))
	_, ok := f.store.Row(f.ref.DeviceID)
	require.True(t, ok)
}

func TestService_ForgetWithoutARowStillPurges(t *testing.T) {
	f := newSinkFixture(t)
	require.NoError(t, f.svc.Forget(f.ctx(t), f.ref.DeviceID))
	require.Len(t, f.rt.Forgets, 1)
	require.Empty(t, f.rt.Forgets[0].JID)
}

func TestService_LinkOnALinkingRowRestoresNothingWhenTheRuntimeRefuses(t *testing.T) {
	f := newSinkFixture(t)
	f.seed(whatsapp.StateLinking, "")
	before, _ := f.store.Row(f.ref.DeviceID)
	saves := f.store.SaveCount()
	f.rt.LinkFn = func(context.Context, whatsapp.SessionRef) (whatsapp.LinkState, error) {
		return whatsapp.LinkState{}, &whatsapp.NotOwnedError{ID: "x"}
	}

	_, err := f.svc.Link(f.ctx(t), f.ref.DeviceID)

	require.True(t, whatsapp.IsNotOwnedError(err))
	after, ok := f.store.Row(f.ref.DeviceID)
	require.True(t, ok)
	require.Equal(t, before.Version, after.Version, "a link that wrote nothing must roll back nothing")
	require.Equal(t, saves, f.store.SaveCount())
}

func TestService_LinkRollbackLeavesANewerStateAlone(t *testing.T) {
	f := newSinkFixture(t)
	f.seed(whatsapp.StateLoggedOut, "")
	f.rt.LinkFn = func(ctx context.Context, _ whatsapp.SessionRef) (whatsapp.LinkState, error) {
		row, err := f.store.ByDevice(f.ctx(t), f.ref.DeviceID)
		require.NoError(t, err)
		_, _ = row.Connected()
		require.NoError(t, f.store.Save(f.ctx(t), row, row.Version))
		return whatsapp.LinkState{}, &whatsapp.NotOwnedError{ID: "x"}
	}

	_, err := f.svc.Link(f.ctx(t), f.ref.DeviceID)

	require.True(t, whatsapp.IsNotOwnedError(err))
	row, _ := f.store.Row(f.ref.DeviceID)
	require.Equal(t, whatsapp.StateConnected, row.State, "a refused link must not clobber a state the engine reported since")
}

type blindFirstRead struct {
	*fakes.WhatsAppSessions
	mu    sync.Mutex
	reads int
}

func (s *blindFirstRead) ByDevice(ctx context.Context, id uuid.UUID) (*whatsapp.Session, error) {
	s.mu.Lock()
	s.reads++
	first := s.reads == 1
	s.mu.Unlock()
	if first {
		return nil, &whatsapp.SessionNotFoundError{ID: id.String()}
	}
	return s.WhatsAppSessions.ByDevice(ctx, id)
}

func TestService_ConcurrentLinkThatLandsARowFirstIsATypedConflict(t *testing.T) {
	f := newSinkFixtureWith(t, nil, func(s *fakes.WhatsAppSessions) whatsapp.Store { return &blindFirstRead{WhatsAppSessions: s} })
	f.seed(whatsapp.StateDisconnected, f.ref.JID)

	_, err := f.svc.Link(f.ctx(t), f.ref.DeviceID)

	require.True(t, whatsapp.IsAlreadyLinkedError(err), "got %v", err)
	require.Zero(t, f.unexpected.count(), "a lost race is a conflict, not a 500")
	require.Empty(t, f.rt.Links)
}

func TestService_ConcurrentLinkLosingTheVersionRaceIsATypedConflict(t *testing.T) {
	f := newSinkFixtureWith(t, nil, flaky(1))
	f.seed(whatsapp.StateLoggedOut, "")

	_, err := f.svc.Link(f.ctx(t), f.ref.DeviceID)

	require.True(t, whatsapp.IsAlreadyLinkedError(err), "got %v", err)
	require.Zero(t, f.unexpected.count())
	require.Empty(t, f.rt.Links)
}
