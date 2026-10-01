package whatsapp_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/whatsapp"
)

type testSender struct {
	ref whatsapp.SessionRef
	run func(context.Context)
}

func newTestSender(sess whatsapp.EngineSession, out whatsapp.Outbound, wake <-chan struct{}, typing bool) testSender {
	ref := whatsapp.SessionRef{DeviceID: uuid.New(), OrgID: uuid.New(), ProjectID: uuid.New()}
	cfg := whatsapp.SenderConfig{SpacingMin: time.Second, SpacingMax: 3 * time.Second, TypingBeforeText: typing,
		Jitter: func(d time.Duration) time.Duration { return d }}
	return testSender{ref: ref, run: whatsapp.NewTestSender(ref, sess, out, wake, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))}
}

func textRow(waid string) *whatsapp.OutboundRow {
	return &whatsapp.OutboundRow{ID: uuid.New(), Message: whatsapp.OutboundMessage{WAID: waid, Kind: whatsapp.KindText, To: "628111@s.whatsapp.net", Text: "hi"}}
}

func start(t *testing.T, s testSender) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); s.run(ctx) }()
	return func() { cancel(); <-done }
}

func TestSender_SpacesSendsAndClaimsOneRowAtATime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sess := fakes.NewMessagingSession()
		out := &fakes.Outbound{}
		out.Push(textRow("A"))
		out.Push(textRow("B"))
		s := newTestSender(sess, out, make(chan struct{}, 1), false)
		stop := start(t, s)
		time.Sleep(20 * time.Second)
		synctest.Wait()
		stop()

		sent, failed, claims := out.Snapshot()
		require.Len(t, sent, 2)
		require.Empty(t, failed)
		require.GreaterOrEqual(t, claims, 3, "an empty claim ends each drain")
		require.Len(t, sess.SendTimes, 2)
		require.Equal(t, 3*time.Second, sess.SendTimes[1].Sub(sess.SendTimes[0]), "SpacingMin plus the full jitter")
		for _, tc := range out.Scopes {
			require.Equal(t, s.ref.OrgID, tc.OrgID, "every claim runs in the device's tenant")
			require.Equal(t, s.ref.ProjectID, tc.ProjectID)
		}
	})
}

func TestSender_PassesUploadKeysToMarkSent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sess := fakes.NewMessagingSession()
		keys := &whatsapp.MediaKeys{DirectPath: "/v/t62", MediaKey: []byte{1}}
		sess.SendFn = func(_ context.Context, out whatsapp.OutboundMessage) (whatsapp.SendResult, error) {
			return whatsapp.SendResult{At: time.Now(), ChatJID: out.To, Media: keys}, nil
		}
		out := &fakes.Outbound{}
		out.Push(textRow("A"))
		stop := start(t, newTestSender(sess, out, make(chan struct{}, 1), false))
		time.Sleep(3 * time.Second)
		synctest.Wait()
		stop()
		require.Equal(t, []*whatsapp.MediaKeys{keys}, out.SentMedia)
	})
}

func TestSender_TypesBeforeText(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sess := fakes.NewMessagingSession()
		out := &fakes.Outbound{}
		out.Push(textRow("A"))
		stop := start(t, newTestSender(sess, out, make(chan struct{}, 1), true))
		begin := time.Now()
		time.Sleep(10 * time.Second)
		synctest.Wait()
		stop()
		require.Equal(t, []string{"typing:on", "send:A", "typing:off"}, sess.Recorded())
		require.Equal(t, 2*time.Second+2*time.Second, sess.SendTimes[0].Sub(begin), "the 2 s poll, then min(2 s, spacing) of typing")
	})
}

func TestSender_WakeBeatsThePoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sess := fakes.NewMessagingSession()
		out := &fakes.Outbound{}
		wake := make(chan struct{}, 1)
		stop := start(t, newTestSender(sess, out, wake, false))
		time.Sleep(500 * time.Millisecond)
		begin := time.Now()
		out.Push(textRow("A"))
		wake <- struct{}{}
		synctest.Wait()
		stop()
		require.Len(t, sess.SendTimes, 1)
		require.Equal(t, time.Duration(0), sess.SendTimes[0].Sub(begin), "a wake sends at once, not at the next 2 s tick")
	})
}

func TestSender_ClassifiesFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sess := fakes.NewMessagingSession()
		sess.SendFn = func(_ context.Context, out whatsapp.OutboundMessage) (whatsapp.SendResult, error) {
			if out.WAID == "RETRY" {
				return whatsapp.SendResult{}, &whatsapp.EngineError{Op: "send", Reason: "server_error_503", Retryable: true, Err: errors.New("503")}
			}
			return whatsapp.SendResult{}, &whatsapp.EngineError{Op: "send", Reason: "reachout_timelock", Err: errors.New("463")}
		}
		out := &fakes.Outbound{}
		out.Push(textRow("RETRY"))
		out.Push(textRow("FATAL"))
		stop := start(t, newTestSender(sess, out, make(chan struct{}, 1), false))
		time.Sleep(20 * time.Second)
		synctest.Wait()
		stop()
		_, failed, _ := out.Snapshot()
		require.Len(t, failed, 2)
		require.True(t, failed[0].Retryable)
		require.False(t, failed[1].Retryable)
	})
}

