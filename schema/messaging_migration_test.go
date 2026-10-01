package schema_test

import (
	"database/sql"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/internal/testutil/pgtest"
	"altalune.id/openwa/schema"
)

func migrated(t *testing.T) (*sql.DB, string, string) {
	t.Helper()
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)
	cfg := config.Defaults()
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true
	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))
	return sqlDB, h.Schema, cfg.DB.TablePrefix
}

func TestMessagingMigration_TablesCarryRLS(t *testing.T) {
	sqlDB, sch, pfx := migrated(t)
	for _, table := range []string{"chats", "contacts", "messages", "message_retention"} {
		var rls, forced bool
		require.NoError(t, sqlDB.QueryRowContext(t.Context(),
			`SELECT c.relrowsecurity, c.relforcerowsecurity FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND c.relname = $2`,
			sch, pfx+table).Scan(&rls, &forced))
		require.True(t, rls && forced, "%s must enable and force RLS", table)
	}
}

func TestMessagingMigration_ActivityAtIsGenerated(t *testing.T) {
	sqlDB, sch, pfx := migrated(t)
	var generated string
	require.NoError(t, sqlDB.QueryRowContext(t.Context(),
		`SELECT is_generated FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 AND column_name = 'activity_at'`,
		sch, pfx+"chats").Scan(&generated))
	require.Equal(t, "ALWAYS", generated)
}

func TestMessagingMigration_PartialUniqueIndexes(t *testing.T) {
	sqlDB, sch, pfx := migrated(t)
	for name, want := range map[string]string{
		pfx + "messages_device_wa_id_key": "WHERE (wa_message_id <> ''::text)",
		pfx + "chats_device_lid_key":      "WHERE (lid <> ''::text)",
		pfx + "chats_public_id_key":       "(public_id)",
		pfx + "messages_public_id_key":    "(public_id)",
	} {
		var def string
		require.NoError(t, sqlDB.QueryRowContext(t.Context(),
			`SELECT indexdef FROM pg_indexes WHERE schemaname = $1 AND indexname = $2`, sch, name).Scan(&def))
		require.Contains(t, def, "UNIQUE")
		require.Contains(t, def, want)
	}
}

func TestMessagingMigration_QueueIndexMatchesTheClaim(t *testing.T) {
	sqlDB, sch, pfx := migrated(t)
	var def string
	require.NoError(t, sqlDB.QueryRowContext(t.Context(),
		`SELECT indexdef FROM pg_indexes WHERE schemaname = $1 AND indexname = $2`, sch, pfx+"messages_outbound_queue_idx").Scan(&def))
	require.Contains(t, def, "(device_id, created_at, id)")
	require.Contains(t, def, "direction = 'out'")
}

func TestMessagingMigration_TablesAreTenantScoped(t *testing.T) {
	for _, table := range []string{"chats", "contacts", "messages", "message_retention"} {
		require.True(t, slices.Contains(schema.TenantTableSuffixes, table), "run make tenant-tables: %s missing", table)
	}
}
