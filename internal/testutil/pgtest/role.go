package pgtest

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const roleLockKey = 7401

// CreateRole creates a role under one advisory lock so parallel packages sharing TEST_PG_DSN never race the role catalog, grants it to the fixture user, and drops it when the test ends. The lock and DDL run on the base TEST_PG_DSN database; a test that creates objects as the role inside another database (NewDatabase) drops them itself before it ends, or DROP ROLE fails.
func CreateRole(t *testing.T, admin *sql.DB, name, attrs string) {
	t.Helper()
	ident := pgx.Identifier{name}.Sanitize()
	base := admin
	if dsn := os.Getenv(envDSN); dsn != "" {
		base = openRaw(t, dsn)
		t.Cleanup(func() { _ = base.Close() })
	}
	tx, err := base.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("pgtest: begin: %v", err)
	}
	if _, err := tx.ExecContext(t.Context(), "SELECT pg_advisory_xact_lock($1)", roleLockKey); err != nil {
		_ = tx.Rollback()
		t.Fatalf("pgtest: role lock: %v", err)
	}
	if _, err := tx.ExecContext(t.Context(), "CREATE ROLE "+ident+" "+attrs); err != nil {
		_ = tx.Rollback()
		t.Fatalf("pgtest: create role %s: %v", name, err)
	}
	if _, err := tx.ExecContext(t.Context(), "GRANT "+ident+" TO CURRENT_USER"); err != nil {
		_ = tx.Rollback()
		t.Fatalf("pgtest: grant role %s: %v", name, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("pgtest: commit role: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tx, err := base.BeginTx(ctx, nil)
		if err != nil {
			t.Errorf("pgtest: begin drop role: %v", err)
			return
		}
		_, _ = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", roleLockKey)
		_, _ = tx.ExecContext(ctx, "REASSIGN OWNED BY "+ident+" TO CURRENT_USER")
		_, _ = tx.ExecContext(ctx, "DROP OWNED BY "+ident)
		if _, err := tx.ExecContext(ctx, "DROP ROLE IF EXISTS "+ident); err != nil {
			_ = tx.Rollback()
			t.Errorf("pgtest: leaked role %s: %v", name, err)
			return
		}
		_ = tx.Commit()
	})
}
