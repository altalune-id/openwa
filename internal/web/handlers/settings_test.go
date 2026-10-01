package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/web/handlers"
)

func TestSettings_ShowsRetentionAndPendingAndSaves(t *testing.T) {
	x := newInboxFixture(t)
	x.Store.Seed(message.NewInbound(message.Ref{DeviceID: x.device, OrgID: x.tc.OrgID, ProjectID: x.tc.ProjectID}, uuid.New(), fakes.MessagePublicID(),
		message.InboundInput{WAID: "OLD", Type: "text", Timestamp: time.Now().AddDate(0, 0, -40)}))
	mux := http.NewServeMux()
	handlers.NewSettingsHandler(x.Deps, x.Projects, x.Messages).Register(mux)
	p := session.Principal{UserID: x.uid, ActiveOrgID: x.org}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, x.authedRequest(t, http.MethodGet, "/orgs/acme/projects/alpha/settings", "", p))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `value="30"`)
	require.Contains(t, rec.Body.String(), "settings.pending", "the pending count renders through its plural key")

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, x.authedRequest(t, http.MethodPost, "/orgs/acme/projects/alpha/settings/retention", url.Values{"days": {"400"}}.Encode(), p))
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, rec.Body.String(), "settings.error.invalid_days")

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, x.authedRequest(t, http.MethodPost, "/orgs/acme/projects/alpha/settings/retention", url.Values{"days": {"7"}}.Encode(), p))
	require.Equal(t, http.StatusSeeOther, rec.Code)
	days, err := x.Messages.Retention(x.ctx())
	require.NoError(t, err)
	require.Equal(t, 7, days)
}

// SECURITY: a plain member sees the setting but cannot change it.
func TestSettings_RetentionPostNeedsAnOwnerOrAdmin(t *testing.T) {
	x := newInboxFixture(t)
	member := uuid.New()
	_, err := x.Orgs.AddMember(setTenant(context.Background(), x.org, x.uid), x.org, member, org.RoleMember)
	require.NoError(t, err)
	mux := http.NewServeMux()
	handlers.NewSettingsHandler(x.Deps, x.Projects, x.Messages).Register(mux)
	p := session.Principal{UserID: member, ActiveOrgID: x.org}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, x.authedRequest(t, http.MethodGet, "/orgs/acme/projects/alpha/settings", "", p))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), `name="days"`)
	require.Contains(t, rec.Body.String(), "settings.managers_only")

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, x.authedRequest(t, http.MethodPost, "/orgs/acme/projects/alpha/settings/retention", url.Values{"days": {"1"}}.Encode(), p))
	require.Equal(t, http.StatusForbidden, rec.Code)
	days, err := x.Messages.Retention(x.ctx())
	require.NoError(t, err)
	require.Equal(t, 30, days, "the refused post changed nothing")
}
