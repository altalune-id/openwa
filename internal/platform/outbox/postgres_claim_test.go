package outbox_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/outbox"
)

func TestPostgresClaimDueLeasesTheEntryAgainstTheNextTick(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	require.NoError(t, s.Enqueue(ctx, entry(tc, uuid.New())))

	now := time.Now().UTC()
	first, err := s.ClaimDue(ctx, now, 10)
	require.NoError(t, err)
	require.Len(t, first, 1)
	assert.Equal(t, 1, first[0].Attempt)
	assert.Equal(t, outbox.StatusPending, first[0].Status)
	assert.WithinDuration(t, now.Add(outbox.ClaimLease), first[0].NextAttemptAt, time.Second)

	second, err := s.ClaimDue(ctx, now.Add(time.Minute), 10)
	require.NoError(t, err)
	assert.Empty(t, second, "the lease did not hold the entry back from the next tick")

	third, err := s.ClaimDue(ctx, now.Add(outbox.ClaimLease+time.Second), 10)
	require.NoError(t, err)
	require.Len(t, third, 1)
	assert.Equal(t, 2, third[0].Attempt)
}

func TestPostgresClaimDueSkipsARowAnotherClaimerHasLocked(t *testing.T) {
	s, ctx, tc, sqlDB := newPostgresStoreAndDB(t)
	e := entry(tc, uuid.New())
	require.NoError(t, s.Enqueue(ctx, e))

	tx, err := sqlDB.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	_, err = tx.ExecContext(t.Context(), "SELECT id FROM "+prefix+"outbox_entries WHERE id = $1 FOR UPDATE", e.ID)
	require.NoError(t, err)

	claimed, err := s.ClaimDue(ctx, time.Now(), 10)
	require.NoError(t, err)
	assert.Empty(t, claimed, "a locked row was claimed instead of skipped")

	require.NoError(t, tx.Rollback())
	claimed, err = s.ClaimDue(ctx, time.Now(), 10)
	require.NoError(t, err)
	assert.Len(t, claimed, 1)
}

func TestPostgresClaimDueReapsEntriesWhoseAttemptsRanOut(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	e := entry(tc, uuid.New())
	require.NoError(t, s.Enqueue(ctx, e))

	at := time.Now().UTC()
	for i := 1; i <= outbox.MaxAttempts; i++ {
		claimed, err := s.ClaimDue(ctx, at, 10)
		require.NoError(t, err, "attempt %d", i)
		require.Len(t, claimed, 1, "attempt %d", i)
		at = at.Add(outbox.ClaimLease + time.Second)
	}

	none, err := s.ClaimDue(ctx, at.Add(time.Hour), 10)
	require.NoError(t, err)
	require.Empty(t, none)

	err = s.Succeed(ctx, e, time.Now())
	require.Error(t, err)
	assert.True(t, outbox.IsTerminalStateError(err), "got %v", err)
}

func TestPostgresFailSettlesTerminallyOnceAttemptsRunOut(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	e := entry(tc, uuid.New())
	require.NoError(t, s.Enqueue(ctx, e))

	now := time.Now().UTC()
	for i := 1; i <= outbox.MaxAttempts; i++ {
		claimed, err := s.ClaimDue(ctx, now, 10)
		require.NoError(t, err, "attempt %d", i)
		require.Len(t, claimed, 1, "attempt %d", i)
		require.Equal(t, i, claimed[0].Attempt)
		require.NoError(t, s.Fail(ctx, claimed[0], now, "endpoint keeps refusing"))
	}

	settled, err := s.ClaimDue(ctx, now.Add(time.Hour), 10)
	require.NoError(t, err)
	assert.Empty(t, settled)
}

