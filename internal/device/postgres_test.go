package device_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/pgtest"
	"altalune.id/openwa/schema"
)

type pgFixture struct {
	store  device.Store
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
	return pgFixture{store: device.NewStore(dbCfg, pool, pc), sqlDB: sqlDB, prefix: pfx, tc: tc}
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

func seedOtherTenant(t *testing.T, sqlDB *sql.DB, prefix string) tenant.Context {
	t.Helper()
	return seedTenant(t, sqlDB, prefix)
}

func TestPostgres_Device_SaveRoundTripsRules(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	d, err := device.New(f.tc.OrgID, f.tc.ProjectID, testPublicID(t), "Sales")
	require.NoError(t, err)
	require.NoError(t, d.SetRules(device.Rules{GroupMode: device.GroupOpen, AllowedSenders: []string{"628111111111"}, AllowedGroups: []string{"1@g.us"}, TriggerPrefix: "!", IgnoreFromMe: false}))
	require.NoError(t, f.store.Save(ctx, d, 0))

	got, err := f.store.ByID(ctx, d.ID)
	require.NoError(t, err)
	require.Equal(t, d.Name, got.Name)
	require.Equal(t, d.PublicID, got.PublicID)
	require.Equal(t, d.Rules, got.Rules)
	require.Equal(t, 1, got.Version)

	byPub, err := f.store.ByPublicID(ctx, d.PublicID)
	require.NoError(t, err)
	require.Equal(t, d.ID, byPub.ID)
	_, err = f.store.ByPublicID(ctx, testPublicID(t))
	require.True(t, device.IsNotFoundError(err))
}

func TestPostgres_Device_BatchIDReadsAreProjectScoped(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	a, err := device.New(f.tc.OrgID, f.tc.ProjectID, testPublicID(t), "A")
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, a, 0))
	other := seedOtherTenant(t, f.sqlDB, f.prefix)
	b, err := device.New(other.OrgID, other.ProjectID, testPublicID(t), "B")
	require.NoError(t, err)
	require.NoError(t, f.store.Save(tenant.Into(t.Context(), other), b, 0))
	siblingProject := uuid.New()
	_, err = f.sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+f.prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, $3, 'Sibling', $4, now(), now())",
		siblingProject, f.tc.OrgID, siblingProject.String()[:8], f.tc.UserID)
	require.NoError(t, err)
	c, err := device.New(f.tc.OrgID, siblingProject, testPublicID(t), "C")
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, c, 0))

	pubs, err := f.store.PublicIDsByIDs(ctx, []uuid.UUID{a.ID, b.ID, c.ID, uuid.New()})
	require.NoError(t, err)
	require.Equal(t, map[uuid.UUID]string{a.ID: a.PublicID}, pubs, "another org's, another project's and unknown ids are absent")
	ids, err := f.store.IDsByPublicIDs(ctx, []string{a.PublicID, b.PublicID, c.PublicID})
	require.NoError(t, err)
	require.Equal(t, map[string]uuid.UUID{a.PublicID: a.ID}, ids)
	empty, err := f.store.PublicIDsByIDs(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, empty)
}

func TestPostgres_Device_BatchReadsAndListAreOrgScopedWithTheSameProjectID(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	mine, err := device.New(f.tc.OrgID, f.tc.ProjectID, testPublicID(t), "Mine")
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, mine, 0))

	foreign := seedOtherTenant(t, f.sqlDB, f.prefix)
	theirs, err := device.New(foreign.OrgID, foreign.ProjectID, testPublicID(t), "Theirs")
	require.NoError(t, err)
	require.NoError(t, f.store.Save(tenant.Into(t.Context(), foreign), theirs, 0))

	sameProjectOtherOrg := tenant.Into(t.Context(), tenant.Context{OrgID: foreign.OrgID, ProjectID: f.tc.ProjectID, UserID: f.tc.UserID})
	pubs, err := f.store.PublicIDsByIDs(sameProjectOtherOrg, []uuid.UUID{mine.ID, theirs.ID})
	require.NoError(t, err)
	require.Empty(t, pubs, "the project id matches mine but the org does not")
	ids, err := f.store.IDsByPublicIDs(sameProjectOtherOrg, []string{mine.PublicID, theirs.PublicID})
	require.NoError(t, err)
	require.Empty(t, ids)
	list, err := f.store.List(sameProjectOtherOrg, device.ListOpts{})
	require.NoError(t, err)
	require.Empty(t, list)
}

