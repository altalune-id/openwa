package apikey_test

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/apikey"
	"altalune.id/openwa/internal/platform/authn"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
)

const benchKeyRows = 1000

func benchCredentials(b *testing.B) (*apikey.Authenticator, map[string]string) {
	b.Helper()

	store := fakes.NewAPIKey()
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}
	ctx := tenant.Into(b.Context(), tc)

	var valid string
	for i := range benchKeyRows {
		k, plaintext, err := apikey.Scheme{}.Mint(tc.OrgID, tc.ProjectID, "bench", []string{authn.ScopePostsRead}, nil, nil, time.Now().UTC())
		require.NoError(b, err)
		require.NoError(b, store.Save(ctx, k))
		if i == benchKeyRows/2 {
			valid = plaintext
		}
	}

	buf := make([]byte, 32)
	_, err := rand.Read(buf)
	require.NoError(b, err)

	return apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers()), map[string]string{
		"malformed": "not-a-credential",
		"unknown":   apikey.DefaultPrefix + base64.RawURLEncoding.EncodeToString(buf),
		"valid":     valid,
	}
}

func BenchmarkAuthenticate(b *testing.B) {
	auth, creds := benchCredentials(b)
	ctx := b.Context()

	for _, name := range []string{"malformed", "unknown", "valid"} {
		raw := creds[name]
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				_, _ = auth.Authenticate(ctx, raw)
			}
		})
	}
}
