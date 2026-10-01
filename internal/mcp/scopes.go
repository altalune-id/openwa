package mcp

import (
	chatv1mcp "altalune.id/openwa/gen/go/chat/v1/chatv1mcp"
	contactv1mcp "altalune.id/openwa/gen/go/contact/v1/contactv1mcp"
	devicev1mcp "altalune.id/openwa/gen/go/device/v1/devicev1mcp"
	messagev1mcp "altalune.id/openwa/gen/go/message/v1/messagev1mcp"
	orgv1mcp "altalune.id/openwa/gen/go/org/v1/orgv1mcp"
	projectv1mcp "altalune.id/openwa/gen/go/project/v1/projectv1mcp"
	"altalune.id/openwa/internal/platform/authn"
)

// NOTE: these names are a wire contract with every MCP host.
const (
	ToolMemberList   = orgv1mcp.MemberListToolName
	ToolProjectList  = projectv1mcp.ProjectListToolName
	ToolDeviceList   = devicev1mcp.DeviceListToolName
	ToolDeviceGet    = devicev1mcp.DeviceGetToolName
	ToolDevicePair   = devicev1mcp.DevicePairToolName
	ToolDeviceLogout = devicev1mcp.DeviceLogoutToolName
	ToolMessageSend  = messagev1mcp.MessageSendToolName
	ToolMessageList  = messagev1mcp.MessageListToolName
	ToolChatList     = chatv1mcp.ChatListToolName
	ToolGroupList    = chatv1mcp.GroupListToolName
	ToolGroupJoin    = chatv1mcp.GroupJoinToolName
	ToolContactList  = contactv1mcp.ContactListToolName
)

// ScopeTable declares the scope a caller must hold for every tool this surface publishes. SECURITY: registration reads this table through ScopeFor, so the runtime check cannot drift from it; a tool missing here resolves to the empty scope, which the root mcp server denies.
func ScopeTable() authn.ScopeTable {
	return authn.ScopeTable{
		ToolMemberList:   authn.ScopeMembersRead,
		ToolProjectList:  authn.ScopeProjectsRead,
		ToolDeviceList:   authn.ScopeDevicesRead,
		ToolDeviceGet:    authn.ScopeDevicesRead,
		ToolDevicePair:   authn.ScopeDevicesWrite,
		ToolDeviceLogout: authn.ScopeDevicesWrite,
		ToolMessageSend:  authn.ScopeMessagesWrite,
		ToolMessageList:  authn.ScopeMessagesRead,
		ToolChatList:     authn.ScopeChatsRead,
		ToolGroupList:    authn.ScopeChatsRead,
		ToolGroupJoin:    authn.ScopeChatsWrite,
		ToolContactList:  authn.ScopeContactsRead,
	}
}

// ScopeFor returns the scope tool name requires, or the empty scope for a tool absent from the catalog.
func ScopeFor(name string) string { return ScopeTable()[name] }
