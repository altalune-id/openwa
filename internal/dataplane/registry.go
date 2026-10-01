package dataplane

import (
	"maps"
	"slices"

	"altalune.id/openwa/internal/platform/surfaces"
)

// VerbTable names the domain verb every route this surface registers exposes, keyed by method and path suffix.
func VerbTable() map[string]surfaces.Verb {
	return map[string]surfaces.Verb{
		"GET":                    {Module: "blog", Aggregate: "post", Operation: "list"},
		"GET /{slug}":            {Module: "blog", Aggregate: "post", Operation: "get"},
		"POST":                   {Module: "blog", Aggregate: "post", Operation: "create"},
		"PUT /{slug}":            {Module: "blog", Aggregate: "post", Operation: "replace"},
		"PATCH /{slug}":          {Module: "blog", Aggregate: "post", Operation: "update"},
		"DELETE /{slug}":         {Module: "blog", Aggregate: "post", Operation: "delete"},
		"POST /{slug}/publish":   {Module: "blog", Aggregate: "post", Operation: "publish"},
		"POST /{slug}/unpublish": {Module: "blog", Aggregate: "post", Operation: "unpublish"},

		"GET /devices":                        {Module: "device", Aggregate: "device", Operation: "list"},
		"POST /devices":                       {Module: "device", Aggregate: "device", Operation: "create"},
		"GET /devices/{device}":               {Module: "device", Aggregate: "device", Operation: "get"},
		"PATCH /devices/{device}":             {Module: "device", Aggregate: "device", Operation: "update"},
		"DELETE /devices/{device}":            {Module: "device", Aggregate: "device", Operation: "delete"},
		"POST /devices/{device}/links":        {Module: "device", Aggregate: "link", Operation: "create"},
		"GET /devices/{device}/links/current": {Module: "device", Aggregate: "link", Operation: "get"},
		"POST /devices/{device}/unlink":       {Module: "device", Aggregate: "device", Operation: "unlink"},

		"POST /devices/{device}/messages":    {Module: "message", Aggregate: "message", Operation: "send"},
		"GET /devices/{device}/messages":     {Module: "message", Aggregate: "message", Operation: "list"},
		"GET /messages/{message}":            {Module: "message", Aggregate: "message", Operation: "get"},
		"GET /messages/{message}/media":      {Module: "message", Aggregate: "media", Operation: "get"},
		"POST /messages/{message}/reactions": {Module: "message", Aggregate: "reaction", Operation: "create"},
		"POST /messages/{message}/revoke":    {Module: "message", Aggregate: "message", Operation: "revoke"},
		"PATCH /messages/{message}":          {Module: "message", Aggregate: "message", Operation: "edit"},
		"POST /messages/{message}/read":      {Module: "message", Aggregate: "message", Operation: "read"},
		"GET /chats":                         {Module: "chat", Aggregate: "chat", Operation: "list"},
		"GET /chats/{chat}":                  {Module: "chat", Aggregate: "chat", Operation: "get"},
		"GET /chats/{chat}/messages":         {Module: "message", Aggregate: "message", Operation: "list"},
		"POST /chats/{chat}/read":            {Module: "chat", Aggregate: "chat", Operation: "read"},
		"GET /chats/{chat}/group":            {Module: "chat", Aggregate: "group", Operation: "get"},
		"POST /chats/{chat}/leave":           {Module: "chat", Aggregate: "group", Operation: "leave"},
		"POST /devices/{device}/groups":      {Module: "chat", Aggregate: "group", Operation: "join"},
		"GET /contacts":                      {Module: "contact", Aggregate: "contact", Operation: "list"},
	}
}

// Verbs returns the domain verbs S3 exposes, deduplicated.
func Verbs() []surfaces.Verb {
	return slices.Collect(maps.Values(VerbTable()))
}
