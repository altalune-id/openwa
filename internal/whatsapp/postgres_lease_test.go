package whatsapp_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/whatsapp"
)

func seedLease(t *testing.T, f pgFixture, owner string, expiresIn time.Duration) uuid.UUID {
	t.Helper()
	devID := seedDevice(t, f, f.tc)
	l := whatsapp.Lease{DeviceID: devID, OrgID: f.tc.OrgID, ProjectID: f.tc.ProjectID, JID: "628111:1@s.whatsapp.net"}
	if owner != "" {
		exp := time.Now().UTC().Add(expiresIn)
		l.Owner, l.ExpiresAt = owner, &exp
	}
	require.NoError(t, f.leases.Upsert(t.Context(), l))
	return devID
}

func TestPostgres_Lease_ClaimIsExclusiveAcrossOwners(t *testing.T) {
	f := newPgFixture(t)
	devID := seedLease(t, f, "", 0)

	got, err := f.leases.Claim(t.Context(), "A", time.Minute, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, devID, got[0].DeviceID)
	require.Equal(t, "A", got[0].Owner)
	require.NotNil(t, got[0].ExpiresAt)

	again, err := f.leases.Claim(t.Context(), "B", time.Minute, 10)
	require.NoError(t, err)
	require.Empty(t, again)
}

func TestPostgres_Lease_ClaimNeverReturnsTheCallersOwnLeases(t *testing.T) {
	f := newPgFixture(t)
	seedLease(t, f, "A", -time.Minute)
	got, err := f.leases.Claim(t.Context(), "A", time.Minute, 10)
	require.NoError(t, err)
	require.Empty(t, got, "an expired lease A already holds must not be handed back to A by Claim")
}

func TestPostgres_Lease_ExpiredLeaseIsClaimable(t *testing.T) {
	f := newPgFixture(t)
	devID := seedLease(t, f, "A", -time.Minute)
	got, err := f.leases.Claim(t.Context(), "B", time.Minute, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, devID, got[0].DeviceID)
	require.Equal(t, "B", got[0].Owner)
}

func TestPostgres_Lease_ClaimHonoursLimitAndPrefersNulls(t *testing.T) {
	f := newPgFixture(t)
	free := seedLease(t, f, "", 0)
	seedLease(t, f, "Z", -time.Minute)
	got, err := f.leases.Claim(t.Context(), "A", time.Minute, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, free, got[0].DeviceID)
}

func TestPostgres_Lease_RenewReportsLostAndLeavesUpdatedAt(t *testing.T) {
	f := newPgFixture(t)
	kept := seedLease(t, f, "A", time.Minute)
	lost := seedLease(t, f, "A", -time.Minute)
	taken, err := f.leases.Claim(t.Context(), "B", time.Minute, 10)
	require.NoError(t, err)
	require.Len(t, taken, 1)
	require.Equal(t, lost, taken[0].DeviceID)

	var before time.Time
	require.NoError(t, f.sqlDB.QueryRowContext(t.Context(), "SELECT updated_at FROM "+f.prefix+"whatsapp_leases WHERE device_id = $1", kept).Scan(&before))

	held, err := f.leases.Renew(t.Context(), "A", time.Minute)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{kept}, held)

	var after time.Time
	var exp time.Time
	require.NoError(t, f.sqlDB.QueryRowContext(t.Context(), "SELECT updated_at, expires_at FROM "+f.prefix+"whatsapp_leases WHERE device_id = $1", kept).Scan(&after, &exp))
	require.True(t, after.Equal(before), "Renew must not touch updated_at")
	require.True(t, exp.After(time.Now().Add(50*time.Second)))
}

func TestPostgres_Lease_ReleaseOnlyByOwner(t *testing.T) {
	f := newPgFixture(t)
	devID := seedLease(t, f, "A", time.Minute)
	require.NoError(t, f.leases.Release(t.Context(), "B", devID))
	var owner *string
	require.NoError(t, f.sqlDB.QueryRowContext(t.Context(), "SELECT owner FROM "+f.prefix+"whatsapp_leases WHERE device_id = $1", devID).Scan(&owner))
	require.NotNil(t, owner)
	require.Equal(t, "A", *owner)

	require.NoError(t, f.leases.Release(t.Context(), "A", devID))
	require.NoError(t, f.sqlDB.QueryRowContext(t.Context(), "SELECT owner FROM "+f.prefix+"whatsapp_leases WHERE device_id = $1", devID).Scan(&owner))
	require.Nil(t, owner)
}

func TestPostgres_Lease_ReleaseAllExistsDelete(t *testing.T) {
	f := newPgFixture(t)
	a := seedLease(t, f, "A", time.Minute)
	b := seedLease(t, f, "A", time.Minute)
	other := seedLease(t, f, "B", time.Minute)
	require.NoError(t, f.leases.ReleaseAll(t.Context(), "A"))
	held, err := f.leases.Renew(t.Context(), "A", time.Minute)
	require.NoError(t, err)
	require.Empty(t, held)
	heldB, err := f.leases.Renew(t.Context(), "B", time.Minute)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{other}, heldB)

	ok, err := f.leases.Exists(t.Context(), a)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, f.leases.Delete(t.Context(), a))
	ok, err = f.leases.Exists(t.Context(), a)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, f.leases.Delete(t.Context(), a), "Delete is idempotent")
	_ = b
}

func TestPostgres_Lease_OwnedWithNullExpiryIsReclaimable(t *testing.T) {
	f := newPgFixture(t)
	devID := seedDevice(t, f, f.tc)
	l := whatsapp.Lease{DeviceID: devID, OrgID: f.tc.OrgID, ProjectID: f.tc.ProjectID, JID: "628111:1@s.whatsapp.net", Owner: "A"}
	require.NoError(t, f.leases.Upsert(t.Context(), l))

	got, err := f.leases.Claim(t.Context(), "B", time.Minute, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, devID, got[0].DeviceID)
	require.Equal(t, "B", got[0].Owner)
	require.NotNil(t, got[0].ExpiresAt)
}

func TestPostgres_Lease_ConcurrentClaimHasOneWinner(t *testing.T) {
	f := newPgFixture(t)
	const racers = 8
	for round := range 5 {
		devID := seedLease(t, f, "", 0)
		start := make(chan struct{})
		wins := make(chan uuid.UUID, racers)
		errs := make(chan error, racers)
		var wg sync.WaitGroup
		for i := range racers {
			wg.Go(func() {
				<-start
				got, err := f.leases.Claim(t.Context(), fmt.Sprintf("owner-%d-%d", round, i), time.Minute, 10)
				if err != nil {
					errs <- err
					return
				}
				for _, l := range got {
					wins <- l.DeviceID
				}
			})
		}
		close(start)
		wg.Wait()
		close(wins)
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		var claimed []uuid.UUID
		for id := range wins {
			claimed = append(claimed, id)
		}
		require.Equal(t, []uuid.UUID{devID}, claimed, "round %d: exactly one racer wins the lease", round)
		require.NoError(t, f.leases.Delete(t.Context(), devID))
	}
}
