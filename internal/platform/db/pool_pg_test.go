package db_test

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/testutil/pgtest"
)

func TestOpenPool_ReaderAliasesWriterWhenUnset(t *testing.T) {
	h := pgtest.New(t)
	p, err := db.OpenPool(t.Context(), db.DBConfig{DSN: h.DSN}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	require.NotNil(t, p.W)
	require.Same(t, p.W, p.R, "reader must alias writer when unset")
}

func TestOpenPool_SeparateReaderWhenConfigured(t *testing.T) {
	h := pgtest.New(t)
	p, err := db.OpenPool(t.Context(), db.DBConfig{DSN: h.DSN, Reader: db.ReaderConfig{DSN: h.DSN}}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	require.NotSame(t, p.W, p.R, "a configured reader DSN must open its own handle")
	require.NoError(t, p.R.PingContext(t.Context()))
}

func TestOpen_AppliesPoolTuning(t *testing.T) {
	h := pgtest.New(t)
	sqlDB, err := db.Open(t.Context(), db.DBConfig{DSN: h.DSN, MaxOpenConns: 3, MaxIdleConns: 2}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.Equal(t, 3, sqlDB.Stats().MaxOpenConnections)
}

func TestOpen_PinsSearchPathToSchema(t *testing.T) {
	h := pgtest.New(t)
	cfg := db.DBConfig{DSN: h.DSN, Schema: h.Schema}
	conn, err := db.Open(t.Context(), cfg, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	var sp string
	require.NoError(t, conn.QueryRowContext(t.Context(), "SHOW search_path").Scan(&sp))
	require.Equal(t, h.Schema, sp)
}
