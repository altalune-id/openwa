package controlplane

import (
	"maps"
	"slices"

	apikeyv1connect "altalune.id/template/gen/go/apikey/v1/apikeyv1connect"
	authv1connect "altalune.id/template/gen/go/auth/v1/authv1connect"
	blogv1connect "altalune.id/template/gen/go/blog/v1/blogv1connect"
	projectv1connect "altalune.id/template/gen/go/project/v1/projectv1connect"
	todov1connect "altalune.id/template/gen/go/todo/v1/todov1connect"
	"altalune.id/template/internal/platform/surfaces"
)

// VerbTable names the domain verb every mounted procedure exposes, keyed by procedure path.
func VerbTable() map[string]surfaces.Verb {
	return map[string]surfaces.Verb{
		apikeyv1connect.APIKeyServiceListProcedure:           {Module: "apikey", Aggregate: "apikey", Operation: "list"},
		apikeyv1connect.APIKeyServiceCreateProcedure:         {Module: "apikey", Aggregate: "apikey", Operation: "create"},
		apikeyv1connect.APIKeyServiceRevokeProcedure:         {Module: "apikey", Aggregate: "apikey", Operation: "revoke"},
		authv1connect.AuthServiceWhoamiProcedure:             {Module: "auth", Aggregate: "session", Operation: "whoami"},
		blogv1connect.BlogServiceListPostsProcedure:          {Module: "blog", Aggregate: "post", Operation: "list"},
		blogv1connect.BlogServiceGetPostProcedure:            {Module: "blog", Aggregate: "post", Operation: "get"},
		blogv1connect.BlogServiceCreatePostProcedure:         {Module: "blog", Aggregate: "post", Operation: "create"},
		blogv1connect.BlogServiceUpdatePostProcedure:         {Module: "blog", Aggregate: "post", Operation: "update"},
		blogv1connect.BlogServicePublishPostProcedure:        {Module: "blog", Aggregate: "post", Operation: "publish"},
		blogv1connect.BlogServiceUnpublishPostProcedure:      {Module: "blog", Aggregate: "post", Operation: "unpublish"},
		blogv1connect.BlogServiceDeletePostProcedure:         {Module: "blog", Aggregate: "post", Operation: "delete"},
		projectv1connect.ProjectServiceListProjectsProcedure: {Module: "project", Aggregate: "project", Operation: "list"},
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
