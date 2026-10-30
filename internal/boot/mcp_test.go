package boot_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/altalune-id/oidcrp/oidcrptest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/apikey"
	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/boot"
	mcpinternal "altalune.id/openwa/internal/mcp"
	"altalune.id/openwa/internal/onboard"
	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/platform/authn"
	"altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/platform/tokens"
	"altalune.id/openwa/internal/user"
	rootmcp "altalune.id/openwa/mcp"
	"altalune.id/openwa/reqid"
)

const (
	mcpPath            = "/mcp"
	mcpAudience        = "http://127.0.0.1/mcp"
	controlAudience    = "http://127.0.0.1/api"
	tokenSubject       = "mcp-agent-subject"
	tokenEmail         = "mcp-agent@example.com"
	mcpMetadataPath    = "/.well-known/oauth-protected-resource/mcp"
	apiMetadataPath    = "/.well-known/oauth-protected-resource/api"
	introspectorID     = "rs-public-id"
	introspectorSecret = "rs-secret"
	mcpProtocolHeader  = "application/json, text/event-stream"

	todoCreateProcedure = "/api/todo.v1.TodoService/Create"
	todoListProcedure   = "/api/todo.v1.TodoService/List"
)

type tokenIssuer struct {
	idp *oidcrptest.IDP
	url string
}

func newTokenIssuer(t *testing.T) *tokenIssuer {
	t.Helper()
	idp := oidcrptest.New(t)
	idp.User.Subject = tokenSubject
	idp.Introspector(introspectorID, introspectorSecret)
	return &tokenIssuer{idp: idp, url: idp.Issuer()}
}

func (i *tokenIssuer) mint(t *testing.T, audience string, scopes []string) string {
	t.Helper()
	return i.idp.AccessToken(map[string]any{"aud": audience, "scope": strings.Join(scopes, " ")})
}

type mcpOpts struct {
	enabled bool
	appsUI  bool
}

type mcpFixture struct {
	srv       *boot.Server
	issuer    *tokenIssuer
	log       *captureLog
	ownerID   uuid.UUID
	orgID     uuid.UUID
	orgCtx    context.Context
	projCtx   context.Context
	projectID string
	readKey   string
	noneKey   string
	deviceKey string
	msgKey    string
}

func newMCPFixture(t *testing.T, opts mcpOpts) *mcpFixture {
	t.Helper()

	issuer := newTokenIssuer(t)
	cfg := newSmokeCfg(t)
	cfg.Mode = config.ModeCloud
	cfg.OIDC = config.OIDCConfig{Issuer: issuer.url, ClientID: "mcp-client", ClientSecret: "mcp-secret"}
	cfg.HTTP.CSP.Enabled = true
	cfg.API.Enabled = true
	cfg.API.KeyPrefix = apikey.DefaultPrefix
	introspection := tokens.IntrospectionConfig{ClientID: introspectorID, ClientSecret: introspectorSecret}
	cfg.Tokens = tokens.Config{Issuer: issuer.url, Audience: controlAudience, Introspection: introspection}
	cfg.MCP = config.MCPConfig{Enabled: opts.enabled, AppsUI: opts.appsUI, Audience: mcpAudience, Introspection: introspection}

	capture := &captureLog{}
	srv, err := boot.BootServer(context.Background(), cfg, boot.WithLogger(slog.New(capture)), boot.WithOIDCHTTPClient(issuer.idp.Client()),
		boot.WithScheduler(false), boot.WithConsumer(false))
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })
	seedUser, err := srv.Users.Create(context.Background(), user.CreateRequest{
		Email: "mcp-seed@example.com", Name: "MCP Seed", Source: user.SourceOIDC,
	})
	require.NoError(t, err)
	_, err = srv.Onboards.Complete(context.Background(), seedUser.ID, onboard.MethodCLIInit)
	require.NoError(t, err)
	srv.CompleteOnboarding(context.Background())
	requireOnboarded(t, srv)

	ctx := context.Background()
	owner, err := srv.Users.EnsureFromOIDC(ctx, user.Claims{
		Issuer: issuer.url, Subject: tokenSubject, Email: tokenEmail, EmailVerified: true, Name: "MCP Agent",
	})
	require.NoError(t, err)
	o, err := srv.Orgs.Create(ctx, org.CreateRequest{Slug: "mcp-org", Name: "MCP Org", OwnerID: owner.ID})
	require.NoError(t, err)

	orgCtx := tenant.Into(ctx, tenant.Context{OrgID: o.ID, UserID: owner.ID})
	p, err := srv.Projects.Create(orgCtx, o.ID, "mcp-project", "MCP Project")
	require.NoError(t, err)

	projCtx := tenant.WithProject(orgCtx, p.ID)

	_, readKey, err := srv.APIKeys.Mint(projCtx, "mcp-reader", []string{authn.ScopeProjectsRead}, nil, soon())
	require.NoError(t, err)
	_, deviceKey, err := srv.APIKeys.Mint(projCtx, "mcp-devices", []string{authn.ScopeDevicesRead, authn.ScopeDevicesWrite, authn.ScopeProjectsRead}, nil, soon())
	require.NoError(t, err)
	_, msgKey, err := srv.APIKeys.Mint(projCtx, "mcp-messaging", []string{
		authn.ScopeMessagesRead, authn.ScopeMessagesWrite, authn.ScopeChatsRead, authn.ScopeChatsWrite,
		authn.ScopeContactsRead, authn.ScopeProjectsRead,
	}, nil, soon())
	require.NoError(t, err)
	_, noneKey, err := srv.APIKeys.Mint(projCtx, "mcp-keys-only", []string{authn.ScopeAPIKeysRead}, nil, soon())
	require.NoError(t, err)

	return &mcpFixture{
		srv:       srv,
		issuer:    issuer,
		log:       capture,
		ownerID:   owner.ID,
		orgID:     o.ID,
		orgCtx:    orgCtx,
		projCtx:   projCtx,
		projectID: p.ID.String(),
		readKey:   readKey,
		noneKey:   noneKey,
		deviceKey: deviceKey,
		msgKey:    msgKey,
	}
}

