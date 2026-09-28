package mcp

import (
	blogv1mcp "altalune.id/template/gen/go/blog/v1/blogv1mcp"
	projectv1mcp "altalune.id/template/gen/go/project/v1/projectv1mcp"
	todov1mcp "altalune.id/template/gen/go/todo/v1/todov1mcp"
	"altalune.id/template/internal/platform/authn"
)

// NOTE: these names are a wire contract with every MCP host.
const (
	ToolTodoCreate  = todov1mcp.TodoCreateToolName
	ToolBlogPublish = blogv1mcp.BlogPublishToolName
	ToolBlogList    = blogv1mcp.BlogListToolName
	ToolProjectList = projectv1mcp.ProjectListToolName
)

// ScopeTable declares the scope a caller must hold for every tool this surface publishes. SECURITY: registration reads this table through ScopeFor, so the runtime check cannot drift from it; a tool missing here resolves to the empty scope, which the root mcp server denies.
func ScopeTable() authn.ScopeTable {
	return authn.ScopeTable{
		ToolTodoCreate:  authn.ScopePostsWrite,
		ToolBlogPublish: authn.ScopePostsWrite,
		ToolBlogList:    authn.ScopePostsRead,
		// NOTE: posts:read, not a projects:read of its own — a new scope string obliges every operator to extend an external authorization server's catalog and re-attach the client before project discovery works at all.
		ToolProjectList: authn.ScopePostsRead,
	}
}

// ScopeFor returns the scope tool name requires, or the empty scope for a tool absent from the catalog.
func ScopeFor(name string) string { return ScopeTable()[name] }
