package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/user"
	"altalune.id/openwa/internal/web/handlers"
)

func gateStatus(t *testing.T, required bool, since time.Time, accepted time.Time, path string) (int, string) {
	t.Helper()
	return gateStatusFor(t, required, since, session.Principal{UserID: uuid.New(), Name: "Named", TermsAcceptedAt: accepted}, path)
}

func gateStatusFor(t *testing.T, required bool, since time.Time, p session.Principal, path string) (int, string) {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(session.PrincipalInto(req.Context(), p))
	rec := httptest.NewRecorder()
	handlers.WelcomeGate("", required, since)(next).ServeHTTP(rec, req)
	return rec.Code, rec.Header().Get("Location")
}

func TestWelcomeGate_DatedReacceptance(t *testing.T) {
	t.Parallel()
	updated := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		required bool
		since    time.Time
		accepted time.Time
		want     int
	}{
		{"accepted before the update is asked again", true, updated, updated.Add(-24 * time.Hour), http.StatusSeeOther},
		{"accepted after the update passes", true, updated, updated.Add(time.Hour), http.StatusTeapot},
		{"accepted exactly at the update passes", true, updated, updated, http.StatusTeapot},
		{"never accepted is asked", true, updated, time.Time{}, http.StatusSeeOther},
		{"zero date gates first acceptance only", true, time.Time{}, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), http.StatusTeapot},
		{"zero date still asks a first-timer", true, time.Time{}, time.Time{}, http.StatusSeeOther},
		{"acceptance not required passes", false, updated, time.Time{}, http.StatusTeapot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, loc := gateStatus(t, tc.required, tc.since, tc.accepted, "/orgs/acme")
			assert.Equal(t, tc.want, code)
			if tc.want == http.StatusSeeOther {
				assert.Equal(t, "/welcome?return_to=/orgs/acme", loc)
			}
		})
	}
}

func TestWelcomeGate_SkipsWelcomeAndLegalPaths(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"/welcome", "/terms", "/privacy", "/logout", "/static/app.css"} {
		code, _ := gateStatus(t, true, time.Now(), time.Time{}, p)
		assert.Equal(t, http.StatusTeapot, code, p)
	}
}

func TestWelcomeGate_LetsLanguageAndThemeThroughBeforeAcceptance(t *testing.T) {
	t.Parallel()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	gate := handlers.WelcomeGate("", true, time.Time{})(next)
	p := session.Principal{UserID: uuid.New(), Name: "Named"}
	for _, path := range []string{"/locale", "/theme"} {
		r := httptest.NewRequest(http.MethodPost, path, nil)
		r = r.WithContext(session.PrincipalInto(r.Context(), p))
		rec := httptest.NewRecorder()
		gate.ServeHTTP(rec, r)
		assert.Equal(t, http.StatusTeapot, rec.Code, "%s must reach its handler so the welcome page can switch language and theme", path)
	}
	code, _ := gateStatus(t, true, time.Time{}, time.Time{}, "/orgs")
	assert.Equal(t, http.StatusSeeOther, code, "other pages still wait for acceptance")
}

func TestWelcome_ShowsTermsChangedWhenReaccepting(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Cfg.Compliance.RequireAcceptance = true
	deps := f.Deps
	deps.TermsUpdatedAt = time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	h := handlers.NewWelcomeHandler(deps, f.Users)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	p := session.Principal{UserID: uuid.New(), Email: "a@b.co", Name: "A", TermsAcceptedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/welcome", "", p))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "welcome.terms_changed")
	assert.Contains(t, rec.Body.String(), `name="accept_terms"`)
}

func TestWelcomeGate_AsksANamelessUserForAName(t *testing.T) {
	t.Parallel()
	accepted := time.Now()
	for _, required := range []bool{true, false} {
		code, loc := gateStatusFor(t, required, time.Time{}, session.Principal{UserID: uuid.New(), TermsAcceptedAt: accepted}, "/orgs/acme")
		assert.Equal(t, http.StatusSeeOther, code, "terms required=%v", required)
		assert.Equal(t, "/welcome?return_to=/orgs/acme", loc)

		code, _ = gateStatusFor(t, required, time.Time{}, session.Principal{UserID: uuid.New(), Name: "  ", TermsAcceptedAt: accepted}, "/orgs/acme")
		assert.Equal(t, http.StatusSeeOther, code, "a blank name counts as none, terms required=%v", required)

		code, _ = gateStatusFor(t, required, time.Time{}, session.Principal{UserID: uuid.New(), Name: "Alice", TermsAcceptedAt: accepted}, "/orgs/acme")
		assert.Equal(t, http.StatusTeapot, code, "a named user passes, terms required=%v", required)
	}
}

func TestWelcomeGate_NamelessUserNeverLoops(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/welcome", "/logout", "/locale", "/theme", "/static/app.css", "/signup/complete", "/terms"} {
		code, _ := gateStatusFor(t, false, time.Time{}, session.Principal{UserID: uuid.New()}, path)
		assert.Equal(t, http.StatusTeapot, code, path)
	}
	code, _ := gateStatusFor(t, false, time.Time{}, session.Principal{}, "/orgs/acme")
	assert.Equal(t, http.StatusTeapot, code, "a signed-out request is not the gate's business")
}

func TestWelcome_NameSubmitClearsTheGateWithoutTerms(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	u, err := f.Users.Create(t.Context(), user.CreateRequest{Email: "noname@example.com", Name: "x", Source: user.SourceOIDC})
	require.NoError(t, err)
	mux := http.NewServeMux()
	handlers.NewWelcomeHandler(f.Deps, f.Users).Register(mux)
	p := session.Principal{UserID: u.ID, Email: u.Email}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/welcome?return_to=/orgs", "", p))
	require.Equal(t, http.StatusOK, rec.Code, "the name step renders with terms off")
	assert.Contains(t, rec.Body.String(), `name="name"`)
	assert.NotContains(t, rec.Body.String(), `name="accept_terms"`, "terms are not demanded when not required")

	req := f.authedRequest(t, http.MethodPost, "/welcome", "name=Alice&return_to=/orgs", p)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, "/orgs", rec.Header().Get("Location"))

	sp, _, ok := f.Deps.LoadSession(req)
	require.True(t, ok)
	assert.Equal(t, "Alice", sp.Name)
	code, _ := gateStatusFor(t, false, time.Time{}, sp, "/orgs")
	assert.Equal(t, http.StatusTeapot, code, "after naming, the gate lets the user through")
}
