package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/apikey"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/i18n"
	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/platform/authn"
	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/platform/outbox"
	"altalune.id/openwa/internal/platform/sealer"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/project"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/web/handlers"
	"altalune.id/openwa/internal/webhook"
)

type connectFixture struct {
	*handlerFixture
	Keys    *apikey.Service
	Devices *device.Service
	Hooks   *webhook.Service
	Outbox  *fakes.Outbox
	Store   *fakes.WebhookStore
	Mux     *http.ServeMux
	owner   uuid.UUID
	member  session.Principal
	org     uuid.UUID
	alpha   *project.Project
	beta    *project.Project
	dev     *device.Device
}

type keyDevices struct{ svc *device.Service }

func (k keyDevices) DeviceIDs(ctx context.Context, publicIDs []string) (map[string]uuid.UUID, error) {
	return k.svc.IDs(ctx, publicIDs)
}

func (k keyDevices) DevicePublicIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	return k.svc.PublicIDs(ctx, ids)
}

var reKey = regexp.MustCompile(`key_[A-Za-z0-9_-]{20,}`)

func newConnectFixture(t *testing.T) *connectFixture {
	t.Helper()
	f := newFixture(t)
	owner := uuid.New()
	o := f.seedOrg(t, "acme", owner)
	alpha, err := f.Projects.Create(setTenant(context.Background(), o.ID, owner), o.ID, "alpha", "Alpha")
	require.NoError(t, err)
	beta, err := f.Projects.Create(setTenant(context.Background(), o.ID, owner), o.ID, "beta", "Beta")
	require.NoError(t, err)

	keys := apikey.NewService(fakes.NewAPIKey(), apikey.Scheme{}, f.Orgs, projectCatalog{svc: f.Projects}, discardLogger(), passthroughUnexpected())
	devices := device.NewService(fakes.NewDevice(), discardLogger(), passthroughUnexpected(), fakes.UnitOfWork, fakes.NewDeviceSessions(), "")
	keys.Devices = keyDevices{svc: devices}
	key, err := sealer.GenerateKey()
	require.NoError(t, err)
	sl, err := sealer.New(key)
	require.NoError(t, err)
	ob, store := fakes.NewOutbox(), fakes.NewWebhookStore()
	hooks := webhook.NewService(store, discardLogger(), passthroughUnexpected(), sl, ob, slugOf{}, deviceIDs{svc: devices})

	x := &connectFixture{handlerFixture: f, Keys: keys, Devices: devices, Hooks: hooks, Outbox: ob, Store: store, owner: owner, org: o.ID, alpha: alpha, beta: beta}
	x.member = addMember(t, f, o.ID, owner, org.RoleMember)
	x.dev = x.seedDevice(t, alpha.ID, "Sales")
	x.Mux = http.NewServeMux()
	handlers.NewConnectHandler(f.Deps, f.Projects, keys, devices, hooks).Register(x.Mux)
	return x
}

func (x *connectFixture) seedDevice(t *testing.T, projectID uuid.UUID, name string) *device.Device {
	t.Helper()
	d, err := x.Devices.Create(setTenantProject(context.Background(), x.org, projectID, x.owner), name)
	require.NoError(t, err)
	return d
}

func (x *connectFixture) ctx() context.Context {
	return setTenantProject(context.Background(), x.org, x.alpha.ID, x.owner)
}

func (x *connectFixture) path(suffix string) string {
	return "/orgs/acme/projects/alpha/devices/" + x.dev.PublicID + "/connect" + suffix
}

func (x *connectFixture) as(t *testing.T, p session.Principal, method, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := x.authedRequest(t, method, target, form.Encode(), p)
	r.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, r)
	return rec
}

func (x *connectFixture) owned(t *testing.T, method, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return x.as(t, session.Principal{UserID: x.owner, ActiveOrgID: x.org}, method, target, form)
}

