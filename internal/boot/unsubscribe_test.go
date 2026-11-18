package boot_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func unsubscribeToken(t *testing.T, f *mcpFixture, email string) string {
	t.Helper()
	plain, err := json.Marshal(map[string]string{"o": f.orgID.String(), "p": f.projectID, "e": email})
	require.NoError(t, err)
	ct, err := f.srv.Platform.Sealer.Seal(plain, []byte("alert-optout:v1"))
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(ct)
}

// SECURITY: a mail client's one-click POST carries no browser headers and must pass; a cross-site browser POST is still refused.
func TestUnsubscribe_OneClickPassesCrossOriginProtection(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	_, err := uuid.Parse(f.projectID)
	require.NoError(t, err)
	target := "/alerts/unsubscribe?t=" + url.QueryEscape(unsubscribeToken(t, f, "ops@example.com"))
	form := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}

	rec := serveWithHeaders(t, f, http.MethodGet, target, "", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = serveWithHeaders(t, f, http.MethodPost, target, "List-Unsubscribe=One-Click", form)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	cross := map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "cross-site"}
	rec = serveWithHeaders(t, f, http.MethodPost, target, "List-Unsubscribe=One-Click", cross)
	require.Equal(t, http.StatusForbidden, rec.Code, "the cross-origin chain is unchanged for this route")

	rec = serveWithHeaders(t, f, http.MethodPost, "/alerts/unsubscribe?t=bogus", "List-Unsubscribe=One-Click", form)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