func (f *mcpFixture) call(t *testing.T, credential string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return f.post(t, mcpPath, credential, body)
}

func (f *mcpFixture) post(t *testing.T, path, credential string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", mcpProtocolHeader)
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	rec := httptest.NewRecorder()
	f.srv.Web.ServeHTTP(rec, req)
	return rec
}

func (f *mcpFixture) get(t *testing.T, path, credential, requestID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	if requestID != "" {
		req.Header.Set(reqid.Header, requestID)
	}
	rec := httptest.NewRecorder()
	f.srv.Web.ServeHTTP(rec, req)
	return rec
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeRPC(t *testing.T, rec *httptest.ResponseRecorder) rpcResponse {
	t.Helper()
	var out rpcResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "body=%s", rec.Body.String())
	return out
}

func scopeDeniedFor(t *testing.T, rec *httptest.ResponseRecorder, scope string) bool {
	t.Helper()
	resp := decodeRPC(t, rec)
	require.Nil(t, resp.Error, "a scope denial must answer in the result with isError, not the JSON-RPC envelope")

	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(resp.Result, &result), "result=%s", string(resp.Result))
	if !result.IsError || len(result.Content) == 0 {
		return false
	}
	var payload rootmcp.ErrorPayload
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].Text), &payload), "content=%s", result.Content[0].Text)
	return payload.Code == apperror.CodeForbidden && payload.Meta["scope"] == scope
}

func listToolsBody() map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}
}

func callToolBody(name string, args map[string]any) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	}
}

func mcpRejectsAControlPlaneAudienceToken(t *testing.T, f *mcpFixture) {
	controlToken := f.issuer.mint(t, controlAudience, []string{authn.ScopeProjectsRead})
	mcpToken := f.issuer.mint(t, mcpAudience, []string{authn.ScopeProjectsRead})

	t.Run("the mcp-audience token is accepted at /mcp", func(t *testing.T) {
		rec := f.call(t, mcpToken, listToolsBody())
		require.Equal(t, http.StatusOK, rec.Code,
			"an mcp-audience token must authenticate at /mcp, or the rejection below proves nothing; body=%s", rec.Body.String())
	})

	t.Run("the control-plane token is rejected at /mcp", func(t *testing.T) {
		rec := f.call(t, controlToken, listToolsBody())
		require.Equal(t, http.StatusUnauthorized, rec.Code,
			"RFC 8707: a token minted for tokens.audience was accepted at /mcp — the mcp mount is not on its own verifier; body=%s", rec.Body.String())
		require.Contains(t, rec.Header().Get("WWW-Authenticate"), "resource_metadata=",
			"a 401 from /mcp must point the host at its RFC 9728 metadata document")
	})

	t.Run("the control-plane token is accepted at /api", func(t *testing.T) {
		require.False(t, credentialRejected(f.connect(t, controlToken)),
			"the control-plane token must authenticate at /api, or the split above is not an audience split")
	})

	t.Run("the mcp token is rejected at /api", func(t *testing.T) {
		require.True(t, credentialRejected(f.connect(t, mcpToken)),
			"RFC 8707: an mcp-audience token was accepted at the control plane")
	})
}