func (x *connectFixture) hooks(t *testing.T) []*webhook.Endpoint {
	t.Helper()
	got, err := x.Hooks.List(x.ctx())
	require.NoError(t, err)
	return got
}

func TestConnect_PageShowsTheAddressFactsAndNoSecrets(t *testing.T) {
	t.Parallel()
	x := newConnectFixture(t)
	rec := x.owned(t, http.MethodGet, x.path(""), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "http://localhost/api/v1/orgs/acme/projects/alpha")
	assert.Contains(t, body, x.dev.PublicID)
	assert.Empty(t, reKey.FindString(body))
	assert.Empty(t, reSecret.FindString(body))
}

func TestConnect_SiblingProjectsNumberIsNotFound(t *testing.T) {
	t.Parallel()
	x := newConnectFixture(t)
	sibling := x.seedDevice(t, x.beta.ID, "Beta")
	base := "/orgs/acme/projects/alpha/devices/" + sibling.PublicID + "/connect"

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, base}, {http.MethodPost, base + "/key"}, {http.MethodPost, base + "/address"}, {http.MethodPost, base + "/test"},
	} {
		rec := x.owned(t, tc.method, tc.path, url.Values{"url": {"https://agent.example.com/hook"}})
		assert.Equal(t, http.StatusNotFound, rec.Code, "%s %s", tc.method, tc.path)
	}
	assert.Empty(t, x.hooks(t))
	keys, err := x.Keys.List(x.ctx(), x.alpha.ID)
	require.NoError(t, err)
	assert.Empty(t, keys)
}

func TestConnect_KeyIsBoundToTheNumberAndShownOnce(t *testing.T) {
	t.Parallel()
	x := newConnectFixture(t)
	rec := x.owned(t, http.MethodPost, x.path("/key"), url.Values{})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	plaintext := reKey.FindString(rec.Body.String())
	require.NotEmpty(t, plaintext)

	keys, err := x.Keys.List(x.ctx(), x.alpha.ID)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	got := slices.Clone(keys[0].Scopes)
	slices.Sort(got)
	assert.Equal(t, []string{authn.ScopeMessagesRead, authn.ScopeMessagesWrite}, got)
	assert.Equal(t, []uuid.UUID{x.dev.ID}, keys[0].ResourceIDs)

	again := x.owned(t, http.MethodGet, x.path(""), nil)
	assert.NotContains(t, again.Body.String(), plaintext)
}

