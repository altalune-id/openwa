package whatsapp_test

import (
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/whatsapp"
)

const (
	testGrace    = 10 * time.Minute
	testCooldown = 6 * time.Hour
)

type outageFixture struct {
	pgFixture
	outages *whatsapp.Outages
}

func newOutageFixture(t *testing.T) outageFixture {
	t.Helper()
	f := newPgFixture(t)
	return outageFixture{pgFixture: f, outages: whatsapp.NewOutages(db.DBConfig{Schema: schemaOf(t, f.sqlDB), TablePrefix: f.prefix}, tenant.NewPgConn(f.sqlDB))}
}

func schemaOf(t *testing.T, sqlDB *sql.DB) string {
	t.Helper()
	var s string
	require.NoError(t, sqlDB.QueryRowContext(t.Context(), "SELECT current_schema()").Scan(&s))
	return s
}

type alertCols struct {
	disconnectedAt, notifiedAt sql.NullTime
	version                    int
}

func (f outageFixture) cols(t *testing.T, dev uuid.UUID) alertCols {
	t.Helper()
	var c alertCols
	require.NoError(t, f.sqlDB.QueryRowContext(t.Context(),
		"SELECT disconnected_at, disconnect_notified_at, version FROM "+f.prefix+"whatsapp_sessions WHERE device_id = $1", dev).
		Scan(&c.disconnectedAt, &c.notifiedAt, &c.version))
	return c
}

func (f outageFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	_, err := f.sqlDB.ExecContext(t.Context(), "UPDATE "+f.prefix+"whatsapp_sessions SET "+q, args...)
	require.NoError(t, err)
}

func (f outageFixture) connected(t *testing.T, tc tenant.Context) (*whatsapp.Session, uuid.UUID) {
	t.Helper()
	ctx := tenant.Into(t.Context(), tc)
	dev := seedDevice(t, f.pgFixture, tc)
	s := whatsapp.NewSession(dev, tc.OrgID, tc.ProjectID, whatsapp.EngineWhatsmeow)
	require.NoError(t, f.store.Save(ctx, s, 0))
	_, err := s.Linking()
	require.NoError(t, err)
	_, err = s.Linked(whatsapp.Identity{JID: "6281322220198:1@s.whatsapp.net", PushName: "Ops"})
	require.NoError(t, err)
	_, err = s.Connected()
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, s, 0))
	got, err := f.store.ByDevice(ctx, dev)
	require.NoError(t, err)
	return got, dev
}

func (f outageFixture) save(t *testing.T, tc tenant.Context, s *whatsapp.Session) *whatsapp.Session {
	t.Helper()
	ctx := tenant.Into(t.Context(), tc)
	require.NoError(t, f.store.Save(ctx, s, s.Version))
	got, err := f.store.ByDevice(ctx, s.DeviceID)
	require.NoError(t, err)
	return got
}

func (f outageFixture) backdate(t *testing.T, dev uuid.UUID, d time.Duration) {
	t.Helper()
	f.exec(t, "disconnected_at = disconnected_at - $2::interval WHERE device_id = $1", dev, d.String())
}

func TestOutages_DisconnectedAtStartsWhenAConnectedNumberDrops(t *testing.T) {
	f := newOutageFixture(t)
	s, dev := f.connected(t, f.tc)
	assert.False(t, f.cols(t, dev).disconnectedAt.Valid, "a live number has no outage")

	_, err := s.Disconnected("stream_error")
	require.NoError(t, err)
	s = f.save(t, f.tc, s)
	first := f.cols(t, dev).disconnectedAt
	require.True(t, first.Valid, "connected to disconnected starts the outage")

	_, err = s.LoggedOut("logged_out_by_phone")
	require.NoError(t, err)
	f.save(t, f.tc, s)
	assert.Equal(t, first.Time, f.cols(t, dev).disconnectedAt.Time, "disconnected to logged out keeps the outage start, so the grace is not reset")
}

func TestOutages_DisconnectedAtStartsOnConnectedToLoggedOutThroughTheUpsert(t *testing.T) {
	f := newOutageFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	s, dev := f.connected(t, f.tc)
	_, err := s.LoggedOut("logged_out_by_phone")
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, s, 0))
	assert.True(t, f.cols(t, dev).disconnectedAt.Valid, "the unconditional upsert path sets it too")
}

func TestOutages_ReconnectThenDropStartsANewOutage(t *testing.T) {
	f := newOutageFixture(t)
	s, dev := f.connected(t, f.tc)
	_, err := s.Disconnected("x")
	require.NoError(t, err)
	s = f.save(t, f.tc, s)
	f.backdate(t, dev, time.Hour)
	old := f.cols(t, dev).disconnectedAt.Time

	_, err = s.Connected()
	require.NoError(t, err)
	s = f.save(t, f.tc, s)
	_, err = s.Disconnected("x")
	require.NoError(t, err)
	f.save(t, f.tc, s)
	assert.True(t, f.cols(t, dev).disconnectedAt.Time.After(old))
}

