// Package rlsfixture migrates a schema as an owner role and hands back a pool bound to a NOBYPASSRLS login role, so row level security applies to the code under test.
package rlsfixture

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/pgtest"
	"altalune.id/openwa/nanoid"
	"altalune.id/openwa/schema"
)

// Fixture is a migrated schema plus the pools that reach it as owner and as the RLS-enforced app role.
type Fixture struct {
	Prefix string
	DBCfg  db.DBConfig
	MigDB  *sql.DB
	AppDB  *sql.DB
}

// Open creates the roles, migrates as the owner and opens the app pool; both roles are dropped when the test ends.
func Open(t *testing.T) *Fixture {
	t.Helper()
	h := pgtest.New(t)
	id, err := nanoid.New(10)
	require.NoError(t, err)
	suffix := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(id))
	ownerRole := "openwa_rlsowner_" + suffix
	appRole := "openwa_rlsapp_" + suffix
	prefix := "r" + suffix + "_"

	admin, err := db.Open(t.Context(), db.DBConfig{DSN: h.DSN}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })

	pgtest.CreateRole(t, admin, ownerRole, "NOLOGIN BYPASSRLS")
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT USAGE, CREATE ON SCHEMA public TO %q`, ownerRole))
	require.NoError(t, err)
	pgtest.CreateRole(t, admin, appRole, "LOGIN PASSWORD 'pw' NOBYPASSRLS")
	_, err = admin.ExecContext(t.Context(), fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %q`, appRole))
	require.NoError(t, err)
	for _, stmt := range []string{
		`ALTER DEFAULT PRIVILEGES FOR ROLE %[1]q IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %[2]q`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE %[1]q IN SCHEMA public GRANT EXECUTE ON FUNCTIONS TO %[2]q`,
	} {
		_, err = admin.ExecContext(t.Context(), fmt.Sprintf(stmt, ownerRole, appRole))
		require.NoError(t, err)
	}

	migDB, err := db.Open(t.Context(), db.DBConfig{DSN: h.DSN, Role: ownerRole, MaxOpenConns: 1}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = migDB.Close() })

	cfg := config.Defaults()
	cfg.DB.Schema = "public"
	cfg.DB.TablePrefix = prefix
	cfg.DB.AllowBypassRLS = false
	cfg.Tenant.RLSEnforce = true
	require.NoError(t, schema.MigrateUp(t.Context(), migDB, cfg))

	appDB, err := db.Open(t.Context(), db.DBConfig{DSN: pgtest.DSNWithUser(t, h.DSN, appRole, "pw"), MaxOpenConns: 2}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = appDB.Close() })

	var bypass bool
	require.NoError(t, appDB.QueryRowContext(t.Context(),
		`SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&bypass))
	require.False(t, bypass, "the app role must enforce RLS or the tests using it prove nothing")

	return &Fixture{
		Prefix: prefix,
		DBCfg:  db.DBConfig{Schema: "public", TablePrefix: prefix},
		MigDB:  migDB,
		AppDB:  appDB,
	}
}

// Pool returns the RLS-enforced pool.
func (f *Fixture) Pool() db.Pool { return db.Pool{W: f.AppDB, R: f.AppDB} }

// PgConn returns the tenant connection factory over the RLS-enforced pool.
func (f *Fixture) PgConn() *tenant.PgConn { return tenant.NewPgConn(f.AppDB) }

// SeedTenant inserts a user, an owner membership, an org and a project as the owner role.
func (f *Fixture) SeedTenant(t *testing.T) tenant.Context {
	t.Helper()
	userID, orgID, projID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	p := "public." + f.Prefix
	for _, s := range []struct {
		q    string
		args []any
	}{
		{"INSERT INTO " + p + "users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1, $2, '', '', false, $3, $3)",
			[]any{userID, userID.String() + "@example.com", now}},
		{"INSERT INTO " + p + "orgs (id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, 'Org', $3, $4, $4)",
			[]any{orgID, orgID.String()[:8], userID, now}},
		{"INSERT INTO " + p + "memberships (id, org_id, user_id, role, created_at) VALUES ($1, $2, $3, 'owner', $4)",
			[]any{uuid.New(), orgID, userID, now}},
		{"INSERT INTO " + p + "projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, $3, 'Web', $4, $5, $5)",
			[]any{projID, orgID, projID.String()[:8], userID, now}},
	} {
		_, err := f.MigDB.ExecContext(t.Context(), s.q, s.args...)
		require.NoError(t, err)
	}
	return tenant.Context{OrgID: orgID, ProjectID: projID, UserID: userID}
}

// IsRLSViolation reports whether err is Postgres refusing a row under a row level security policy (SQLSTATE 42501).
func IsRLSViolation(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "42501"
}
