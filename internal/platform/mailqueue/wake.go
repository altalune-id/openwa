package mailqueue

import (
	"context"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/mailer"
)

// Waker coalesces wake-ups for one send Worker.
type Waker struct {
	ch chan struct{}
}

// NewWaker returns a Waker that holds at most one pending wake-up.
func NewWaker() *Waker { return &Waker{ch: make(chan struct{}, 1)} }

// Wake signals the worker without blocking.
func (w *Waker) Wake() {
	select {
	case w.ch <- struct{}{}:
	default:
	}
}

type wakingStore struct {
	Store
	waker *Waker
}

// WakeOnEnqueue returns s with every committed Enqueue waking waker.
func WakeOnEnqueue(s Store, waker *Waker) Store { return &wakingStore{Store: s, waker: waker} }

func (s *wakingStore) Enqueue(ctx context.Context, m mailer.Message) (uuid.UUID, error) {
	id, err := s.Store.Enqueue(ctx, m)
	if err != nil {
		return uuid.Nil, err
	}
	db.AfterCommit(ctx, s.waker.Wake)
	return id, nil
}
