package handlers_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/user"
	"altalune.id/openwa/internal/web/handlers"
)

func (f *handlerFixture) seedLoginUser(t *testing.T, email string) *user.User {
	t.Helper()
	u, err := f.Users.Create(t.Context(), user.CreateRequest{Email: email, Name: "N", Source: user.SourceOIDC, Password: "secret12"})
	require.NoError(t, err)
	return u
}

func (f *handlerFixture) login(t *testing.T, u *user.User) session.Principal {
	t.Helper()
	return postLogin(t, f, newAuthHandlerWithGenesis(t, f, "root@example.com", "unused-genesis-password"), u.Email, "secret12")
}

func (f *handlerFixture) openTodos(t *testing.T, path string, p session.Principal) int {
	t.Helper()
	mux := http.NewServeMux()
	handlers.NewTodoHandler(f.Deps, f.Projects, f.Todos).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, path, "", p))
	return rec.Code
}

func TestLastProject_SurvivesSignOut(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := f.seedLoginUser(t, "back@example.com")
	o := f.seedOrg(t, "acme", u.ID)
	f.seedProject(t, o, u.ID, "alpha")
	beta := f.seedProject(t, o, u.ID, "beta")

	require.Equal(t, http.StatusOK, f.openTodos(t, "/orgs/acme/projects/beta/todos", session.Principal{UserID: u.ID, ActiveOrgID: o.ID}))
	stored, err := f.UserStore.ByID(t.Context(), u.ID)
	require.NoError(t, err)
	require.Equal(t, beta.ID, stored.LastProjectID, "opening a project remembers it on the account")

	p := f.login(t, u)
	assert.Equal(t, beta.ID, p.ActiveProjectID, "a fresh sign-in starts in the remembered project")
	assert.Equal(t, "/orgs/acme/projects/beta/overview", f.landing(t, p))
}

func TestLastProject_LandingUsesItWhenTheSessionHasNoProject(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := uuid.New()
	o := f.seedOrg(t, "acme", u)
	f.seedProject(t, o, u, "alpha")
	beta := f.seedProject(t, o, u, "beta")

	got := f.landing(t, session.Principal{UserID: u, LastProjectID: beta.ID})

	assert.Equal(t, "/orgs/acme/projects/beta/overview", got)
}

func TestLastProject_NeverUsedAfterTheMembershipIsRemoved(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner := uuid.New()
	u := f.seedLoginUser(t, "left@example.com")
	left := f.seedOrg(t, "left", owner)
	gone := f.seedProject(t, left, owner, "gone")
	_, err := f.Orgs.AddMember(setTenant(t.Context(), left.ID, owner), left.ID, u.ID, org.RoleMember)
	require.NoError(t, err)
	require.NoError(t, f.UserStore.SetLastProject(t.Context(), u.ID, gone.ID))
	require.NoError(t, f.Orgs.RemoveMember(setTenant(t.Context(), left.ID, owner), left.ID, u.ID))
	home := f.seedOrg(t, "home", u.ID)
	mine := f.seedProject(t, home, u.ID, "mine")

	p := f.login(t, u)
	assert.Equal(t, mine.ID, p.ActiveProjectID)
	assert.Equal(t, "/orgs/home/projects/mine/overview", f.landing(t, p))
	assert.Equal(t, "/orgs/home/projects/mine/overview", f.landing(t, session.Principal{UserID: u.ID, LastProjectID: gone.ID}))
}

func TestLastProject_NeverUsesAProjectOfAnotherTenant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	stranger := uuid.New()
	u := f.seedLoginUser(t, "sibling@example.com")
	other := f.seedOrg(t, "other", stranger)
	secret := f.seedProject(t, other, stranger, "secret")
	require.NoError(t, f.UserStore.SetLastProject(t.Context(), u.ID, secret.ID))
	home := f.seedOrg(t, "home", u.ID)
	f.seedProject(t, home, u.ID, "one")
	f.seedProject(t, home, u.ID, "two")

	p := f.login(t, u)
	assert.NotEqual(t, secret.ID, p.ActiveProjectID)
	assert.NotEqual(t, other.ID, p.ActiveOrgID)
	assert.Equal(t, "/orgs", f.landing(t, session.Principal{UserID: u.ID, LastProjectID: secret.ID}))
}

func TestLastProject_ADeletedProjectFallsBack(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := uuid.New()
	home := f.seedOrg(t, "home", u)
	f.seedProject(t, home, u, "only")

	got := f.landing(t, session.Principal{UserID: u, LastProjectID: uuid.New()})

	assert.Equal(t, "/orgs/home/projects/only/overview", got)
}

// SECURITY: only a project RequireProject admitted is ever remembered.
func TestLastProject_ARefusedProjectIsNotRemembered(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	stranger := uuid.New()
	u := f.seedLoginUser(t, "nosy@example.com")
	other := f.seedOrg(t, "other", stranger)
	f.seedProject(t, other, stranger, "secret")

	assert.Equal(t, http.StatusNotFound, f.openTodos(t, "/orgs/other/projects/secret/todos", session.Principal{UserID: u.ID}))
	stored, err := f.UserStore.ByID(t.Context(), u.ID)
	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, stored.LastProjectID)
}

func TestLastProject_AFailedWriteIsNotRetriedEveryRequest(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u := uuid.New()
	o := f.seedOrg(t, "acme", u)
	f.seedProject(t, o, u, "alpha")
	f.UserStore.SetLastProjectErr = errors.New("database down")
	mux := http.NewServeMux()
	handlers.NewTodoHandler(f.Deps, f.Projects, f.Todos).Register(mux)

	first := f.authedRequest(t, http.MethodGet, "/orgs/acme/projects/alpha/todos", "", session.Principal{UserID: u, ActiveOrgID: o.ID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, first)
	require.Equal(t, http.StatusOK, rec.Code)

	second := httptest.NewRequest(http.MethodGet, "/orgs/acme/projects/alpha/todos", nil)
	for _, c := range first.Cookies() {
		second.AddCookie(c)
	}
	sp, _, ok := f.Deps.LoadSession(second)
	require.True(t, ok)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, second.WithContext(session.PrincipalInto(second.Context(), sp)))
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, 1, f.UserStore.SetLastProjectCalls(), "a failed write is not retried on every page view")
}
