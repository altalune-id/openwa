package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/i18n"
)

func TestErrorPage_HTMXRequestGetsFragmentNotFullPage(t *testing.T) {
	f := newFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/orgs/acme/projects/site/categories/x/delete", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	f.Deps.ErrorPage(rec, req, http.StatusNotFound, "Not found", "That category no longer exists.")

	body := rec.Body.String()
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.NotContains(t, body, "<html", "an htmx error response must be a fragment, not a page")
	require.NotContains(t, body, "<!doctype", "an htmx error response must be a fragment, not a page")
	require.Contains(t, body, `class="alt-error`)
	require.Contains(t, body, "That category no longer exists.")
}

func TestErrorPage_NonHTMXRequestStillGetsFullPage(t *testing.T) {
	f := newFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/orgs/acme", nil)
	rec := httptest.NewRecorder()

	f.Deps.ErrorPage(rec, req, http.StatusNotFound, "Not found", "gone")

	require.Contains(t, rec.Body.String(), "<html", "browser navigation must still get a full page")
}

func TestErrorPageKey_TranslatesTitleAndBody(t *testing.T) {
	f := newFixture(t)
	bundle := i18n.NewEmbeddedBundle(i18n.EnUS)
	req := httptest.NewRequest(http.MethodGet, "/orgs/acme", nil)
	req = req.WithContext(i18n.TranslatorInto(req.Context(), bundle.For(i18n.EnUS)))
	rec := httptest.NewRecorder()

	f.Deps.ErrorPageKey(rec, req, http.StatusNotFound, "error.not_found", nil)

	body := rec.Body.String()
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, body, "<title>Not found · OpenWA</title>")
	require.Contains(t, body, "We could not find that.")
	require.NotContains(t, body, "error.not_found_title")
}

func TestErrorPageKey_HTMXGetsFragmentWithCode(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/orgs/acme/projects/alpha/apikeys/x/revoke", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	f.Deps.ErrorPageKey(rec, req, http.StatusBadRequest, "error.bad_id", nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.NotContains(t, rec.Body.String(), "<html")
	require.Contains(t, rec.Body.String(), `class="alt-error`)
	require.Contains(t, rec.Body.String(), "error.bad_id_title")
}
