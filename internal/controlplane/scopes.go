package controlplane

import (
	apikeyv1connect "altalune.id/openwa/gen/go/apikey/v1/apikeyv1connect"
	authv1connect "altalune.id/openwa/gen/go/auth/v1/authv1connect"
	blogv1connect "altalune.id/openwa/gen/go/blog/v1/blogv1connect"
	chatv1connect "altalune.id/openwa/gen/go/chat/v1/chatv1connect"
	contactv1connect "altalune.id/openwa/gen/go/contact/v1/contactv1connect"
	devicev1connect "altalune.id/openwa/gen/go/device/v1/devicev1connect"
	messagev1connect "altalune.id/openwa/gen/go/message/v1/messagev1connect"
	orgv1connect "altalune.id/openwa/gen/go/org/v1/orgv1connect"
	projectv1connect "altalune.id/openwa/gen/go/project/v1/projectv1connect"
	todov1connect "altalune.id/openwa/gen/go/todo/v1/todov1connect"
	"altalune.id/openwa/internal/platform/authn"
)

// ScopeTable declares the scope a key principal must hold for every mounted procedure. SECURITY: a procedure missing from this map is denied to key principals, not admitted.
func ScopeTable() authn.ScopeTable {
	return authn.ScopeTable{
		apikeyv1connect.APIKeyServiceListProcedure:           authn.ScopeAPIKeysRead,
		apikeyv1connect.APIKeyServiceCreateProcedure:         authn.ScopeAPIKeysWrite,
		apikeyv1connect.APIKeyServiceRevokeProcedure:         authn.ScopeAPIKeysWrite,
		blogv1connect.BlogServiceListPostsProcedure:          authn.ScopePostsRead,
		blogv1connect.BlogServiceGetPostProcedure:            authn.ScopePostsRead,
		blogv1connect.BlogServiceCreatePostProcedure:         authn.ScopePostsWrite,
		blogv1connect.BlogServiceUpdatePostProcedure:         authn.ScopePostsWrite,
		blogv1connect.BlogServicePublishPostProcedure:        authn.ScopePostsWrite,
		blogv1connect.BlogServiceUnpublishPostProcedure:      authn.ScopePostsWrite,
		blogv1connect.BlogServiceDeletePostProcedure:         authn.ScopePostsAdmin,
		projectv1connect.ProjectServiceListProjectsProcedure: authn.ScopeProjectsRead,
		orgv1connect.MemberServiceListMembersProcedure:       authn.ScopeMembersRead,
		todov1connect.TodoServiceListProcedure:               authn.ScopePostsRead,
		todov1connect.TodoServiceCreateProcedure:             authn.ScopePostsWrite,
		todov1connect.TodoServiceToggleProcedure:             authn.ScopePostsWrite,
		todov1connect.TodoServiceDeleteProcedure:             authn.ScopePostsAdmin,
		devicev1connect.DeviceServiceListDevicesProcedure:    authn.ScopeDevicesRead,
		devicev1connect.DeviceServiceGetDeviceProcedure:      authn.ScopeDevicesRead,
		devicev1connect.DeviceServiceGetLinkStateProcedure:   authn.ScopeDevicesRead,
		devicev1connect.DeviceServiceCreateDeviceProcedure:   authn.ScopeDevicesWrite,
		devicev1connect.DeviceServiceUpdateDeviceProcedure:   authn.ScopeDevicesWrite,
		devicev1connect.DeviceServiceDeleteDeviceProcedure:   authn.ScopeDevicesWrite,
		devicev1connect.DeviceServiceStartLinkProcedure:      authn.ScopeDevicesWrite,
		devicev1connect.DeviceServiceLinkWithPhoneProcedure:  authn.ScopeDevicesWrite,
		devicev1connect.DeviceServiceUnlinkProcedure:         authn.ScopeDevicesWrite,
		messagev1connect.MessageServiceSendProcedure:         authn.ScopeMessagesWrite,
		messagev1connect.MessageServiceGetProcedure:          authn.ScopeMessagesRead,
		messagev1connect.MessageServiceListProcedure:         authn.ScopeMessagesRead,
		messagev1connect.MessageServiceReactProcedure:        authn.ScopeMessagesWrite,
		messagev1connect.MessageServiceRevokeProcedure:       authn.ScopeMessagesWrite,
		messagev1connect.MessageServiceEditProcedure:         authn.ScopeMessagesWrite,
		messagev1connect.MessageServiceMarkReadProcedure:     authn.ScopeMessagesWrite,
		chatv1connect.ChatServiceListProcedure:               authn.ScopeChatsRead,
		chatv1connect.ChatServiceGetProcedure:                authn.ScopeChatsRead,
		chatv1connect.ChatServiceMarkReadProcedure:           authn.ScopeChatsWrite,
		chatv1connect.ChatServiceListGroupsProcedure:         authn.ScopeChatsRead,
		chatv1connect.ChatServiceGroupInfoProcedure:          authn.ScopeChatsRead,
		chatv1connect.ChatServiceJoinGroupProcedure:          authn.ScopeChatsWrite,
		chatv1connect.ChatServiceLeaveGroupProcedure:         authn.ScopeChatsWrite,
		contactv1connect.ContactServiceListProcedure:         authn.ScopeContactsRead,
		contactv1connect.ContactServiceGetProcedure:          authn.ScopeContactsRead,
		authv1connect.AuthServiceWhoamiProcedure:             authn.ScopeAPIKeysRead,
	}
}
