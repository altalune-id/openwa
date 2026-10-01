package tenant

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/db"
)

func TestNewOrgReader_PostgresReadsTheDefinerWrapper(t *testing.T) {
	r, ok := NewOrgReader(db.Pool{}, "public", "openwa_").(*pgOrgReader)
	require.True(t, ok, "postgres must select the wrapper-backed reader")
	require.Contains(t, r.query, "public.openwa_list_org_ids() o")
	require.Contains(t, r.query, "ORDER BY o.created_at ASC, o.id ASC",
		"the outer statement must re-order: Postgres may inline the wrapper and drop its internal ORDER BY")
	require.NotContains(t, r.query, "openwa_orgs",
		"SECURITY: reading openwa_orgs directly returns zero rows under FORCE row level security")
	require.Empty(t, r.args)
}

func TestNewOrgReader_PostgresDefaultsBlankSchemaToPublic(t *testing.T) {
	r, ok := NewOrgReader(db.Pool{}, "", "openwa_").(*pgOrgReader)
	require.True(t, ok)
	require.Contains(t, r.query, "public.openwa_list_org_ids() o")
}
