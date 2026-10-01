package meow

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow/types/events"

	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/whatsapp"
)

func TestClose_WhileAHandlerIsParkedOnAFullQueueDoesNotDeadlock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fc := newFakeClient()
		sink := &fakes.Sink{Gate: make(chan struct{})}
		s := newSession(whatsapp.SessionRef{DeviceID: uuid.New()}, fc, sink, 1, discard())

		go fc.emit(&events.Disconnected{})
		synctest.Wait()
		go fc.emit(&events.Disconnected{})
		synctest.Wait()
		parked := make(chan struct{})
		go func() {
			fc.emit(&events.Disconnected{})
			close(parked)
		}()
		synctest.Wait()

		closed := make(chan struct{})
		go func() {
			_ = s.Close(context.Background())
			close(closed)
		}()
		<-parked
		close(sink.Gate)
		<-closed

		require.Zero(t, fc.handlerCount(), "Close removes the handler")
		require.Len(t, sink.Calls(), 1, "nothing is dispatched after done closes")
		fc.mu.Lock()
		require.Equal(t, 1, fc.disconnects)
		fc.mu.Unlock()
	})
}

func TestClosedSessionRefusesEveryCall(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestSession(t)
	require.NoError(t, s.Close(t.Context()))
	require.NoError(t, s.Close(t.Context()), "Close is idempotent")
	_, err := s.Link(t.Context())
	require.True(t, whatsapp.IsNotConnectedError(err))
	_, err = s.LinkWithPhone(t.Context(), "628123456")
	require.True(t, whatsapp.IsNotConnectedError(err))
	require.True(t, whatsapp.IsNotConnectedError(s.Unlink(t.Context())))
	require.False(t, s.Connected())
}

func TestLink_FeedsCodesThenEndsOnSuccessAndPhoneWaitsForTheFirstCode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, fc, _ := newTestSession(t)
		out, err := s.Link(t.Context())
		require.NoError(t, err)
		require.True(t, fc.IsConnected(), "Link connects after asking for the QR channel")

		codes := make(chan string, 1)
		go func() {
			c, perr := s.LinkWithPhone(t.Context(), "628123456")
			if perr != nil {
				c = "error: " + perr.Error()
			}
			codes <- c
		}()
		synctest.Wait()
		fc.mu.Lock()
		require.Empty(t, fc.pairCalls, "PairPhone waits for the first QR code")
		fc.mu.Unlock()

		fc.qr <- qrCode("2@a")
		ev := <-out
		require.Equal(t, whatsapp.LinkEventCode, ev.Kind)
		require.Equal(t, "WXYZ-1234", <-codes)

		fc.qr <- qrSuccess()
		require.Equal(t, whatsapp.LinkEventSuccess, (<-out).Kind)
		_, open := <-out
		require.False(t, open, "the feeder closes the channel after a terminal item")
	})
}

func TestLink_ACloseDuringConnectLeavesNoSocket(t *testing.T) {
	t.Parallel()
	s, fc, _ := newTestSession(t)
	fc.mu.Lock()
	fc.onConnect = func() { require.NoError(t, s.Close(context.Background())) }
	fc.mu.Unlock()

	_, err := s.Link(t.Context())
	require.True(t, whatsapp.IsNotConnectedError(err), "got %v", err)
	require.False(t, fc.IsConnected(), "a Connect that finished after Close must be disconnected again")
}

func TestLinkWithPhone_GivesUpWhenNoCodeArrives(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, fc, _ := newTestSession(t)
		_, err := s.Link(t.Context())
		require.NoError(t, err)

		start := time.Now()
		_, err = s.LinkWithPhone(t.Context(), "628123456")
		require.Error(t, err)
		require.Equal(t, firstQRWait, time.Since(start), "the wait for the first code is bounded, not the caller's ctx")
		fc.mu.Lock()
		require.Empty(t, fc.pairCalls)
		fc.mu.Unlock()
	})
}

func TestUnlink_FallsBackToDeletingTheStoreWhenOffline(t *testing.T) {
	t.Parallel()
	s, fc, _ := newTestSession(t)
	fc.logoutErr = errLogout
	require.NoError(t, s.Unlink(t.Context()))
	require.True(t, fc.deleted)

	s2, fc2, _ := newTestSession(t)
	fc2.logoutErr = errLogout
	fc2.connected = true
	require.True(t, whatsapp.IsEngineError(s2.Unlink(t.Context())))
}

type countingHandler struct{ warns atomic.Int64 }

func (h *countingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *countingHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level == slog.LevelWarn {
		h.warns.Add(1)
	}
	return nil
}
func (h *countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *countingHandler) WithGroup(string) slog.Handler      { return h }

func TestWarnFull_IsRateLimitedToOncePerMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := &countingHandler{}
		s := &session{log: slog.New(h)}
		s.warnFull()
		s.warnFull()
		require.Equal(t, int64(1), h.warns.Load())
		time.Sleep(61 * time.Second)
		s.warnFull()
		require.Equal(t, int64(2), h.warns.Load())
	})
}

func TestLink_QRChannelSurvivesCancellationOfTheRequestContext(t *testing.T) {
	t.Parallel()
	s, fc, _ := newTestSession(t)
	reqCtx, cancel := context.WithCancel(t.Context())
	_, err := s.Link(reqCtx)
	require.NoError(t, err)
	cancel()

	fc.mu.Lock()
	qrCtx := fc.qrCtx
	fc.mu.Unlock()
	require.NoError(t, qrCtx.Err(), "the request's end must not end the QR channel")

	require.NoError(t, s.Close(t.Context()))
	require.Error(t, qrCtx.Err(), "Close ends the session-lifetime context")
}

func TestClose_ReturnsWhenItsContextExpiresWhileTheLoopIsStuck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fc := newFakeClient()
		sink := &fakes.Sink{Gate: make(chan struct{})}
		s := newSession(whatsapp.SessionRef{DeviceID: uuid.New()}, fc, sink, 4, discard())
		fc.emit(&events.Disconnected{})
		synctest.Wait()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		start := time.Now()
		require.ErrorIs(t, s.Close(ctx), context.DeadlineExceeded)
		require.Equal(t, 5*time.Second, time.Since(start))

		close(sink.Gate)
		require.NoError(t, s.Close(context.Background()))
	})
}
