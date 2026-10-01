package controlplane

import (
	"maps"
	"slices"

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
	"altalune.id/openwa/internal/platform/surfaces"
)

// VerbTable names the domain verb every mounted procedure exposes, keyed by procedure path.
func VerbTable() map[string]surfaces.Verb {
	return map[string]surfaces.Verb{
		messagev1connect.MessageServiceSendProcedure:         {Module: "message", Aggregate: "message", Operation: "send"},
		messagev1connect.MessageServiceGetProcedure:          {Module: "message", Aggregate: "message", Operation: "get"},
		messagev1connect.MessageServiceListProcedure:         {Module: "message", Aggregate: "message", Operation: "list"},
		messagev1connect.MessageServiceReactProcedure:        {Module: "message", Aggregate: "reaction", Operation: "create"},
		messagev1connect.MessageServiceRevokeProcedure:       {Module: "message", Aggregate: "message", Operation: "revoke"},
		messagev1connect.MessageServiceEditProcedure:         {Module: "message", Aggregate: "message", Operation: "edit"},
		messagev1connect.MessageServiceMarkReadProcedure:     {Module: "message", Aggregate: "message", Operation: "read"},
		chatv1connect.ChatServiceListProcedure:               {Module: "chat", Aggregate: "chat", Operation: "list"},
		chatv1connect.ChatServiceGetProcedure:                {Module: "chat", Aggregate: "chat", Operation: "get"},
		chatv1connect.ChatServiceMarkReadProcedure:           {Module: "chat", Aggregate: "chat", Operation: "read"},
		chatv1connect.ChatServiceListGroupsProcedure:         {Module: "chat", Aggregate: "group", Operation: "list"},
		chatv1connect.ChatServiceGroupInfoProcedure:          {Module: "chat", Aggregate: "group", Operation: "get"},
		chatv1connect.ChatServiceJoinGroupProcedure:          {Module: "chat", Aggregate: "group", Operation: "join"},
		chatv1connect.ChatServiceLeaveGroupProcedure:         {Module: "chat", Aggregate: "group", Operation: "leave"},
		contactv1connect.ContactServiceListProcedure:         {Module: "contact", Aggregate: "contact", Operation: "list"},
		contactv1connect.ContactServiceGetProcedure:          {Module: "contact", Aggregate: "contact", Operation: "get"},
		apikeyv1connect.APIKeyServiceListProcedure:           {Module: "apikey", Aggregate: "apikey", Operation: "list"},
		apikeyv1connect.APIKeyServiceCreateProcedure:         {Module: "apikey", Aggregate: "apikey", Operation: "create"},
		apikeyv1connect.APIKeyServiceRevokeProcedure:         {Module: "apikey", Aggregate: "apikey", Operation: "revoke"},
		devicev1connect.DeviceServiceListDevicesProcedure:    {Module: "device", Aggregate: "device", Operation: "list"},
		devicev1connect.DeviceServiceGetDeviceProcedure:      {Module: "device", Aggregate: "device", Operation: "get"},
		devicev1connect.DeviceServiceCreateDeviceProcedure:   {Module: "device", Aggregate: "device", Operation: "create"},
		devicev1connect.DeviceServiceUpdateDeviceProcedure:   {Module: "device", Aggregate: "device", Operation: "update"},
		devicev1connect.DeviceServiceDeleteDeviceProcedure:   {Module: "device", Aggregate: "device", Operation: "delete"},
		devicev1connect.DeviceServiceUnlinkProcedure:         {Module: "device", Aggregate: "device", Operation: "unlink"},
		devicev1connect.DeviceServiceStartLinkProcedure:      {Module: "device", Aggregate: "link", Operation: "create"},
		devicev1connect.DeviceServiceGetLinkStateProcedure:   {Module: "device", Aggregate: "link", Operation: "get"},
		devicev1connect.DeviceServiceLinkWithPhoneProcedure:  {Module: "device", Aggregate: "link", Operation: "create_phone"},
		authv1connect.AuthServiceWhoamiProcedure:             {Module: "auth", Aggregate: "session", Operation: "whoami"},
		blogv1connect.BlogServiceListPostsProcedure:          {Module: "blog", Aggregate: "post", Operation: "list"},
		blogv1connect.BlogServiceGetPostProcedure:            {Module: "blog", Aggregate: "post", Operation: "get"},
		blogv1connect.BlogServiceCreatePostProcedure:         {Module: "blog", Aggregate: "post", Operation: "create"},
		blogv1connect.BlogServiceUpdatePostProcedure:         {Module: "blog", Aggregate: "post", Operation: "update"},
		blogv1connect.BlogServicePublishPostProcedure:        {Module: "blog", Aggregate: "post", Operation: "publish"},
		blogv1connect.BlogServiceUnpublishPostProcedure:      {Module: "blog", Aggregate: "post", Operation: "unpublish"},
		blogv1connect.BlogServiceDeletePostProcedure:         {Module: "blog", Aggregate: "post", Operation: "delete"},
		projectv1connect.ProjectServiceListProjectsProcedure: {Module: "project", Aggregate: "project", Operation: "list"},
		orgv1connect.MemberServiceListMembersProcedure:       {Module: "org", Aggregate: "member", Operation: "list"},
		todov1connect.TodoServiceListProcedure:               {Module: "todo", Aggregate: "todo", Operation: "list"},
		todov1connect.TodoServiceCreateProcedure:             {Module: "todo", Aggregate: "todo", Operation: "create"},
		todov1connect.TodoServiceToggleProcedure:             {Module: "todo", Aggregate: "todo", Operation: "toggle"},
		todov1connect.TodoServiceDeleteProcedure:             {Module: "todo", Aggregate: "todo", Operation: "delete"},
	}
}

// Verbs returns the domain verbs S2 exposes, deduplicated.
func Verbs() []surfaces.Verb {
	return slices.Collect(maps.Values(VerbTable()))
}
