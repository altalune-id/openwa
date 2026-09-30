package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/web"
)

func TestStatic_ImmutableCacheOnlyOn200(t *testing.T) {
	t.Parallel()
	h := web.NewServer(web.ServerOpts{})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/static/app.css", nil))
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "public, max-age=31536000, immutable", rr.Header().Get("Cache-Control"))

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/static/does-not-exist.css", nil))
	require.Equal(t, http.StatusNotFound, rr.Code)
	require.Empty(t, rr.Header().Get("Cache-Control"))
}
