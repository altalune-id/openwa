package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/web/handlers"
)

func gateStatus(t *testing.T, required bool, since time.Time, accepted time.Time, path string) (int, string) {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(session.PrincipalInto(req.Context(), session.Principal{UserID: uuid.New(), TermsAcceptedAt: accepted}))
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
