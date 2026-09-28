package authn

import "slices"

// The scope catalog. NOTE: these strings are a wire contract with every minted key.
const (
	ScopePostsRead    = "posts:read"
	ScopePostsWrite   = "posts:write"
	ScopePostsAdmin   = "posts:admin"
	ScopeAPIKeysRead  = "apikeys:read"
	ScopeAPIKeysWrite = "apikeys:write"
)

//nolint:gochecknoglobals // immutable catalog, returned by copy from AllScopes.
var allScopes = []string{
	ScopePostsRead, ScopePostsWrite, ScopePostsAdmin,
	ScopeAPIKeysRead, ScopeAPIKeysWrite,
}

// AllScopes returns every scope in the catalog.
func AllScopes() []string { return slices.Clone(allScopes) }

// Valid reports whether scope is in the catalog.
func Valid(scope string) bool { return slices.Contains(allScopes, scope) }
