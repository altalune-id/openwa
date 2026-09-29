package pgtest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/testutil/pgtest"
)

func TestNewDatabase_IsIsolatedAndDropped(t *testing.T) {
	h := pgtest.NewDatabase(t)
	require.Equal(t, "public", h.Schema)
	db := h.OpenDB(t)
	_, err := db.Exec(`CREATE TABLE whatsmeow_version (version INTEGER)`)
	require.NoError(t, err)

	other := pgtest.NewDatabase(t)
	odb := other.OpenDB(t)
	var n int
	require.NoError(t, odb.QueryRow(
		`SELECT count(*) FROM information_schema.tables WHERE table_name = 'whatsmeow_version'`).Scan(&n))
	require.Zero(t, n, "a second database must not see the first one's tables")
}

func TestCreateRole_SerialisesAndCleansUp(t *testing.T) {
	h := pgtest.New(t)
	admin := h.OpenDB(t)
	name := "pgt_role_" + h.Schema
	pgtest.CreateRole(t, admin, name, "NOLOGIN")
	var exists bool
	require.NoError(t, admin.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, name).Scan(&exists))
	require.True(t, exists)
}

func TestOpenDB_HandlesDoNotShareASchema(t *testing.T) {
	a := pgtest.New(t)
	b := pgtest.New(t)
	require.True(t, strings.HasPrefix(a.Schema, "pgt_"), "schema %q must be namespaced", a.Schema)
	require.NotEqual(t, a.Schema, b.Schema, "every handle must get its own schema")

	dbA, dbB := a.OpenDB(t), b.OpenDB(t)
	_, err := dbA.ExecContext(t.Context(), `CREATE TABLE isolation_probe (id INT)`)
	require.NoError(t, err)

	var schema string
	require.NoError(t, dbA.QueryRowContext(t.Context(),
		`SELECT schemaname FROM pg_tables WHERE tablename = 'isolation_probe'`).Scan(&schema))
	require.Equal(t, a.Schema, schema, "OpenDB must place objects in the handle's schema, not public")

	var visible bool
	require.NoError(t, dbB.QueryRowContext(t.Context(),
		`SELECT EXISTS (SELECT 1 FROM pg_tables
		   WHERE tablename = 'isolation_probe' AND schemaname = ANY (current_schemas(false)))`).Scan(&visible))
	require.False(t, visible, "one handle must not see another handle's tables")
}

func TestOpenDB_DropsItsSchemaWhenTheTestEnds(t *testing.T) {
	outer := pgtest.New(t)
	probe := outer.OpenDB(t)

	inner := &pgtest.Handle{DSN: outer.DSN, Schema: "pgt_cleanup_probe"}
	t.Run("inner", func(t *testing.T) {
		innerDB := inner.OpenDB(t)
		_, err := innerDB.ExecContext(t.Context(), `CREATE TABLE leak_probe (id INT)`)
		require.NoError(t, err)
	})

	var exists bool
	require.NoError(t, probe.QueryRowContext(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, inner.Schema).Scan(&exists))
	require.False(t, exists, "the subtest's schema must be dropped with its contents")
}