func TestSender_IdlesWhileDisconnected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sess := fakes.NewMessagingSession()
		sess.SetConnected(false)
		out := &fakes.Outbound{}
		out.Push(textRow("A"))
		stop := start(t, newTestSender(sess, out, make(chan struct{}, 1), false))
		time.Sleep(10 * time.Second)
		synctest.Wait()
		stop()
		_, _, claims := out.Snapshot()
		require.Zero(t, claims, "a disconnected session claims nothing, so the row stays queued")
	})
}

func TestSender_CancelAfterSendStartedLeavesTheRowForStaleReclaim(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sess := fakes.NewMessagingSession()
		sess.SendFn = func(ctx context.Context, _ whatsapp.OutboundMessage) (whatsapp.SendResult, error) {
			<-ctx.Done()
			return whatsapp.SendResult{}, ctx.Err()
		}
		out := &fakes.Outbound{}
		out.Push(textRow("A"))
		stop := start(t, newTestSender(sess, out, make(chan struct{}, 1), false))
		time.Sleep(3 * time.Second)
		synctest.Wait()
		stop()
		sent, failed, _ := out.Snapshot()
		require.Empty(t, sent)
		require.Empty(t, failed, "the send may be in flight, so the row stays sending until StaleAfter")
		require.Empty(t, out.RequeuedIDs(), "an invoked send is never requeued at once")
	})
}

func TestSender_SendDeadlineLeavesTheRowForStaleReclaim(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sess := fakes.NewMessagingSession()
		sess.SendFn = func(ctx context.Context, _ whatsapp.OutboundMessage) (whatsapp.SendResult, error) {
			<-ctx.Done()
			return whatsapp.SendResult{}, &whatsapp.EngineError{Op: "send", Reason: "ack_timeout", Retryable: true, Err: ctx.Err()}
		}
		out := &fakes.Outbound{}
		out.Push(textRow("A"))
		stop := start(t, newTestSender(sess, out, make(chan struct{}, 1), false))
		time.Sleep(2*time.Second + whatsapp.AckTimeout + time.Second)
		synctest.Wait()
		stop()
		_, failed, _ := out.Snapshot()
		require.Empty(t, failed, "a timed-out send is not requeued at once; a second claim could send it twice")
		require.Empty(t, out.RequeuedIDs())
	})
}

func TestSender_CancelBeforeSendRequeues(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sess := fakes.NewMessagingSession()
		out := &fakes.Outbound{}
		out.Push(textRow("A"))
		stop := start(t, newTestSender(sess, out, make(chan struct{}, 1), true))
		time.Sleep(3 * time.Second)
		synctest.Wait()
		stop()
		_, failed, _ := out.Snapshot()
		require.Empty(t, failed)
		require.Len(t, out.RequeuedIDs(), 1, "the cancel came during the typing pause, before Send, so the attempt is given back")
		require.Equal(t, []string{"typing:on", "typing:off"}, sess.Recorded(), "Send was never invoked, and the typing indicator is cleared")
	})
}

func TestSender_CancelRightAfterAClaimRequeues(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sess := fakes.NewMessagingSession()
		out := &fakes.Outbound{}
		out.Push(textRow("A"))
		ctx, cancel := context.WithCancel(t.Context())
		out.ClaimHook = cancel
		s := newTestSender(sess, out, make(chan struct{}, 1), false)
		done := make(chan struct{})
		go func() { defer close(done); s.run(ctx) }()
		time.Sleep(3 * time.Second)
		synctest.Wait()
		<-done
		_, failed, _ := out.Snapshot()
		require.Empty(t, failed)
		require.Len(t, out.RequeuedIDs(), 1, "a committed claim is handed back, not stranded in sending")
		require.Empty(t, sess.SendTimes)
	})
}

func TestSender_SendContextNeverOutlivesTheReclaimWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sess := fakes.NewMessagingSession()
		var deadline time.Time
		sess.SendFn = func(ctx context.Context, _ whatsapp.OutboundMessage) (whatsapp.SendResult, error) {
			deadline, _ = ctx.Deadline()
			return whatsapp.SendResult{At: time.Now()}, nil
		}
		out := &fakes.Outbound{}
		out.Push(textRow("A"))
		stop := start(t, newTestSender(sess, out, make(chan struct{}, 1), false))
		time.Sleep(3 * time.Second)
		synctest.Wait()
		stop()
		require.Equal(t, whatsapp.AckTimeout, deadline.Sub(sess.SendTimes[0]), "sendCtx = staleAfter - spacingMax")
	})
}