func TestOutages_GraceAndPairedAndPreDeployRowsAreSkipped(t *testing.T) {
	f := newOutageFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	s, dev := f.connected(t, f.tc)
	_, err := s.Disconnected("x")
	require.NoError(t, err)
	f.save(t, f.tc, s)

	claimed, err := f.outages.Claim(ctx, testGrace, testCooldown)
	require.NoError(t, err)
	assert.Empty(t, claimed, "inside the grace")

	f.exec(t, "reason = 'paired', disconnected_at = now() - interval '1 hour' WHERE device_id = $1", dev)
	claimed, err = f.outages.Claim(ctx, testGrace, testCooldown)
	require.NoError(t, err)
	assert.Empty(t, claimed, "a freshly paired number is not an outage")

	f.exec(t, "reason = 'x', disconnected_at = NULL WHERE device_id = $1", dev)
	claimed, err = f.outages.Claim(ctx, testGrace, testCooldown)
	require.NoError(t, err)
	assert.Empty(t, claimed, "an outage from before the deploy has no start and is never emailed")

	f.exec(t, "disconnected_at = now() - interval '1 hour', last_connected_at = NULL WHERE device_id = $1", dev)
	claimed, err = f.outages.Claim(ctx, testGrace, testCooldown)
	require.NoError(t, err)
	assert.Empty(t, claimed, "a number that never connected is not an outage")
}

func TestOutages_ClaimIsOncePerOutageAndNeverBumpsTheVersion(t *testing.T) {
	f := newOutageFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	s, dev := f.connected(t, f.tc)
	_, err := s.LoggedOut("logged_out_by_phone")
	require.NoError(t, err)
	s = f.save(t, f.tc, s)
	f.backdate(t, dev, time.Hour)
	before := f.cols(t, dev).version

	projects, err := f.outages.ProjectsNeedingAlert(ctx, testGrace, testCooldown)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{f.tc.ProjectID}, projects)

	claimed, err := f.outages.Claim(tenant.WithProject(ctx, f.tc.ProjectID), testGrace, testCooldown)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	assert.Equal(t, dev, claimed[0].DeviceID)
	assert.Equal(t, whatsapp.StateLoggedOut, claimed[0].State)
	assert.NotEmpty(t, claimed[0].PublicID)
	c := f.cols(t, dev)
	assert.True(t, c.notifiedAt.Valid)
	assert.Equal(t, before, c.version, "the claim leaves the aggregate's version alone")

	again, err := f.outages.Claim(ctx, testGrace, testCooldown)
	require.NoError(t, err)
	assert.Empty(t, again, "one email per outage")

	_, err = s.Unlinked()
	require.NoError(t, err)
	f.save(t, f.tc, s)
	assert.Equal(t, c.notifiedAt.Time, f.cols(t, dev).notifiedAt.Time, "Save never writes or clears disconnect_notified_at")
}

func TestOutages_ADropAfterReconnectWaitsForTheCooldown(t *testing.T) {
	f := newOutageFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	s, dev := f.connected(t, f.tc)
	_, err := s.Disconnected("x")
	require.NoError(t, err)
	s = f.save(t, f.tc, s)
	f.backdate(t, dev, time.Hour)
	claimed, err := f.outages.Claim(ctx, testGrace, testCooldown)
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	_, err = s.Connected()
	require.NoError(t, err)
	s = f.save(t, f.tc, s)
	_, err = s.Disconnected("x")
	require.NoError(t, err)
	f.save(t, f.tc, s)
	f.backdate(t, dev, time.Hour)

	claimed, err = f.outages.Claim(ctx, testGrace, testCooldown)
	require.NoError(t, err)
	assert.Empty(t, claimed, "a new outage inside the cooldown is not emailed")

	f.exec(t, "disconnect_notified_at = disconnect_notified_at - interval '7 hours', last_connected_at = last_connected_at - interval '1 minute' WHERE device_id = $1", dev)
	claimed, err = f.outages.Claim(ctx, testGrace, testCooldown)
	require.NoError(t, err)
	assert.Len(t, claimed, 1, "after the cooldown, a new outage gets its own email")

	f.exec(t, "disconnect_notified_at = now() - interval '7 hours', last_connected_at = now() - interval '8 hours' WHERE device_id = $1", dev)
	claimed, err = f.outages.Claim(ctx, testGrace, testCooldown)
	require.NoError(t, err)
	assert.Empty(t, claimed, "without a reconnect since the last email, the same outage is never emailed again")
}

