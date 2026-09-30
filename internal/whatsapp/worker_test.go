package whatsapp_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/publicid"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/whatsapp"
)

const linkedJID = "628111:1@s.whatsapp.net"

type rtFixture struct {
	rt     *whatsapp.Runtime
	eng    *fakes.Engine
	leases *fakes.LeaseStore
	sink   *fakes.Sink
	cfg    whatsapp.RuntimeConfig
	org    uuid.UUID
	proj   uuid.UUID
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newRuntimeFixture() *rtFixture {
	f := &rtFixture{
		eng:    fakes.NewEngine(),
		leases: fakes.NewLeaseStore(),
		sink:   &fakes.Sink{},
		cfg:    whatsapp.RuntimeConfig{Owner: "me", LeaseTTL: 45 * time.Second, LeaseInterval: 15 * time.Second, LinkTimeout: 3 * time.Minute},
		org:    uuid.New(),
		proj:   uuid.New(),
	}
	f.rt = whatsapp.NewRuntime(f.eng, f.leases, f.cfg, discard())
	f.rt.Subscribe(f.sink)
	return f
}

func (f *rtFixture) start(t *testing.T) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() { errc <- f.rt.Run(ctx) }()
	synctest.Wait()
	return func() {
		cancel()
		require.NoError(t, <-errc, "Run must return nil on cancel")
	}
}

func (f *rtFixture) freeLease() uuid.UUID {
	id := uuid.New()
	f.leases.Seed(whatsapp.Lease{DeviceID: id, OrgID: f.org, ProjectID: f.proj, JID: linkedJID})
	return id
}

func (f *rtFixture) ref(id uuid.UUID) whatsapp.SessionRef {
	return whatsapp.SessionRef{DeviceID: id, OrgID: f.org, ProjectID: f.proj}
}

func codeEvent() whatsapp.LinkEvent {
	return whatsapp.LinkEvent{Kind: whatsapp.LinkEventCode, Code: "2@abc,def", Expires: time.Now().Add(60 * time.Second)}
}

func TestRuntime_NameAndLinkBeforeRun(t *testing.T) {
	f := newRuntimeFixture()
	require.Equal(t, "whatsapp.runtime", f.rt.Name())
	_, err := f.rt.Link(t.Context(), f.ref(uuid.New()))
	require.True(t, whatsapp.IsNotOwnedError(err), "a runtime that is not running owns nothing, got %v", err)
	require.Equal(t, whatsapp.OutcomeNone, f.rt.LinkState(uuid.New()).Outcome)
}

func TestRuntime_ZeroDurationsTakeTheirDefaults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		f.rt = whatsapp.NewRuntime(f.eng, f.leases, whatsapp.RuntimeConfig{Owner: "me"}, discard())
		id := f.freeLease()
		stop := f.start(t)
		defer stop()

		require.True(t, f.rt.Live(id), "a zero LeaseInterval must not panic time.NewTicker")
		f.leases.Steal(id, "other")
		time.Sleep(15 * time.Second)
		synctest.Wait()
		require.False(t, f.rt.Live(id), "the default interval is 15s")
	})
}

func TestRuntime_ClaimOpensOnceAndALiveDeviceIsNeverReopened(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		id := f.freeLease()
		stop := f.start(t)
		defer stop()

		require.Equal(t, 1, f.eng.Opened(id))
		require.True(t, f.rt.Live(id))
		sess, ok := f.rt.Session(id)
		require.True(t, ok)
		require.NotNil(t, sess)

		l, _ := f.leases.Get(id)
		f.leases.SetClaimFn(func(context.Context, string, time.Duration, int) ([]whatsapp.Lease, error) {
			return []whatsapp.Lease{l}, nil
		})
		time.Sleep(3 * f.cfg.LeaseInterval)
		synctest.Wait()
		require.Equal(t, 1, f.eng.Opened(id), "a held device must not be opened a second time")
	})
}

func TestRuntime_ASlowOpenNeverDelaysRenew(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		slow := f.freeLease()
		fast := f.freeLease()
		f.eng.SetOpenHook(func(ctx context.Context, ref whatsapp.SessionRef) error {
			if ref.DeviceID != slow {
				return nil
			}
			<-ctx.Done()
			return ctx.Err()
		})
		stop := f.start(t)
		defer stop()

		require.True(t, f.rt.Live(fast), "a slow open does not hold up its neighbours")
		require.Equal(t, 1, f.leases.Renews())

		time.Sleep(f.cfg.LeaseInterval)
		synctest.Wait()
		require.Equal(t, 2, f.leases.Renews(), "renew runs on schedule while an open is still in flight")
		require.Zero(t, f.eng.Opened(slow))

		time.Sleep(f.cfg.LeaseInterval)
		synctest.Wait()
		require.Equal(t, []string{"disconnected:open_failed: context deadline exceeded"}, f.sink.States(slow), "openBudget bounds every open")
	})
}

