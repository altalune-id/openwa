package whatsapp

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"time"

	"altalune.id/openwa/internal/platform/tenant"
)

// AckTimeout is how long the engine waits for WhatsApp to acknowledge one send.
const AckTimeout = 75 * time.Second

const (
	senderPoll       = 2 * time.Second
	maxTyping        = 2 * time.Second
	markTimeout      = 10 * time.Second
	typingOffTimeout = 5 * time.Second
)

// StaleAfter is how long a sending row stays claimed before a claim may take it again: the ack timeout, one outcome write and the widest spacing.
func StaleAfter(spacingMax time.Duration) time.Duration { return AckTimeout + markTimeout + spacingMax }

// SenderConfig spaces and shapes one device's sends; Jitter returns a duration in [0, d] and defaults to uniform random.
type SenderConfig struct {
	SpacingMin       time.Duration
	SpacingMax       time.Duration
	TypingBeforeText bool
	Jitter           func(d time.Duration) time.Duration
}

type sender struct {
	ref       SessionRef
	sess      EngineSession
	out       Outbound
	wake      <-chan struct{}
	cfg       SenderConfig
	log       *slog.Logger
	claimErrs int
}

// NOTE: the runtime runs it and waits for it before closing the session.
func (s *sender) run(ctx context.Context) {
	tick := time.NewTicker(senderPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-tick.C:
		}
		if !s.drain(ctx) {
			return
		}
	}
}

func (s *sender) drain(ctx context.Context) bool {
	for s.sess.Connected() {
		row, err := s.out.ClaimNext(s.scoped(ctx), s.ref.DeviceID)
		if row != nil && ctx.Err() != nil {
			s.requeue(ctx, row, ctx.Err())
			return false
		}
		if ctx.Err() != nil {
			return false
		}
		if err != nil {
			s.claimFailed(ctx, err)
			return true
		}
		s.claimErrs = 0
		if row == nil {
			return true
		}
		s.deliver(ctx, row)
		if !wait(ctx, s.spacing()) {
			return false
		}
	}
	return ctx.Err() == nil
}

// NOTE: warns on the first failed claim, then logs at debug until one succeeds.
func (s *sender) claimFailed(ctx context.Context, err error) {
	s.claimErrs++
	level := slog.LevelDebug
	if s.claimErrs == 1 {
		level = slog.LevelWarn
	}
	s.log.Log(ctx, level, "whatsapp.sender: claim", slog.String("device_id", s.ref.DeviceID.String()), slog.Int("failures", s.claimErrs), slog.Any("error", err))
}

func (s *sender) deliver(ctx context.Context, row *OutboundRow) {
	sendCtx, cancel := context.WithTimeout(ctx, AckTimeout)
	defer cancel()
	res, invoked, err := s.send(sendCtx, row.Message)
	switch {
	case err == nil:
		s.markSent(ctx, row, res)
	case sendCtx.Err() != nil && invoked:
		s.log.WarnContext(ctx, "whatsapp.sender: send interrupted; left for stale reclaim",
			slog.String("message_id", row.ID.String()), slog.Any("error", err))
	case sendCtx.Err() != nil:
		s.requeue(ctx, row, sendCtx.Err())
	default:
		s.log.WarnContext(ctx, "whatsapp.sender: send failed", slog.String("message_id", row.ID.String()), slog.Any("error", err))
		s.markFailed(ctx, row, err, IsRetryable(err))
	}
}

// NOTE: detached context, because ctx may already be over.
func (s *sender) requeue(ctx context.Context, row *OutboundRow, cause error) {
	mctx, cancel := context.WithTimeout(s.scoped(context.WithoutCancel(ctx)), markTimeout)
	defer cancel()
	if err := s.out.Requeue(mctx, row.ID); err != nil {
		s.log.ErrorContext(ctx, "whatsapp.sender: requeue", slog.String("message_id", row.ID.String()), slog.Any("cause", cause), slog.Any("error", err))
	}
}

func (s *sender) markFailed(ctx context.Context, row *OutboundRow, cause error, retryable bool) {
	mctx, cancel := context.WithTimeout(s.scoped(context.WithoutCancel(ctx)), markTimeout)
	defer cancel()
	if err := s.out.MarkFailed(mctx, row.ID, cause, retryable); err != nil {
		s.log.ErrorContext(ctx, "whatsapp.sender: mark failed", slog.String("message_id", row.ID.String()), slog.Any("error", err))
	}
}

func (s *sender) markSent(ctx context.Context, row *OutboundRow, res SendResult) {
	mctx, cancel := context.WithTimeout(s.scoped(context.WithoutCancel(ctx)), markTimeout)
	defer cancel()
	if err := s.out.MarkSent(mctx, row.ID, res.At, res.Media); err != nil {
		s.log.ErrorContext(ctx, "whatsapp.sender: mark sent", slog.String("message_id", row.ID.String()), slog.Any("error", err))
	}
}

func (s *sender) send(ctx context.Context, m OutboundMessage) (SendResult, bool, error) {
	ms, ok := s.sess.(MessagingSession)
	if !ok {
		return SendResult{}, false, &UnsupportedError{Feature: "messaging"}
	}
	typing := m.Kind == KindText && s.cfg.TypingBeforeText
	if typing {
		_ = ms.SendTyping(ctx, m.To, true)
		defer s.stopTyping(ctx, ms, m.To)
		if !wait(ctx, min(maxTyping, s.spacing())) {
			return SendResult{}, false, ctx.Err()
		}
	}
	res, err := ms.Send(ctx, m)
	return res, true, err
}

func (s *sender) stopTyping(ctx context.Context, ms MessagingSession, to string) {
	tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), typingOffTimeout)
	defer cancel()
	_ = ms.SendTyping(tctx, to, false)
}

func (s *sender) spacing() time.Duration {
	spread := s.cfg.SpacingMax - s.cfg.SpacingMin
	if spread <= 0 {
		return s.cfg.SpacingMin
	}
	jitter := s.cfg.Jitter
	if jitter == nil {
		jitter = func(d time.Duration) time.Duration { return rand.N(d + 1) }
	}
	return s.cfg.SpacingMin + jitter(spread)
}

func (s *sender) scoped(ctx context.Context) context.Context {
	return tenant.Into(ctx, tenant.Context{OrgID: s.ref.OrgID, ProjectID: s.ref.ProjectID})
}

func wait(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
