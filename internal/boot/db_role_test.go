package boot

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/config"
)

func TestMigratorConfig_RoleAlwaysFromMigratorRole(t *testing.T) {
	cases := []struct {
		name        string
		migratorDSN string
		dbRole      string
		migRole     string
		wantDSN     string
		wantRole    string
	}{
		{"separate dsn uses migrator role", "postgres://mig@h/db", "openwa_service", "openwa_owner", "postgres://mig@h/db", "openwa_owner"},
		{"single dsn still uses migrator role", "", "openwa_service", "openwa_owner", "postgres://app@h/db", "openwa_owner"},
		{"no migrator role means none", "", "openwa_service", "", "postgres://app@h/db", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.DB.DSN = "postgres://app@h/db"
			cfg.DB.Role = tc.dbRole
			cfg.DB.Migrator.DSN = tc.migratorDSN
			cfg.DB.Migrator.Role = tc.migRole

			got := MigratorDBConfig(cfg)

			require.Equal(t, tc.wantRole, got.Role, "db.role must never leak into migrations")
			require.Equal(t, tc.wantDSN, got.DSN)
			require.Equal(t, 2, got.MaxOpenConns, "one session holds the lock, one runs the migrations")
			require.Equal(t, 2, got.MaxIdleConns)
			require.Equal(t, tc.dbRole, cfg.DB.Role, "shaping the migrator config must not mutate the runtime config")
		})
	}
}
