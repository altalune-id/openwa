package outbox_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/outbox"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
)

const prefix = "openwa_"

func entry(tc tenant.Context, eventID uuid.UUID) outbox.Entry {
	return outbox.Entry{
		ID:        uuid.New(),
		EventID:   eventID,
		OrgID:     tc.OrgID,
		ProjectID: tc.ProjectID,
		Target:    "delivery-endpoint",
		Payload:   []byte(`{"kind":"example"}`),
	}
}

type storeCase struct {
	name string
	new  func(t *testing.T) (outbox.Store, context.Context, tenant.Context)
}

func storeCases() []storeCase {
	return []storeCase{
		{name: "fake", new: newFakeStore},
	}
}

func newFakeStore(t *testing.T) (outbox.Store, context.Context, tenant.Context) {
	t.Helper()
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}
	return fakes.NewOutbox(), tenant.Into(t.Context(), tc), tc
}

func failToTerminal(ctx context.Context, t *testing.T, s outbox.Store, e outbox.Entry) outbox.Entry {
	t.Helper()
	now := time.Now().UTC()
	var last outbox.Entry
	for i := 1; i <= outbox.MaxAttempts; i++ {
		claimed, err := s.ClaimDue(ctx, now, 10)
		require.NoError(t, err, "attempt %d", i)
		require.Len(t, claimed, 1, "attempt %d", i)
		last = claimed[0]
		require.NoError(t, s.Fail(ctx, last, now, "endpoint keeps refusing"))
	}
	return last
}

func TestBackoffFollowsTheSchedule(t *testing.T) {
	cases := []struct {
		attempt int
		base    time.Duration
	}{
		{-1, 30 * time.Second},
		{1, 30 * time.Second},
		{2, 30 * time.Second},
		{3, 5 * time.Minute},
		{4, 30 * time.Minute},
		{5, 2 * time.Hour},
		{6, 5 * time.Hour},
		{7, 10 * time.Hour},
		{8, 10 * time.Hour},
		{99, 10 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("attempt %d", tc.attempt), func(t *testing.T) {
			spread := tc.base / 10
			low, high := tc.base-spread, tc.base+spread
			first := outbox.Backoff(tc.attempt)
			allEqual := true
			for range 200 {
				got := outbox.Backoff(tc.attempt)
				assert.GreaterOrEqual(t, got, low, "Backoff(%d)", tc.attempt)
				assert.LessOrEqual(t, got, high, "Backoff(%d)", tc.attempt)
				if got != first {
					allEqual = false
				}
			}
			assert.False(t, allEqual, "Backoff(%d) is not jittered; every replica will retry in lockstep", tc.attempt)
		})
	}
}

func TestStatusValidAndTerminal(t *testing.T) {
	assert.True(t, outbox.StatusPending.Valid())
	assert.True(t, outbox.StatusDelivered.Valid())
	assert.True(t, outbox.StatusFailed.Valid())
	assert.False(t, outbox.Status("queued").Valid())

	assert.False(t, outbox.StatusPending.Terminal())
	assert.True(t, outbox.StatusDelivered.Terminal())
	assert.True(t, outbox.StatusFailed.Terminal())
}

func TestRequeueResetsAFailedEntry(t *testing.T) {
	for _, sc := range storeCases() {
		t.Run(sc.name, func(t *testing.T) {
			s, ctx, tc := sc.new(t)
			e := entry(tc, uuid.New())
			require.NoError(t, s.Enqueue(ctx, e))
			failToTerminal(ctx, t, s, e)

			require.NoError(t, s.Requeue(ctx, e.ID, e.Target))

			listed, err := s.ListByTarget(ctx, e.Target, outbox.MaxListLimit)
			require.NoError(t, err)
			require.Len(t, listed, 1)
			assert.Equal(t, outbox.StatusPending, listed[0].Status)
			assert.Equal(t, 0, listed[0].Attempt)
			assert.Empty(t, listed[0].LastError)

			claimed, err := s.ClaimDue(ctx, time.Now(), 10)
			require.NoError(t, err)
			require.Len(t, claimed, 1, "a requeued entry was not claimable")
			assert.Equal(t, e.ID, claimed[0].ID)
		})
	}
}

