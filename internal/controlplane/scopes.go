package controlplane

import (
	apikeyv1connect "altalune.id/template/gen/go/apikey/v1/apikeyv1connect"
	authv1connect "altalune.id/template/gen/go/auth/v1/authv1connect"
	blogv1connect "altalune.id/template/gen/go/blog/v1/blogv1connect"
	projectv1connect "altalune.id/template/gen/go/project/v1/projectv1connect"
	todov1connect "altalune.id/template/gen/go/todo/v1/todov1connect"
	"altalune.id/template/internal/platform/authn"
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
		projectv1connect.ProjectServiceListProjectsProcedure: authn.ScopePostsRead,
		todov1connect.TodoServiceListProcedure:               authn.ScopePostsRead,
		todov1connect.TodoServiceCreateProcedure:             authn.ScopePostsWrite,
		todov1connect.TodoServiceToggleProcedure:             authn.ScopePostsWrite,
		todov1connect.TodoServiceDeleteProcedure:             authn.ScopePostsAdmin,
		authv1connect.AuthServiceWhoamiProcedure:             authn.ScopeAPIKeysRead,
	}
}
