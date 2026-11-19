package alert

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/i18n"
	"altalune.id/openwa/internal/platform/sealer"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/mailer"
)

type fakeStore struct {
	settings  map[uuid.UUID]Settings
	optOuts   map[uuid.UUID][]string
	testSlots map[uuid.UUID]int
	sentToday int
	sentErr   error
	pruned    int
}

func newFakeStore() *fakeStore {
	return &fakeStore{settings: map[uuid.UUID]Settings{}, optOuts: map[uuid.UUID][]string{}, testSlots: map[uuid.UUID]int{}}
}

func (f *fakeStore) Settings(ctx context.Context) (Settings, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return Settings{}, err
	}
	if s, ok := f.settings[tc.ProjectID]; ok {
		return s, nil
	}
	return Settings{OrgID: tc.OrgID, ProjectID: tc.ProjectID, Enabled: true, Recipients: []string{}}, nil
}

func (f *fakeStore) SaveSettings(_ context.Context, s *Settings) error {
	f.settings[s.ProjectID] = *s
	return nil
}

func (f *fakeStore) OptOuts(ctx context.Context) ([]string, error) {
	tc, _ := tenant.From(ctx)
	return f.optOuts[tc.ProjectID], nil
}

func (f *fakeStore) AddOptOut(ctx context.Context, email string) error {
	tc, _ := tenant.From(ctx)
	f.optOuts[tc.ProjectID] = append(f.optOuts[tc.ProjectID], email)
	return nil
}

func (f *fakeStore) TakeTestSlot(ctx context.Context, limit int) (bool, error) {
	tc, _ := tenant.From(ctx)
	if f.testSlots[tc.ProjectID] >= limit {
		return false, nil
	}
	f.testSlots[tc.ProjectID]++
	return true, nil
}

func (f *fakeStore) AddSent(_ context.Context, n int) error { f.sentToday += n; return nil }

func (f *fakeStore) SentToday(context.Context) (int, error) { return f.sentToday, f.sentErr }

func (f *fakeStore) PruneCounts(context.Context, time.Time) error { f.pruned++; return nil }

type fakeSessions struct {
	projects []uuid.UUID
	outages  map[uuid.UUID][]Outage
	claims   []uuid.UUID
}

func (f *fakeSessions) ProjectsNeedingAlert(context.Context, time.Duration, time.Duration) ([]uuid.UUID, error) {
	return f.projects, nil
}

func (f *fakeSessions) ClaimOutages(ctx context.Context, _, _ time.Duration) ([]Outage, error) {
	tc, _ := tenant.From(ctx)
	f.claims = append(f.claims, tc.ProjectID)
	out := f.outages[tc.ProjectID]
	delete(f.outages, tc.ProjectID)
	return out, nil
}

type fakeMembers []Member

func (f fakeMembers) Members(context.Context, uuid.UUID) ([]Member, error) { return f, nil }

type fakeProjects struct{}

func (fakeProjects) Describe(_ context.Context, id uuid.UUID) (Project, error) {
	return Project{Name: "Shop " + id.String()[:4], Slug: "shop", OrgSlug: "acme"}, nil
}

type fakeQueue struct {
	mu   sync.Mutex
	sent []mailer.Message
	err  error
}

func (f *fakeQueue) Enqueue(_ context.Context, m mailer.Message) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return uuid.Nil, f.err
	}
	f.sent = append(f.sent, m)
	return uuid.New(), nil
}

func (f *fakeQueue) to() []string {
	out := make([]string, len(f.sent))
	for i, m := range f.sent {
		out[i] = m.To
	}
	return out
}

type harness struct {
	svc      *Service
	store    *fakeStore
	sessions *fakeSessions
	queue    *fakeQueue
	sealer   sealer.Sealer
	ctx      context.Context
	org      uuid.UUID
	project  uuid.UUID
}

func testOptions() Options {
	return Options{MailEnabled: true, Enabled: true, Grace: 10 * time.Minute, Cooldown: 6 * time.Hour, DailyOrgCap: 50, LinkBase: "https://wa.example.com", Brand: "OpenWA"}
}

