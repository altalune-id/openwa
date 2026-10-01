package whatsapp_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/pgtest"
	"altalune.id/openwa/internal/whatsapp"
	"altalune.id/openwa/schema"
)

type pgFixture struct {
	store  whatsapp.Store
	leases whatsapp.LeaseStore
	sqlDB  *sql.DB
	prefix string
	tc     tenant.Context
}

func newPgFixture(t *testing.T) pgFixture {
	t.Helper()
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)
	cfg := config.Defaults()
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true
	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))

	pfx := cfg.DB.TablePrefix
	tc := seedTenant(t, sqlDB, pfx)
	dbCfg := db.DBConfig{DSN: h.DSN, Schema: h.Schema, TablePrefix: pfx}
	pool := db.Pool{W: sqlDB, R: sqlDB}
	pc := tenant.NewPgConn(sqlDB)
	return pgFixture{
		store:  whatsapp.NewStore(dbCfg, pool, pc),
		leases: whatsapp.NewLeaseStore(dbCfg, pool),
		sqlDB:  sqlDB,
		prefix: pfx,
		tc:     tc,
	}
}

func seedTenant(t *testing.T, sqlDB *sql.DB, prefix string) tenant.Context {
	t.Helper()
	userID, orgID, projID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	_, err := sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1, $2, '', '', false, $3, $3)",
		userID, userID.String()+"@example.com", now)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"orgs (id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, 'Org', $3, $4, $4)",
		orgID, orgID.String()[:8], userID, now)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, $3, 'Web', $4, $5, $5)",
		projID, orgID, projID.String()[:8], userID, now)
	require.NoError(t, err)
	return tenant.Context{OrgID: orgID, ProjectID: projID, UserID: userID}
}

func seedDevice(t *testing.T, f pgFixture, tc tenant.Context) uuid.UUID {
	t.Helper()
	id := uuid.New()
	now := time.Now().UTC()
	_, err := f.sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+f.prefix+"devices (id, public_id, org_id, project_id, name, rules, version, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, '{}'::jsonb, 1, $6, $6)",
		id, "dev_"+id.String()[:12], tc.OrgID, tc.ProjectID, id.String()[:8], now)
	require.NoError(t, err)
	return id
}

func TestPostgres_Session_SaveRoundTrip(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	devID := seedDevice(t, f, f.tc)
	s := whatsapp.NewSession(devID, f.tc.OrgID, f.tc.ProjectID, whatsapp.EngineWhatsmeow)
	require.NoError(t, f.store.Save(ctx, s, 0))

	_, err := s.Linked(whatsapp.Identity{JID: "628111:1@s.whatsapp.net", LID: "1@lid", PushName: "Ops", Platform: "android"})
	require.NoError(t, err)
	_, err = s.Connected()
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, s, 1))

	got, err := f.store.ByDevice(ctx, devID)
	require.NoError(t, err)
	require.Equal(t, whatsapp.StateConnected, got.State)
	require.Equal(t, "628111", got.Phone)
	require.Equal(t, "1@lid", got.LID)
	require.Equal(t, 2, got.Version)
	require.NotNil(t, got.LastConnectedAt)
	require.NotNil(t, got.LastSeenAt)

	require.True(t, whatsapp.IsStaleVersionError(f.store.Save(ctx, s, 1)))
}

func TestPostgres_Session_ByDevicesAndDelete(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	a, b := seedDevice(t, f, f.tc), seedDevice(t, f, f.tc)
	require.NoError(t, f.store.Save(ctx, whatsapp.NewSession(a, f.tc.OrgID, f.tc.ProjectID, whatsapp.EngineWhatsmeow), 0))
	require.NoError(t, f.store.Save(ctx, whatsapp.NewSession(b, f.tc.OrgID, f.tc.ProjectID, whatsapp.EngineWhatsmeow), 0))

	rows, err := f.store.ByDevices(ctx, []uuid.UUID{a, b, uuid.New()})
	require.NoError(t, err)
	require.Len(t, rows, 2)

	empty, err := f.store.ByDevices(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, empty)

	require.NoError(t, f.store.Delete(ctx, a))
	_, err = f.store.ByDevice(ctx, a)
	require.True(t, whatsapp.IsSessionNotFoundError(err))
	require.True(t, whatsapp.IsSessionNotFoundError(f.store.Delete(ctx, a)))
}
