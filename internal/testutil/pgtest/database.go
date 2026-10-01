package pgtest

import (
	"context"
	"database/sql"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// NewDatabase returns a handle whose DSN names a database created for this test alone; boot fixtures and whatsmeow upgrades use it because whatsmeow's dbutil checks information_schema.tables without a schema filter.
func NewDatabase(t *testing.T) *Handle {
	t.Helper()
	base := New(t)
	name := uniqueSchema(t)

	admin := openRaw(t, base.DSN)
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0"); err != nil {
		_ = admin.Close()
		t.Fatalf("pgtest: create database %s (the TEST_PG_DSN role needs CREATEDB): %v", name, err)
	}
	if base.container == nil {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
				t.Errorf("pgtest: leaked database %s: %v", name, err)
			}
			_ = admin.Close()
		})
	} else {
		t.Cleanup(func() { _ = admin.Close() })
	}

	u, err := url.Parse(base.DSN)
	if err != nil {
		t.Fatalf("pgtest: parse DSN: %v", err)
	}
	u.Path = "/" + name
	return &Handle{DSN: u.String(), Schema: "public", container: nil}
}

func openRaw(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("pgtest: parse DSN: %v", err)
	}
	db := stdlib.OpenDB(*cfg)
	if err := db.PingContext(t.Context()); err != nil {
		_ = db.Close()
		t.Fatalf("pgtest: ping: %v", err)
	}
	return db
}
