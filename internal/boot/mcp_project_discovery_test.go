package boot_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/apikey"
	"altalune.id/openwa/internal/apperror"
	mcpinternal "altalune.id/openwa/internal/mcp"
	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/platform/authn"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/user"
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

// TestMCP_ProjectListIsScopedToTheCallersOrg drives the discovery tool over the real surface. SECURITY: project_list is the only tool that hands an agent project UUIDs, so a foreign row surfacing here is a UUID the agent can then feed to every other tool.
func TestMCP_ProjectListIsScopedToTheCallersOrg(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	foreign := seedForeignTenant(t, f)

	credentials := map[string]string{
		"key": f.readKey,
		"jwt": f.issuer.mint(t, mcpAudience, []string{authn.ScopeProjectsRead}),
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

// TestMCP_KeyNeverReachesASiblingProject drives a project-bound key over the real surface. SECURITY: the fixture runs on superuser Postgres (RLS bypassed), so the reach check is the only guard between the key and its sibling project.
func TestMCP_KeyNeverReachesASiblingProject(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})

	_, err := f.srv.Projects.Create(f.orgCtx, f.orgID, "sibling-project", "Sibling Project")
	require.NoError(t, err)

	t.Run("project_list hides the sibling project", func(t *testing.T) {
		rec := f.call(t, f.readKey, callToolBody(mcpinternal.ToolProjectList, map[string]any{}))
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		slugs := make([]string, 0)
		for _, p := range projectsFromTool(t, rec) {
			slugs = append(slugs, p.Slug)
		}
		require.Equal(t, []string{"mcp-project"}, slugs, "a project-bound key discovered a project it does not reach")
	})

	t.Run("a person in the org still discovers the sibling project", func(t *testing.T) {
		token := f.issuer.mint(t, mcpAudience, []string{authn.ScopeProjectsRead})
		rec := f.call(t, token, callToolBody(mcpinternal.ToolProjectList, map[string]any{}))
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		slugs := make([]string, 0)
		for _, p := range projectsFromTool(t, rec) {
			slugs = append(slugs, p.Slug)
		}
		require.ElementsMatch(t, []string{"mcp-project", "sibling-project"}, slugs, "body=%s", rec.Body.String())
	})
}

// TestMCP_OrgKeyReachesOnlyItsGrantedProjects drives an org key over the real surface with RLS bypassed, where no RLS backs the reach check.
func TestMCP_OrgKeyReachesOnlyItsGrantedProjects(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})

	granted, err := f.srv.Projects.Create(f.orgCtx, f.orgID, "granted-project", "Granted Project")
	require.NoError(t, err)
	_, err = f.srv.Projects.Create(f.orgCtx, f.orgID, "ungranted-project", "Ungranted Project")
	require.NoError(t, err)

	_, selected, err := f.srv.APIKeys.MintOrg(f.orgCtx, "org-reader", []string{authn.ScopeProjectsRead},
		apikey.ProjectGrant{ProjectIDs: []uuid.UUID{granted.ID}}, soon())
	require.NoError(t, err)
	_, everything, err := f.srv.APIKeys.MintOrg(f.orgCtx, "org-all", []string{authn.ScopeProjectsRead}, apikey.ProjectGrant{All: true}, soon())
	require.NoError(t, err)

	slugsFor := func(t *testing.T, key string) []string {
		t.Helper()
		rec := f.call(t, key, callToolBody(mcpinternal.ToolProjectList, map[string]any{}))
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		out := []string{}
		for _, p := range projectsFromTool(t, rec) {
			out = append(out, p.Slug)
		}
		return out
	}

	t.Run("a selected-projects key discovers only its grant", func(t *testing.T) {
		require.Equal(t, []string{"granted-project"}, slugsFor(t, selected))
	})
	t.Run("an all-projects key discovers every project of its org", func(t *testing.T) {
		require.ElementsMatch(t, []string{"mcp-project", "granted-project", "ungranted-project"}, slugsFor(t, everything))
	})
}

// TestMCP_PersonalTokenActsForItsOwner drives a member's personal token over the real surface: it reaches its grant, and dies the moment the member leaves.
func TestMCP_PersonalTokenActsForItsOwner(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	ctx := t.Context()

	o, err := f.srv.Orgs.BySlug(ctx, "mcp-org")
	require.NoError(t, err)
	member, err := f.srv.Users.Create(ctx, user.CreateRequest{Email: "pat-member@example.com", Name: "PAT Member", Source: user.SourceOIDC})
	require.NoError(t, err)
	memberCtx := tenant.Into(ctx, tenant.Context{OrgID: o.ID, UserID: member.ID})
	_, err = f.srv.Orgs.AddMember(memberCtx, o.ID, member.ID, org.RoleMember)
	require.NoError(t, err)

	_, token, err := f.srv.APIKeys.MintPersonal(memberCtx, "laptop", []string{authn.ScopeProjectsRead}, apikey.ProjectGrant{All: true}, soon())
	require.NoError(t, err)

	rec := f.call(t, token, callToolBody(mcpinternal.ToolProjectList, map[string]any{}))
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.NotEmpty(t, projectsFromTool(t, rec), "a member's token must read what the member can; body=%s", rec.Body.String())

	require.NoError(t, f.srv.Orgs.RemoveMember(f.orgCtx, f.orgID, member.ID))
	rec = f.call(t, token, callToolBody(mcpinternal.ToolProjectList, map[string]any{}))
	require.Equal(t, http.StatusUnauthorized, rec.Code, "a departed member's token must be refused at the door; body=%s", rec.Body.String())

	_, err = f.srv.Orgs.AddMember(memberCtx, o.ID, member.ID, org.RoleMember)
	require.NoError(t, err)
	rec = f.call(t, token, callToolBody(mcpinternal.ToolProjectList, map[string]any{}))
	require.Equal(t, http.StatusUnauthorized, rec.Code, "removal revoked the token for good, so re-joining must not revive it; body=%s", rec.Body.String())
}

// TestMCP_MemberListIsAnOrgLevelRead drives the org-level member_list tool: an org key holding members:read reads its own org's members, and nothing else can.
func TestMCP_MemberListIsAnOrgLevelRead(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})
	seedForeignTenant(t, f)

	_, reader, err := f.srv.APIKeys.MintOrg(f.orgCtx, "members", []string{authn.ScopeMembersRead}, apikey.ProjectGrant{All: true}, soon())
	require.NoError(t, err)

	rec := f.call(t, reader, callToolBody(mcpinternal.ToolMemberList, map[string]any{}))
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Contains(t, rec.Body.String(), tokenEmail, "the org's own member must be listed; body=%s", rec.Body.String())
	require.NotContains(t, rec.Body.String(), "outsider@example.com", "another org's member must never be listed; body=%s", rec.Body.String())

	rec = f.call(t, f.readKey, callToolBody(mcpinternal.ToolMemberList, map[string]any{}))
	require.Equal(t, apperror.CodeForbidden, toolPayload(t, rec).Code, "a key without members:read must be refused; body=%s", rec.Body.String())
}