func TestRuntime_RenewLossClosesHeldSessionsButNeverALinkingAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		held := f.freeLease()
		f.eng.SetLinkScript(codeEvent())
		stop := f.start(t)
		defer stop()

		linking := uuid.New()
		_, err := f.rt.Link(t.Context(), f.ref(linking))
		require.NoError(t, err)

		f.leases.Steal(held, "other")
		time.Sleep(f.cfg.LeaseInterval)
		synctest.Wait()

		require.True(t, f.eng.Last(held).Closed())
		require.False(t, f.rt.Live(held))
		require.Contains(t, f.sink.States(held), "disconnected:lease_lost")
		require.False(t, f.eng.Last(linking).Closed())
		require.Equal(t, whatsapp.OutcomePending, f.rt.LinkState(linking).Outcome)
	})
}

func TestRuntime_RenewFailingPastTheFenceClosesEverySessionAndStopsClaiming(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		id := f.freeLease()
		stop := f.start(t)
		defer stop()
		require.True(t, f.rt.Live(id))

		f.leases.SetRenewErr(errors.New("database unreachable"))
		late := f.freeLease()
		time.Sleep(f.cfg.LeaseInterval)
		synctest.Wait()
		require.True(t, f.rt.Live(id), "15s without a renew is inside the LeaseTTL - LeaseInterval window")

		time.Sleep(f.cfg.LeaseInterval)
		synctest.Wait()
		require.False(t, f.rt.Live(id))
		require.True(t, f.eng.Last(id).Closed())
		require.Equal(t, []string{"disconnected:lease_unverified"}, f.sink.States(id))
		require.Zero(t, f.eng.OpenCalls(late), "a fenced runtime claims nothing")

		time.Sleep(f.cfg.LeaseInterval)
		synctest.Wait()
		require.Equal(t, []string{"disconnected:lease_unverified"}, f.sink.States(id), "the fence reports once")

		f.leases.SetRenewErr(nil)
		time.Sleep(f.cfg.LeaseInterval)
		synctest.Wait()
		require.Equal(t, 1, f.eng.Opened(late), "claiming resumes once renew succeeds")
		require.Equal(t, 2, f.eng.Opened(id), "a fenced lease that is still ours reopens")
		require.True(t, f.rt.Live(id))
	})
}

func TestRuntime_AHungRenewStillFencesOnTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		id := f.freeLease()
		stop := f.start(t)
		defer stop()
		require.True(t, f.rt.Live(id))

		f.leases.SetRenewHook(func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})
		time.Sleep(2 * f.cfg.LeaseInterval)
		synctest.Wait()

		require.False(t, f.rt.Live(id), "a Renew that never answers is bounded by LeaseInterval, then counted as silence")
		require.True(t, f.eng.Last(id).Closed())
		require.Equal(t, []string{"disconnected:lease_unverified"}, f.sink.States(id))
		f.leases.SetRenewHook(nil)
	})
}

func TestRuntime_LinkSuccessWritesTheLeaseAndPromotesTheAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		f.eng.SetLinkScript(codeEvent())
		stop := f.start(t)
		defer stop()

		id := uuid.New()
		st, err := f.rt.Link(t.Context(), f.ref(id))
		require.NoError(t, err)
		require.Equal(t, whatsapp.OutcomePending, st.Outcome)
		require.Equal(t, whatsapp.LinkMethodQR, st.Method)
		require.Equal(t, "2@abc,def", st.QR)
		require.NotEmpty(t, st.PNG)
		require.False(t, st.StartedAt.IsZero())
		require.True(t, publicid.Valid(whatsapp.LinkIDPrefix, st.ID), "the attempt carries a lnk_ public id, got %q", st.ID)

		sess := f.eng.Last(id)
		sess.Sink.OnLinked(sess.Ref, whatsapp.Identity{JID: "628222:3@s.whatsapp.net", PushName: "Ops"})
		synctest.Wait()

		l, ok := f.leases.Get(id)
		require.True(t, ok)
		require.Equal(t, "me", l.Owner)
		require.Equal(t, "628222:3@s.whatsapp.net", l.JID)
		require.NotNil(t, l.ExpiresAt)
		require.Equal(t, whatsapp.OutcomeConnected, f.rt.LinkState(id).Outcome)
		_, held := f.rt.Session(id)
		require.True(t, held)

		var linked []fakes.SinkCall
		for _, c := range f.sink.Calls() {
			if c.Kind == "linked" {
				linked = append(linked, c)
			}
		}
		require.Len(t, linked, 1)
		require.Equal(t, "628222:3@s.whatsapp.net", linked[0].Ref.JID)

		_, err = f.rt.Link(t.Context(), f.ref(id))
		require.True(t, whatsapp.IsAlreadyLinkedError(err))

		time.Sleep(f.cfg.LinkTimeout + time.Second)
		synctest.Wait()
		require.Equal(t, whatsapp.OutcomeNone, f.rt.LinkState(id).Outcome, "the terminal state is dropped after LinkTimeout")
		require.False(t, sess.Closed(), "the promoted session stays open")
		require.Equal(t, 1, f.eng.Opened(id), "the promoted lease is renewed, never reopened")
	})
}

