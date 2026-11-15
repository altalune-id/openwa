package boot

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// SECURITY: browsers send a lowercased origin with no default port, so the trusted baseURL origin must be normalized the same way.
func TestBuildCrossOrigin_TrustsTheNormalizedBaseURLOrigin(t *testing.T) {
	cop, err := buildCrossOrigin("https://OpenWA.Example:443/console")
	require.NoError(t, err)
	h := cop.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	for origin, want := range map[string]int{
		"https://openwa.example": http.StatusNoContent,
		"https://evil.example":   http.StatusForbidden,
	} {
		req := httptest.NewRequest(http.MethodPost, "http://internal:8080/logout", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, want, rec.Code, origin)
	}
}
