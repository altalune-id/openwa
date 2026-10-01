package device_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/tenant"
)

// SECURITY: the fixture user bypasses RLS, so these prove the store's own org predicate.
func TestPostgres_Device_OtherOrgIsInvisibleWithoutRLS(t *testing.T) {
	f := newPgFixture(t)
	victim := tenant.Into(t.Context(), f.tc)
	d, err := device.New(f.tc.OrgID, f.tc.ProjectID, testPublicID(t), "Victim")
	require.NoError(t, err)
	require.NoError(t, f.store.Save(victim, d, 0))

	attackerTC := seedOtherTenant(t, f.sqlDB, f.prefix)
	attacker := tenant.Into(t.Context(), attackerTC)

	_, err = f.store.ByID(attacker, d.ID)
	require.True(t, device.IsNotFoundError(err), "ByID across orgs must be not-found, got %v", err)
	_, err = f.store.ByPublicID(attacker, d.PublicID)
	require.True(t, device.IsNotFoundError(err), "ByPublicID across orgs must be not-found, got %v", err)

	list, err := f.store.List(attacker, device.ListOpts{})
	require.NoError(t, err)
	require.Empty(t, list)

	require.True(t, device.IsNotFoundError(f.store.Delete(attacker, d.ID)))

	planted, err := device.New(f.tc.OrgID, f.tc.ProjectID, testPublicID(t), "Planted")
	require.NoError(t, err)
	require.True(t, device.IsNotFoundError(f.store.Save(attacker, planted, 0)),
		"a fresh row naming another org must be refused before the insert")
	_, err = f.store.ByID(victim, planted.ID)
	require.True(t, device.IsNotFoundError(err), "nothing was planted in the victim org")

	hijack := *d
	hijack.Name = "Hijacked"
	err = f.store.Save(attacker, &hijack, 0)
	require.True(t, device.IsNotFoundError(err), "an upsert carrying another org's row id must be refused, got %v", err)
	got, err := f.store.ByID(victim, d.ID)
	require.NoError(t, err)
	require.Equal(t, "Victim", got.Name)
}

func TestPostgres_Device_ReskinnedRowIsRefusedAndLeavesVictimUntouched(t *testing.T) {
	f := newPgFixture(t)
	victim := tenant.Into(t.Context(), f.tc)
	d, err := device.New(f.tc.OrgID, f.tc.ProjectID, testPublicID(t), "Victim")
	require.NoError(t, err)
	require.NoError(t, f.store.Save(victim, d, 0))

	attackerTC := seedOtherTenant(t, f.sqlDB, f.prefix)
	attacker := tenant.Into(t.Context(), attackerTC)

	for _, ifVersion := range []int{0, 1, 99} {
		reskinned := *d
		reskinned.OrgID = attackerTC.OrgID
		reskinned.ProjectID = attackerTC.ProjectID
		reskinned.PublicID = testPublicID(t)
		reskinned.Name = "Hijacked"
		err = f.store.Save(attacker, &reskinned, ifVersion)
		require.True(t, device.IsNotFoundError(err), "ifVersion %d: got %v", ifVersion, err)

		got, err := f.store.ByID(victim, d.ID)
		require.NoError(t, err)
		require.Equal(t, "Victim", got.Name, "ifVersion %d", ifVersion)
		require.Equal(t, d.PublicID, got.PublicID)
		require.Equal(t, f.tc.OrgID, got.OrgID)
		require.Equal(t, 1, got.Version, "ifVersion %d", ifVersion)
	}
}
