package schema_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/internal/testutil/pgtest"
	"altalune.id/openwa/schema"
)

func TestMigrateUp_DevicesTablesAndRLS(t *testing.T) {
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)
	cfg := config.Defaults()
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true
	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))

	cases := []struct {
		table string
		rls   bool
	}{
		{"openwa_devices", true},
		{"openwa_whatsapp_sessions", true},
		{"openwa_whatsapp_leases", false},
	}
	for _, tc := range cases {
		var rls bool
		err := sqlDB.QueryRowContext(t.Context(),
			`SELECT c.relrowsecurity FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND c.relname = $2`,
			h.Schema, tc.table).Scan(&rls)
		require.NoError(t, err, tc.table)
		require.Equal(t, tc.rls, rls, "%s row security", tc.table)
	}

	var idx string
	require.NoError(t, sqlDB.QueryRowContext(t.Context(),
		`SELECT indexdef FROM pg_indexes WHERE schemaname = $1 AND indexname = 'openwa_devices_project_lower_name_idx'`, h.Schema).Scan(&idx))
	require.Contains(t, idx, "UNIQUE")
	require.Contains(t, idx, "lower(name)")

	var pub string
	require.NoError(t, sqlDB.QueryRowContext(t.Context(),
		`SELECT indexdef FROM pg_indexes WHERE schemaname = $1 AND indexname = 'openwa_devices_public_id_key'`, h.Schema).Scan(&pub))
	require.Contains(t, pub, "UNIQUE")
	require.Contains(t, pub, "(public_id)")
}