func credentialRejected(rec *httptest.ResponseRecorder) bool {
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		return false
	}
	return rec.Code == http.StatusUnauthorized && body.Message == "unauthorized"
}

func (f *mcpFixture) connect(t *testing.T, credential string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/todo.v1.TodoService/List",
		strings.NewReader(`{"projectId":"`+f.projectID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Authorization", "Bearer "+credential)
	rec := httptest.NewRecorder()
	f.srv.Web.ServeHTTP(rec, req)
	return rec
}

// mcpTokenWithoutTheToolScopeGetsAStepUpChallenge is the R4 S7 rule for OAuth tokens: every tool is scope-checked, and a missing scope is an RFC 6750 403 before dispatch, so the host can ask for it.
func mcpTokenWithoutTheToolScopeGetsAStepUpChallenge(t *testing.T, f *mcpFixture) {
	tests := []struct {
		name       string
		scopes     []string
		wantDenied bool
	}{
		{"no scopes at all", nil, true},
		{"an unrelated scope", []string{authn.ScopeAPIKeysRead}, true},
		{"the tool's own scope", []string{authn.ScopeProjectsRead}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := f.issuer.mint(t, mcpAudience, tt.scopes)
			rec := f.call(t, token, callToolBody(mcpinternal.ToolProjectList, map[string]any{}))
			if tt.wantDenied {
				require.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())
				challenge := rec.Header().Get("WWW-Authenticate")
				require.Contains(t, challenge, `error="insufficient_scope"`)
				require.Contains(t, challenge, `scope="`+authn.ScopeProjectsRead+`"`)
				require.Contains(t, challenge, `resource_metadata="http://127.0.0.1`+mcpMetadataPath+`"`)
				return
			}
			require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
			require.False(t, scopeDeniedFor(t, rec, authn.ScopeProjectsRead),
				"a token holding the tool's scope was denied it; body=%s", rec.Body.String())
		})
	}
}

// mcpIDTokenIsNeverAnAccessToken pins RFC 9068's typ check on both resources.
func mcpIDTokenIsNeverAnAccessToken(t *testing.T, f *mcpFixture) {
	now := time.Now()
	for _, aud := range []string{mcpAudience, controlAudience} {
		idToken := f.issuer.idp.IDToken(map[string]any{
			"iss": f.issuer.url, "sub": tokenSubject, "aud": aud, "client_id": "test-client", "jti": "j1",
			"scope": authn.ScopePostsRead, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		})
		require.Equal(t, http.StatusUnauthorized, f.call(t, idToken, listToolsBody()).Code, "an ID token opened /mcp")
		require.True(t, credentialRejected(f.connect(t, idToken)), "an ID token opened /api")
	}
}

func mcpPersonalTokenIsRevokedAtOnce(t *testing.T, f *mcpFixture) {
	const pt = tokens.PersonalTokenPrefix + "boot-test"
	f.issuer.idp.Opaque(pt, map[string]any{
		"aud": controlAudience, "sub": tokenSubject, "scope": authn.ScopePostsRead, "exp": time.Now().Add(time.Hour).Unix(),
	})

	require.Equal(t, http.StatusOK, f.connect(t, pt).Code, "a live personal token must reach the control plane")
	f.issuer.idp.Revoke(pt)
	require.True(t, credentialRejected(f.connect(t, pt)), "a revoked personal token still opened the control plane")
}

func mcpAPIKeyIsNeverIntrospected(t *testing.T, f *mcpFixture) {
	before := f.issuer.idp.Introspections()
	f.connect(t, f.readKey)
	f.connect(t, f.readKey+"x")
	f.call(t, f.readKey+"x", listToolsBody())
	require.Equal(t, before, f.issuer.idp.Introspections(), "an API key reached the issuer's introspection endpoint")
}

