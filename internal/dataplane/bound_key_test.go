package dataplane_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/apikey"
	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/platform/authn"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
)

// SECURITY: a bot key minted through the real binding rule with messages:write sends on its own device and gets the masked 404 on a sibling device.
func TestBoundBotKey_SendsOnlyOnItsDevice(t *testing.T) {
	e := newEnv(true)
	store := fakes.NewAPIKey()
	log := discardLogger()
	keys := apikey.NewService(store, apikey.Scheme{}, fakes.PermissiveMembers(), fakes.NewOrgProjects(), log, apperror.NewReporter(log, false).Unexpected)
	resolver := fakes.NewDeviceResolver()
	resolver.Add(e.deviceAPub, e.deviceA)
	keys.Devices = resolver
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: e.orgID, ProjectID: e.projectID, UserID: uuid.New()})

	scopes := []string{authn.ScopeMessagesWrite}
	ids, err := keys.ResourceIDs(ctx, scopes, []string{e.deviceAPub})
	require.NoError(t, err)
	expires := time.Now().Add(time.Hour)
	_, plaintext, err := keys.Mint(ctx, "bot", scopes, ids, &expires)
	require.NoError(t, err)
	e.keyAuthz = apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers())
	h := e.handler()

	post := func(devicePub string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, project+"/devices/"+devicePub+"/messages", strings.NewReader(`{"to":"628111222333","text":"halo"}`))
		req.Header.Set("Authorization", "Bearer "+plaintext)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	own := post(e.deviceAPub)
	require.Equal(t, http.StatusAccepted, own.Code, own.Body.String())
	require.Len(t, e.messages.sends, 1)

	sibling := post(e.deviceBPub)
	require.Equal(t, http.StatusNotFound, sibling.Code, sibling.Body.String())
	require.Len(t, e.messages.sends, 1, "nothing is sent through a sibling device")
}