func TestRuntime_PairingAfterTheAttemptEndedWritesNoLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		f.eng.SetLinkScript(codeEvent())
		stop := f.start(t)
		defer stop()

		id := uuid.New()
		_, err := f.rt.Link(t.Context(), f.ref(id))
		require.NoError(t, err)
		time.Sleep(f.cfg.LinkTimeout)
		synctest.Wait()
		require.Equal(t, whatsapp.OutcomeTimeout, f.rt.LinkState(id).Outcome)

		sess := f.eng.Last(id)
		sess.Sink.OnLinked(sess.Ref, whatsapp.Identity{JID: "628222:3@s.whatsapp.net"})
		synctest.Wait()

		_, ok := f.leases.Get(id)
		require.False(t, ok, "a PairSuccess that lost the race to the timeout writes no lease")
		_, held := f.rt.Session(id)
		require.False(t, held)
		require.Len(t, f.eng.Purged(), 1, "the engine's orphaned credentials are purged")
		for _, c := range f.sink.Calls() {
			require.NotEqual(t, "linked", c.Kind)
		}
	})
}

func TestRuntime_LinkTimeoutLeavesTheDeviceUnlinked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		f.eng.SetLinkScript(codeEvent())
		stop := f.start(t)
		defer stop()

		id := uuid.New()
		_, err := f.rt.Link(t.Context(), f.ref(id))
		require.NoError(t, err)
		time.Sleep(f.cfg.LinkTimeout)
		synctest.Wait()

		require.True(t, f.eng.Last(id).Closed())
		require.Equal(t, []string{"unlinked:link_timeout"}, f.sink.States(id))
		require.Equal(t, whatsapp.OutcomeTimeout, f.rt.LinkState(id).Outcome)
		_, ok := f.leases.Get(id)
		require.False(t, ok, "a link that never paired writes no lease")

		st, err := f.rt.Link(t.Context(), f.ref(id))
		require.NoError(t, err, "a terminal attempt does not block a fresh one")
		require.Equal(t, whatsapp.OutcomePending, st.Outcome)
		require.Equal(t, 2, f.eng.Opened(id))
	})
}

func TestRuntime_EngineTimeoutAndErrorEndTheAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		f.eng.SetLinkScript(codeEvent())
		stop := f.start(t)
		defer stop()

		a, b := uuid.New(), uuid.New()
		_, err := f.rt.Link(t.Context(), f.ref(a))
		require.NoError(t, err)
		_, err = f.rt.Link(t.Context(), f.ref(b))
		require.NoError(t, err)
		f.eng.Last(a).LinkCh <- whatsapp.LinkEvent{Kind: whatsapp.LinkEventTimeout}
		f.eng.Last(b).LinkCh <- whatsapp.LinkEvent{Kind: whatsapp.LinkEventUnsupported, Err: &whatsapp.UnsupportedError{Feature: "passkey pairing"}}
		synctest.Wait()

		require.Equal(t, whatsapp.OutcomeTimeout, f.rt.LinkState(a).Outcome)
		require.Equal(t, []string{"unlinked:qr_timeout"}, f.sink.States(a))
		sb := f.rt.LinkState(b)
		require.Equal(t, whatsapp.OutcomeFailed, sb.Outcome)
		require.True(t, whatsapp.IsUnsupportedError(sb.Err))
		require.Equal(t, []string{"unlinked:unsupported"}, f.sink.States(b))
	})
}