func newHarness(t *testing.T, opts Options, members ...Member) *harness {
	t.Helper()
	key, err := sealer.GenerateKey()
	require.NoError(t, err)
	sl, err := sealer.New(key)
	require.NoError(t, err)
	h := &harness{store: newFakeStore(), sessions: &fakeSessions{outages: map[uuid.UUID][]Outage{}}, queue: &fakeQueue{}, sealer: sl,
		org: uuid.New(), project: uuid.New()}
	if members == nil {
		members = []Member{
			{Email: "owner@example.com", Locale: "id-ID", Manager: true},
			{Email: "member@example.com", Locale: "en-US"},
		}
	}
	uow := func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }
	h.svc = NewService(Deps{
		Store: h.store, Sessions: h.sessions, Members: fakeMembers(members), Projects: fakeProjects{}, Queue: h.queue,
		UnitOfWork: uow, Sealer: sl, Translations: i18n.NewEmbeddedBundle(i18n.EnUS),
		Log: slog.New(slog.DiscardHandler), Unexpected: apperror.NewReporter(slog.New(slog.DiscardHandler), false).Unexpected,
	}, opts)
	h.ctx = tenant.Into(t.Context(), tenant.Context{OrgID: h.org})
	return h
}

func (h *harness) projectCtx() context.Context { return tenant.WithProject(h.ctx, h.project) }

func outage(name, phone string) Outage {
	return Outage{DeviceID: "dev_" + name, Name: name, Phone: phone, DisconnectedAt: time.Date(2026, 11, 18, 14, 5, 0, 0, time.UTC)}
}

func TestRunDisconnect_DoesNothingWhenMailIsOff(t *testing.T) {
	for name, opts := range map[string]Options{
		"no delivering driver": {MailEnabled: false, Enabled: true, Grace: time.Minute, Cooldown: time.Hour, DailyOrgCap: 50},
		"server kill switch":   {MailEnabled: true, Enabled: false, Grace: time.Minute, Cooldown: time.Hour, DailyOrgCap: 50},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, opts)
			h.sessions.projects = []uuid.UUID{h.project}
			h.sessions.outages[h.project] = []Outage{outage("Sales", "6281322220198")}
			require.NoError(t, h.svc.RunDisconnect(h.ctx))
			assert.Empty(t, h.sessions.claims, "nothing is claimed, so the outage is emailed once mail is on")
			assert.Empty(t, h.queue.sent)
		})
	}
}

func TestRunDisconnect_EmailsOwnersAndAdminsByDefault(t *testing.T) {
	h := newHarness(t, testOptions())
	h.sessions.projects = []uuid.UUID{h.project}
	h.sessions.outages[h.project] = []Outage{outage("Sales", "6281322220198")}

	require.NoError(t, h.svc.RunDisconnect(h.ctx))
	assert.Equal(t, []string{"owner@example.com"}, h.queue.to())
	assert.Equal(t, 1, h.store.sentToday)
	m := h.queue.sent[0]
	assert.Contains(t, m.Headers["List-Unsubscribe"], "https://wa.example.com/alerts/unsubscribe?t=")
	assert.Equal(t, "List-Unsubscribe=One-Click", m.Headers["List-Unsubscribe-Post"])
}

func TestRunDisconnect_SkipsADisabledProjectWithoutClaiming(t *testing.T) {
	h := newHarness(t, testOptions())
	h.store.settings[h.project] = Settings{OrgID: h.org, ProjectID: h.project, Enabled: false}
	h.sessions.projects = []uuid.UUID{h.project}
	h.sessions.outages[h.project] = []Outage{outage("Sales", "6281322220198")}

	require.NoError(t, h.svc.RunDisconnect(h.ctx))
	assert.Empty(t, h.sessions.claims)
	assert.Empty(t, h.queue.sent)
}

func TestRunDisconnect_OneDigestPerRecipient(t *testing.T) {
	h := newHarness(t, testOptions())
	h.store.settings[h.project] = Settings{OrgID: h.org, ProjectID: h.project, Enabled: true, Recipients: []string{"ops@example.com", "owner@example.com"}}
	h.sessions.projects = []uuid.UUID{h.project}
	h.sessions.outages[h.project] = []Outage{outage("Sales", "6281322220198"), outage("Support", "6281399990001")}

	require.NoError(t, h.svc.RunDisconnect(h.ctx))
	require.Equal(t, []string{"ops@example.com", "owner@example.com"}, h.queue.to())
	for _, m := range h.queue.sent {
		assert.Contains(t, m.TextBody, "Sales")
		assert.Contains(t, m.TextBody, "Support")
		assert.Contains(t, m.HTMLBody, "Support")
	}
	assert.Equal(t, 2, h.store.sentToday)
}

