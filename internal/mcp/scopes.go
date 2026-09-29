package mcp

import (
	orgv1mcp "altalune.id/openwa/gen/go/org/v1/orgv1mcp"
	projectv1mcp "altalune.id/openwa/gen/go/project/v1/projectv1mcp"
	"altalune.id/openwa/internal/platform/authn"
)

// NOTE: these names are a wire contract with every MCP host.
const (
	ToolProjectList = projectv1mcp.ProjectListToolName
	ToolMemberList  = orgv1mcp.MemberListToolName
)

// ScopeTable declares the scope a caller must hold for every tool this surface publishes. SECURITY: registration reads this table through ScopeFor, so the runtime check cannot drift from it; a tool missing here resolves to the empty scope, which the root mcp server denies.
func ScopeTable() authn.ScopeTable {
	return authn.ScopeTable{ToolProjectList: authn.ScopeProjectsRead, ToolMemberList: authn.ScopeMembersRead}
}

// ScopeFor returns the scope tool name requires, or the empty scope for a tool absent from the catalog.
func ScopeFor(name string) string { return ScopeTable()[name] }
