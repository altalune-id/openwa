package todo_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/rlsfixture"
	"altalune.id/openwa/internal/todo"
)

type hijackFixture struct {
	store todo.Store
	orgA  tenant.Context
	orgB  tenant.Context
}

func newHijackFixture(t *testing.T) hijackFixture {
	t.Helper()
	f := rlsfixture.Open(t)
	return hijackFixture{
		store: todo.NewStore(f.DBCfg, f.Pool(), f.PgConn()),
		orgA:  f.SeedTenant(t),
		orgB:  f.SeedTenant(t),
	}
}

func (f hijackFixture) seedVictim(t *testing.T) *todo.Todo {
	t.Helper()
	victim, err := todo.New(f.orgA.OrgID, f.orgA.ProjectID, "org A's todo")
	require.NoError(t, err)
	require.NoError(t, f.store.Save(tenant.Into(t.Context(), f.orgA), victim))
	return victim
}

func (f hijackFixture) requireTitle(t *testing.T, id uuid.UUID, want string) {
	t.Helper()
	got, err := f.store.ByID(tenant.Into(t.Context(), f.orgA), id)
	require.NoError(t, err)
	require.Equal(t, want, got.Title, "another org rewrote org A's row across the tenant boundary")
}

func requireBlocked(t *testing.T, err error, what string) {
	t.Helper()
	require.Error(t, err, "%s: a cross-tenant write must be refused", what)
	require.True(t, todo.IsNotFoundError(err) || rlsfixture.IsRLSViolation(err), "%s: got %T: %v", what, err, err)
}

func TestPostgres_Todo_Save_RejectsReskinnedHijack(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)

	attack, err := todo.New(f.orgB.OrgID, f.orgB.ProjectID, "Hijacked")
	require.NoError(t, err)
	attack.ID = victim.ID

	requireBlocked(t, f.store.Save(tenant.Into(t.Context(), f.orgB), attack), "reskinned Save")
	f.requireTitle(t, victim.ID, "org A's todo")
}

func TestPostgres_Todo_Save_RejectsVerbatimHijack(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)

	attack := *victim
	attack.Title = "Hijacked"

	requireBlocked(t, f.store.Save(tenant.Into(t.Context(), f.orgB), &attack), "verbatim Save")
	f.requireTitle(t, victim.ID, "org A's todo")
}

func TestPostgres_Todo_Save_UpdatesOwnRow(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)

	victim.Title = "renamed by its owner"
	require.NoError(t, f.store.Save(tenant.Into(t.Context(), f.orgA), victim))
	f.requireTitle(t, victim.ID, "renamed by its owner")
}

func TestPostgres_Todo_Delete_RejectsCrossTenant(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)

	requireBlocked(t, f.store.Delete(tenant.Into(t.Context(), f.orgB), victim.ID), "cross-tenant Delete")
	f.requireTitle(t, victim.ID, "org A's todo")
}

func TestPostgres_Todo_TenantMissing(t *testing.T) {
	f := newHijackFixture(t)
	td, err := todo.New(f.orgA.OrgID, f.orgA.ProjectID, "milk")
	require.NoError(t, err)

	err = f.store.Save(t.Context(), td)
	assert.True(t, tenant.IsMissingError(err), "got %T: %v", err, err)
	var notFound *todo.NotFoundError
	assert.False(t, errors.As(err, &notFound), "MissingError should not resolve to NotFoundError")
}
