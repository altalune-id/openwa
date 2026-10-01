package fakes

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/whatsapp"
)

// FailedCall is one recorded Outbound.MarkFailed.
type FailedCall struct {
	ID        uuid.UUID
	Err       error
	Retryable bool
}

// MarkCall is one recorded outcome call with the tenant scope its context carried.
type MarkCall struct {
	Kind  string
	ID    uuid.UUID
	Scope tenant.Context
}

// Outbound is a scripted whatsapp.Outbound that hands out Queue in order and records the outcome of each row.
type Outbound struct {
	mu        sync.Mutex
	Queue     []*whatsapp.OutboundRow
	Claims    int
	Sent      []uuid.UUID
	SentAt    []time.Time
	SentMedia []*whatsapp.MediaKeys
	Failed    []FailedCall
	Requeued  []uuid.UUID
	Scopes    []tenant.Context
	Marks     []MarkCall
	// ClaimErr is read under mu; change it through SetClaimErr while a sender runs.
	ClaimErr error
	// MarkSentFn, when set, runs first in MarkSent with its context; a non-nil error fails the call.
	MarkSentFn func(ctx context.Context) error
	// ClaimHook, when set, runs after a row is handed out and before ClaimNext returns.
	ClaimHook func()
}

var _ whatsapp.Outbound = (*Outbound)(nil)

// Push appends a row to the queue.
func (f *Outbound) Push(row *whatsapp.OutboundRow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Queue = append(f.Queue, row)
}

//nolint:nilnil // an empty queue is not an error.
func (f *Outbound) ClaimNext(ctx context.Context, _ uuid.UUID) (*whatsapp.OutboundRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Claims++
	if tc, err := tenant.From(ctx); err == nil {
		f.Scopes = append(f.Scopes, tc)
	}
	if f.ClaimErr != nil {
		return nil, f.ClaimErr
	}
	if len(f.Queue) == 0 {
		return nil, nil
	}
	row := f.Queue[0]
	f.Queue = f.Queue[1:]
	if f.ClaimHook != nil {
		f.ClaimHook()
	}
	return row, nil
}

func (f *Outbound) mark(ctx context.Context, kind string, id uuid.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tc, _ := tenant.From(ctx)
	f.Marks = append(f.Marks, MarkCall{Kind: kind, ID: id, Scope: tc})
	return nil
}

func (f *Outbound) MarkSent(ctx context.Context, id uuid.UUID, at time.Time, media *whatsapp.MediaKeys) error {
	f.mu.Lock()
	fn := f.MarkSentFn
	f.mu.Unlock()
	if fn != nil {
		if err := fn(ctx); err != nil {
			return err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.mark(ctx, "sent", id); err != nil {
		return err
	}
	f.Sent = append(f.Sent, id)
	f.SentAt = append(f.SentAt, at)
	f.SentMedia = append(f.SentMedia, media)
	return nil
}

func (f *Outbound) MarkFailed(ctx context.Context, id uuid.UUID, err error, retryable bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if cerr := f.mark(ctx, "failed", id); cerr != nil {
		return cerr
	}
	f.Failed = append(f.Failed, FailedCall{ID: id, Err: err, Retryable: retryable})
	return nil
}

func (f *Outbound) Requeue(ctx context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.mark(ctx, "requeued", id); err != nil {
		return err
	}
	f.Requeued = append(f.Requeued, id)
	return nil
}

// SetClaimErr makes every ClaimNext fail with err; nil clears it.
func (f *Outbound) SetClaimErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ClaimErr = err
}

// RequeuedIDs returns a copy of the rows handed back.
func (f *Outbound) RequeuedIDs() []uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uuid.UUID(nil), f.Requeued...)
}

// MarkCalls returns a copy of the recorded outcome calls.
func (f *Outbound) MarkCalls() []MarkCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]MarkCall(nil), f.Marks...)
}

// Snapshot returns copies of the recorded outcomes.
func (f *Outbound) Snapshot() (sent []uuid.UUID, failed []FailedCall, claims int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uuid.UUID(nil), f.Sent...), append([]FailedCall(nil), f.Failed...), f.Claims
}
