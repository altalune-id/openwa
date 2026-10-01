package db_test

import (
	"database/sql"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/db"
)

func TestOpen_EmptyDSN(t *testing.T) {
	t.Parallel()
	if _, err := db.Open(t.Context(), db.DBConfig{}, nil); err == nil {
		t.Fatal("Open with empty DSN = nil error, want failure")
	}
}

func TestDBConfig_Validate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     db.DBConfig
		wantErr bool
	}{
		{"ok postgres", db.DBConfig{DSN: "postgres://x"}, false},
		{"missing DSN", db.DBConfig{}, true},
		{"negative MaxOpenConns", db.DBConfig{DSN: "postgres://x", MaxOpenConns: -1}, true},
		{"negative MaxIdleConns", db.DBConfig{DSN: "postgres://x", MaxIdleConns: -1}, true},
		{"negative ConnMaxLifetime", db.DBConfig{DSN: "postgres://x", ConnMaxLifetime: -1}, true},
		{"negative ConnMaxIdleTime", db.DBConfig{DSN: "postgres://x", ConnMaxIdleTime: -1}, true},
		{"negative connectTimeout", db.DBConfig{DSN: "postgres://x", ConnectTimeout: -1}, true},
		{"negative connectBackoff", db.DBConfig{DSN: "postgres://x", ConnectBackoff: -1}, true},
		{"negative health.interval", db.DBConfig{DSN: "postgres://x", Health: db.HealthConfig{Interval: -1}}, true},
		{"negative health.timeout", db.DBConfig{DSN: "postgres://x", Health: db.HealthConfig{Timeout: -1}}, true},
		{"negative reader.maxOpenConns", db.DBConfig{DSN: "postgres://x", Reader: db.ReaderConfig{MaxOpenConns: -1}}, true},
		{"ok reader and health set", db.DBConfig{DSN: "postgres://x", Reader: db.ReaderConfig{DSN: "postgres://x", MaxOpenConns: 2}, Health: db.HealthConfig{Interval: 30 * time.Second, Timeout: 2 * time.Second}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestOpen_RetriesThenFailsWithinBudget(t *testing.T) {
	cfg := db.DBConfig{
		DSN:            "postgres://nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1",
		ConnectTimeout: 2 * time.Second,
		ConnectBackoff: 200 * time.Millisecond,
	}
	start := time.Now()
	_, err := db.Open(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.Error(t, err)
	require.GreaterOrEqual(t, time.Since(start), 2*time.Second, "must have retried across the budget")
}

func TestPool_Close_HandlesAliasing(t *testing.T) {
	tests := []struct {
		name string
		pool func(w *sql.DB) db.Pool
	}{
		{"reader aliased", func(w *sql.DB) db.Pool { return db.Pool{W: w, R: w} }},
		{"nil reader", func(w *sql.DB) db.Pool { return db.Pool{W: w} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := openMockDB(t)
			require.NoError(t, tt.pool(w).Close())
		})
	}
}

func TestPool_Close_DistinctHandles(t *testing.T) {
	w, r := openMockDB(t), openMockDB(t)
	require.NoError(t, db.Pool{W: w, R: r}.Close())

	for name, d := range map[string]*sql.DB{"writer": w, "reader": r} {
		require.Error(t, d.PingContext(t.Context()), "%s must be closed", name)
	}
}

func openMockDB(t *testing.T) *sql.DB {
	t.Helper()
	d, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectClose()
	return d
}
