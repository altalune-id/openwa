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
	}
}

// Verbs returns the domain verbs S3 exposes, deduplicated.
func Verbs() []surfaces.Verb {
	return slices.Collect(maps.Values(VerbTable()))
}