func TestRunDisconnect_LeavesOptedOutAddressesOut(t *testing.T) {
	h := newHarness(t, testOptions())
	h.store.settings[h.project] = Settings{OrgID: h.org, ProjectID: h.project, Enabled: true, Recipients: []string{"ops@example.com", "gone@example.com"}}
	h.store.optOuts[h.project] = []string{"gone@example.com"}
	h.sessions.projects = []uuid.UUID{h.project}
	h.sessions.outages[h.project] = []Outage{outage("Sales", "6281322220198")}

	require.NoError(t, h.svc.RunDisconnect(h.ctx))
	assert.Equal(t, []string{"ops@example.com"}, h.queue.to())
}

func TestRunDisconnect_ClaimsEvenWhenEveryAddressOptedOut(t *testing.T) {
	h := newHarness(t, testOptions())
	h.store.optOuts[h.project] = []string{"owner@example.com"}
	h.sessions.projects = []uuid.UUID{h.project}
	h.sessions.outages[h.project] = []Outage{outage("Sales", "6281322220198")}

	require.NoError(t, h.svc.RunDisconnect(h.ctx))
	assert.Equal(t, []uuid.UUID{h.project}, h.sessions.claims, "the outage is marked notified, or it rescans every minute")
	assert.Empty(t, h.queue.sent)
}

func TestRunDisconnect_DailyCapSkipsWithoutClaiming(t *testing.T) {
	opts := testOptions()
	opts.DailyOrgCap = 2
	h := newHarness(t, opts)
	h.store.settings[h.project] = Settings{OrgID: h.org, ProjectID: h.project, Enabled: true, Recipients: []string{"a@example.com", "b@example.com"}}
	h.store.sentToday = 1
	h.sessions.projects = []uuid.UUID{h.project}
	h.sessions.outages[h.project] = []Outage{outage("Sales", "6281322220198")}

	require.NoError(t, h.svc.RunDisconnect(h.ctx))
	assert.Empty(t, h.sessions.claims, "over the cap the outage stays unclaimed for tomorrow")
	assert.Empty(t, h.queue.sent)
}

func TestRunDisconnect_EnqueueFailureFailsTheProject(t *testing.T) {
	h := newHarness(t, testOptions())
	h.queue.err = errors.New("db down")
	h.sessions.projects = []uuid.UUID{h.project}
	h.sessions.outages[h.project] = []Outage{outage("Sales", "6281322220198")}
	assert.Error(t, h.svc.RunDisconnect(h.ctx))
}

func TestRunDisconnect_WritesInTheRecipientsLocale(t *testing.T) {
	h := newHarness(t, testOptions())
	h.store.settings[h.project] = Settings{OrgID: h.org, ProjectID: h.project, Enabled: true, Recipients: []string{"owner@example.com", "ops@example.com"}}
	h.sessions.projects = []uuid.UUID{h.project}
	h.sessions.outages[h.project] = []Outage{outage("Sales", "6281322220198")}

	require.NoError(t, h.svc.RunDisconnect(h.ctx))
	require.Len(t, h.queue.sent, 2)
	assert.Contains(t, h.queue.sent[0].HTMLBody, `lang="id-ID"`, "a member's address uses the member's locale")
	assert.Contains(t, h.queue.sent[1].HTMLBody, `lang="en-US"`, "a typed address uses the default locale")
}

func TestRunDisconnect_NeverPutsMessageContentOrRawPhoneInTheEmail(t *testing.T) {
	h := newHarness(t, testOptions())
	h.sessions.projects = []uuid.UUID{h.project}
	h.sessions.outages[h.project] = []Outage{outage("Sales", "6281322220198")}

	require.NoError(t, h.svc.RunDisconnect(h.ctx))
	m := h.queue.sent[0]
	for _, body := range []string{m.Subject, m.TextBody, m.HTMLBody} {
		assert.NotContains(t, body, "6281322220198")
	}
	assert.Contains(t, m.TextBody, "+62 813 •••• 0198")
	assert.Contains(t, m.TextBody, "https://wa.example.com/orgs/acme/projects/shop/devices/dev_Sales")
}

