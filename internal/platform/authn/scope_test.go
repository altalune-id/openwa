package authn_test

import (
	"errors"
	"slices"
	"testing"

	"altalune.id/template/internal/platform/authn"
)

func TestValidRejectsUnknownScope(t *testing.T) {
	if authn.Valid("posts:destroy") {
		t.Fatal("Valid accepted a scope outside the catalog")
	}
	if !authn.Valid(authn.ScopePostsRead) {
		t.Fatalf("Valid rejected %q", authn.ScopePostsRead)
	}
}

func TestAllScopesReturnsACopy(t *testing.T) {
	first := authn.AllScopes()
	first[0] = "mutated"
	if slices.Contains(authn.AllScopes(), "mutated") {
		t.Fatal("AllScopes leaked its backing array")
	}
}

func TestErrorHelpers(t *testing.T) {
	var unauthorized error = &authn.UnauthorizedError{}
	if !authn.IsUnauthorizedError(unauthorized) {
		t.Fatal("IsUnauthorizedError missed its own type")
	}
	scoped := &authn.InsufficientScopeError{Scope: authn.ScopePostsWrite}
	if !authn.IsInsufficientScopeError(scoped) {
		t.Fatal("IsInsufficientScopeError missed its own type")
	}
	if authn.IsUnauthorizedError(scoped) {
		t.Fatal("an authz failure must not read as an authn failure")
	}
	if !errors.Is(errors.Join(nil, scoped), scoped) {
		t.Fatal("InsufficientScopeError does not survive errors.Join")
	}
}