func mcpControlPlaneEnforcesScopesOnTokens(t *testing.T, f *mcpFixture) {
	read := f.issuer.mint(t, controlAudience, []string{authn.ScopePostsRead})

	require.Equal(t, http.StatusOK, f.connectRPC(t, read, todoListProcedure, map[string]any{"projectId": f.projectID}).Code)
	denied := f.connectRPC(t, read, todoCreateProcedure, map[string]any{"projectId": f.projectID, "title": "x"})
	require.Equal(t, http.StatusForbidden, denied.Code, "a token without posts:write created a todo; body=%s", denied.Body.String())
	require.Equal(t,
		`Bearer resource_metadata="http://127.0.0.1`+apiMetadataPath+`", error="insufficient_scope", scope="`+authn.ScopePostsWrite+`"`,
		denied.Header().Get("WWW-Authenticate"), "a scope denial on Connect must name the scope to ask for")
	require.Contains(t, denied.Body.String(), apperror.CodeForbidden, "the body keeps the canonical envelope")

	unauth := f.connectRPC(t, "not.a.token", todoListProcedure, map[string]any{"projectId": f.projectID})
	require.Equal(t, http.StatusUnauthorized, unauth.Code)
	require.Equal(t, `Bearer resource_metadata="http://127.0.0.1`+apiMetadataPath+`", error="invalid_token"`, unauth.Header().Get("WWW-Authenticate"),
		"a 401 for a presented token on Connect must say invalid_token and point at the API's RFC 9728 document")
}

// mcpEachResourceServesItsOwnMetadata pins the two RFC 9728 documents Othra verifies before it mints tokens.
func mcpEachResourceServesItsOwnMetadata(t *testing.T, f *mcpFixture) {
	for path, resource := range map[string]string{mcpMetadataPath: mcpAudience, apiMetadataPath: controlAudience} {
		rec := f.get(t, path, "", "")
		require.Equal(t, http.StatusOK, rec.Code, "%s: body=%s", path, rec.Body.String())
		var doc struct {
			Resource             string   `json:"resource"`
			AuthorizationServers []string `json:"authorization_servers"`
			ScopesSupported      []string `json:"scopes_supported"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
		require.Equal(t, resource, doc.Resource)
		require.Equal(t, []string{f.issuer.url}, doc.AuthorizationServers)
		require.Contains(t, doc.ScopesSupported, authn.ScopeProjectsRead)
		for _, scope := range doc.ScopesSupported {
			require.True(t, authn.Mintable(scope), "%s advertises %q, which no client may register", path, scope)
		}
	}
}

func (f *mcpFixture) connectRPC(t *testing.T, credential, procedure string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, procedure, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Authorization", "Bearer "+credential)
	rec := httptest.NewRecorder()
	f.srv.Web.ServeHTTP(rec, req)
	return rec
}

func mcpKeyScopeIsCheckedPerTool(t *testing.T, f *mcpFixture) {
	t.Run("a key holding projects:read lists projects", func(t *testing.T) {
		rec := f.call(t, f.readKey, callToolBody(mcpinternal.ToolProjectList, map[string]any{}))
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		require.False(t, scopeDeniedFor(t, rec, authn.ScopeProjectsRead), "body=%s", rec.Body.String())
		require.Contains(t, rec.Body.String(), `\"slug\":\"mcp-project\"`,
			"the tool must answer from the same project service /api serves; body=%s", rec.Body.String())
	})

	t.Run("a key without projects:read is denied", func(t *testing.T) {
		rec := f.call(t, f.noneKey, callToolBody(mcpinternal.ToolProjectList, map[string]any{}))
		require.True(t, scopeDeniedFor(t, rec, authn.ScopeProjectsRead),
			"a key lacking the tool's scope called it; body=%s", rec.Body.String())
	})
}

func mcpCallWithoutArgumentsDecodes(t *testing.T, f *mcpFixture) {
	token := f.issuer.mint(t, mcpAudience, []string{authn.ScopeProjectsRead})
	rec := f.call(t, token, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": mcpinternal.ToolProjectList},
	})
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.NotEmpty(t, projectsFromTool(t, rec),
		"the call did not reach the service, so protojson was handed a nil body; body=%s", rec.Body.String())
}