func TestRunDisconnect_PrunesOldCounts(t *testing.T) {
	h := newHarness(t, testOptions())
	require.NoError(t, h.svc.RunDisconnect(h.ctx))
	assert.Equal(t, 1, h.store.pruned)
}

func TestSaveSettings_ParsesAndStores(t *testing.T) {
	h := newHarness(t, testOptions())
	s, err := h.svc.SaveSettings(h.projectCtx(), false, "Ops@Example.com, ops@example.com")
	require.NoError(t, err)
	assert.False(t, s.Enabled)
	assert.Equal(t, []string{"ops@example.com"}, h.store.settings[h.project].Recipients)

	_, err = h.svc.SaveSettings(h.projectCtx(), true, "nope")
	assert.True(t, IsInvalidRecipientsError(err))
	assert.False(t, h.store.settings[h.project].Enabled, "a refused save stores nothing")
}

func TestSendTest(t *testing.T) {
	h := newHarness(t, testOptions())
	for range TestsPerHour {
		to, err := h.svc.SendTest(h.projectCtx())
		require.NoError(t, err)
		assert.Equal(t, []string{"owner@example.com"}, to)
	}
	_, err := h.svc.SendTest(h.projectCtx())
	assert.True(t, IsTestLimitError(err))
	assert.Len(t, h.queue.sent, TestsPerHour)
	assert.Equal(t, TestsPerHour, h.store.sentToday, "test emails count toward the daily cap")
	assert.Contains(t, h.queue.sent[0].Headers["List-Unsubscribe"], "/alerts/unsubscribe?t=")
}

func TestSendTest_Refusals(t *testing.T) {
	off := testOptions()
	off.MailEnabled = false
	h := newHarness(t, off)
	_, err := h.svc.SendTest(h.projectCtx())
	assert.True(t, IsMailUnavailableError(err))

	killed := testOptions()
	killed.Enabled = false
	h = newHarness(t, killed)
	_, err = h.svc.SendTest(h.projectCtx())
	assert.True(t, IsDisabledError(err))

	h = newHarness(t, testOptions())
	h.store.optOuts[h.project] = []string{"owner@example.com"}
	_, err = h.svc.SendTest(h.projectCtx())
	assert.True(t, IsNoRecipientsError(err))

	capped := testOptions()
	capped.DailyOrgCap = 1
	h = newHarness(t, capped)
	h.store.sentToday = 1
	_, err = h.svc.SendTest(h.projectCtx())
	assert.True(t, IsTestLimitError(err))
	assert.Zero(t, h.store.testSlots[h.project], "a capped test does not use an hourly slot")
}

func TestUnsubscribe_RecordsTheOptOutUnderTheTokensProject(t *testing.T) {
	h := newHarness(t, testOptions())
	h.sessions.projects = []uuid.UUID{h.project}
	h.sessions.outages[h.project] = []Outage{outage("Sales", "6281322220198")}
	require.NoError(t, h.svc.RunDisconnect(h.ctx))
	link := h.queue.sent[0].Headers["List-Unsubscribe"]
	token := link[strings.Index(link, "t=")+2 : len(link)-1]

	target, err := h.svc.UnsubscribeTarget(t.Context(), token)
	require.NoError(t, err)
	assert.Equal(t, "owner@example.com", target.Email)
	assert.Empty(t, h.store.optOuts[h.project], "the confirm page records nothing")

	target, err = h.svc.Unsubscribe(t.Context(), token)
	require.NoError(t, err)
	assert.Equal(t, "owner@example.com", target.Email)
	assert.Equal(t, []string{"owner@example.com"}, h.store.optOuts[h.project])

	_, err = h.svc.Unsubscribe(t.Context(), "bogus")
	assert.True(t, IsInvalidUnsubscribeTokenError(err))
}

func TestRunDisconnect_NamesALoggedOutNumberWithoutAPhone(t *testing.T) {
	h := newHarness(t, testOptions())
	h.sessions.projects = []uuid.UUID{h.project}
	h.sessions.outages[h.project] = []Outage{outage("Sales", "")}

	require.NoError(t, h.svc.RunDisconnect(h.ctx))
	m := h.queue.sent[0]
	assert.Equal(t, "\u2068Sales\u2069 terputus", m.Subject, "id-ID owner, name-only subject")
	assert.NotContains(t, m.TextBody, "()")
	assert.NotContains(t, m.TextBody, "alertmail.", "every key resolves")
}

