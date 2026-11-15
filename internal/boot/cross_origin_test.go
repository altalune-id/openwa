package boot_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func serveWithHeaders(t *testing.T, f *mcpFixture, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.srv.Web.ServeHTTP(rec, req)
	return rec
}

// SECURITY: a cross-origin form post to the console is refused; same-origin and the configured public origin pass.
func TestConsoleRefusesCrossOriginPosts(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	form := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	with := func(k, v string) map[string]string {
		h := map[string]string{k: v}
		for fk, fv := range form {
			h[fk] = fv
		}
		return h
	}

	for name, h := range map[string]map[string]string{
		"cross-site fetch metadata": with("Sec-Fetch-Site", "cross-site"),
		"sibling subdomain":         with("Sec-Fetch-Site", "same-site"),
		"foreign origin header":     with("Origin", "https://evil.example"),
	} {
		t.Run(name, func(t *testing.T) {
			rec := serveWithHeaders(t, f, http.MethodPost, "/logout", "", h)
			require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		})
	}

	for name, h := range map[string]map[string]string{
		"same-origin fetch metadata":   with("Sec-Fetch-Site", "same-origin"),
		"no browser headers":           form,
		"configured public origin":     with("Origin", "http://127.0.0.1"),
		"user-initiated top-level nav": with("Sec-Fetch-Site", "none"),
	} {
		t.Run(name, func(t *testing.T) {
			rec := serveWithHeaders(t, f, http.MethodPost, "/logout", "", h)
			require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
		})
	}
}

// TestCrossOriginCheckLeavesBearerSurfacesAlone pins that /api and /mcp answer a cross-site bearer call as if the header were absent.
func TestCrossOriginCheckLeavesBearerSurfacesAlone(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	for _, tc := range []struct {
		name, path, body string
		headers          map[string]string
	}{
		{"control plane", deviceListProcedure, `{}`, map[string]string{"Content-Type": "application/json", "Connect-Protocol-Version": "1"}},
		{"mcp", mcpPath, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, map[string]string{"Content-Type": "application/json", "Accept": mcpProtocolHeader}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.headers["Authorization"] = "Bearer " + f.deviceKey
			want := serveWithHeaders(t, f, http.MethodPost, tc.path, tc.body, tc.headers)
			require.Equal(t, http.StatusOK, want.Code, "the bearer call must succeed, or this guard proves nothing: %s", want.Body.String())

			tc.headers["Sec-Fetch-Site"] = "cross-site"
			tc.headers["Origin"] = "https://evil.example"
			got := serveWithHeaders(t, f, http.MethodPost, tc.path, tc.body, tc.headers)
			require.Equal(t, want.Code, got.Code, got.Body.String())
		})
	}
}
