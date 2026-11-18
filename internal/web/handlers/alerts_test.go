package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/alert"
	"altalune.id/openwa/internal/i18n"
	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/platform/sealer"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/web/handlers"
)

type alertMembers struct{}

func (alertMembers) Members(context.Context, uuid.UUID) ([]alert.Member, error) {
	return []alert.Member{{Email: "owner@example.com", Locale: "en-US", Manager: true}}, nil
}

type alertProjects struct{}

func (alertProjects) Describe(context.Context, uuid.UUID) (alert.Project, error) {
	return alert.Project{Name: "Alpha", Slug: "alpha", OrgSlug: "acme"}, nil
}

type noOutages struct{}

func (noOutages) ProjectsNeedingAlert(context.Context, time.Duration, time.Duration) ([]uuid.UUID, error) {
	return nil, nil
}

func (noOutages) ClaimOutages(context.Context, time.Duration, time.Duration) ([]alert.Outage, error) {
	return nil, nil
}

type testAlerts struct {
	svc   *alert.Service
	store *fakes.AlertStore
	queue *fakes.MailQueue
	sl    sealer.Sealer
}

func newTestAlertsWith(t *testing.T, opts *alert.Options) testAlerts {
	t.Helper()
	key, err := sealer.GenerateKey()
	require.NoError(t, err)
	sl, err := sealer.New(key)
	require.NoError(t, err)
	o := alert.Options{MailEnabled: true, Enabled: true, Grace: 10 * time.Minute, Cooldown: 6 * time.Hour, DailyOrgCap: 50, LinkBase: "https://wa.example.com", Brand: "OpenWA"}
	if opts != nil {
		o = *opts
	}
	x := testAlerts{store: fakes.NewAlertStore(), queue: &fakes.MailQueue{}, sl: sl}
	x.svc = alert.NewService(alert.Deps{
		Store: x.store, Sessions: noOutages{}, Members: alertMembers{}, Projects: alertProjects{}, Queue: x.queue,
		UnitOfWork: fakes.UnitOfWork, Sealer: sl, Translations: i18n.NewEmbeddedBundle(i18n.EnUS),
		Log: discardLogger(), Unexpected: passthroughUnexpected(),
	}, o)
	return x
}

func newTestAlerts(t *testing.T, opts *alert.Options) *alert.Service {
	t.Helper()
	return newTestAlertsWith(t, opts).svc
}

func (x *inboxFixture) alertsMux(t *testing.T, a testAlerts) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	handlers.NewSettingsHandler(x.Deps, x.Projects, x.Messages, a.svc).Register(mux)
	handlers.NewUnsubscribeHandler(x.Deps, a.svc).Register(mux)
	return mux
}

func serve(mux *http.ServeMux, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec
}

const alertsPath = "/orgs/acme/projects/alpha/settings/alerts"

func TestAlerts_ManagerSavesAndRefusesABadAddress(t *testing.T) {
	x := newInboxFixture(t)
	a := newTestAlertsWith(t, nil)
	mux := x.alertsMux(t, a)
	p := session.Principal{UserID: x.uid, ActiveOrgID: x.org}

	rec := serve(mux, x.authedRequest(t, http.MethodPost, alertsPath, url.Values{"recipients": {"Ops@Example.com, ops@example.com"}}.Encode(), p))
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	s, err := a.svc.Settings(x.ctx())
	require.NoError(t, err)
	require.False(t, s.Enabled, "an unchecked toggle saves off")
	require.Equal(t, []string{"ops@example.com"}, s.Recipients)

	rec = serve(mux, x.authedRequest(t, http.MethodPost, alertsPath, url.Values{"disconnect_enabled": {"1"}, "recipients": {"Ops <ops@example.com>"}}.Encode(), p))
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, rec.Body.String(), "Ops")
	s, err = a.svc.Settings(x.ctx())
	require.NoError(t, err)
	require.False(t, s.Enabled, "a refused save stores nothing")
}

// SECURITY: alert settings and test emails are manager-only; a member reads them but cannot change them.
func TestAlerts_MemberCannotWrite(t *testing.T) {
	x := newInboxFixture(t)
	member := uuid.New()
	_, err := x.Orgs.AddMember(setTenant(context.Background(), x.org, x.uid), x.org, member, org.RoleMember)
	require.NoError(t, err)
	a := newTestAlertsWith(t, nil)
	mux := x.alertsMux(t, a)
	p := session.Principal{UserID: member, ActiveOrgID: x.org}

	rec := serve(mux, x.authedRequest(t, http.MethodGet, "/orgs/acme/projects/alpha/settings", "", p))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, mainOf(t, rec.Body.String()), "alerts.readonly")

	rec = serve(mux, x.authedRequest(t, http.MethodPost, alertsPath, url.Values{"recipients": {"spy@example.com"}}.Encode(), p))
	require.Equal(t, http.StatusForbidden, rec.Code)
	rec = serve(mux, x.authedRequest(t, http.MethodPost, alertsPath+"/test", "", p))
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Empty(t, a.queue.Messages())
	s, err := a.svc.Settings(x.ctx())
	require.NoError(t, err)
	require.Empty(t, s.Recipients)
}

