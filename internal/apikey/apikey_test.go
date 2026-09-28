package apikey_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/apikey"
	"altalune.id/template/internal/platform/authn"
)

func TestMintReturnsPlaintextOnceAndStoresOnlyAHash(t *testing.T) {
	now := time.Now().UTC()
	k, plaintext, err := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "ci", []string{authn.ScopePostsRead}, nil, nil, now)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if !strings.HasPrefix(plaintext, apikey.DefaultPrefix) {
		t.Fatalf("plaintext %q lacks prefix %q", plaintext, apikey.DefaultPrefix)
	}
	if strings.Contains(plaintext, "\n") || len(plaintext) < 32 {
		t.Fatalf("plaintext looks malformed: %q", plaintext)
	}
	if k.SecretHash == [32]byte{} {
		t.Fatal("SecretHash is zero")
	}
}

func TestMintRejectsUnknownScope(t *testing.T) {
	_, _, err := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "bad", []string{"posts:destroy"}, nil, nil, time.Now().UTC())
	if err == nil {
		t.Fatal("Mint accepted a scope outside the catalog")
	}
	if !apikey.IsUnknownScopeError(err) {
		t.Fatalf("err = %v, want *UnknownScopeError", err)
	}
}

func TestAllows(t *testing.T) {
	catA, catB := uuid.New(), uuid.New()
	now := time.Now().UTC()

	wide, _, _ := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "wide", []string{authn.ScopePostsWrite}, nil, nil, now)
	narrow, _, _ := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "narrow", []string{authn.ScopePostsWrite}, []uuid.UUID{catA}, nil, now)

	tests := []struct {
		name     string
		key      *apikey.APIKey
		scope    string
		resource uuid.UUID
		want     bool
	}{
		{"wide key any resource", wide, authn.ScopePostsWrite, catB, true},
		{"wide key wrong scope", wide, authn.ScopePostsAdmin, catB, false},
		{"narrow key listed resource", narrow, authn.ScopePostsWrite, catA, true},
		{"narrow key unlisted resource", narrow, authn.ScopePostsWrite, catB, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.key.Allows(tt.scope, tt.resource); got != tt.want {
				t.Fatalf("Allows = %v, want %v", got, tt.want)
			}
		})
	}
}

// SECURITY: a resource-restricted key must not satisfy a route that names no resource.
func TestAllowsProjectRefusesRestrictedKey(t *testing.T) {
	now := time.Now().UTC()
	narrow, _, _ := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "narrow", []string{authn.ScopePostsWrite}, []uuid.UUID{uuid.New()}, nil, now)
	if narrow.AllowsProject(authn.ScopePostsWrite) {
		t.Fatal("a resource-restricted key satisfied a project-wide check")
	}
	wide, _, _ := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "wide", []string{authn.ScopePostsWrite}, nil, nil, now)
	if !wide.AllowsProject(authn.ScopePostsWrite) {
		t.Fatal("an unrestricted key failed a project-wide check")
	}
}

func TestUsable(t *testing.T) {
	now := time.Now().UTC()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)

	k, _, _ := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "k", []string{authn.ScopePostsRead}, nil, nil, now)
	if !k.Usable(now) {
		t.Fatal("fresh key not usable")
	}

	expired, _, _ := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "k", []string{authn.ScopePostsRead}, nil, &past, now)
	if expired.Usable(now) {
		t.Fatal("expired key usable")
	}

	notYet, _, _ := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "k", []string{authn.ScopePostsRead}, nil, &future, now)
	if !notYet.Usable(now) {
		t.Fatal("unexpired key not usable")
	}

	revoked, _, _ := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "k", []string{authn.ScopePostsRead}, nil, nil, now)
	revoked.RevokedAt = &past
	if revoked.Usable(now) {
		t.Fatal("revoked key usable")
	}
}