func TestRequeueWithTheWrongTargetIsNotFound(t *testing.T) {
	for _, sc := range storeCases() {
		t.Run(sc.name, func(t *testing.T) {
			s, ctx, tc := sc.new(t)
			e := entry(tc, uuid.New())
			require.NoError(t, s.Enqueue(ctx, e))
			failToTerminal(ctx, t, s, e)

			err := s.Requeue(ctx, e.ID, "another-endpoint")
			require.Error(t, err)
			assert.True(t, outbox.IsNotFoundError(err), "got %v", err)
		})
	}
}

func TestRequeueOnANonFailedRowIsRefused(t *testing.T) {
	for _, sc := range storeCases() {
		t.Run(sc.name+"/pending", func(t *testing.T) {
			s, ctx, tc := sc.new(t)
			e := entry(tc, uuid.New())
			require.NoError(t, s.Enqueue(ctx, e))

			err := s.Requeue(ctx, e.ID, e.Target)
			require.Error(t, err)
			assert.True(t, outbox.IsNotFailedError(err), "got %v", err)
		})

		t.Run(sc.name+"/delivered", func(t *testing.T) {
			s, ctx, tc := sc.new(t)
			e := entry(tc, uuid.New())
			require.NoError(t, s.Enqueue(ctx, e))
			claimed, err := s.ClaimDue(ctx, time.Now(), 10)
			require.NoError(t, err)
			require.Len(t, claimed, 1)
			require.NoError(t, s.Succeed(ctx, claimed[0], time.Now()))

			err = s.Requeue(ctx, e.ID, e.Target)
			require.Error(t, err)
			assert.True(t, outbox.IsNotFailedError(err), "got %v", err)
		})
	}
}

func TestRequeueFailedRequeuesOnlyFailedRowsOfOneTargetInOneOrg(t *testing.T) {
	for _, sc := range storeCases() {
		t.Run(sc.name, func(t *testing.T) {
			s, ctx, tc := sc.new(t)

			failedA := entry(tc, uuid.New())
			require.NoError(t, s.Enqueue(ctx, failedA))
			failToTerminal(ctx, t, s, failedA)

			failedB := entry(tc, uuid.New())
			require.NoError(t, s.Enqueue(ctx, failedB))
			failToTerminal(ctx, t, s, failedB)

			otherTarget := entry(tc, uuid.New())
			otherTarget.Target = "another-endpoint"
			require.NoError(t, s.Enqueue(ctx, otherTarget))
			failToTerminal(ctx, t, s, otherTarget)

			stillPending := entry(tc, uuid.New())
			require.NoError(t, s.Enqueue(ctx, stillPending))

			n, err := s.RequeueFailed(ctx, failedA.Target)
			require.NoError(t, err)
			assert.Equal(t, 2, n)

			claimed, err := s.ClaimDue(ctx, time.Now(), 10)
			require.NoError(t, err)
			ids := map[uuid.UUID]bool{}
			for _, c := range claimed {
				ids[c.ID] = true
			}
			assert.True(t, ids[failedA.ID])
			assert.True(t, ids[failedB.ID])
			assert.True(t, ids[stillPending.ID])
			assert.False(t, ids[otherTarget.ID], "RequeueFailed touched another target's row")

			other := tenant.Into(t.Context(), tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()})
			n, err = s.RequeueFailed(other, otherTarget.Target)
			require.NoError(t, err)
			assert.Equal(t, 0, n, "RequeueFailed touched another org's row")
		})
	}
}

