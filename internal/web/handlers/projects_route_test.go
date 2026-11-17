package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/web/handlers"
)

func TestProjectsList_RouteIsGone(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner := uuid.New()
	o := f.seedOrg(t, "acme", owner)
	mux := http.NewServeMux()
	handlers.NewProjectHandler(f.Deps, f.Projects).Register(mux)

	for name, p := range map[string]session.Principal{"owner": {UserID: owner, ActiveOrgID: o.ID}, "non-member": {UserID: uuid.New()}} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/orgs/acme/projects", "", p))
		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, name)
		assert.Equal(t, http.MethodPost, rec.Header().Get("Allow"), "%s: the path only takes the create POST", name)
		assert.Empty(t, rec.Header().Get("Location"), "%s: no redirect", name)
	}
}