func TestSender_ClaimErrorDoesNotStopTheLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sess := fakes.NewMessagingSession()
		out := &fakes.Outbound{}
		out.SetClaimErr(errors.New("db down"))
		stop := start(t, newTestSender(sess, out, make(chan struct{}, 1), false))
		time.Sleep(7 * time.Second)
		synctest.Wait()
		out.SetClaimErr(nil)
		out.Push(textRow("A"))
		time.Sleep(3 * time.Second)
		synctest.Wait()
		stop()
		_, _, claims := out.Snapshot()
		require.GreaterOrEqual(t, claims, 4)
		require.Len(t, sess.SendTimes, 1, "the loop survived the failed claims")
	})
}

func TestSender_NonMessagingSessionFailsTheRowAsUnsupported(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		plain := &fakes.EngineSession{}
		plain.SetConnected(true)
		out := &fakes.Outbound{}
		out.Push(textRow("A"))
		stop := start(t, newTestSender(plain, out, make(chan struct{}, 1), false))
		time.Sleep(3 * time.Second)
		synctest.Wait()
		stop()
		_, failed, _ := out.Snapshot()
		require.Len(t, failed, 1)
		require.False(t, failed[0].Retryable)
	})
}

func TestSender_RequeueRunsDetachedAndInTheDevicesTenant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sess := fakes.NewMessagingSession()
		out := &fakes.Outbound{}
		out.Push(textRow("A"))
		s := newTestSender(sess, out, make(chan struct{}, 1), true)
		stop := start(t, s)
		time.Sleep(3 * time.Second)
		synctest.Wait()
		stop()
		marks := out.MarkCalls()
		require.Len(t, marks, 1, "the fake refuses a call on a done context, so only a detached requeue is recorded")
		require.Equal(t, "requeued", marks[0].Kind)
		require.Equal(t, s.ref.OrgID, marks[0].Scope.OrgID)
		require.Equal(t, s.ref.ProjectID, marks[0].Scope.ProjectID)
	})
}

func TestSender_MarkSentSurvivesACancelDuringTheSendAndCarriesTheTenant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sess := fakes.NewMessagingSession()
		ctx, cancel := context.WithCancel(t.Context())
		sess.SendFn = func(_ context.Context, out whatsapp.OutboundMessage) (whatsapp.SendResult, error) {
			cancel()
			return whatsapp.SendResult{At: time.Now(), ChatJID: out.To}, nil
		}
		out := &fakes.Outbound{}
		out.Push(textRow("A"))
		s := newTestSender(sess, out, make(chan struct{}, 1), false)
		done := make(chan struct{})
		go func() { defer close(done); s.run(ctx) }()
		time.Sleep(3 * time.Second)
		synctest.Wait()
		<-done
		marks := out.MarkCalls()
		require.Len(t, marks, 1, "a send that reached WhatsApp is recorded even though the sender was cancelled")
		require.Equal(t, "sent", marks[0].Kind)
		require.Equal(t, s.ref.OrgID, marks[0].Scope.OrgID)
		require.Equal(t, s.ref.ProjectID, marks[0].Scope.ProjectID)
	})
}

func TestSender_FailureMarkCarriesTheTenant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sess := fakes.NewMessagingSession()
		sess.SendFn = func(context.Context, whatsapp.OutboundMessage) (whatsapp.SendResult, error) {
			return whatsapp.SendResult{}, &whatsapp.InvalidJIDError{Raw: "x"}
		}
		out := &fakes.Outbound{}
		out.Push(textRow("A"))
		s := newTestSender(sess, out, make(chan struct{}, 1), false)
		stop := start(t, s)
		time.Sleep(3 * time.Second)
		synctest.Wait()
		stop()
		marks := out.MarkCalls()
		require.Len(t, marks, 1)
		require.Equal(t, "failed", marks[0].Kind)
		require.Equal(t, s.ref.ProjectID, marks[0].Scope.ProjectID)
	})
}

func TestStaleAfterCoversTheSendTheMarkAndTheSpacing(t *testing.T) {
	spacing := 3 * time.Second
	require.Greater(t, whatsapp.StaleAfter(spacing), whatsapp.AckTimeout+whatsapp.MarkTimeout+spacing-1)
}

func TestSender_SlowMarkSentEndsBeforeTheStaleWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sess := fakes.NewMessagingSession()
		sess.SendFn = func(ctx context.Context, out whatsapp.OutboundMessage) (whatsapp.SendResult, error) {
			time.Sleep(whatsapp.AckTimeout - time.Second)
			return whatsapp.SendResult{At: time.Now(), ChatJID: out.To}, nil
		}
		var claimedAt, markEnded time.Time
		out := &fakes.Outbound{}
		out.ClaimHook = func() { claimedAt = time.Now() }
		out.MarkSentFn = func(ctx context.Context) error {
			<-ctx.Done()
			markEnded = time.Now()
			return ctx.Err()
		}
		out.Push(textRow("A"))
		s := newTestSender(sess, out, make(chan struct{}, 1), false)
		stop := start(t, s)
		time.Sleep(3 * time.Minute)
		synctest.Wait()
		stop()
		require.False(t, markEnded.IsZero(), "MarkSent ran and hit its own deadline")
		require.Less(t, markEnded.Sub(claimedAt), whatsapp.StaleAfter(3*time.Second),
			"a slow MarkSent must be over before another claim may reclaim the row")
	})
}
