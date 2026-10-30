package boot_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/apperror"
	mcpinternal "altalune.id/openwa/internal/mcp"
	"altalune.id/openwa/internal/mcp/ui"
	"altalune.id/openwa/internal/platform/authn"
)

func mcpMessagingToolsCarryTheirAnnotationsAndUI(t *testing.T, f *mcpFixture) {
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

func mcpChatListFallsBackToTheActiveProject(t *testing.T, f *mcpFixture) {
	rec := f.call(t, f.msgKey, callToolBody("chat_list", map[string]any{}))
	require.Equal(t, 200, rec.Code)
	require.NotContains(t, rec.Body.String(), `"isError":true`, rec.Body.String())
}

func mcpMessageSendWithoutADeviceNamesTheWayOut(t *testing.T, f *mcpFixture) {
	rec := f.call(t, f.msgKey, callToolBody("message_send", map[string]any{"to": "628111222333", "text": "halo"}))
	require.Equal(t, 200, rec.Code)
	require.Equal(t, apperror.CodeValidation, toolPayload(t, rec).Code)
	require.Contains(t, rec.Body.String(), "no device yet")
}

func mcpInlineMediaIsCappedAtTheMount(t *testing.T, f *mcpFixture) {
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

func mcpMessageSendRefusesAKeyWithoutMessagesWrite(t *testing.T, f *mcpFixture) {
	rec := f.call(t, f.readKey, callToolBody("message_send", map[string]any{"to": "628111", "text": "x"}))
	require.Equal(t, 200, rec.Code)
	require.True(t, scopeDeniedFor(t, rec, "messages:write"))
}

// SECURITY: every messaging tool enters through the project scope, whose ReachesWholeProject refuses a device-bound key even when the scope is held.
func TestMCP_MessagingToolsRefuseADeviceBoundKey(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})

	device, err := f.srv.Devices.Create(f.projCtx, "Bound Device")
	require.NoError(t, err)
	_, boundKey, err := f.srv.APIKeys.Mint(f.projCtx, "mcp-bound", []string{
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

func mcpContactListReturnsAnEmptyPage(t *testing.T, f *mcpFixture) {
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

// SECURITY: the step-up runs inside the MCP body cap: a token short of messages:write gets the scope 403 on a large call under the cap, and a call over the cap is refused 413.
func mcpTokenStepUpRunsInsideTheBodyCap(t *testing.T, f *mcpFixture) {
	token := f.issuer.mint(t, mcpAudience, []string{authn.ScopeProjectsRead})

	under := f.call(t, token, callToolBody(mcpinternal.ToolMessageSend, map[string]any{"to": "628111222333", "text": strings.Repeat("x", 5<<20)}))
	require.Equal(t, http.StatusForbidden, under.Code, "a 5 MiB call short of its scope")
	require.Contains(t, under.Header().Get("WWW-Authenticate"), `error="insufficient_scope", scope="`+authn.ScopeMessagesWrite+`"`)

	over := f.call(t, token, callToolBody(mcpinternal.ToolMessageSend, map[string]any{"to": "628111222333", "text": strings.Repeat("x", 7<<20)}))
	require.Equal(t, http.StatusRequestEntityTooLarge, over.Code, "a call over the 6 MiB cap")
}