func TestRuntime_LinkRefusesWhenALeaseRowExists(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		id := uuid.New()
		exp := time.Now().Add(time.Hour)
		f.leases.Seed(whatsapp.Lease{DeviceID: id, OrgID: f.org, ProjectID: f.proj, JID: linkedJID, Owner: "other", ExpiresAt: &exp})
		stop := f.start(t)
		defer stop()

		_, err := f.rt.Link(t.Context(), f.ref(id))
		require.True(t, whatsapp.IsAlreadyLinkedError(err), "got %v", err)
		require.Zero(t, f.eng.OpenCalls(id))
	})
}

func TestRuntime_AFailedOpenIsRetriedAndReportedOncePerReason(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		id := f.freeLease()
		var failures atomic.Int32
		failures.Store(2)
		f.eng.SetOpenHook(func(context.Context, whatsapp.SessionRef) error {
			if failures.Add(-1) >= 0 {
				return errors.New("boom")
			}
			return nil
		})
		stop := f.start(t)
		defer stop()

		require.Equal(t, []string{"disconnected:open_failed: boom"}, f.sink.States(id))
		require.Zero(t, f.eng.Opened(id))
		l, _ := f.leases.Get(id)
		require.Equal(t, "me", l.Owner, "the lease stays held across a failed open")

		time.Sleep(f.cfg.LeaseInterval)
		synctest.Wait()
		require.Equal(t, 2, f.eng.OpenCalls(id))
		require.Equal(t, []string{"disconnected:open_failed: boom"}, f.sink.States(id), "the same failure is not re-reported every tick")

		time.Sleep(f.cfg.LeaseInterval)
		synctest.Wait()
		require.Equal(t, 1, f.eng.Opened(id))
		require.True(t, f.rt.Live(id))
	})
}

func TestRuntime_SessionGoneDeletesTheLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		id := f.freeLease()
		f.eng.SetOpenHook(func(_ context.Context, ref whatsapp.SessionRef) error {
			return &whatsapp.SessionGoneError{ID: ref.DeviceID.String()}
		})
		stop := f.start(t)
		defer stop()

		_, ok := f.leases.Get(id)
		require.False(t, ok)
		require.Len(t, f.eng.Purged(), 1)
		require.Equal(t, []string{"logged_out:device_missing"}, f.sink.States(id))
	})
}

func TestRuntime_PermanentDisconnectsParkTheDevice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		replaced := f.freeLease()
		outdated := f.freeLease()
		stop := f.start(t)
		defer stop()

		s1, s2 := f.eng.Last(replaced), f.eng.Last(outdated)
		s1.Sink.OnState(s1.Ref, whatsapp.StateDisconnected, whatsapp.ReasonStreamReplaced)
		s2.Sink.OnState(s2.Ref, whatsapp.StateDisconnected, whatsapp.ReasonClientOutdated)
		synctest.Wait()

		require.True(t, s1.Closed())
		require.True(t, s2.Closed())
		require.Equal(t, []string{"disconnected:stream_replaced"}, f.sink.States(replaced))
		l, _ := f.leases.Get(replaced)
		require.Equal(t, "me", l.Owner, "a parked device keeps its lease")

		time.Sleep(2 * f.cfg.LeaseInterval)
		synctest.Wait()
		require.Equal(t, 1, f.eng.Opened(replaced), "parked: no reopen")

		time.Sleep(5 * f.cfg.LeaseTTL)
		synctest.Wait()
		require.Equal(t, 2, f.eng.Opened(replaced), "reopened once ParkFor (5 x LeaseTTL) elapsed")
		require.Equal(t, 1, f.eng.Opened(outdated), "client_outdated stays parked until restart")
	})
}

func TestRuntime_LoggedOutFromThePhoneDeletesTheLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		id := f.freeLease()
		stop := f.start(t)
		defer stop()

		s := f.eng.Last(id)
		s.Sink.OnState(s.Ref, whatsapp.StateLoggedOut, whatsapp.ReasonLoggedOutByPhone)
		synctest.Wait()

		require.True(t, s.Closed())
		_, ok := f.leases.Get(id)
		require.False(t, ok)
		require.Len(t, f.eng.Purged(), 1)
		require.Equal(t, []string{"logged_out:logged_out_by_phone"}, f.sink.States(id))
		require.False(t, f.rt.Live(id))
	})
}