func toolPayload(t *testing.T, rec *httptest.ResponseRecorder) rootmcp.ErrorPayload {
	t.Helper()
	resp := decodeRPC(t, rec)
	require.Nil(t, resp.Error, "a tool failure must answer in the result, not the JSON-RPC envelope")

	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(resp.Result, &result), "result=%s", string(resp.Result))
	require.True(t, result.IsError, "the tool call succeeded; result=%s", string(resp.Result))
	require.NotEmpty(t, result.Content, "the tool error carried no content; result=%s", string(resp.Result))

	var payload rootmcp.ErrorPayload
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].Text), &payload), "content=%s", result.Content[0].Text)
	return payload
}

// mcpUnauthenticatedCallIsChallenged keeps the surface fail-closed: no credential, an unparsable one and a credential of the wrong shape all answer one masked 401 carrying the RFC 9728 challenge.
func mcpUnauthenticatedCallIsChallenged(t *testing.T, f *mcpFixture) {
	for _, credential := range []string{"", "key_not_a_real_key", "not.a.token", "opaque-credential"} {
		t.Run("credential="+credential, func(t *testing.T) {
			rec := f.call(t, credential, listToolsBody())
			require.Equal(t, http.StatusUnauthorized, rec.Code,
				"/mcp admitted an unauthenticated call; body=%s", rec.Body.String())
			require.Contains(t, rec.Header().Get("WWW-Authenticate"), mcpMetadataPath,
				"the 401 must name the protected-resource metadata document")
			require.NotContains(t, rec.Body.String(), "tools",
				"an unauthenticated caller must not learn the tool catalog; body=%s", rec.Body.String())
		})
	}
}

func mcpRunsTheMCPChainNotTheConsoleChain(t *testing.T, f *mcpFixture) {
	require.NotEmpty(t, f.get(t, "/login", "", "").Header().Get("Content-Security-Policy"),
		"the console chain must set CSP, or this guard proves nothing")

	tests := []struct {
		name       string
		credential string
	}{
		{"unauthenticated", ""},
		{"authenticated", f.readKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const id = "mcp-chain-probe-0123456789"
			rec := f.call(t, tt.credential, listToolsBody())
			require.Empty(t, rec.Header().Get("Content-Security-Policy"),
				"/mcp ran the console chain: CSP is an SSR concern")
			require.Empty(t, rec.Header().Values("Set-Cookie"),
				"/mcp ran the console chain: a machine surface issues no session cookie")

			echoed := f.get(t, mcpPath, tt.credential, id)
			require.Equal(t, id, echoed.Header().Get(reqid.Header),
				"/mcp lost webmw.RequestID: it answered without echoing %s", reqid.Header)
			require.True(t, f.log.logged(mcpPath, id),
				"/mcp lost webmw.RequestLog: no http.request line carrying request id %q", id)

			minted := f.get(t, mcpPath, tt.credential, "").Header().Get(reqid.Header)
			require.NotEmpty(t, minted, "/mcp lost webmw.RequestID: it minted no id of its own")
			require.True(t, f.log.logged(mcpPath, minted),
				"/mcp lost webmw.RequestLog: no http.request line carrying minted id %q", minted)
		})
	}
}

// TestMCP_DisabledLeavesThePathUnmounted keeps mcp.enabled meaningful.
func TestMCP_DisabledLeavesThePathUnmounted(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: false})

	for _, path := range []string{mcpPath, mcpPath + "/", mcpMetadataPath} {
		t.Run(path, func(t *testing.T) {
			rec := f.post(t, path, f.readKey, listToolsBody())
			require.NotEqual(t, http.StatusOK, rec.Code,
				"%s answered while mcp.enabled is false; body=%s", path, rec.Body.String())
			require.NotContains(t, rec.Body.String(), "project_list",
				"a disabled MCP surface published its tool catalog; body=%s", rec.Body.String())
		})
	}

	require.Nil(t, f.srv.MCP, "no MCP server may be built while mcp.enabled is false")
}

