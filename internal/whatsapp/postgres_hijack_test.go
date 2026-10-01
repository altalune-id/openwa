package whatsapp_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/whatsapp"
)

// SECURITY: the fixture user bypasses RLS, so these prove the store's own org predicate.
func TestPostgres_Session_OtherOrgIsInvisibleWithoutRLS(t *testing.T) {
	f := newPgFixture(t)
	victim := tenant.Into(t.Context(), f.tc)
	devID := seedDevice(t, f, f.tc)
	s := whatsapp.NewSession(devID, f.tc.OrgID, f.tc.ProjectID, whatsapp.EngineWhatsmeow)
	require.NoError(t, f.store.Save(victim, s, 0))

	attackerTC := seedTenant(t, f.sqlDB, f.prefix)
	attacker := tenant.Into(t.Context(), attackerTC)

	_, err := f.store.ByDevice(attacker, devID)
	require.True(t, whatsapp.IsSessionNotFoundError(err), "got %v", err)

	rows, err := f.store.ByDevices(attacker, []uuid.UUID{devID})
	require.NoError(t, err)
	require.Empty(t, rows)

	require.True(t, whatsapp.IsSessionNotFoundError(f.store.Delete(attacker, devID)))

	plantedID := seedDevice(t, f, f.tc)
	planted := whatsapp.NewSession(plantedID, f.tc.OrgID, f.tc.ProjectID, whatsapp.EngineWhatsmeow)
	require.True(t, whatsapp.IsSessionNotFoundError(f.store.Save(attacker, planted, 0)),
		"a fresh row naming another org must be refused before the insert")
	_, err = f.store.ByDevice(victim, plantedID)
	require.True(t, whatsapp.IsSessionNotFoundError(err))

	hijack := *s
	hijack.PushName = "Hijacked"
	err = f.store.Save(attacker, &hijack, 0)
	require.True(t, whatsapp.IsSessionNotFoundError(err), "an upsert carrying another org's row id must be refused, got %v", err)
	got, err := f.store.ByDevice(victim, devID)
	require.NoError(t, err)
	require.Empty(t, got.PushName)
}

// SECURITY: same-org pre-check passes here, so only the ON CONFLICT org guard stands between the attacker and the victim's row.
func TestPostgres_Session_ConflictGuardRefusesForeignDeviceID(t *testing.T) {
	f := newPgFixture(t)
	victim := tenant.Into(t.Context(), f.tc)
	devID := seedDevice(t, f, f.tc)
	s := whatsapp.NewSession(devID, f.tc.OrgID, f.tc.ProjectID, whatsapp.EngineWhatsmeow)
	s.PushName = "Victim"
	require.NoError(t, f.store.Save(victim, s, 0))

	attackerTC := seedTenant(t, f.sqlDB, f.prefix)
	attacker := tenant.Into(t.Context(), attackerTC)
	forged := whatsapp.NewSession(devID, attackerTC.OrgID, attackerTC.ProjectID, whatsapp.EngineWhatsmeow)
	forged.PushName = "Hijacked"
	forged.State = whatsapp.StateLoggedOut

	err := f.store.Save(attacker, forged, 0)
	require.True(t, whatsapp.IsSessionNotFoundError(err), "got %v", err)

	got, err := f.store.ByDevice(victim, devID)
	require.NoError(t, err)
	require.Equal(t, "Victim", got.PushName)
	require.Equal(t, f.tc.OrgID, got.OrgID)
	require.Equal(t, whatsapp.StateLinking, got.State)
	require.Equal(t, 1, got.Version)
}