func TestRuntime_ShutdownClosesEverythingAndReleasesEveryLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		held := f.freeLease()
		parked := f.freeLease()
		f.eng.SetLinkScript(codeEvent())
		stop := f.start(t)

		linking := uuid.New()
		_, err := f.rt.Link(t.Context(), f.ref(linking))
		require.NoError(t, err)
		sp := f.eng.Last(parked)
		sp.Sink.OnState(sp.Ref, whatsapp.StateDisconnected, whatsapp.ReasonStreamReplaced)
		synctest.Wait()

		stop()

		require.True(t, f.eng.Last(held).Closed())
		require.True(t, f.eng.Last(linking).Closed())
		require.Equal(t, []string{"unlinked:link_cancelled"}, f.sink.States(linking), "shutdown cancels an attempt; it did not time out")
		for _, id := range []uuid.UUID{held, parked} {
			l, ok := f.leases.Get(id)
			require.True(t, ok)
			require.Empty(t, l.Owner, "ReleaseAll must free %s so another replica need not wait out the TTL", id)
			require.Nil(t, l.ExpiresAt)
		}
	})
}

func TestRuntime_ShutdownReleasesLeasesWhenAWorkerOverrunsTheBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		f.sink.Gate = make(chan struct{})
		stuck := f.freeLease()
		other := f.freeLease()
		stop := f.start(t)

		s := f.eng.Last(stuck)
		s.Sink.OnState(s.Ref, whatsapp.StateLoggedOut, whatsapp.ReasonLoggedOutByPhone)
		synctest.Wait()

		stop()

		l, ok := f.leases.Get(other)
		require.True(t, ok)
		require.Empty(t, l.Owner, "ReleaseAll runs on its own context after the bounded wait")
		close(f.sink.Gate)
		synctest.Wait()
	})
}

func TestRuntime_LinkWithPhoneReusesTheAttemptAndItsCode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		f.eng.SetLinkScript(codeEvent())
		stop := f.start(t)
		defer stop()

		id := uuid.New()
		_, err := f.rt.Link(t.Context(), f.ref(id))
		require.NoError(t, err)
		first, err := f.rt.LinkWithPhone(t.Context(), f.ref(id), "+62 812-3456")
		require.NoError(t, err)
		second, err := f.rt.LinkWithPhone(t.Context(), f.ref(id), "+62 812-3456")
		require.NoError(t, err)

		require.Equal(t, "ABCD-EFGH", first.PairingCode)
		require.Equal(t, first.PairingCode, second.PairingCode)
		require.Equal(t, whatsapp.LinkMethodPhone, second.Method)
		require.Equal(t, 1, f.eng.Last(id).PhoneCalls())
		require.Equal(t, 1, f.eng.Opened(id))

		_, err = f.rt.LinkWithPhone(t.Context(), f.ref(uuid.New()), "0812")
		require.True(t, whatsapp.IsInvalidPhoneError(err))
	})
}

func TestRuntime_ConcurrentLinkWithPhoneCallsPairOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		f.eng.SetLinkScript(codeEvent())
		gate := make(chan struct{})
		f.eng.SetPhoneGate(gate)
		stop := f.start(t)
		defer stop()

		id := uuid.New()
		_, err := f.rt.Link(t.Context(), f.ref(id))
		require.NoError(t, err)
		codes := make(chan string, 2)
		for range 2 {
			go func() {
				st, err := f.rt.LinkWithPhone(t.Context(), f.ref(id), "+62 812-3456")
				if err != nil {
					codes <- "error: " + err.Error()
					return
				}
				codes <- st.PairingCode
			}()
		}
		synctest.Wait()
		close(gate)

		require.Equal(t, "ABCD-EFGH", <-codes)
		require.Equal(t, "ABCD-EFGH", <-codes)
		require.Equal(t, 1, f.eng.Last(id).PhoneCalls(), "the second caller waits for the first one's code")
	})
}

func TestRuntime_UnlinkPurgesDeletesTheLeaseAndReportsLoggedOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		id := f.freeLease()
		stop := f.start(t)
		defer stop()

		s := f.eng.Last(id)
		require.NoError(t, f.rt.Unlink(t.Context(), id))
		require.Equal(t, 1, s.Unlinks())
		require.True(t, s.Closed())
		_, ok := f.leases.Get(id)
		require.False(t, ok)
		require.Len(t, f.eng.Purged(), 1)
		require.Equal(t, []string{"logged_out:unlinked"}, f.sink.States(id))

		require.True(t, whatsapp.IsNotOwnedError(f.rt.Unlink(t.Context(), uuid.New())))
	})
}

func TestRuntime_UnlinkWinsOverTheEnginesOwnLoggedOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		id := f.freeLease()
		stop := f.start(t)
		defer stop()

		s := f.eng.Last(id)
		s.SetOnUnlink(func() { s.Sink.OnState(s.Ref, whatsapp.StateLoggedOut, whatsapp.ReasonLoggedOutByPhone) })
		require.NoError(t, f.rt.Unlink(t.Context(), id))
		synctest.Wait()

		require.Equal(t, []string{"logged_out:unlinked"}, f.sink.States(id), "the server's 401 during Logout must not report logged_out_by_phone")
		require.Len(t, f.eng.Purged(), 1, "one purge, not two")
	})
}

