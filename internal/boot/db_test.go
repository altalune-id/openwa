package boot_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/boot"
	"altalune.id/openwa/internal/platform/config"
)

func TestMigratorDBConfig_AllowsLockPlusWorker(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	cfg.DB.DSN = "postgres://x"
	require.Equal(t, 2, boot.MigratorDBConfig(cfg).MaxOpenConns)
}

func TestMigrationLockName_DerivesFromPrefix(t *testing.T) {
	t.Parallel()
	require.Equal(t, "migrate:openwa_", boot.MigrationLockName("openwa_"))
}
