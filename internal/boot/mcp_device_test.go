package boot_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/apikey"
	"altalune.id/openwa/internal/apperror"
	mcpinternal "altalune.id/openwa/internal/mcp"
	"altalune.id/openwa/internal/mcp/ui"
	"altalune.id/openwa/internal/platform/authn"
	"altalune.id/openwa/internal/platform/tenant"
)

func toolsByName(t *testing.T, body []byte) map[string]map[string]any {
	t.Helper()
	var out struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(body, &out), string(body))
	byName := map[string]map[string]any{}
	for _, tool := range out.Result.Tools {
		byName[tool["name"].(string)] = tool
	}
	return byName
}

func mcpDeviceToolsCarryTheirAnnotationsAndUI(t *testing.T, f *mcpFixture) {
	rec := f.call(t, f.deviceKey, listToolsBody())
	require.Equal(t, 200, rec.Code)
	tools := toolsByName(t, rec.Body.Bytes())

	for _, name := range []string{"device_list", "device_pair"} {
		meta, ok := tools[name]["_meta"].(map[string]any)
		require.True(t, ok, "%s carries no _meta", name)
		require.Equal(t, ui.ResourceURI, meta["ui"].(map[string]any)["resourceUri"], name)
	}
	_, hasMeta := tools["device_get"]["_meta"]
	require.False(t, hasMeta, "device_get is not an app tool")

	logout := tools["device_logout"]["annotations"].(map[string]any)
	require.Equal(t, true, logout["destructiveHint"])
	require.Equal(t, false, logout["readOnlyHint"])
	pair := tools["device_pair"]["annotations"].(map[string]any)
	require.Equal(t, false, pair["destructiveHint"])
}

func mcpDeviceListIsScopeChecked(t *testing.T, f *mcpFixture) {
	rec := f.call(t, f.deviceKey, callToolBody("device_list", map[string]any{}))
	require.Equal(t, 200, rec.Code)
	require.NotContains(t, rec.Body.String(), `"isError":true`, rec.Body.String())

	rec = f.call(t, f.noneKey, callToolBody("device_list", map[string]any{"projectId": f.projectID}))
	require.Equal(t, 200, rec.Code)
	require.Equal(t, apperror.CodeForbidden, toolPayload(t, rec).Code, "a key without devices:read is refused in the result")
}

func mcpDevicePairWithoutADeviceNamesTheWayOut(t *testing.T, f *mcpFixture) {
	rec := f.call(t, f.deviceKey, callToolBody("device_pair", map[string]any{}))
	require.Equal(t, 200, rec.Code)
	require.Equal(t, apperror.CodeValidation, toolPayload(t, rec).Code)
	require.Contains(t, rec.Body.String(), "no device yet")
}

// TestMCP_DeviceListReachIsChecked re-adds on device_list the reach proofs PR #36 ran on blog_list (removed with blog): a project key cannot reach a sibling project, a person in the org can, an org key must name a project, and a selected-projects org key sees only its grant. It drives the real surface on superuser Postgres (RLS bypassed), where the handler's reach check is the only guard.
func TestMCP_DeviceListReachIsChecked(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})

	sibling, err := f.srv.Projects.Create(f.orgCtx, f.orgID, "sibling-project", "Sibling Project")
	require.NoError(t, err)
	_, err = f.srv.Devices.Create(tenant.WithProject(f.orgCtx, sibling.ID), "Sibling Device")
	require.NoError(t, err)

	t.Run("a project key is refused a sibling project", func(t *testing.T) {
		rec := f.call(t, f.deviceKey, callToolBody(mcpinternal.ToolDeviceList, map[string]any{"projectId": sibling.ID.String()}))
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		require.Equal(t, apperror.CodeForbidden, toolPayload(t, rec).Code, "body=%s", rec.Body.String())
	})

	t.Run("a person in the org reaches the sibling project", func(t *testing.T) {
		token := f.issuer.mint(t, mcpAudience, []string{authn.ScopeDevicesRead})
		rec := f.call(t, token, callToolBody(mcpinternal.ToolDeviceList, map[string]any{"projectId": sibling.ID.String()}))
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		require.NotContains(t, rec.Body.String(), `"isError":true`, "body=%s", rec.Body.String())
	})

	_, selected, err := f.srv.APIKeys.MintOrg(f.orgCtx, "org-devices", []string{authn.ScopeDevicesRead},
		apikey.ProjectGrant{ProjectIDs: []uuid.UUID{sibling.ID}}, soon())
	require.NoError(t, err)

	t.Run("an org key without projectId names the remedy", func(t *testing.T) {
		rec := f.call(t, selected, callToolBody(mcpinternal.ToolDeviceList, map[string]any{}))
		require.Equal(t, apperror.CodeProjectUnresolved, toolPayload(t, rec).Code, "body=%s", rec.Body.String())
	})
	t.Run("a selected-projects org key reads only its grant", func(t *testing.T) {
		rec := f.call(t, selected, callToolBody(mcpinternal.ToolDeviceList, map[string]any{"projectId": sibling.ID.String()}))
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		require.NotContains(t, rec.Body.String(), `"isError":true`, "body=%s", rec.Body.String())

		rec = f.call(t, selected, callToolBody(mcpinternal.ToolDeviceList, map[string]any{"projectId": f.projectID}))
		require.Equal(t, apperror.CodeForbidden, toolPayload(t, rec).Code, "the org key cannot read its ungranted home project; body=%s", rec.Body.String())
	})
}

// SECURITY: a device-bound key short of a tool's scope is refused in the tool result, never with an HTTP 403 challenge meant for OAuth tokens.
func TestMCP_DeviceBoundKeyScopeDenialStaysInTheResult(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})

	device, err := f.srv.Devices.Create(f.projCtx, "Bound Device")
	require.NoError(t, err)
	_, boundKey, err := f.srv.APIKeys.Mint(f.projCtx, "mcp-bound-read", []string{authn.ScopeDevicesRead}, []uuid.UUID{device.ID}, soon())
	require.NoError(t, err)

	rec := f.call(t, boundKey, callToolBody("message_send", map[string]any{"to": "628111222333", "text": "x"}))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Empty(t, rec.Header().Get("WWW-Authenticate"))
	require.True(t, scopeDeniedFor(t, rec, authn.ScopeMessagesWrite), rec.Body.String())
}
