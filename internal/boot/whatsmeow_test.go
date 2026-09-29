package boot_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/boot"
	"altalune.id/openwa/internal/testutil/pgtest"
)

func TestBoot_WhatsmeowTablesLiveInConfiguredSchema(t *testing.T) {
	h := pgtest.NewDatabase(t)
	admin := h.OpenDB(t)
	_, err := admin.ExecContext(t.Context(), "CREATE SCHEMA app")
	require.NoError(t, err)
	cfg := smokeCfgFor(t, h)
	cfg.DB.Schema = "app"
	srv, err := boot.BootServer(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })
	for _, table := range []string{"whatsmeow_device", "whatsmeow_version", "openwa_orgs"} {
		var schemaName string
		require.NoError(t, admin.QueryRowContext(t.Context(),
			`SELECT schemaname FROM pg_tables WHERE tablename = $1`, table).Scan(&schemaName), table)
		require.Equal(t, "app", schemaName, table)
	}
}
