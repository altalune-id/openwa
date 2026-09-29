package project_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/project"
	"altalune.id/openwa/internal/testutil/rlsfixture"
)

type hijackFixture struct {
	store project.Store
	orgA  tenant.Context
	orgB  tenant.Context
}

func newHijackFixture(t *testing.T) hijackFixture {
	t.Helper()
	f := rlsfixture.Open(t)
	return hijackFixture{
		store: project.NewStore(f.DBCfg, f.Pool(), f.PgConn()),
		orgA:  f.SeedTenant(t),
		orgB:  f.SeedTenant(t),
	}
}

func (f hijackFixture) seedVictim(t *testing.T) *project.Project {
	t.Helper()
	victim, err := project.New(f.orgA.OrgID, "victim", "org A's project")
	require.NoError(t, err)
	require.NoError(t, f.store.Save(tenant.Into(t.Context(), f.orgA), victim))
	return victim
}

func (f hijackFixture) requireName(t *testing.T, id uuid.UUID, want string) {
	t.Helper()
	got, err := f.store.ByID(tenant.Into(t.Context(), f.orgA), id)
	require.NoError(t, err)
	require.Equal(t, want, got.Name, "another org rewrote org A's row across the tenant boundary")
}

func requireBlocked(t *testing.T, err error, what string) {
	t.Helper()
	require.Error(t, err, "%s: a cross-tenant write must be refused", what)
	require.True(t, project.IsNotFoundError(err) || rlsfixture.IsRLSViolation(err), "%s: got %T: %v", what, err, err)
}

func TestPostgres_Project_Save_RejectsReskinnedHijack(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)

	attack, err := project.New(f.orgB.OrgID, "victim", "Hijacked")
	require.NoError(t, err)
	attack.ID = victim.ID

	requireBlocked(t, f.store.Save(tenant.Into(t.Context(), f.orgB), attack), "reskinned Save")
	f.requireName(t, victim.ID, "org A's project")
}

func TestPostgres_Project_Save_RejectsVerbatimHijack(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)

	attack := *victim
	attack.Name = "Hijacked"

	requireBlocked(t, f.store.Save(tenant.Into(t.Context(), f.orgB), &attack), "verbatim Save")
	f.requireName(t, victim.ID, "org A's project")
}

func TestPostgres_Project_Save_UpdatesOwnRow(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)

	victim.Name = "renamed by its owner"
	require.NoError(t, f.store.Save(tenant.Into(t.Context(), f.orgA), victim))
	f.requireName(t, victim.ID, "renamed by its owner")
}

func TestPostgres_Project_OtherOrgIsInvisible(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)
	ctxB := tenant.Into(t.Context(), f.orgB)

	_, err := f.store.ByID(ctxB, victim.ID)
	assert.True(t, project.IsNotFoundError(err), "got %T: %v", err, err)
	_, err = f.store.BySlug(ctxB, f.orgA.OrgID, "victim")
	assert.True(t, project.IsNotFoundError(err), "got %T: %v", err, err)
	listed, err := f.store.List(ctxB, f.orgA.OrgID)
	require.NoError(t, err)
	assert.Empty(t, listed, "naming org A from org B's context must list nothing")
}

func TestPostgres_Project_MissingTenant(t *testing.T) {
	f := newHijackFixture(t)
	ctx := t.Context()

	assert.True(t, tenant.IsMissingError(f.store.Save(ctx, &project.Project{ID: uuid.New()})))
	_, err := f.store.ByID(ctx, uuid.New())
	assert.True(t, tenant.IsMissingError(err), "ByID: got %T: %v", err, err)
	_, err = f.store.BySlug(ctx, uuid.New(), "x")
	assert.True(t, tenant.IsMissingError(err), "BySlug: got %T: %v", err, err)
	_, err = f.store.List(ctx, uuid.New())
	assert.True(t, tenant.IsMissingError(err), "List: got %T: %v", err, err)
}
