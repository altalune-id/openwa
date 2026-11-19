package alert_test

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/alert"
	"altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/pgtest"
	"altalune.id/openwa/schema"
)

type pgFixture struct {
	db     *sql.DB
	prefix string
	store  alert.Store
}

func newPgFixture(t *testing.T) *pgFixture {
	t.Helper()
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)
	cfg := config.Defaults()
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true
	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))
	return &pgFixture{db: sqlDB, prefix: cfg.DB.TablePrefix,
		store: alert.NewStore(db.DBConfig{Schema: h.Schema, TablePrefix: cfg.DB.TablePrefix}, tenant.NewPgConn(sqlDB))}
}

func (f *pgFixture) seedOrg(t *testing.T) tenant.Context {
	t.Helper()
	userID, orgID := uuid.New(), uuid.New()
	now := time.Now().UTC()
	_, err := f.db.ExecContext(t.Context(),
		"INSERT INTO "+f.prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES ($1, $2, '', '', false, $3, $3)",
		userID, userID.String()+"@example.com", now)
	require.NoError(t, err)
	_, err = f.db.ExecContext(t.Context(),
		"INSERT INTO "+f.prefix+"orgs (id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, 'Org', $3, $4, $4)",
		orgID, orgID.String()[:8], userID, now)
	require.NoError(t, err)
	return f.seedProject(t, tenant.Context{OrgID: orgID, UserID: userID})
}

func (f *pgFixture) seedProject(t *testing.T, org tenant.Context) tenant.Context {
	t.Helper()
	projID := uuid.New()
	_, err := f.db.ExecContext(t.Context(),
		"INSERT INTO "+f.prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, $3, 'Web', $4, $5, $5)",
		projID, org.OrgID, projID.String()[:8], org.UserID, time.Now().UTC())
	require.NoError(t, err)
	org.ProjectID = projID
	return org
}

func in(t *testing.T, tc tenant.Context) context.Context { return tenant.Into(t.Context(), tc) }

func TestPgSettings_DefaultThenRoundTrip(t *testing.T) {
	f := newPgFixture(t)
	tc := f.seedOrg(t)

	s, err := f.store.Settings(in(t, tc))
	require.NoError(t, err)
	assert.True(t, s.Enabled, "no saved row means enabled")
	assert.Empty(t, s.Recipients)

	require.NoError(t, f.store.SaveSettings(in(t, tc), &alert.Settings{Enabled: false, Recipients: []string{"ops@example.com"}, UpdatedAt: time.Now()}))
	s, err = f.store.Settings(in(t, tc))
	require.NoError(t, err)
	assert.False(t, s.Enabled)
	assert.Equal(t, []string{"ops@example.com"}, s.Recipients)
}

func TestPgSettings_SiblingProjectIsSeparate(t *testing.T) {
	f := newPgFixture(t)
	a := f.seedOrg(t)
	b := f.seedProject(t, a)
	require.NoError(t, f.store.SaveSettings(in(t, a), &alert.Settings{Enabled: false, Recipients: []string{"a@example.com"}, UpdatedAt: time.Now()}))
	require.NoError(t, f.store.AddOptOut(in(t, a), "a@example.com"))

	s, err := f.store.Settings(in(t, b))
	require.NoError(t, err)
	assert.True(t, s.Enabled)
	assert.Empty(t, s.Recipients)
	out, err := f.store.OptOuts(in(t, b))
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestPgSaveSettings_RefusesAProjectOfAnotherOrg(t *testing.T) {
	f := newPgFixture(t)
	victim := f.seedOrg(t)
	require.NoError(t, f.store.SaveSettings(in(t, victim), &alert.Settings{Enabled: true, Recipients: []string{"owner@victim.test"}, UpdatedAt: time.Now()}))
	attacker := f.seedOrg(t)

	hijack := tenant.Context{OrgID: attacker.OrgID, ProjectID: victim.ProjectID}
	err := f.store.SaveSettings(in(t, hijack), &alert.Settings{Enabled: true, Recipients: []string{"spy@attacker.test"}, UpdatedAt: time.Now()})
	require.Error(t, err)
	s, err := f.store.Settings(in(t, victim))
	require.NoError(t, err)
	assert.Equal(t, []string{"owner@victim.test"}, s.Recipients)
}

func TestPgOptOut_IsIdempotent(t *testing.T) {
	f := newPgFixture(t)
	tc := f.seedOrg(t)
	require.NoError(t, f.store.AddOptOut(in(t, tc), "x@example.com"))
	require.NoError(t, f.store.AddOptOut(in(t, tc), "x@example.com"))
	out, err := f.store.OptOuts(in(t, tc))
	require.NoError(t, err)
	assert.Equal(t, []string{"x@example.com"}, out)
}

func TestPgTakeTestSlot_HoldsTheLimitUnderConcurrency(t *testing.T) {
	f := newPgFixture(t)
	tc := f.seedOrg(t)
	var granted atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			ok, err := f.store.TakeTestSlot(in(t, tc), alert.TestsPerHour)
			assert.NoError(t, err)
			if ok {
				granted.Add(1)
			}
		})
	}
	wg.Wait()
	assert.Equal(t, int32(alert.TestsPerHour), granted.Load())

	other := f.seedProject(t, tc)
	ok, err := f.store.TakeTestSlot(in(t, other), alert.TestsPerHour)
	require.NoError(t, err)
	assert.True(t, ok, "the limit is per project")
}

func TestPgSentToday_SumsTheWholeOrg(t *testing.T) {
	f := newPgFixture(t)
	a := f.seedOrg(t)
	b := f.seedProject(t, a)
	stranger := f.seedOrg(t)
	require.NoError(t, f.store.AddSent(in(t, a), 2))
	require.NoError(t, f.store.AddSent(in(t, a), 1))
	require.NoError(t, f.store.AddSent(in(t, b), 4))
	require.NoError(t, f.store.AddSent(in(t, stranger), 9))
	_, err := f.db.ExecContext(t.Context(),
		"INSERT INTO "+f.prefix+"alert_send_counts (project_id, org_id, kind, window_start, sent) VALUES ($1, $2, 'test', date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC', 2)",
		a.ProjectID, a.OrgID)
	require.NoError(t, err)

	n, err := f.store.SentToday(in(t, a))
	require.NoError(t, err)
	assert.Equal(t, 7, n, "emails of every project of the org, not test clicks, not other orgs")
}

func TestPgPruneCounts_DropsOnlyOldWindowsOfTheOrg(t *testing.T) {
	f := newPgFixture(t)
	a := f.seedOrg(t)
	stranger := f.seedOrg(t)
	require.NoError(t, f.store.AddSent(in(t, a), 1))
	require.NoError(t, f.store.AddSent(in(t, stranger), 1))
	_, err := f.db.ExecContext(t.Context(), "UPDATE "+f.prefix+"alert_send_counts SET window_start = window_start - interval '3 days'")
	require.NoError(t, err)
	require.NoError(t, f.store.AddSent(in(t, a), 5))

	require.NoError(t, f.store.PruneCounts(in(t, a), time.Now().Add(-48*time.Hour)))
	var left int
	require.NoError(t, f.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+f.prefix+"alert_send_counts").Scan(&left))
	assert.Equal(t, 2, left, "today's row of the org and the stranger's old row stay")
}