func TestAlerts_TestEmailQueuesWithUnsubscribeAndRespectsMailOff(t *testing.T) {
	x := newInboxFixture(t)
	a := newTestAlertsWith(t, nil)
	mux := x.alertsMux(t, a)
	p := session.Principal{UserID: x.uid, ActiveOrgID: x.org}

	rec := serve(mux, x.authedRequest(t, http.MethodPost, alertsPath+"/test", "", p))
	require.Equal(t, http.StatusSeeOther, rec.Code)
	msgs := a.queue.Messages()
	require.Len(t, msgs, 1)
	require.Equal(t, "owner@example.com", msgs[0].To)
	require.Equal(t, "List-Unsubscribe=One-Click", msgs[0].Headers["List-Unsubscribe-Post"])

	off := newTestAlertsWith(t, &alert.Options{Enabled: true, Grace: time.Minute, Cooldown: time.Hour, DailyOrgCap: 50})
	rec = serve(x.alertsMux(t, off), x.authedRequest(t, http.MethodPost, alertsPath+"/test", "", p))
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Empty(t, off.queue.Messages(), "nothing is queued without a delivering mail driver")
}

func TestUnsubscribe_ConfirmThenDoneRecordsOnlyOnPost(t *testing.T) {
	x := newInboxFixture(t)
	a := newTestAlertsWith(t, nil)
	mux := x.alertsMux(t, a)
	_, err := a.svc.SendTest(x.ctx())
	require.NoError(t, err)
	link := a.queue.Messages()[0].Headers["List-Unsubscribe"]
	token := link[strings.Index(link, "t=")+2 : len(link)-1]

	rec := serve(mux, httptest.NewRequest(http.MethodGet, "/alerts/unsubscribe?t="+url.QueryEscape(token), nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `name="t"`)
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	out, err := a.store.OptOuts(tenant.Into(context.Background(), x.tc))
	require.NoError(t, err)
	require.Empty(t, out, "a GET never unsubscribes, so link prefetchers cannot")

	req := httptest.NewRequest(http.MethodPost, "/alerts/unsubscribe?t="+url.QueryEscape(token), strings.NewReader("List-Unsubscribe=One-Click"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = serve(mux, req)
	require.Equal(t, http.StatusOK, rec.Code)
	out, err = a.store.OptOuts(tenant.Into(context.Background(), x.tc))
	require.NoError(t, err)
	require.Equal(t, []string{"owner@example.com"}, out)

	rec = serve(mux, httptest.NewRequest(http.MethodGet, "/alerts/unsubscribe?t=nope", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAlerts_CardWarnsWhenTheServiceCannotDeliver(t *testing.T) {
	x := newInboxFixture(t)
	p := session.Principal{UserID: x.uid, ActiveOrgID: x.org}
	ready := newTestAlertsWith(t, nil)
	rec := serve(x.alertsMux(t, ready), x.authedRequest(t, http.MethodGet, "/orgs/acme/projects/alpha/settings", "", p))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, mainOf(t, rec.Body.String()), "alerts.email_off")

	notReady := newTestAlertsWith(t, &alert.Options{Enabled: true, Grace: time.Minute, Cooldown: time.Hour, DailyOrgCap: 50})
	rec = serve(x.alertsMux(t, notReady), x.authedRequest(t, http.MethodGet, "/orgs/acme/projects/alpha/settings", "", p))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, mainOf(t, rec.Body.String()), "alerts.email_off", "the card reads the same readiness the service sends with")
}

func TestAlerts_AnEleventhAddressIsACountErrorNotAnInvalidOne(t *testing.T) {
	x := newInboxFixture(t)
	a := newTestAlertsWith(t, nil)
	p := session.Principal{UserID: x.uid, ActiveOrgID: x.org}
	addrs := make([]string, 11)
	for i := range addrs {
		addrs[i] = "u" + string(rune('a'+i)) + "@example.com"
	}
	rec := serve(x.alertsMux(t, a), x.authedRequest(t, http.MethodPost, alertsPath, url.Values{"recipients": {strings.Join(addrs, ", ")}}.Encode(), p))
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := mainOf(t, rec.Body.String())
	require.Contains(t, body, "alerts.recipients_error")
	require.NotContains(t, body, "alerts.recipient_invalid", "no address is invalid; the list is just too long")
}