func TestConnect_AddressCreatesOneScopedMatchedWebhook(t *testing.T) {
	t.Parallel()
	x := newConnectFixture(t)

	bad := x.owned(t, http.MethodPost, x.path("/address"), url.Values{"url": {"javascript:alert(1)"}})
	require.Equal(t, http.StatusOK, bad.Code)
	assert.Contains(t, bad.Body.String(), "webhooks.error.invalid_url")
	assert.Empty(t, x.hooks(t))

	rec := x.owned(t, http.MethodPost, x.path("/address"), url.Values{"url": {"https://agent.example.com/hook"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	secret := reSecret.FindString(rec.Body.String())
	require.NotEmpty(t, secret)

	hooks := x.hooks(t)
	require.Len(t, hooks, 1)
	assert.Equal(t, []events.Type{events.MessageMatched}, hooks[0].EventTypes)
	require.NotNil(t, hooks[0].DeviceID)
	assert.Equal(t, x.dev.ID, *hooks[0].DeviceID)

	twice := x.owned(t, http.MethodPost, x.path("/address"), url.Values{"url": {"https://other.example.com/hook"}})
	require.Equal(t, http.StatusOK, twice.Code)
	assert.Len(t, x.hooks(t), 1, "a second save never creates a second endpoint")
	assert.Empty(t, reSecret.FindString(twice.Body.String()))

	for _, later := range []*httptest.ResponseRecorder{
		x.owned(t, http.MethodGet, x.path(""), nil),
		x.owned(t, http.MethodPost, x.path("/test"), url.Values{}),
		x.owned(t, http.MethodGet, x.path("/test?n=1"), nil),
	} {
		assert.NotContains(t, later.Body.String(), secret, "the secret is only in the save response")
	}
}

func TestConnect_TestPollMapsTheLatestDelivery(t *testing.T) {
	t.Parallel()
	x := newConnectFixture(t)
	e, _, err := x.Hooks.Create(x.ctx(), "https://agent.example.com/hook", "", []events.Type{events.MessageMatched}, x.dev.PublicID)
	require.NoError(t, err)

	sent := x.owned(t, http.MethodPost, x.path("/test"), url.Values{})
	require.Equal(t, http.StatusOK, sent.Code, sent.Body.String())
	assert.Contains(t, sent.Body.String(), "connect.state_waiting")
	assert.Contains(t, sent.Body.String(), x.path("/test?n=1"))
	require.Len(t, x.Outbox.Entries(), 1)

	capped := x.owned(t, http.MethodGet, x.path("/test?n=10"), nil)
	assert.Contains(t, capped.Body.String(), "connect.check_again", "polling stops after a bounded count")
	assert.NotContains(t, capped.Body.String(), x.path("/test?n=11"))

	claimed, err := x.Outbox.ClaimDue(x.ctx(), time.Now().Add(time.Hour), outbox.MaxClaimLimit)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.NoError(t, x.Store.SaveAttempt(x.ctx(), webhook.Attempt{
		ID: uuid.New(), OrgID: x.org, ProjectID: x.alpha.ID, EndpointID: e.ID, DeliveryID: claimed[0].ID, EventID: claimed[0].EventID,
		EventType: events.WebhookPing, Attempt: 1, StatusCode: 401, CreatedAt: time.Now(),
	}))
	require.NoError(t, x.Outbox.Fail(x.ctx(), claimed[0], time.Now().Add(time.Minute), "status 401"))
	retrying := x.owned(t, http.MethodGet, x.path("/test?n=2"), nil)
	assert.Contains(t, retrying.Body.String(), "connect.state_retrying")
	assert.Contains(t, retrying.Body.String(), "connect.detail_status", "the HTTP status sits in Details when the agent answered")

	claimed, err = x.Outbox.ClaimDue(x.ctx(), time.Now().Add(time.Hour), outbox.MaxClaimLimit)
	require.NoError(t, err)
	require.NoError(t, x.Outbox.Succeed(x.ctx(), claimed[0], time.Now()))
	done := x.owned(t, http.MethodGet, x.path("/test?n=3"), nil)
	assert.Contains(t, done.Body.String(), "connect.state_delivered")
	assert.NotContains(t, done.Body.String(), x.path("/test?n=4"), "a settled delivery stops the poll")
}

func TestConnect_MembersReadOnlyAndRefused(t *testing.T) {
	t.Parallel()
	x := newConnectFixture(t)

	page := x.as(t, x.member, http.MethodGet, x.path(""), nil)
	require.Equal(t, http.StatusOK, page.Code)
	assert.NotContains(t, page.Body.String(), x.path("/key"))
	assert.NotContains(t, page.Body.String(), x.path("/address"))
	assert.Contains(t, page.Body.String(), "connect.manager_only")
	assert.NotContains(t, page.Body.String(), x.path("/test"), "no Send a test for a member")
	assert.Empty(t, reKey.FindString(page.Body.String()))
	assert.Empty(t, reSecret.FindString(page.Body.String()))

	for _, suffix := range []string{"/key", "/address", "/test"} {
		rec := x.as(t, x.member, http.MethodPost, x.path(suffix), url.Values{"url": {"https://agent.example.com/hook"}})
		assert.Equal(t, http.StatusForbidden, rec.Code, suffix)
	}
	assert.Empty(t, x.hooks(t))
	keys, err := x.Keys.List(x.ctx(), x.alpha.ID)
	require.NoError(t, err)
	assert.Empty(t, keys)
}

func TestConnect_RecommendedAgentTileFollowsConfig(t *testing.T) {
	t.Parallel()
	x := newConnectFixture(t)
	plain := x.owned(t, http.MethodGet, x.path(""), nil)
	assert.NotContains(t, plain.Body.String(), "common.recommended", "no tile, not even an empty one, without config")

	x.Cfg.Connect.RecommendedAgent.Name = "Helper"
	x.Cfg.Connect.RecommendedAgent.ConsoleURL = "https://agent.example.com/console"
	x.Cfg.Connect.RecommendedAgent.GuideURL = "https://agent.example.com/guide"
	tile := x.owned(t, http.MethodGet, x.path(""), nil)
	assert.Contains(t, tile.Body.String(), "https://agent.example.com/console")
	assert.Contains(t, tile.Body.String(), "https://agent.example.com/guide")
}

func TestConnect_TestStateReadsThePingOnly(t *testing.T) {
	t.Parallel()
	x := newConnectFixture(t)
	e, _, err := x.Hooks.Create(x.ctx(), "https://agent.example.com/hook", "", []events.Type{events.MessageMatched}, x.dev.PublicID)
	require.NoError(t, err)
	require.NoError(t, x.Hooks.SendTest(x.ctx(), e.ID))
	claimed, err := x.Outbox.ClaimDue(x.ctx(), time.Now().Add(time.Hour), outbox.MaxClaimLimit)
	require.NoError(t, err)
	require.NoError(t, x.Outbox.Succeed(x.ctx(), claimed[0], time.Now()))

	require.NoError(t, fakes.UnitOfWork(x.ctx(), func(ctx context.Context) error {
		return x.Hooks.EnqueueForDevice(ctx, events.MessageMatched, x.dev.ID, events.MessageEventV1{})
	}))
	require.Len(t, x.Outbox.Entries(), 2)

	rec := x.owned(t, http.MethodGet, x.path("/test?n=1"), nil)
	assert.Contains(t, rec.Body.String(), "connect.state_delivered", "a newer real message never hides the test result")
}

func TestConnect_AddressRacingASecondSubmitShowsTheWinner(t *testing.T) {
	t.Parallel()
	x := newConnectFixture(t)
	winner, _, err := x.Hooks.Create(x.ctx(), "https://agent.example.com/hook", "", []events.Type{events.MessageMatched}, x.dev.PublicID)
	require.NoError(t, err)
	x.Store.SaveErr = func(e *webhook.Endpoint) error {
		if e.ID != winner.ID {
			return &webhook.DuplicateEndpointError{URL: e.URL}
		}
		return nil
	}
	hidden := *winner
	hidden.DeviceID = nil
	x.Store.Seed(&hidden)

	rec := x.owned(t, http.MethodPost, x.path("/address"), url.Values{"url": {"https://agent.example.com/hook"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, reSecret.FindString(rec.Body.String()), "the losing submit never shows a secret")
	assert.NotContains(t, rec.Body.String(), "webhooks.error.", "the loser is not shown an error")
	x.Store.Seed(winner)
	assert.Len(t, x.hooks(t), 1)
}

func TestConnect_AddressErrorRendersItsParameters(t *testing.T) {
	t.Parallel()
	x := newConnectFixture(t)
	r := x.authedRequest(t, http.MethodPost, x.path("/address"), url.Values{"url": {"javascript:alert(1)"}}.Encode(), session.Principal{UserID: x.owner, ActiveOrgID: x.org})
	r.Header.Set("HX-Request", "true")
	r = r.WithContext(i18n.TranslatorInto(r.Context(), i18n.NewEmbeddedBundle(i18n.EnUS).For(i18n.EnUS)))
	rec := httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, r)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.NotContains(t, body, "<no value>", "every parameter of the error copy is filled")
	assert.NotContains(t, body, "&lt;no value&gt;")
	assert.Contains(t, body, strconv.Itoa(webhook.MaxURLLen))
}
