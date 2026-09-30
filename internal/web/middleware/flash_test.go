package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/web"
	"altalune.id/openwa/internal/web/middleware"
)

func flashSecretBytes() []byte { return []byte("supersecret-1234567890abcdef") }

func flashCookie(t *testing.T, p web.FlashPayload) *http.Cookie {
	t.Helper()
	raw, err := web.EncodeFlash(p)
	require.NoError(t, err)
	return &http.Cookie{Name: web.FlashCookieName, Value: web.SignCookie(flashSecretBytes(), raw)}
}

func TestFlash_ReadsOnceAndClears(t *testing.T) {
	t.Parallel()
	var seen web.FlashPayload
	var ok bool
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen, ok = middleware.FlashFrom(r.Context())
	})
	mw := middleware.Flash(flashSecretBytes(), "/app", true)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/app/orgs", nil)
	req.AddCookie(flashCookie(t, web.FlashPayload{Kind: web.FlashOK, Key: "flash.org_created", Args: []string{"Name", "Acme"}}))
	mw(next).ServeHTTP(rr, req)

	require.True(t, ok)
	assert.Equal(t, web.FlashOK, seen.Kind)
	assert.Equal(t, "flash.org_created", seen.Key)
	assert.Equal(t, []string{"Name", "Acme"}, seen.Args)
	set := rr.Header().Get("Set-Cookie")
	assert.Contains(t, set, web.FlashCookieName+"=;")
	assert.Contains(t, set, "Max-Age=0")
	assert.Contains(t, set, "Path=/app/")
}

func TestFlash_TamperedCookieIsIgnoredAndCleared(t *testing.T) {
	t.Parallel()
	var ok bool
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { _, ok = middleware.FlashFrom(r.Context()) })
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: web.FlashCookieName, Value: "forged|sig"})
	middleware.Flash(flashSecretBytes(), "", false)(next).ServeHTTP(rr, req)
	assert.False(t, ok)
	assert.Contains(t, rr.Header().Get("Set-Cookie"), "Max-Age=0")
}

func TestFlash_HTMXAndStaticRequestsPassThrough(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		basePath string
		path     string
		hx       bool
	}{
		{"htmx poll", "", "/orgs/acme/projects/alpha/inbox", true},
		{"static asset", "", "/static/app.css", false},
		{"static asset under basePath", "/app", "/app/static/app.css", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ok bool
			next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { _, ok = middleware.FlashFrom(r.Context()) })
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.hx {
				req.Header.Set("HX-Request", "true")
			}
			req.AddCookie(flashCookie(t, web.FlashPayload{Kind: web.FlashOK, Key: "k"}))
			middleware.Flash(flashSecretBytes(), tc.basePath, false)(next).ServeHTTP(rr, req)
			assert.False(t, ok, "the flash must wait for the next full-page navigation")
			assert.Empty(t, rr.Header().Get("Set-Cookie"), "the cookie must survive")
		})
	}
}

func TestFlash_NoCookieIsPassthrough(t *testing.T) {
	t.Parallel()
	var ok bool
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { _, ok = middleware.FlashFrom(r.Context()) })
	rr := httptest.NewRecorder()
	middleware.Flash(flashSecretBytes(), "", false)(next).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.False(t, ok)
	assert.Empty(t, rr.Header().Get("Set-Cookie"))
}
