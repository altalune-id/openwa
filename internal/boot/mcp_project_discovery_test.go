package boot_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/apperror"
	mcpinternal "altalune.id/template/internal/mcp"
	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/authn"
	"altalune.id/template/internal/user"
)

type toolProject struct {
	ID    string `json:"id"`
	OrgID string `json:"orgId"`
	Slug  string `json:"slug"`
	Name  string `json:"name"`
}

func projectsFromTool(t *testing.T, rec *httptest.ResponseRecorder) []toolProject {
	t.Helper()
	resp := decodeRPC(t, rec)
	require.Nil(t, resp.Error, "a tool failure must answer in the result, not the JSON-RPC envelope")

	var result struct {
		IsError           bool `json:"isError"`
		StructuredContent struct {
			Projects []toolProject `json:"projects"`
		} `json:"structuredContent"`
	}
	require.NoError(t, json.Unmarshal(resp.Result, &result), "result=%s", string(resp.Result))
	require.False(t, result.IsError, "project_list reported an error: %s", string(resp.Result))
	return result.StructuredContent.Projects
}

func postSlugsFromTool(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	resp := decodeRPC(t, rec)
	require.Nil(t, resp.Error, "a tool failure must answer in the result, not the JSON-RPC envelope")

	var result struct {
		IsError           bool `json:"isError"`
		StructuredContent struct {
			Posts []struct {
				Slug      string `json:"slug"`
				ProjectID string `json:"projectId"`
			} `json:"posts"`
		} `json:"structuredContent"`
	}
	require.NoError(t, json.Unmarshal(resp.Result, &result), "result=%s", string(resp.Result))
	require.False(t, result.IsError, "blog_list reported an error: %s", string(resp.Result))

	slugs := make([]string, 0, len(result.StructuredContent.Posts))
	for _, p := range result.StructuredContent.Posts {
		slugs = append(slugs, p.Slug)
	}
	return slugs
}

// TestMCP_ProjectListIsScopedToTheCallersOrg drives the discovery tool over the real surface. SECURITY: project_list is the only tool that hands an agent project UUIDs, so a foreign row surfacing here is a UUID the agent can then feed to every other tool.
func TestMCP_ProjectListIsScopedToTheCallersOrg(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	foreign := seedForeignTenant(t, f)

	credentials := map[string]string{
		"key": f.readKey,
		"jwt": f.issuer.mint(t, mcpAudience, []string{authn.ScopePostsRead}),
	}
	for name, credential := range credentials {
		t.Run(name, func(t *testing.T) {
			rec := f.call(t, credential, callToolBody(mcpinternal.ToolProjectList, map[string]any{}))
			require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
			require.NotContains(t, rec.Body.String(), foreign.projectID,
				"project_list leaked another org's project UUID; body=%s", rec.Body.String())
			require.NotContains(t, rec.Body.String(), foreign.orgID,
				"project_list leaked another org's id; body=%s", rec.Body.String())

			projects := projectsFromTool(t, rec)
			require.NotEmpty(t, projects, "project_list returned nothing; an agent cannot discover a projectId")

			slugs := make([]string, 0, len(projects))
			for _, p := range projects {
				slugs = append(slugs, p.Slug)
				require.NotEqual(t, foreign.orgID, p.OrgID,
					"project_list returned a project owned by the foreign org")
			}
			require.Contains(t, slugs, "mcp-project", "project_list omitted the caller's own project; got %v", slugs)
			require.NotContains(t, slugs, "foreign-project",
				"project_list returned a project from an org the caller is not a member of; got %v", slugs)
		})
	}
}

// TestMCP_BlogListResolvesItsProject pins the three ways blog_list arrives at a project over the MCP surface: the explicit argument, the credential's active project, and neither.
func TestMCP_BlogListResolvesItsProject(t *testing.T) {
	t.Run("an omitted projectId reads the credential's active project", func(t *testing.T) {
		f := newMCPFixture(t, mcpOpts{enabled: true})
		seedForeignTenant(t, f)

		rec := f.call(t, f.readKey, callToolBody(mcpinternal.ToolBlogList, map[string]any{}))
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		require.Equal(t, []string{"hello"}, postSlugsFromTool(t, rec),
			"an argument-less blog_list did not read the key's own project; body=%s", rec.Body.String())
		require.NotContains(t, rec.Body.String(), "foreign-secret",
			"an argument-less blog_list read another org's posts; body=%s", rec.Body.String())
	})

	t.Run("an explicit projectId still reads that project", func(t *testing.T) {
		f := newMCPFixture(t, mcpOpts{enabled: true})

		rec := f.call(t, f.readKey, callToolBody(mcpinternal.ToolBlogList, map[string]any{"projectId": f.projectID}))
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		require.Equal(t, []string{"hello"}, postSlugsFromTool(t, rec), "body=%s", rec.Body.String())
	})

	t.Run("an explicit projectId in another org stays unreadable", func(t *testing.T) {
		f := newMCPFixture(t, mcpOpts{enabled: true})
		foreign := seedForeignTenant(t, f)

		rec := f.call(t, f.readKey, callToolBody(mcpinternal.ToolBlogList, map[string]any{"projectId": foreign.projectID}))
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		require.NotContains(t, rec.Body.String(), "foreign-secret",
			"naming another org's project UUID made its posts readable; body=%s", rec.Body.String())
		require.NotEmpty(t, toolPayload(t, rec).Code,
			"a cross-org read must fail with a code, not an empty list; body=%s", rec.Body.String())
	})

	t.Run("no projectId and no active project names the remedy", func(t *testing.T) {
		f := newMCPFixture(t, mcpOpts{enabled: true})
		token := f.untenantedToken(t)

		rec := f.call(t, token, callToolBody(mcpinternal.ToolBlogList, map[string]any{}))
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

		payload := toolPayload(t, rec)
		require.Equal(t, apperror.CodeProjectUnresolved, payload.Code,
			"an unresolvable project must answer with its own code, not a generic failure; body=%s", rec.Body.String())
		require.Contains(t, payload.Message, mcpinternal.ToolProjectList,
			"the error must name the tool that resolves it; message=%q", payload.Message)
	})
}

func (f *mcpFixture) untenantedToken(t *testing.T) string {
	t.Helper()
	const subject = "mcp-agent-no-project"

	u, err := f.srv.Users.EnsureFromOIDC(t.Context(), user.Claims{
		Issuer: f.issuer.url, Subject: subject, Email: "no-project@example.com", Name: "No Project",
	})
	require.NoError(t, err)
	_, err = f.srv.Orgs.Create(t.Context(), org.CreateRequest{
		Slug: "no-project-org", Name: "No Project Org", OwnerID: u.ID,
	})
	require.NoError(t, err)

	return f.issuer.mintWith(t, mcpAudience, []string{authn.ScopePostsRead}, map[string]any{
		"sub": subject, "email": "no-project@example.com",
	})
}
