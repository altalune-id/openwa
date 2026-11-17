package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/user"
	"altalune.id/openwa/internal/web/handlers"
)

func orgCreate(slug, name string, owner uuid.UUID) org.CreateRequest {
	return org.CreateRequest{Slug: slug, Name: name, OwnerID: owner}
}

func TestSignupHandler_GetSignup_RedirectsUnauth(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Cfg.Mode = config.ModeCloud
	h := handlers.NewSignupHandler(f.Deps, f.Users, f.Orgs, f.Projects)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/signup/complete", nil))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/login", rec.Header().Get("Location"))
}

func TestSignupHandler_GetSignup_NotFoundOutsideCloud(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Cfg.Mode = config.ModeSelfhosted
	h := handlers.NewSignupHandler(f.Deps, f.Users, f.Orgs, f.Projects)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/signup/complete", "", session.Principal{UserID: uuid.New()}))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSignupHandler_GetSignup_RedirectsWhenAlreadyHasMembership(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Cfg.Mode = config.ModeCloud
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "alice@example.com", Name: "Alice", Source: user.SourceOIDC})
	require.NoError(t, err)
	_, err = f.Orgs.Create(ctx, orgCreate("acme", "Acme", u.ID))
	require.NoError(t, err)

	h := handlers.NewSignupHandler(f.Deps, f.Users, f.Orgs, f.Projects)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/signup/complete", "", session.Principal{UserID: u.ID, Email: u.Email, Name: u.Name}))
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/", rec.Header().Get("Location"))
}

func TestSignupHandler_GetSignup_Renders(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Cfg.Mode = config.ModeCloud
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "alice@example.com", Name: "Alice", Source: user.SourceOIDC})
	require.NoError(t, err)

	h := handlers.NewSignupHandler(f.Deps, f.Users, f.Orgs, f.Projects)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/signup/complete", "", session.Principal{UserID: u.ID, Email: u.Email, Name: u.Name}))
	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `action="/signup/complete"`)
	assert.Contains(t, body, `name="workspace_name"`)
	for _, gone := range []string{`name="org_name"`, `name="org_slug"`, `name="project_name"`, `name="project_slug"`} {
		assert.NotContains(t, body, gone, "sign-up asks for one name only")
	}
}

func (f *handlerFixture) postSignup(t *testing.T, body string, p session.Principal) *httptest.ResponseRecorder {
	t.Helper()
	f.Cfg.Mode = config.ModeCloud
	mux := http.NewServeMux()
	handlers.NewSignupHandler(f.Deps, f.Users, f.Orgs, f.Projects).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/signup/complete", body, p))
	return rec
}

func TestSignupHandler_PostSignup_MissingNameRerenders(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u, err := f.Users.Create(t.Context(), user.CreateRequest{Email: "alice@example.com", Name: "Alice", Source: user.SourceOIDC})
	require.NoError(t, err)

	rec := f.postSignup(t, "workspace_name=+", session.Principal{UserID: u.ID, Email: u.Email, Name: u.Name})

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "signup.error.name_required")
	orgs, err := f.Orgs.List(t.Context(), u.ID)
	require.NoError(t, err)
	assert.Empty(t, orgs)
}

func TestSignupHandler_PostSignup_InvalidNameCreatesNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u, err := f.Users.Create(t.Context(), user.CreateRequest{Email: "alice@example.com", Name: "Alice", Source: user.SourceOIDC})
	require.NoError(t, err)

	rec := f.postSignup(t, "workspace_name="+strings.Repeat("x", 300), session.Principal{UserID: u.ID, Email: u.Email, Name: u.Name})

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "signup.error.name_invalid")
	orgs, err := f.Orgs.List(t.Context(), u.ID)
	require.NoError(t, err)
	assert.Empty(t, orgs)
}

func TestSignupHandler_PostSignup_OneNameWithRandomSlugs(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u, err := f.Users.Create(t.Context(), user.CreateRequest{Email: "alice@example.com", Name: "Alice", Source: user.SourceOIDC})
	require.NoError(t, err)

	body := "workspace_name=Acme+Support&org_name=Evil&org_slug=acme&project_name=Main&project_slug=main"
	rec := f.postSignup(t, body, session.Principal{UserID: u.ID, Email: u.Email, Name: u.Name})

	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	orgs, err := f.Orgs.List(t.Context(), u.ID)
	require.NoError(t, err)
	require.Len(t, orgs, 1)
	o := orgs[0]
	assert.Equal(t, "Acme Support", o.Name)
	assert.Regexp(t, generatedSlugShape, o.Slug, "the org slug is random, never posted or derived from the name")
	assert.Equal(t, u.ID, o.OwnerID)
	projects, err := f.Projects.List(setTenant(t.Context(), o.ID, u.ID), o.ID)
	require.NoError(t, err)
	require.Len(t, projects, 1)
	assert.Equal(t, "Acme Support", projects[0].Name)
	assert.Regexp(t, generatedSlugShape, projects[0].Slug, "the project slug is random, never posted or derived from the name")
	assert.Equal(t, "/orgs/"+o.Slug+"/projects/"+projects[0].Slug+"/overview", rec.Header().Get("Location"))

	got, err := f.Users.ByID(t.Context(), u.ID)
	require.NoError(t, err)
	assert.Equal(t, "Alice", got.Name, "the workspace name never renames the user")
}

func TestSignupHandler_PostSignup_RequiresTerms(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Cfg.Compliance.RequireAcceptance = true
	u, err := f.Users.Create(t.Context(), user.CreateRequest{Email: "alice@example.com", Name: "Alice", Source: user.SourceOIDC})
	require.NoError(t, err)

	rec := f.postSignup(t, "workspace_name=Acme", session.Principal{UserID: u.ID, Email: u.Email, Name: u.Name})
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "signup.error.accept_terms")
	orgs, err := f.Orgs.List(t.Context(), u.ID)
	require.NoError(t, err)
	assert.Empty(t, orgs)

	rec = f.postSignup(t, "workspace_name=Acme&accept_terms=1", session.Principal{UserID: u.ID, Email: u.Email, Name: u.Name})
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	got, err := f.Users.ByID(t.Context(), u.ID)
	require.NoError(t, err)
	assert.NotNil(t, got.TermsAcceptedAt)
}
