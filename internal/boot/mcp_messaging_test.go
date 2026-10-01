package boot_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/mcp/ui"
	"altalune.id/openwa/internal/platform/authn"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/user"
)

func TestMCP_MessagingToolsCarryTheirAnnotationsAndUI(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true, appsUI: true})
	rec := f.call(t, f.msgKey, listToolsBody())
	require.Equal(t, 200, rec.Code)
	tools := toolsByName(t, rec.Body.Bytes())

	for _, name := range []string{"message_send", "chat_list"} {
		meta, ok := tools[name]["_meta"].(map[string]any)
		require.True(t, ok, "%s carries no _meta", name)
		require.Equal(t, ui.ResourceURI, meta["ui"].(map[string]any)["resourceUri"], name)
	}
	for _, name := range []string{"message_list", "contact_list", "group_list", "group_join"} {
		_, hasMeta := tools[name]["_meta"]
		require.False(t, hasMeta, "%s is not an app tool", name)
	}
	for _, name := range []string{"message_send", "group_join"} {
		ann := tools[name]["annotations"].(map[string]any)
		require.Equal(t, false, ann["readOnlyHint"], name)
		require.Equal(t, false, ann["destructiveHint"], name)
	}
	require.Equal(t, true, tools["chat_list"]["annotations"].(map[string]any)["readOnlyHint"])

	schema := tools["message_send"]["inputSchema"].(map[string]any)
	require.Equal(t, []any{"to"}, schema["required"])
	props := schema["properties"].(map[string]any)
	for _, field := range []string{"to", "text", "media", "location", "deviceId", "replyTo", "mentions"} {
		require.Contains(t, props, field)
	}
}

func TestMCP_ChatListFallsBackToTheActiveProject(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	rec := f.call(t, f.msgKey, callToolBody("chat_list", map[string]any{}))
	require.Equal(t, 200, rec.Code)
	require.NotContains(t, rec.Body.String(), `"isError":true`, rec.Body.String())
}

func TestMCP_MessageSendWithoutADeviceNamesTheWayOut(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	rec := f.call(t, f.msgKey, callToolBody("message_send", map[string]any{"to": "628111222333", "text": "halo"}))
	require.Equal(t, 200, rec.Code)
	require.Equal(t, apperror.CodeValidation, toolPayload(t, rec).Code)
	require.Contains(t, rec.Body.String(), "no device yet")
}

func TestMCP_InlineMediaIsCappedAtTheMount(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	media := func(size int) map[string]any {
		data := base64.StdEncoding.EncodeToString(make([]byte, size))
		return map[string]any{"to": "628111", "media": map[string]any{"data": data, "mime": "image/png"}}
	}

	small := f.call(t, f.msgKey, callToolBody("message_send", media(2<<20)))
	require.Contains(t, small.Body.String(), "no device yet", "a body under the cap must reach the tool, or the refusal below proves nothing")

	lifted := f.call(t, f.msgKey, callToolBody("message_send", media(7<<19)))
	require.Contains(t, lifted.Body.String(), "no device yet", "3.5 MiB raw is over the SDK default 4 MiB body cap once base64-encoded; it must pass under rootmcp.MaxRequestBodyBytes")

	big := f.call(t, f.msgKey, callToolBody("message_send", media(5<<20)))
	require.Equal(t, http.StatusRequestEntityTooLarge, big.Code, "a body over rootmcp.MaxRequestBodyBytes never reaches the tool")
	require.NotContains(t, big.Body.String(), "no device yet")
}

func TestMCP_MessageSendRefusesAKeyWithoutMessagesWrite(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	rec := f.call(t, f.readKey, callToolBody("message_send", map[string]any{"to": "628111", "text": "x"}))
	require.Equal(t, 200, rec.Code)
	require.True(t, scopeDeniedFor(t, rec, "messages:write"))
}

// SECURITY: every messaging tool enters through the project scope, whose ReachesWholeProject refuses a device-bound key even when the scope is held.
func TestMCP_MessagingToolsRefuseADeviceBoundKey(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	ctx := t.Context()

	owner, err := f.srv.Users.EnsureFromOIDC(ctx, user.Claims{Issuer: f.issuer.url, Subject: tokenSubject, Email: tokenEmail, Name: "MCP Agent"})
	require.NoError(t, err)
	o, err := f.srv.Orgs.BySlug(ctx, "mcp-org")
	require.NoError(t, err)
	p, err := f.srv.Projects.BySlug(tenant.Into(ctx, tenant.Context{OrgID: o.ID, UserID: owner.ID}), o.ID, "mcp-project")
	require.NoError(t, err)
	projCtx := tenant.WithProject(tenant.Into(ctx, tenant.Context{OrgID: o.ID, UserID: owner.ID}), p.ID)
	device, err := f.srv.Devices.Create(projCtx, "Bound Device")
	require.NoError(t, err)
	_, boundKey, err := f.srv.APIKeys.Mint(projCtx, "mcp-bound", []string{
		authn.ScopeMessagesRead, authn.ScopeMessagesWrite, authn.ScopeChatsRead,
	}, []uuid.UUID{device.ID}, soon())
	require.NoError(t, err)

	t.Run("the project-wide key reaches the tool", func(t *testing.T) {
		rec := f.call(t, f.msgKey, callToolBody("chat_list", map[string]any{}))
		require.NotContains(t, rec.Body.String(), `"isError":true`, rec.Body.String())
	})

	calls := map[string]map[string]any{
		"message_send": {"to": "628111222333", "text": "x"},
		"message_list": {},
		"chat_list":    {},
	}
	for tool, args := range calls {
		t.Run(tool, func(t *testing.T) {
			rec := f.call(t, boundKey, callToolBody(tool, args))
			require.Equal(t, 200, rec.Code)
			require.False(t, scopeDeniedFor(t, rec, "messages:write"), "the bound key holds the scope; the refusal must come from reach: %s", rec.Body.String())
			require.Equal(t, apperror.CodeForbidden, toolPayload(t, rec).Code, rec.Body.String())
		})
	}
}

func TestMCP_ContactListReturnsAnEmptyPage(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	rec := f.call(t, f.msgKey, callToolBody("contact_list", map[string]any{}))
	require.Equal(t, 200, rec.Code)
	var out struct {
		Result struct {
			IsError           bool           `json:"isError"`
			StructuredContent map[string]any `json:"structuredContent"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	require.False(t, out.Result.IsError, rec.Body.String())
}