func TestRuntime_ForgetWinsOverTheEnginesOwnLoggedOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		id := f.freeLease()
		stop := f.start(t)
		defer stop()

		s := f.eng.Last(id)
		s.SetOnUnlink(func() { s.Sink.OnState(s.Ref, whatsapp.StateLoggedOut, whatsapp.ReasonLoggedOutByPhone) })
		ref := f.ref(id)
		ref.JID = linkedJID
		require.NoError(t, f.rt.Forget(t.Context(), ref))
		synctest.Wait()

		require.Empty(t, f.sink.States(id), "a device being deleted must not report logged_out_by_phone")
		require.Len(t, f.eng.Purged(), 1, "one purge, not two")
	})
}

func TestRuntime_AFailedUnlinkKeepsTheDeviceHeld(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		id := f.freeLease()
		stop := f.start(t)
		defer stop()

		f.eng.Last(id).SetUnlinkErr(errors.New("phone offline"))
		err := f.rt.Unlink(t.Context(), id)
		require.True(t, whatsapp.IsEngineError(err), "got %v", err)
		require.True(t, f.rt.Live(id))
		_, ok := f.leases.Get(id)
		require.True(t, ok)
	})
}

func TestRuntime_UnlinkCancelsAPendingAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		f.eng.SetLinkScript(codeEvent())
		stop := f.start(t)
		defer stop()

		id := uuid.New()
		_, err := f.rt.Link(t.Context(), f.ref(id))
		require.NoError(t, err)
		require.NoError(t, f.rt.Unlink(t.Context(), id))
		synctest.Wait()
		require.True(t, f.eng.Last(id).Closed())
		require.Equal(t, []string{"unlinked:link_cancelled"}, f.sink.States(id))
	})
}

func TestRuntime_ForgetIsBestEffortAboutTheEngineButAlwaysPurges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		id := f.freeLease()
		stop := f.start(t)
		defer stop()

		s := f.eng.Last(id)
		s.SetUnlinkErr(errors.New("phone offline"))
		ref := f.ref(id)
		ref.JID = linkedJID
		require.NoError(t, f.rt.Forget(t.Context(), ref))
		require.True(t, s.Closed())
		_, ok := f.leases.Get(id)
		require.False(t, ok)
		require.Len(t, f.eng.Purged(), 1)
		require.False(t, f.rt.Live(id))
	})
}

func TestRuntime_FenceFiresBeforeTheTTLEvenWhenRenewsAreSlow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		id := f.freeLease()
		stop := f.start(t)
		defer stop()
		require.True(t, f.rt.Live(id))

		f.leases.SetRenewHook(func(context.Context) error {
			time.Sleep(14*time.Second + 900*time.Millisecond)
			return errors.New("database slow")
		})
		time.Sleep(35 * time.Second)
		synctest.Wait()

		require.False(t, f.rt.Live(id), "a fence that waits for an exact 30s elapsed could land at the 45s TTL")
		require.True(t, f.eng.Last(id).Closed())
		require.Equal(t, []string{"disconnected:lease_unverified"}, f.sink.States(id))
		f.leases.SetRenewHook(nil)
	})
}

func TestRuntime_LosingTheLeaseCancelsAnInFlightOpenAndInstallsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		id := f.freeLease()
		release := make(chan struct{})
		var cancelled atomic.Bool
		f.eng.SetOpenHook(func(ctx context.Context, _ whatsapp.SessionRef) error {
			<-release
			cancelled.Store(ctx.Err() != nil)
			return nil
		})
		stop := f.start(t)
		defer stop()

		f.leases.Steal(id, "other")
		time.Sleep(f.cfg.LeaseInterval)
		synctest.Wait()
		close(release)
		synctest.Wait()

		require.True(t, cancelled.Load(), "the in-flight open's context is cancelled when the lease is lost")
		require.Equal(t, 1, f.eng.Opened(id))
		require.True(t, f.eng.Last(id).Closed(), "a session opened for a lease we lost is closed")
		require.False(t, f.rt.Live(id))
		_, held := f.rt.Session(id)
		require.False(t, held)
		require.Empty(t, f.sink.States(id))
	})
}