func TestPostgres_Device_PublicIDIsUnique(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	a, err := device.New(f.tc.OrgID, f.tc.ProjectID, testPublicID(t), "Sales")
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, a, 0))
	b, err := device.New(f.tc.OrgID, f.tc.ProjectID, a.PublicID, "Support")
	require.NoError(t, err)
	err = f.store.Save(ctx, b, 0)
	require.True(t, device.IsPublicIDTakenError(err), "a public id collision is its own error, never NameTaken: %v", err)
	require.False(t, device.IsNameTakenError(err))

	other := seedOtherTenant(t, f.sqlDB, f.prefix)
	c, err := device.New(other.OrgID, other.ProjectID, a.PublicID, "Elsewhere")
	require.NoError(t, err)
	require.True(t, device.IsPublicIDTakenError(f.store.Save(tenant.Into(t.Context(), other), c, 0)),
		"public ids are unique across the whole table, not per tenant")
}

func TestPostgres_Device_NameIsUniquePerProjectCaseInsensitive(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	a, err := device.New(f.tc.OrgID, f.tc.ProjectID, testPublicID(t), "Sales")
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, a, 0))
	b, err := device.New(f.tc.OrgID, f.tc.ProjectID, testPublicID(t), "sales")
	require.NoError(t, err)
	err = f.store.Save(ctx, b, 0)
	require.True(t, device.IsNameTakenError(err), "got %v", err)
}

func TestPostgres_Device_SaveBumpsVersionAndGuardsIfVersion(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	d, err := device.New(f.tc.OrgID, f.tc.ProjectID, testPublicID(t), "One")
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, d, 0))
	require.NoError(t, d.Rename("Two"))
	require.NoError(t, f.store.Save(ctx, d, 1))
	got, err := f.store.ByID(ctx, d.ID)
	require.NoError(t, err)
	require.Equal(t, 2, got.Version)
	require.Equal(t, "Two", got.Name)

	err = f.store.Save(ctx, d, 1)
	require.True(t, device.IsStaleVersionError(err), "got %v", err)
	var vm *device.StaleVersionError
	require.ErrorAs(t, err, &vm)
	require.Equal(t, 1, vm.Want)
	require.Equal(t, 2, vm.Got)
}

func TestPostgres_Device_ListIsProjectScopedNewestFirst(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	first, err := device.New(f.tc.OrgID, f.tc.ProjectID, testPublicID(t), "A")
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, first, 0))
	second, err := device.New(f.tc.OrgID, f.tc.ProjectID, testPublicID(t), "B")
	require.NoError(t, err)
	second.CreatedAt = first.CreatedAt.Add(time.Second)
	require.NoError(t, f.store.Save(ctx, second, 0))

	other := tenant.Context{OrgID: f.tc.OrgID, ProjectID: uuid.New(), UserID: f.tc.UserID}
	got, err := f.store.List(tenant.Into(t.Context(), other), device.ListOpts{})
	require.NoError(t, err)
	require.Empty(t, got)

	got, err = f.store.List(ctx, device.ListOpts{})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "B", got[0].Name)
	require.Equal(t, "A", got[1].Name)
}

func TestPostgres_Device_DeleteAndNotFound(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	d, err := device.New(f.tc.OrgID, f.tc.ProjectID, testPublicID(t), "Gone")
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, d, 0))
	require.NoError(t, f.store.Delete(ctx, d.ID))
	_, err = f.store.ByID(ctx, d.ID)
	require.True(t, device.IsNotFoundError(err))
	require.True(t, device.IsNotFoundError(f.store.Delete(ctx, d.ID)))
	require.True(t, device.IsNotFoundError(f.store.Save(ctx, d, 1)), "a conditional write never resurrects a deleted row")
}
