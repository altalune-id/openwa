// Package tenantseed opens a migrated Postgres fixture and seeds the tenant, device and chat rows store tests stand on.
package tenantseed

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/platform/publicid"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/pgtest"
	"altalune.id/openwa/schema"
)

// DB is one migrated fixture; the connecting role bypasses RLS, so a store's own org predicate is the only guard.
type DB struct {
	SQL    *sql.DB
	Cfg    db.DBConfig
	Pool   db.Pool
	PC     *tenant.PgConn
	Prefix string
}

// Open migrates a fresh fixture.
func Open(t *testing.T) DB {
	t.Helper()
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)
	cfg := config.Defaults()
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true
	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))
	// SECURITY: the hijack tests prove a store's own org predicate only while RLS cannot answer for it.
	var bypass bool
	require.NoError(t, sqlDB.QueryRowContext(t.Context(),
		`SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&bypass))
	require.True(t, bypass, "tenantseed needs a role that bypasses RLS")
	return DB{
		SQL:    sqlDB,
		Cfg:    db.DBConfig{DSN: h.DSN, Schema: h.Schema, TablePrefix: cfg.DB.TablePrefix},
		Pool:   db.Pool{W: sqlDB, R: sqlDB},
		PC:     tenant.NewPgConn(sqlDB),
		Prefix: cfg.DB.TablePrefix,
	}
}

// Tenant inserts a user, an org and a project and returns their scope.
func (d DB) Tenant(t *testing.T) tenant.Context {
	t.Helper()
	userID, orgID, projID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	d.exec(t, "INSERT INTO "+d.Prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1, $2, '', '', false, $3, $3)",
		userID, userID.String()+"@example.com", now)
	d.exec(t, "INSERT INTO "+d.Prefix+"orgs (id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, 'Org', $3, $4, $4)",
		orgID, orgID.String()[:8], userID, now)
	d.exec(t, "INSERT INTO "+d.Prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, $3, 'Web', $4, $5, $5)",
		projID, orgID, projID.String()[:8], userID, now)
	return tenant.Context{OrgID: orgID, ProjectID: projID, UserID: userID}
}

// Project inserts a second project in tc's org and returns its scope.
func (d DB) Project(t *testing.T, tc tenant.Context) tenant.Context {
	t.Helper()
	projID := uuid.New()
	now := time.Now().UTC()
	d.exec(t, "INSERT INTO "+d.Prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, $3, 'Sibling', $4, $5, $5)",
		projID, tc.OrgID, projID.String()[:8], tc.UserID, now)
	return tenant.Context{OrgID: tc.OrgID, ProjectID: projID, UserID: tc.UserID}
}

// Device inserts a device in tc's project.
func (d DB) Device(t *testing.T, tc tenant.Context) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	d.exec(t, "INSERT INTO "+d.Prefix+"devices (id, public_id, org_id, project_id, name, rules, version, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, '{}'::jsonb, 1, $6, $6)",
		id, mint(t, device.PublicIDPrefix), tc.OrgID, tc.ProjectID, "dev-"+id.String()[24:], now)
	return id
}

// Chat inserts a direct chat for deviceID keyed by jid.
func (d DB) Chat(t *testing.T, tc tenant.Context, deviceID uuid.UUID, jid string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	d.exec(t, "INSERT INTO "+d.Prefix+"chats (id, public_id, org_id, project_id, device_id, jid, kind, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, 'dm', $7, $7)",
		id, mint(t, chat.PublicIDPrefix), tc.OrgID, tc.ProjectID, deviceID, jid, now)
	return id
}

func mint(t *testing.T, prefix string) string {
	t.Helper()
	id, err := publicid.New(prefix)
	require.NoError(t, err)
	return id
}

func (d DB) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	_, err := d.SQL.ExecContext(t.Context(), query, args...)
	require.NoError(t, err)
}
