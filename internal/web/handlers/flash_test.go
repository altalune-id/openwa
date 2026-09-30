package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/i18n"
	"altalune.id/openwa/internal/web"
	"altalune.id/openwa/internal/web/middleware"
)

func TestSetFlash_RoundTripsIntoLayoutData(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	rec := httptest.NewRecorder()
	f.Deps.SetFlash(rec, httptest.NewRequest(http.MethodPost, "/orgs", nil), web.FlashOK, "flash.project_created", "Name", "Alpha")
	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, web.FlashCookieName, cookies[0].Name)
	assert.Equal(t, web.FlashMaxAge, cookies[0].MaxAge)
	assert.True(t, cookies[0].HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, cookies[0].SameSite)

	var got web.LayoutData
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = f.Deps.Base(r, "t")
	})
	bundle := i18n.NewEmbeddedBundle(i18n.EnUS)
	req := httptest.NewRequest(http.MethodGet, "/orgs/acme", nil)
	req = req.WithContext(i18n.TranslatorInto(req.Context(), bundle.For(i18n.EnUS)))
	req.AddCookie(cookies[0])
	rr := httptest.NewRecorder()
	middleware.Flash(f.Deps.SecretBytes(), "", false)(next).ServeHTTP(rr, req)

	require.NotNil(t, got.Flash)
	assert.Equal(t, web.FlashOK, got.Flash.Kind)
	assert.Equal(t, "Project Alpha created.", got.Flash.Message)
	assert.Contains(t, rr.Header().Get("Set-Cookie"), "Max-Age=0")
}

func TestSetFlash_OversizeIsDroppedNotWritten(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	rec := httptest.NewRecorder()
	big := make([]byte, 5000)
	for i := range big {
		big[i] = 'x'
	}
	f.Deps.SetFlash(rec, httptest.NewRequest(http.MethodPost, "/", nil), web.FlashOK, "flash.project_created", "Name", string(big))
	assert.Empty(t, rec.Result().Cookies())
}

func TestBase_NoFlashLeavesNil(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	d := f.Deps.Base(httptest.NewRequest(http.MethodGet, "/", nil), "t")
	assert.Nil(t, d.Flash)
}
