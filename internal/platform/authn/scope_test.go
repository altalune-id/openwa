package authn_test

import (
	"errors"
	"slices"
	"testing"

	"altalune.id/openwa/internal/platform/authn"
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

// SECURITY: a retired scope keeps validating on existing keys but is never offered to a new one.
func TestRetiredScopeValidatesButIsNotMintable(t *testing.T) {
	if !authn.Valid(authn.ScopeAPIKeysWrite) {
		t.Fatal("a retired scope must keep validating, or keys in the field stop parsing")
	}
	if authn.Mintable(authn.ScopeAPIKeysWrite) {
		t.Fatal("apikeys:write is retired and must not be mintable")
	}
	if slices.Contains(authn.MintableScopes(), authn.ScopeAPIKeysWrite) {
		t.Fatal("MintableScopes offered a retired scope")
	}
	if !authn.Mintable(authn.ScopePostsRead) {
		t.Fatalf("Mintable rejected %q", authn.ScopePostsRead)
	}
}

func TestEveryScopeHasALevel(t *testing.T) {
	for _, s := range authn.AllScopes() {
		level, ok := authn.LevelOf(s)
		if !ok || (level != authn.LevelProject && level != authn.LevelOrg) {
			t.Errorf("%q has no valid level: %q", s, level)
		}
	}
	if _, ok := authn.LevelOf("posts:destroy"); ok {
		t.Fatal("LevelOf resolved a scope outside the catalog")
	}
	if level, _ := authn.LevelOf(authn.ScopeMembersRead); level != authn.LevelOrg {
		t.Fatalf("members:read is %q, want org: it acts on the org itself", level)
	}
	if level, _ := authn.LevelOf(authn.ScopePostsRead); level != authn.LevelProject {
		t.Fatalf("posts:read is %q, want project", level)
	}
}

// The console form hides the demo posts scopes, though they stay valid and mintable over the wire.
func TestConsoleScopesHidesTheDemoScopes(t *testing.T) {
	console := authn.ConsoleScopes()
	for _, demo := range []string{authn.ScopePostsRead, authn.ScopePostsWrite, authn.ScopePostsAdmin} {
		if slices.Contains(console, demo) {
			t.Errorf("ConsoleScopes offered demo scope %q", demo)
		}
		if !authn.Mintable(demo) {
			t.Errorf("%q must stay mintable over the wire", demo)
		}
	}
	if slices.Contains(console, authn.ScopeAPIKeysWrite) {
		t.Error("ConsoleScopes offered the retired apikeys:write")
	}
	for _, want := range []string{
		authn.ScopeProjectsRead, authn.ScopeMembersRead, authn.ScopeAPIKeysRead,
		authn.ScopeDevicesRead, authn.ScopeDevicesWrite, authn.ScopeMessagesRead,
		authn.ScopeMessagesWrite, authn.ScopeChatsRead, authn.ScopeChatsWrite, authn.ScopeContactsRead,
	} {
		if !slices.Contains(console, want) {
			t.Errorf("ConsoleScopes dropped %q", want)
		}
	}
}

func TestConsoleScopesReturnsACopy(t *testing.T) {
	first := authn.ConsoleScopes()
	first[0] = "mutated"
	if slices.Contains(authn.ConsoleScopes(), "mutated") {
		t.Fatal("ConsoleScopes leaked its backing array")
	}
}

func TestAllScopes_ContainsTheOpenwaCatalog(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"projects:read", "devices:read", "devices:write", "messages:read", "messages:write", "chats:read", "chats:write", "contacts:read"} {
		if !authn.Valid(s) {
			t.Fatalf("%s not valid", s)
		}
		if !authn.Mintable(s) {
			t.Fatalf("%s not mintable", s)
		}
		lvl, ok := authn.LevelOf(s)
		if !ok || lvl != authn.LevelProject {
			t.Fatalf("%s level = %q, %v", s, lvl, ok)
		}
	}
}