func TestOutages_TwoConcurrentClaimsGetOneEach(t *testing.T) {
	f := newOutageFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	s, dev := f.connected(t, f.tc)
	_, err := s.Disconnected("x")
	require.NoError(t, err)
	f.save(t, f.tc, s)
	f.backdate(t, dev, time.Hour)

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		total int
	)
	for range 4 {
		wg.Go(func() {
			got, err := f.outages.Claim(ctx, testGrace, testCooldown)
			assert.NoError(t, err)
			mu.Lock()
			total += len(got)
			mu.Unlock()
		})
	}
	wg.Wait()
	assert.Equal(t, 1, total, "concurrent runs re-check the row under its lock, so one email goes out")
}

func TestOutages_StayInsideTheirProject(t *testing.T) {
	f := newOutageFixture(t)
	a := f.tc
	ctxA := tenant.Into(t.Context(), a)
	b := tenant.Context{OrgID: a.OrgID, UserID: a.UserID, ProjectID: uuid.New()}
	_, err := f.sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+f.prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, $3, 'B', $4, now(), now())",
		b.ProjectID, b.OrgID, b.ProjectID.String()[:8], b.UserID)
	require.NoError(t, err)
	ctxB := tenant.Into(t.Context(), b)

	s, dev := f.connected(t, b)
	_, err = s.Disconnected("x")
	require.NoError(t, err)
	f.save(t, b, s)
	f.backdate(t, dev, time.Hour)

	att, err := f.outages.NeedingAttention(ctxA, testGrace)
	require.NoError(t, err)
	assert.Empty(t, att, "a sibling project's number never shows in this project's banner")
	claimed, err := f.outages.Claim(ctxA, testGrace, testCooldown)
	require.NoError(t, err)
	assert.Empty(t, claimed)

	att, err = f.outages.NeedingAttention(ctxB, testGrace)
	require.NoError(t, err)
	require.Len(t, att, 1)
	claimed, err = f.outages.Claim(ctxB, testGrace, testCooldown)
	require.NoError(t, err)
	assert.Len(t, claimed, 1)

	other := seedTenant(t, f.sqlDB, f.prefix)
	projects, err := f.outages.ProjectsNeedingAlert(tenant.Into(t.Context(), other), testGrace, testCooldown)
	require.NoError(t, err)
	assert.Empty(t, projects, "another org never sees this org's outages")
}

func TestOutages_NeedingAttentionWaitsForTheGrace(t *testing.T) {
	f := newOutageFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	s, dev := f.connected(t, f.tc)
	_, err := s.Disconnected("x")
	require.NoError(t, err)
	f.save(t, f.tc, s)

	att, err := f.outages.NeedingAttention(ctx, testGrace)
	require.NoError(t, err)
	assert.Empty(t, att)
	f.backdate(t, dev, time.Hour)
	att, err = f.outages.NeedingAttention(ctx, testGrace)
	require.NoError(t, err)
	assert.Len(t, att, 1)
}

func TestOutages_AnUpStateClearsTheOutageStart(t *testing.T) {
	f := newOutageFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	s, dev := f.connected(t, f.tc)
	_, err := s.Disconnected("x")
	require.NoError(t, err)
	s = f.save(t, f.tc, s)
	f.backdate(t, dev, 3*time.Hour)
	stale := f.cols(t, dev).disconnectedAt.Time

	_, err = s.Unlinked()
	require.NoError(t, err)
	s = f.save(t, f.tc, s)
	assert.False(t, f.cols(t, dev).disconnectedAt.Valid, "unlinking ends the outage")

	_, err = s.Linking()
	require.NoError(t, err)
	s = f.save(t, f.tc, s)
	_, err = s.Linked(whatsapp.Identity{JID: "6281322220198:1@s.whatsapp.net"})
	require.NoError(t, err)
	s = f.save(t, f.tc, s)
	assert.False(t, f.cols(t, dev).disconnectedAt.Valid, "a fresh pairing is not an outage")

	_, err = s.Connected()
	require.NoError(t, err)
	s = f.save(t, f.tc, s)
	_, err = s.Disconnected("x")
	require.NoError(t, err)
	f.save(t, f.tc, s)
	c := f.cols(t, dev)
	require.True(t, c.disconnectedAt.Valid)
	assert.True(t, c.disconnectedAt.Time.After(stale.Add(2*time.Hour)), "the new outage starts now, not at the stale start")
	claimed, err := f.outages.Claim(ctx, testGrace, testCooldown)
	require.NoError(t, err)
	assert.Empty(t, claimed, "inside the grace of the new outage, so no email from the old start")
}
