package mailqueue

import (
	"context"
	"time"

	"github.com/google/uuid"

	"altalune.id/openwa/mailer"
)

// Queue is the port domains use to send mail.
type Queue interface {
	// Enqueue seals m and stores it pending; it joins the transaction on ctx when there is one.
	Enqueue(ctx context.Context, m mailer.Message) (uuid.UUID, error)
}

// WorkerStore is what the send worker needs from the queue.
type WorkerStore interface {
	// ClaimDue takes up to limit due entries exclusively, bumping Attempt and leasing each for ClaimLease.
	ClaimDue(ctx context.Context, now time.Time, limit int) ([]Entry, error)
	// Open unseals the message of a claimed entry.
	Open(e Entry) (mailer.Message, error)
	// Succeed settles the claimed entry e as sent and erases its payload.
	Succeed(ctx context.Context, e Entry, at time.Time) error
	// Fail reschedules the claimed entry e for retryAt, or settles it as failed with its payload erased at MaxAttempts.
	Fail(ctx context.Context, e Entry, retryAt time.Time, cause string) error
	// GiveUp settles the claimed entry e as failed at at, erasing its payload.
	GiveUp(ctx context.Context, e Entry, at time.Time, cause string) error
	// Release hands the claimed entry e back as due now and returns the attempt its claim took.
	Release(ctx context.Context, e Entry) error
	// PruneSettled deletes sent and failed entries settled before before and returns how many it deleted.
	PruneSettled(ctx context.Context, before time.Time) (int64, error)
}

// Store is the whole queue: the domain-facing Queue and the worker side.
type Store interface {
	Queue
	WorkerStore
}