func TestDuration(t *testing.T) {
	tr := i18n.NewEmbeddedBundle(i18n.EnUS).For(i18n.EnUS)
	assert.Equal(t, "1 min", duration(tr, 10*time.Second))
	assert.Equal(t, "25 min", duration(tr, 25*time.Minute))
	assert.Equal(t, "3 h", duration(tr, 3*time.Hour+10*time.Minute))
	assert.Equal(t, "2 d", duration(tr, 50*time.Hour))
	ar := i18n.NewEmbeddedBundle(i18n.EnUS).For("ar-SA")
	assert.Equal(t, "يومين", duration(ar, 50*time.Hour), "days use the plural form for their count")
	assert.Equal(t, "4 أيام", duration(ar, 4*24*time.Hour))
}

func TestRunDisconnect_IsolatesPhoneAndTimeAndLeadsWithTheSubject(t *testing.T) {
	h := newHarness(t, testOptions(), Member{Email: "owner@example.com", Locale: "ar-SA", Manager: true})
	h.sessions.projects = []uuid.UUID{h.project}
	h.sessions.outages[h.project] = []Outage{outage("Sa\u202eles", "6281322220198")}

	require.NoError(t, h.svc.RunDisconnect(h.ctx))
	m := h.queue.sent[0]
	assert.Contains(t, m.TextBody, "\u2066+62 813 •••• 0198\u2069", "the text part keeps the isolate characters")
	assert.Contains(t, m.TextBody, "\u20662026-11-18 14:05\u2069")
	assert.Contains(t, m.TextBody, "\u2068Sales (\u2066+62 813 •••• 0198\u2069)\u2069", "name and phone are one isolated unit; the name's own override is stripped")
	ltrSpan := `<span dir="ltr" style="unicode-bidi:isolate;white-space:nowrap">`
	assert.Contains(t, m.HTMLBody, ltrSpan+"+62 813 •••• 0198</span>", "HTML carries isolating spans that email clients honor")
	assert.Contains(t, m.HTMLBody, ltrSpan+"2026-11-18 14:05</span>")
	assert.Contains(t, m.HTMLBody, `<span dir="auto" style="unicode-bidi:isolate">Sales (`+ltrSpan+"+62 813 •••• 0198</span>)</span>")
	assert.Contains(t, m.HTMLBody, `<td dir="rtl" align="right"`, "every row carries the locale direction, so a Latin-named number cannot flip its row")
	assert.Contains(t, m.HTMLBody, `bgcolor="#ffbb00"`)
	assert.NotContains(t, m.HTMLBody, "\u2066")
	assert.NotContains(t, m.HTMLBody, "\u202e")
	assert.Contains(t, m.HTMLBody, `dir="rtl"`)
	assert.Contains(t, m.HTMLBody, ">OpenWA<")
	assert.Contains(t, m.HTMLBody, "font-size:20px;line-height:28px\">"+`انقطع اتصال <span dir="auto"`, "the subject leads as the headline")
}

func TestBidiHTML_EscapesBeforeAddingSpans(t *testing.T) {
	got := string(bidiHTML("<b>x</b> \u2068<script>\u2069"))
	assert.Equal(t, `&lt;b&gt;x&lt;/b&gt; <span dir="auto" style="unicode-bidi:isolate">&lt;script&gt;</span>`, got)
}

func TestDailyCapReadFailureIsAnErrorNotACap(t *testing.T) {
	h := newHarness(t, testOptions())
	h.store.sentErr = errors.New("db down")
	h.sessions.projects = []uuid.UUID{h.project}
	h.sessions.outages[h.project] = []Outage{outage("Sales", "6281322220198")}

	require.Error(t, h.svc.RunDisconnect(h.ctx), "a failed count must surface, not read as a full cap")
	assert.Empty(t, h.sessions.claims)
	assert.Empty(t, h.queue.sent)

	_, err := h.svc.SendTest(h.projectCtx())
	require.Error(t, err)
	assert.False(t, IsTestLimitError(err), "a failed count is unexpected, not the test limit")
	assert.Empty(t, h.queue.sent)
}
