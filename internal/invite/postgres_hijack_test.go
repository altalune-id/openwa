package invite_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/invite"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/rlsfixture"
)

type hijackFixture struct {
	store invite.Store
	orgA  tenant.Context
	orgB  tenant.Context
}

func newHijackFixture(t *testing.T) hijackFixture {
	t.Helper()
	f := rlsfixture.Open(t)
	return hijackFixture{
		store: invite.NewStore(f.DBCfg, f.Pool(), f.PgConn()),
		orgA:  f.SeedTenant(t),
		orgB:  f.SeedTenant(t),
	}
}

func (f hijackFixture) seedVictim(t *testing.T) *invite.Invite {
	t.Helper()
	victim := newInvite(t, f.orgA.OrgID, "victim@example.com", "victim-token")
	require.NoError(t, f.store.Save(tenant.Into(t.Context(), f.orgA), victim))
	return victim
}

func (f hijackFixture) requireEmail(t *testing.T, id uuid.UUID, want string) {
	t.Helper()
	got, err := f.store.ByID(tenant.Into(t.Context(), f.orgA), id)
	require.NoError(t, err)
	require.Equal(t, want, got.Email, "another org rewrote org A's row across the tenant boundary")
}

func requireBlocked(t *testing.T, err error, what string) {
	t.Helper()
	require.Error(t, err, "%s: a cross-tenant write must be refused", what)
	require.True(t, invite.IsNotFoundError(err) || rlsfixture.IsRLSViolation(err), "%s: got %T: %v", what, err, err)
}

func TestPostgres_Invite_Save_RejectsReskinnedHijack(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)

	attack := newInvite(t, f.orgB.OrgID, "attacker@example.com", "attacker-token")
	attack.ID = victim.ID

	requireBlocked(t, f.store.Save(tenant.Into(t.Context(), f.orgB), attack), "reskinned Save")
	f.requireEmail(t, victim.ID, "victim@example.com")
}

func TestPostgres_Invite_Save_RejectsVerbatimHijack(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)

	attack := *victim
	attack.Email = "attacker@example.com"

	requireBlocked(t, f.store.Save(tenant.Into(t.Context(), f.orgB), &attack), "verbatim Save")
	f.requireEmail(t, victim.ID, "victim@example.com")
}

func TestPostgres_Invite_Save_UpdatesOwnRow(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)

	victim.Email = "victim+renamed@example.com"
	require.NoError(t, f.store.Save(tenant.Into(t.Context(), f.orgA), victim))
	f.requireEmail(t, victim.ID, "victim+renamed@example.com")
}

func TestPostgres_Invite_Delete_RejectsCrossTenant(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)

	requireBlocked(t, f.store.Delete(tenant.Into(t.Context(), f.orgB), victim.ID), "cross-tenant Delete")
	f.requireEmail(t, victim.ID, "victim@example.com")
}

func TestPostgres_Invite_ByID_CrossTenantHidden(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)

	_, err := f.store.ByID(tenant.Into(t.Context(), f.orgB), victim.ID)
	assert.True(t, invite.IsNotFoundError(err), "got %T: %v", err, err)
}

func TestPostgres_Invite_TenantMissing(t *testing.T) {
	f := newHijackFixture(t)
	inv := newInvite(t, f.orgA.OrgID, "alice@example.com", "tok")
	err := f.store.Save(t.Context(), inv)
	assert.True(t, tenant.IsMissingError(err), "got %T: %v", err, err)
}
