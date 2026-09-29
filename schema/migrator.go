// Package schema owns the goose-backed migration runner and the RLS boot guard.
package schema

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"

	"altalune.id/openwa/internal/platform/config"
)

//go:embed migrations/postgres/VERSION migrations/postgres/*.sql
var migrationsFS embed.FS

const migrationsDir = "migrations/postgres"

// TargetVersion reads migrations/postgres/VERSION and returns the pinned goose target.
func TargetVersion() (int64, error) {
	const path = migrationsDir + "/VERSION"
	b, err := fs.ReadFile(migrationsFS, path)
	if err != nil {
		return 0, fmt.Errorf("migrator: read %s: %w — pin the target migration version (a single integer, one line)", path, err)
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return 0, fmt.Errorf("migrator: %s is empty — pin the target migration version (a single integer, one line)", path)
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("migrator: %s %q is not a valid version: %w", path, s, err)
	}
	if v < 0 {
		return 0, fmt.Errorf("migrator: %s %d is negative", path, v)
	}
	return v, nil
}

func migrationsBookkeepingTable(cfg *config.Config) string {
	return cfg.DB.TablePrefix + "goose_db_version"
}

func migrationProvider(sqldb *sql.DB, cfg *config.Config) (*goose.Provider, error) {
	sub, err := fs.Sub(migrationsFS, migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("migrator: sub-fs %s: %w", migrationsDir, err)
	}
	tpl := newTemplatedFS(sub, templateVars{
		Schema:      cfg.DB.Schema,
		TablePrefix: cfg.DB.TablePrefix,
		RLSEnforce:  cfg.Tenant.RLSEnforce,
	})
	store, err := database.NewStore(database.DialectPostgres, migrationsBookkeepingTable(cfg))
	if err != nil {
		return nil, fmt.Errorf("migrator: store: %w", err)
	}
	return goose.NewProvider("", sqldb, tpl, goose.WithStore(store))
}

// MigrateUp applies pending migrations up to the version pinned in migrations/postgres/VERSION. Newer migration files sitting beyond the pinned version are ignored.
func MigrateUp(ctx context.Context, sqldb *sql.DB, cfg *config.Config) error {
	target, err := TargetVersion()
	if err != nil {
		return err
	}
	p, err := migrationProvider(sqldb, cfg)
	if err != nil {
		return err
	}
	if _, err := p.UpTo(ctx, target); err != nil {
		return fmt.Errorf("migrator: up to %d: %w", target, err)
	}
	return nil
}

// MigrationRow is the CLI-facing projection of one migration's status.
type MigrationRow struct {
	Version   int64
	Applied   bool
	AppliedAt time.Time
	Source    string
}

// MigrateStatus reports every known migration and whether it has been applied.
func MigrateStatus(ctx context.Context, sqldb *sql.DB, cfg *config.Config) ([]MigrationRow, error) {
	p, err := migrationProvider(sqldb, cfg)
	if err != nil {
		return nil, err
	}
	items, err := p.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrator: status: %w", err)
	}
	rows := make([]MigrationRow, 0, len(items))
	for _, it := range items {
		row := MigrationRow{Applied: it.State == goose.StateApplied, AppliedAt: it.AppliedAt}
		if it.Source != nil {
			row.Version = it.Source.Version
			row.Source = it.Source.Path
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// MigrateDownTo rolls migrations back to the given goose version.
func MigrateDownTo(ctx context.Context, sqldb *sql.DB, cfg *config.Config, version int64) error {
	p, err := migrationProvider(sqldb, cfg)
	if err != nil {
		return err
	}
	if _, err := p.DownTo(ctx, version); err != nil {
		return fmt.Errorf("migrator: down-to %d: %w", version, err)
	}
	return nil
}

// MigrationsFS returns the embedded migration files.
func MigrationsFS() fs.FS { return migrationsFS }