func TestListByTargetOrdersNewestFirstAndIsOrgScoped(t *testing.T) {
	for _, sc := range storeCases() {
		t.Run(sc.name, func(t *testing.T) {
			s, ctx, tc := sc.new(t)
			ids := make([]uuid.UUID, 0, 5)
			for range 5 {
				e := entry(tc, uuid.New())
				require.NoError(t, s.Enqueue(ctx, e))
				ids = append(ids, e.ID)
				time.Sleep(time.Millisecond)
			}

			list, err := s.ListByTarget(ctx, "delivery-endpoint", outbox.MaxListLimit)
			require.NoError(t, err)
			require.Len(t, list, 5)
			for i := range list {
				assert.Equal(t, ids[len(ids)-1-i], list[i].ID, "position %d not newest-first", i)
				assert.False(t, list[i].CreatedAt.IsZero(), "CreatedAt was not filled")
			}

			limited, err := s.ListByTarget(ctx, "delivery-endpoint", 2)
			require.NoError(t, err)
			require.Len(t, limited, 2)
			assert.Equal(t, ids[4], limited[0].ID)
			assert.Equal(t, ids[3], limited[1].ID)

			overLimit, err := s.ListByTarget(ctx, "delivery-endpoint", 1000)
			require.NoError(t, err)
			assert.Len(t, overLimit, 5, "a limit above MaxListLimit must still be accepted, just clamped")

			other := tenant.Into(t.Context(), tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()})
			leaked, err := s.ListByTarget(other, "delivery-endpoint", outbox.MaxListLimit)
			require.NoError(t, err)
			assert.Empty(t, leaked, "ListByTarget leaked another org's rows")
		})
	}
}

func TestListByTargetBreaksATiedCreatedAtByIDDescending(t *testing.T) {
	t.Run("fake", func(t *testing.T) {
		s, ctx, tc := newFakeStore(t)
		a := entry(tc, uuid.New())
		b := entry(tc, uuid.New())
		require.NoError(t, s.Enqueue(ctx, a))
		require.NoError(t, s.Enqueue(ctx, b))

		fake, ok := s.(*fakes.Outbox)
		require.True(t, ok)
		tied := time.Now().UTC()
		fake.SetCreatedAt(a.ID, tied)
		fake.SetCreatedAt(b.ID, tied)

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
		assert.Equal(t, hi, limited[0].ID, "the limit cutoff did not respect the id-descending tiebreak")
	})
}

func TestByIDReturnsTheEntryOfItsTarget(t *testing.T) {
	for _, sc := range storeCases() {
		t.Run(sc.name, func(t *testing.T) {
			s, ctx, tc := sc.new(t)
			e := entry(tc, uuid.New())
			require.NoError(t, s.Enqueue(ctx, e))

			got, err := s.ByID(ctx, e.ID, e.Target)
			require.NoError(t, err)
			assert.Equal(t, e.ID, got.ID)
			assert.Equal(t, e.EventID, got.EventID)
			assert.Equal(t, tc.OrgID, got.OrgID)
			assert.Equal(t, e.Target, got.Target)
			assert.Equal(t, e.Payload, got.Payload)
			assert.Equal(t, outbox.StatusPending, got.Status)
			assert.False(t, got.CreatedAt.IsZero(), "CreatedAt was not filled")
		})
	}
}

func TestByIDOfAnUnknownEntryIsNotFound(t *testing.T) {
	for _, sc := range storeCases() {
		t.Run(sc.name, func(t *testing.T) {
			s, ctx, _ := sc.new(t)
			_, err := s.ByID(ctx, uuid.New(), "delivery-endpoint")
			require.Error(t, err)
			assert.True(t, outbox.IsNotFoundError(err), "got %v", err)
		})
	}
}

func TestByIDIsScopedToOrgAndTarget(t *testing.T) {
	for _, sc := range storeCases() {
		t.Run(sc.name+"/another org", func(t *testing.T) {
			s, ctx, tc := sc.new(t)
			e := entry(tc, uuid.New())
			require.NoError(t, s.Enqueue(ctx, e))

			other := tenant.Into(t.Context(), tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()})
			_, err := s.ByID(other, e.ID, e.Target)
			require.Error(t, err)
			assert.True(t, outbox.IsNotFoundError(err), "an entry was read across the tenant boundary: %v", err)
		})

		t.Run(sc.name+"/another target", func(t *testing.T) {
			s, ctx, tc := sc.new(t)
			e := entry(tc, uuid.New())
			require.NoError(t, s.Enqueue(ctx, e))

			_, err := s.ByID(ctx, e.ID, "another-endpoint")
			require.Error(t, err)
			assert.True(t, outbox.IsNotFoundError(err), "an entry was read under another target: %v", err)
		})
	}
}