func TestRuntime_AnOpenThatFinishesAfterAFenceNeverOverlapsTheReopen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		id := f.freeLease()
		release := make(chan struct{})
		var calls atomic.Int32
		f.eng.SetOpenHook(func(context.Context, whatsapp.SessionRef) error {
			if calls.Add(1) == 1 {
				<-release
			}
			return nil
		})
		stop := f.start(t)
		defer stop()

		f.leases.SetRenewErr(errors.New("database unreachable"))
		time.Sleep(2 * f.cfg.LeaseInterval)
		synctest.Wait()
		f.leases.SetRenewErr(nil)
		time.Sleep(f.cfg.LeaseInterval)
		synctest.Wait()
		require.True(t, f.rt.Live(id), "the reopen went through while the first open was still stuck")

		close(release)
		synctest.Wait()
		require.Equal(t, 1, f.eng.OpenSessions(id), "the late open closes itself; exactly one live session remains")
		require.True(t, f.rt.Live(id))
	})
}

func TestRuntime_APromotedAttemptDoesNotBlockAReopenOrAReLink(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		f.eng.SetLinkScript(codeEvent())
		stop := f.start(t)
		defer stop()

		id := uuid.New()
		_, err := f.rt.Link(t.Context(), f.ref(id))
		require.NoError(t, err)
		sess := f.eng.Last(id)
		sess.Sink.OnLinked(sess.Ref, whatsapp.Identity{JID: "628222:3@s.whatsapp.net"})
		synctest.Wait()
		sess.SetConnected(true)
		require.True(t, f.rt.Live(id))

		f.leases.SetRenewErr(errors.New("database unreachable"))
		time.Sleep(2 * f.cfg.LeaseInterval)
		synctest.Wait()
		require.False(t, f.rt.Live(id))
		f.leases.SetRenewErr(nil)
		time.Sleep(f.cfg.LeaseInterval)
		synctest.Wait()
		require.Equal(t, 2, f.eng.Opened(id), "a fenced, promoted lease reopens")
		require.True(t, f.rt.Live(id))

		reopened := f.eng.Last(id)
		reopened.Sink.OnState(reopened.Ref, whatsapp.StateLoggedOut, whatsapp.ReasonLoggedOutByPhone)
		synctest.Wait()
		_, err = f.rt.Link(t.Context(), f.ref(id))
		require.NoError(t, err, "a device logged out from the phone can be linked again")
	})
}

func TestRuntime_OpensRunAtMostEightAtATime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		const total = 20
		ids := make([]uuid.UUID, total)
		for i := range ids {
			ids[i] = f.freeLease()
		}
		var inFlight, peak atomic.Int32
		f.eng.SetOpenHook(func(context.Context, whatsapp.SessionRef) error {
			n := inFlight.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(time.Second)
			inFlight.Add(-1)
			return nil
		})
		stop := f.start(t)
		defer stop()
		time.Sleep(5 * time.Second)
		synctest.Wait()

		require.Equal(t, int32(8), peak.Load(), "closeParallel bounds concurrent Engine.Open calls")
		for _, id := range ids {
			require.Equal(t, 1, f.eng.Opened(id))
		}
	})
}

func TestRuntime_LeaseWriteFailureAfterPairingRollsTheAttemptBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		f.eng.SetLinkScript(codeEvent())
		f.leases.SetUpsertErr(errors.New("database down"))
		stop := f.start(t)
		defer stop()

		id := uuid.New()
		_, err := f.rt.Link(t.Context(), f.ref(id))
		require.NoError(t, err)
		sess := f.eng.Last(id)
		sess.Sink.OnLinked(sess.Ref, whatsapp.Identity{JID: "628222:3@s.whatsapp.net"})
		synctest.Wait()

		st := f.rt.LinkState(id)
		require.Equal(t, whatsapp.OutcomeFailed, st.Outcome)
		require.Error(t, st.Err)
		require.True(t, sess.Closed())
		require.Len(t, f.eng.Purged(), 1)
		require.Equal(t, []string{"unlinked:lease_write_failed"}, f.sink.States(id))
		_, ok := f.leases.Get(id)
		require.False(t, ok)
		_, held := f.rt.Session(id)
		require.False(t, held)
		for _, c := range f.sink.Calls() {
			require.NotEqual(t, "linked", c.Kind)
		}
	})
}

