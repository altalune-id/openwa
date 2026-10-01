package schema

import (
	"context"
	"database/sql"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/internal/testutil/pgtest"
)

func memMapFS() fstest.MapFS {
	return fstest.MapFS{
		"001.sql": &fstest.MapFile{Data: []byte(`SELECT '{{.TablePrefix}}';`)},
	}
}

func TestMigrationsBookkeepingTable_Postgres(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	cfg.DB.TablePrefix = "openwa_"
	assert.Equal(t, "openwa_goose_db_version", migrationsBookkeepingTable(cfg))
}

func TestMigrationsBookkeepingTable_CustomPrefix(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	cfg.DB.TablePrefix = "acme_"
	assert.Equal(t, "acme_goose_db_version", migrationsBookkeepingTable(cfg))
}

func TestMigrateStatus_ReportsPending(t *testing.T) {
	t.Parallel()
	sqldb, cfg := pgMigrationEnv(t)

	rows, err := MigrateStatus(context.Background(), sqldb, cfg)
	require.NoError(t, err)
	assert.NotEmpty(t, rows)
	for _, r := range rows {
		assert.False(t, r.Applied)
		assert.NotEmpty(t, r.Source)
	}
}

func TestMigrateStatus_ReportsApplied(t *testing.T) {
	t.Parallel()
	sqldb, cfg := pgMigrationEnv(t)

	require.NoError(t, MigrateUp(context.Background(), sqldb, cfg))
	rows, err := MigrateStatus(context.Background(), sqldb, cfg)
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	applied := 0
	for _, r := range rows {
		if r.Applied {
			applied++
		}
	}
	assert.Positive(t, applied)
}

func TestMigrateDownTo_RollsBackToZero(t *testing.T) {
	t.Parallel()
	sqldb, cfg := pgMigrationEnv(t)

	require.NoError(t, MigrateUp(context.Background(), sqldb, cfg))
	require.NoError(t, MigrateDownTo(context.Background(), sqldb, cfg, 0))

	rows, err := MigrateStatus(context.Background(), sqldb, cfg)
	require.NoError(t, err)
	for _, r := range rows {
		assert.False(t, r.Applied, "version %d should be rolled back", r.Version)
	}
}

func TestTemplatedFS_MemoryFileStatFields(t *testing.T) {
	t.Parallel()
	base := memMapFS()
	tfs := newTemplatedFS(base, templateVars{TablePrefix: "openwa_"})
	f, err := tfs.Open("001.sql")
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	info, err := f.Stat()
	require.NoError(t, err)
	assert.Equal(t, "001.sql", info.Name())
	assert.False(t, info.IsDir())
	assert.NotZero(t, info.Mode())
	assert.NotZero(t, info.Size())
	_ = info.ModTime()
	assert.Nil(t, info.Sys())
}

func pgMigrationEnv(t *testing.T) (*sql.DB, *config.Config) {
	t.Helper()
	h := pgtest.New(t)
	sqldb := h.OpenDB(t)
	cfg := config.Defaults()
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true
	return sqldb, cfg
}