func mcpToolsCarryAScopeAndTheSharedInstances(t *testing.T, f *mcpFixture) {
	require.NotNil(t, f.srv.MCP, "mcp.enabled is true, so boot must have built a server")

	registry := f.srv.MCP.Registry()
	names := registry.Names()
	require.NotEmpty(t, names, "boot registered no MCP tools")

	instances := map[string]any{
		"project_list":  f.srv.API.ProjectSvc,
		"member_list":   f.srv.API.MemberSvc,
		"device_list":   f.srv.API.DeviceSvc,
		"device_get":    f.srv.API.DeviceSvc,
		"device_pair":   f.srv.API.DeviceSvc,
		"device_logout": f.srv.API.DeviceSvc,
		"message_send":  f.srv.API.MessageSvc,
		"message_list":  f.srv.API.MessageSvc,
		"chat_list":     f.srv.API.ChatSvc,
		"group_list":    f.srv.API.ChatSvc,
		"group_join":    f.srv.API.ChatSvc,
		"contact_list":  f.srv.API.ContactSvc,
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			spec, ok := registry.Spec(name)
			require.True(t, ok)
			require.NotEmpty(t, spec.Scope,
				"%s declares no scope; the root mcp package denies such a tool, and nothing here may default one", name)
			require.True(t, authn.Valid(spec.Scope),
				"%s declares %q, which is not in the scope catalog, so no key can ever hold it", name, spec.Scope)
			require.NotEmpty(t, spec.Description, "%s publishes no description for a host to read", name)

			want, known := instances[name]
			require.True(t, known, "%s is registered but this guard does not know which instance it must share", name)
			require.Same(t, want, registry.HandlerFor(name),
				"%s calls a service instance the Connect mount does not serve", name)
		})
	}
}

// mcpToolsShareTheConnectHandlerInstance is the identity guard the surface rests on: each tool must close over the very *controlplane service the Connect mount serves. SECURITY: a second, independently constructed service — or a tool body calling straight into a domain package past Connect — would still satisfy the behavioral test below while duplicating a verb, and a duplicated verb is a verb whose tenant scoping and authorization can diverge.
func mcpToolsShareTheConnectHandlerInstance(t *testing.T, f *mcpFixture) {
	require.NotNil(t, f.srv.MCP, "mcp.enabled is true, so boot must have built a server")
	require.NotNil(t, f.srv.API, "the control plane must be mounted, or there is no instance to share")

	registry := f.srv.MCP.Registry()
	tests := []struct {
		tool string
		want any
	}{
		{mcpinternal.ToolProjectList, f.srv.API.ProjectSvc},
		{mcpinternal.ToolMemberList, f.srv.API.MemberSvc},
		{mcpinternal.ToolDeviceList, f.srv.API.DeviceSvc},
		{mcpinternal.ToolDeviceGet, f.srv.API.DeviceSvc},
		{mcpinternal.ToolDevicePair, f.srv.API.DeviceSvc},
		{mcpinternal.ToolDeviceLogout, f.srv.API.DeviceSvc},
		{mcpinternal.ToolMessageSend, f.srv.API.MessageSvc},
		{mcpinternal.ToolMessageList, f.srv.API.MessageSvc},
		{mcpinternal.ToolChatList, f.srv.API.ChatSvc},
		{mcpinternal.ToolGroupList, f.srv.API.ChatSvc},
		{mcpinternal.ToolGroupJoin, f.srv.API.ChatSvc},
		{mcpinternal.ToolContactList, f.srv.API.ContactSvc},
	}

	covered := make([]string, 0, len(tests))
	for _, tt := range tests {
		covered = append(covered, tt.tool)
	}
	require.ElementsMatch(t, covered, registry.Names(),
		"boot registers a tool this guard names no shared instance for; every tool must be covered")

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			require.NotNil(t, tt.want,
				"the Connect mount holds no service for %s; an identity assertion between two nils proves nothing", tt.tool)
			got := registry.HandlerFor(tt.tool)
			require.NotNil(t, got, "%s is registered without the instance it calls", tt.tool)
			require.Same(t, tt.want, got,
				"%s calls a service instance the Connect mount does not serve, so the verb has two implementations", tt.tool)
			require.Equal(t, reflect.ValueOf(tt.want).Pointer(), reflect.ValueOf(got).Pointer(),
				"%s's handler and the Connect mount's handler are not the same address", tt.tool)
		})
	}
}

func mcpPublishesMemberAndProjectTools(t *testing.T, f *mcpFixture) {
	rec := f.call(t, f.readKey, listToolsBody())
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var out struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "body=%s", rec.Body.String())
	names := make([]string, 0, len(out.Result.Tools))
	for _, tool := range out.Result.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	require.Equal(t, []string{"chat_list", "contact_list", "device_get", "device_list", "device_logout", "device_pair", "group_join", "group_list", "member_list", "message_list", "message_send", "project_list"}, names)
}