func TestRuntime_AFailedPurgeOnUnlinkIsRetriedUntilTheLeaseIsGone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		id := f.freeLease()
		stop := f.start(t)
		defer stop()

		f.eng.SetPurgeErr(errors.New("store locked"))
		err := f.rt.Unlink(t.Context(), id)
		require.True(t, whatsapp.IsEngineError(err), "got %v", err)
		l, ok := f.leases.Get(id)
		require.True(t, ok)
		require.Equal(t, "me", l.Owner)

		f.eng.SetPurgeErr(nil)
		time.Sleep(f.cfg.LeaseInterval)
		synctest.Wait()
		_, ok = f.leases.Get(id)
		require.False(t, ok, "the next tick finishes the purge and deletes the lease")
		require.Equal(t, 1, f.eng.Opened(id), "a retired device is never reopened")
		require.False(t, f.rt.Live(id))
	})
}

func TestRuntime_AFailedLeaseDeleteIsRetriedInsteadOfRenewedForever(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		id := f.freeLease()
		stop := f.start(t)
		defer stop()

		f.leases.SetDeleteErr(errors.New("database down"))
		err := f.rt.Unlink(t.Context(), id)
		require.True(t, whatsapp.IsEngineError(err), "got %v", err)
		require.Len(t, f.eng.Purged(), 1)
		_, ok := f.leases.Get(id)
		require.True(t, ok)

		time.Sleep(2 * f.cfg.LeaseInterval)
		synctest.Wait()
		_, ok = f.leases.Get(id)
		require.True(t, ok, "still failing: the row is kept and retried")
		require.Len(t, f.eng.Purged(), 1, "the purge is not repeated once it succeeded")

		f.leases.SetDeleteErr(nil)
		time.Sleep(f.cfg.LeaseInterval)
		synctest.Wait()
		_, ok = f.leases.Get(id)
		require.False(t, ok)
		require.Equal(t, 1, f.eng.Opened(id))
	})
}

func TestRuntime_LiveNeedsAConnectedSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		id := f.freeLease()
		stop := f.start(t)
		defer stop()

		f.eng.Last(id).SetConnected(false)
		require.False(t, f.rt.Live(id))
		_, held := f.rt.Session(id)
		require.True(t, held, "the lease is still ours; only the socket is down")
	})
}

func TestRuntime_LinkWaitsAtMostFifteenSecondsForTheFirstCode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		stop := f.start(t)
		defer stop()

		begin := time.Now()
		st, err := f.rt.Link(t.Context(), f.ref(uuid.New()))
		require.NoError(t, err)
		require.Equal(t, 15*time.Second, time.Since(begin))
		require.Equal(t, whatsapp.OutcomePending, st.Outcome)
		require.Empty(t, st.QR)
	})
}

func TestRuntime_EventsDeliveredWhileShuttingDownAreNeitherLostNorPromoted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newRuntimeFixture()
		held := f.freeLease()
		f.eng.SetLinkScript(codeEvent())
		stop := f.start(t)

		s := f.eng.Last(held)
		s.SetOnClose(func() { s.Sink.OnState(s.Ref, whatsapp.StateLoggedOut, whatsapp.ReasonLoggedOutByPhone) })
		linking := uuid.New()
		_, err := f.rt.Link(t.Context(), f.ref(linking))
		require.NoError(t, err)
		ls := f.eng.Last(linking)
		var once sync.Once
		ls.SetOnClose(func() {
			once.Do(func() { ls.Sink.OnLinked(ls.Ref, whatsapp.Identity{JID: "628222:3@s.whatsapp.net"}) })
		})

		stop()

		require.Equal(t, []string{"logged_out:logged_out_by_phone"}, f.sink.States(held), "an event that loses the race with shutdown is forwarded, not dropped")
		_, ok := f.leases.Get(linking)
		require.False(t, ok, "no lease is written for a pairing that finishes during shutdown")
		for _, c := range f.sink.Calls() {
			require.NotEqual(t, "linked", c.Kind)
		}
	})
}

func TestRuntime_ALogoutRacingShutdownIsHandledExactlyOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for range 30 {
			f := newRuntimeFixture()
			id := f.freeLease()
			ctx, cancel := context.WithCancel(t.Context())
			errc := make(chan error, 1)
			go func() { errc <- f.rt.Run(ctx) }()
			synctest.Wait()

			s := f.eng.Last(id)
			done := make(chan struct{})
			go func() {
				defer close(done)
				s.Sink.OnState(s.Ref, whatsapp.StateLoggedOut, whatsapp.ReasonLoggedOutByPhone)
			}()
			cancel()
			require.NoError(t, <-errc)
			<-done

			require.True(t, s.Closed())
			require.Equal(t, []string{"logged_out:logged_out_by_phone"}, f.sink.States(id))
			l, ok := f.leases.Get(id)
			require.True(t, !ok || l.Owner == "", "the lease is deleted or released, never left held")
		}
	})
}
