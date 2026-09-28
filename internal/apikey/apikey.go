// Package apikey mints and verifies the machine credentials the control and data planes accept.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"slices"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/platform/authn"
)

// DefaultPrefix marks a template API key when api.keyPrefix is unset.
const DefaultPrefix = "key_"

const secretBytes = 32

// Scheme is the one prefix a deployment mints keys under and recognizes them by; its zero value is DefaultPrefix.
type Scheme struct{ prefix string }

// NewScheme returns the Scheme for prefix, falling back to DefaultPrefix when prefix is empty.
func NewScheme(prefix string) Scheme { return Scheme{prefix: prefix} }

// Prefix returns the literal every plaintext secret minted under this Scheme starts with.
func (sc Scheme) Prefix() string {
	if sc.prefix == "" {
		return DefaultPrefix
	}
	return sc.prefix
}

// Authn returns the shape gate a surface boundary must apply to credentials minted under this Scheme.
func (sc Scheme) Authn() authn.Scheme { return authn.Scheme{Prefix: sc.Prefix()} }

// APIKey is a machine credential scoped to one project.
type APIKey struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	ProjectID   uuid.UUID
	Name        string
	SecretHash  [32]byte
	Scopes      []string
	ResourceIDs []uuid.UUID
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
	LastUsedAt  *time.Time
}

// Mint returns a new key and its plaintext secret, which is never recoverable afterwards.
func (sc Scheme) Mint(orgID, projectID uuid.UUID, name string, scopes []string, resourceIDs []uuid.UUID, expiresAt *time.Time, now time.Time) (*APIKey, string, error) {
	for _, s := range scopes {
		if !authn.Valid(s) {
			return nil, "", &UnknownScopeError{Scope: s}
		}
	}
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return nil, "", err
	}
	plaintext := sc.Prefix() + base64.RawURLEncoding.EncodeToString(buf)
	return &APIKey{
		ID:          uuid.New(),
		OrgID:       orgID,
		ProjectID:   projectID,
		Name:        name,
		SecretHash:  sha256.Sum256([]byte(plaintext)),
		Scopes:      slices.Clone(scopes),
		ResourceIDs: slices.Clone(resourceIDs),
		CreatedAt:   now,
		ExpiresAt:   expiresAt,
	}, plaintext, nil
}

// Matches reports whether plaintext hashes to this key's secret, in constant time.
func (k *APIKey) Matches(plaintext string) bool {
	sum := sha256.Sum256([]byte(plaintext))
	return subtle.ConstantTimeCompare(sum[:], k.SecretHash[:]) == 1
}

// Usable reports whether the key is neither revoked nor expired at now.
func (k *APIKey) Usable(now time.Time) bool {
	if k.RevokedAt != nil && !k.RevokedAt.After(now.UTC()) {
		return false
	}
	if k.ExpiresAt != nil && !k.ExpiresAt.After(now.UTC()) {
		return false
	}
	return true
}

// Allows reports whether the key grants scope on resourceID; empty ResourceIDs means the whole project.
func (k *APIKey) Allows(scope string, resourceID uuid.UUID) bool {
	if !slices.Contains(k.Scopes, scope) {
		return false
	}
	if len(k.ResourceIDs) == 0 {
		return true
	}
	return slices.Contains(k.ResourceIDs, resourceID)
}

// AllowsProject reports whether the key grants scope across its whole project. SECURITY: a resource-restricted key never does.
func (k *APIKey) AllowsProject(scope string) bool {
	return len(k.ResourceIDs) == 0 && slices.Contains(k.Scopes, scope)
}