// TestMCP_SharedFixture runs the MCP checks that change no state another check reads, on one booted fixture.
func TestMCP_SharedFixture(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	for _, c := range []struct {
		name  string
		check func(*testing.T, *mcpFixture)
	}{
		{"UndecodableArgumentsAnswerAValidationErrorNotAnIncident", mcpUndecodableArgumentsAnswerAValidationErrorNotAnIncident},
		{"DeviceListIsScopeChecked", mcpDeviceListIsScopeChecked},
		{"DevicePairWithoutADeviceNamesTheWayOut", mcpDevicePairWithoutADeviceNamesTheWayOut},
		{"ChatListFallsBackToTheActiveProject", mcpChatListFallsBackToTheActiveProject},
		{"MessageSendWithoutADeviceNamesTheWayOut", mcpMessageSendWithoutADeviceNamesTheWayOut},
		{"InlineMediaIsCappedAtTheMount", mcpInlineMediaIsCappedAtTheMount},
		{"MessageSendRefusesAKeyWithoutMessagesWrite", mcpMessageSendRefusesAKeyWithoutMessagesWrite},
		{"ContactListReturnsAnEmptyPage", mcpContactListReturnsAnEmptyPage},
		{"TokenStepUpRunsInsideTheBodyCap", mcpTokenStepUpRunsInsideTheBodyCap},
		{"RejectsAControlPlaneAudienceToken", mcpRejectsAControlPlaneAudienceToken},
		{"TokenWithoutTheToolScopeGetsAStepUpChallenge", mcpTokenWithoutTheToolScopeGetsAStepUpChallenge},
		{"IDTokenIsNeverAnAccessToken", mcpIDTokenIsNeverAnAccessToken},
		{"PersonalTokenIsRevokedAtOnce", mcpPersonalTokenIsRevokedAtOnce},
		{"APIKeyIsNeverIntrospected", mcpAPIKeyIsNeverIntrospected},
		{"ControlPlaneEnforcesScopesOnTokens", mcpControlPlaneEnforcesScopesOnTokens},
		{"EachResourceServesItsOwnMetadata", mcpEachResourceServesItsOwnMetadata},
		{"KeyScopeIsCheckedPerTool", mcpKeyScopeIsCheckedPerTool},
		{"CallWithoutArgumentsDecodes", mcpCallWithoutArgumentsDecodes},
		{"UnauthenticatedCallIsChallenged", mcpUnauthenticatedCallIsChallenged},
		{"RunsTheMCPChainNotTheConsoleChain", mcpRunsTheMCPChainNotTheConsoleChain},
		{"ToolsCarryAScopeAndTheSharedInstances", mcpToolsCarryAScopeAndTheSharedInstances},
		{"ToolsShareTheConnectHandlerInstance", mcpToolsShareTheConnectHandlerInstance},
		{"PublishesMemberAndProjectTools", mcpPublishesMemberAndProjectTools},
		{"OneErrorVocabularyAcrossTheSurface", mcpOneErrorVocabularyAcrossTheSurface},
		{"AppsUIOffPublishesNoResource", mcpAppsUIOffPublishesNoResource},
	} {
		t.Run(c.name, func(t *testing.T) { c.check(t, f) })
	}
}

// TestMCP_SharedAppsUIFixture runs the MCP Apps checks that change no shared state, on one booted fixture with the UI on.
func TestMCP_SharedAppsUIFixture(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true, appsUI: true})
	for _, c := range []struct {
		name  string
		check func(*testing.T, *mcpFixture)
	}{
		{"DeviceToolsCarryTheirAnnotationsAndUI", mcpDeviceToolsCarryTheirAnnotationsAndUI},
		{"MessagingToolDescriptorsAreFrozen", mcpMessagingToolDescriptorsAreFrozen},
		{"MessagingToolsCarryTheirAnnotationsAndUI", mcpMessagingToolsCarryTheirAnnotationsAndUI},
		{"AppsUIServesTheAdvertisedResource", mcpAppsUIServesTheAdvertisedResource},
		{"AppsUIBindsProjectList", mcpAppsUIBindsProjectList},
		{"BootAnnouncesTheMountedSurface", mcpBootAnnouncesTheMountedSurface},
	} {
		t.Run(c.name, func(t *testing.T) { c.check(t, f) })
	}
}