func TestPostgresFailedEntryRetriesAfterTheBackoffTheCallerChose(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	e := entry(tc, uuid.New())
	require.NoError(t, s.Enqueue(ctx, e))

	now := time.Now().UTC()
	claimed, err := s.ClaimDue(ctx, now, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	backoff := outbox.Backoff(claimed[0].Attempt)
	require.NoError(t, s.Fail(ctx, claimed[0], now.Add(backoff), "502"))

	early, err := s.ClaimDue(ctx, now.Add(backoff-time.Second), 10)
	require.NoError(t, err)
	assert.Empty(t, early)

	due, err := s.ClaimDue(ctx, now.Add(backoff+time.Second), 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, 2, due[0].Attempt)
}

func TestPostgresEnqueueKeepsTargetsOfOneEventApart(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	eventID := uuid.New()
	require.NoError(t, s.Enqueue(ctx, entry(tc, eventID)))
	other := entry(tc, eventID)
	other.Target = "another-endpoint"
	require.NoError(t, s.Enqueue(ctx, other))

	claimed, err := s.ClaimDue(ctx, time.Now(), 10)
	require.NoError(t, err)
	assert.Len(t, claimed, 2)
}

func TestPostgresEnqueueRejectsAnEntryOutsideTheTenantScope(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	e := entry(tc, uuid.New())
	e.OrgID = uuid.New()

	err := s.Enqueue(ctx, e)
	require.Error(t, err)
	assert.True(t, outbox.IsInvalidEntryError(err), "got %v", err)
}

func TestPostgresEnqueueRejectsAnIncompleteEntry(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	for name, mutate := range map[string]func(*outbox.Entry){
		"no id":           func(e *outbox.Entry) { e.ID = uuid.Nil },
		"no event id":     func(e *outbox.Entry) { e.EventID = uuid.Nil },
		"no org":          func(e *outbox.Entry) { e.OrgID = uuid.Nil },
		"no project":      func(e *outbox.Entry) { e.ProjectID = uuid.Nil },
		"no target":       func(e *outbox.Entry) { e.Target = "" },
		"long target":     func(e *outbox.Entry) { e.Target = string(make([]byte, outbox.MaxTargetLen+1)) },
		"already settled": func(e *outbox.Entry) { e.Status = outbox.StatusDelivered },
	} {
		t.Run(name, func(t *testing.T) {
			e := entry(tc, uuid.New())
			mutate(&e)
			err := s.Enqueue(ctx, e)
			require.Error(t, err)
			assert.True(t, outbox.IsInvalidEntryError(err), "got %v", err)
		})
	}
}

func TestPostgresStoreOperationsRequireATenantScope(t *testing.T) {
	s, _, tc := newPostgresStore(t)
	bare := t.Context()

	require.Error(t, s.Enqueue(bare, entry(tc, uuid.New())))
	_, err := s.ClaimDue(bare, time.Now(), 10)
	require.Error(t, err)
	require.Error(t, s.Succeed(bare, outbox.Entry{ID: uuid.New()}, time.Now()))
	require.Error(t, s.Fail(bare, outbox.Entry{ID: uuid.New()}, time.Now(), "x"))
	_, err = s.ByID(bare, uuid.New(), "delivery-endpoint")
	require.Error(t, err)
}

func TestPostgresByIDOfAnUnknownEntryIsNotFound(t *testing.T) {
	s, ctx, _ := newPostgresStore(t)
	_, err := s.ByID(ctx, uuid.New(), "delivery-endpoint")
	require.Error(t, err)
	assert.True(t, outbox.IsNotFoundError(err), "got %v", err)
}

func TestPostgresListByTargetBreaksATiedCreatedAtByIDDescending(t *testing.T) {
	s, ctx, tc, sqlDB := newPostgresStoreAndDB(t)
	a := entry(tc, uuid.New())
	b := entry(tc, uuid.New())
	require.NoError(t, s.Enqueue(ctx, a))
	require.NoError(t, s.Enqueue(ctx, b))

	_, err := sqlDB.ExecContext(t.Context(),
		"UPDATE "+prefix+"outbox_entries SET created_at = $1 WHERE id IN ($2, $3)",
		time.Now().UTC(), a.ID, b.ID)
	require.NoError(t, err)

	lo, hi := a.ID, b.ID
	if bytes.Compare(lo[:], hi[:]) > 0 {
		lo, hi = hi, lo
	}

	list, err := s.ListByTarget(ctx, a.Target, outbox.MaxListLimit)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, hi, list[0].ID, "a tied created_at did not break to the larger id first")
	assert.Equal(t, lo, list[1].ID)

	limited, err := s.ListByTarget(ctx, a.Target, 1)
	require.NoError(t, err)
	require.Len(t, limited, 1)
	assert.Equal(t, hi, limited[0].ID)
}
