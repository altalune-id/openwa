package outbox_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/tenant"
)

// SECURITY: the fixture bypasses RLS, so the project predicate is the only thing keeping a sibling project's newer delivery out.
func TestPostgresLatestByTargetsPicksTheNewestPerTargetInTheProject(t *testing.T) {
	s, ctx, tc, sqlDB := newPostgresStoreAndDB(t)
	sibling := tenant.Context{OrgID: tc.OrgID, ProjectID: uuid.New(), UserID: tc.UserID}
	_, err := sqlDB.ExecContext(t.Context(),
		"INSERT INTO openwa_projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, $3, 'Sib', $4, now(), now())",
		sibling.ProjectID, sibling.OrgID, sibling.ProjectID.String()[:8], sibling.UserID)
	require.NoError(t, err)

	at := func(c tenant.Context, target string) uuid.UUID {
		e := entry(c, uuid.New())
		e.Target = target
		require.NoError(t, s.Enqueue(tenant.Into(t.Context(), c), e))
		time.Sleep(2 * time.Millisecond)
		return e.ID
	}
	at(tc, "a")
	newestA := at(tc, "a")
	onlyB := at(tc, "b")
	at(tc, "c")
	at(sibling, "a")

	got, err := s.LatestByTargets(ctx, []string{"a", "b", "missing"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, newestA, got["a"].ID, "the newest of the caller's project, not the sibling's newer one")
	require.Equal(t, onlyB, got["b"].ID)

	none, err := s.LatestByTargets(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, none)

	other := tenant.Into(t.Context(), tenant.Context{OrgID: uuid.New(), ProjectID: tc.ProjectID})
	leaked, err := s.LatestByTargets(other, []string{"a", "b"})
	require.NoError(t, err)
	require.Empty(t, leaked)
}
