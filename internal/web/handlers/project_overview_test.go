package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/i18n"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/web/handlers"
)

const overviewBase = "/orgs/acme/projects/alpha"

type overviewFixture struct {
	*handlerFixture
	Mux     *http.ServeMux
	uid     uuid.UUID
	org     uuid.UUID
	project uuid.UUID
}

func newOverviewFixture(t *testing.T) *overviewFixture {
	t.Helper()
	f := newFixture(t)
	uid := uuid.New()
	o := f.seedOrg(t, "acme", uid)
	octx := setTenant(context.Background(), o.ID, uid)
	proj, err := f.Projects.Create(octx, o.ID, "alpha", "Alpha")
	require.NoError(t, err)
	mux := http.NewServeMux()
	handlers.NewProjectOverviewHandler(f.Deps, f.Projects, nil).Register(mux)
	return &overviewFixture{handlerFixture: f, Mux: mux, uid: uid, org: o.ID, project: proj.ID}
}

func (x *overviewFixture) do(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	p := session.Principal{UserID: x.uid, ActiveOrgID: x.org, ActiveProjectID: x.project}
	rec := httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, x.authedRequest(t, method, target, body, p))
	return rec
}

func TestProjectOverview_RendersZeroTilesWithoutTodos(t *testing.T) {
	x := newOverviewFixture(t)
	rec := x.do(t, http.MethodGet, overviewBase+"/overview", "")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "overview.devices")
	require.Contains(t, body, "overview.messages_today")
	require.NotContains(t, body, "/todos")
}

func TestOverview_ShowsCopyableIdentifiers(t *testing.T) {
	x := newOverviewFixture(t)

	rec := x.do(t, http.MethodGet, overviewBase+"/overview", "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, x.project.String(), "overview must show the project UUID")
	assert.Contains(t, body, `data-copy="`+x.project.String()+`"`, "project UUID needs a copy button")
	assert.Contains(t, body, `data-copy="alpha"`, "project slug needs a copy button")
	assert.Contains(t, body, `data-copy="acme"`, "org slug needs a copy button")
	assert.Contains(t, body, "font-mono", "the identifiers are rendered in a monospace face")
	assert.Len(t, namedCopyControl.FindAllString(body, -1), 3,
		"org slug, project slug and project id must each be a copy control with an accessible name")
}

var namedCopyControl = regexp.MustCompile(`<button[^>]*data-copy="[^"]*"[^>]*aria-label="[^"]+"`)

var noncedCopyScript = regexp.MustCompile(`<script nonce="[^"]*">[^<]*navigator\.clipboard`)

func TestOverview_CopyScriptCarriesNonce(t *testing.T) {
	x := newOverviewFixture(t)
	x.Cfg.HTTP.CSP.Enabled = true

	rec := x.do(t, http.MethodGet, overviewBase+"/overview", "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	require.Contains(t, body, "data-copied-label", "the copy buttons must be on the page")
	assert.Regexp(t, noncedCopyScript, body, "the copy script must carry a nonce or CSP silently drops it")
}

func TestProjectOverview_DevicesTileShowsTotalAndConnected(t *testing.T) {
	x := newOverviewFixture(t)
	sessions := fakes.NewDeviceSessions()
	devices := device.NewService(fakes.NewDevice(), discardLogger(), passthroughUnexpected(), fakes.UnitOfWork, sessions)
	ctx := setTenantProject(context.Background(), x.org, x.project, x.uid)
	up, err := devices.Create(ctx, "Sales")
	require.NoError(t, err)
	_, err = devices.Create(ctx, "Support")
	require.NoError(t, err)
	sessions.Statuses[up.ID] = device.SessionStatus{State: device.SessionConnected}
	x.Mux = http.NewServeMux()
	handlers.NewProjectOverviewHandler(x.Deps, x.Projects, devices).Register(x.Mux)

	p := session.Principal{UserID: x.uid, ActiveOrgID: x.org, ActiveProjectID: x.project}
	req := x.authedRequest(t, http.MethodGet, overviewBase+"/overview", "", p)
	bundle := i18n.NewEmbeddedBundle(i18n.EnUS)
	req = req.WithContext(i18n.TranslatorInto(req.Context(), bundle.For(i18n.EnUS)))
	rec := httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "2 · 1 connected")
}
