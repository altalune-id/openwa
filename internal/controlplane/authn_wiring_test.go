package controlplane

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	blogv1 "altalune.id/template/gen/go/blog/v1"
	blogv1connect "altalune.id/template/gen/go/blog/v1/blogv1connect"
	"altalune.id/template/internal/apikey"
	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/platform"
	"altalune.id/template/internal/platform/authn"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/platform/tokens"
	"altalune.id/template/internal/testutil/fakes"
)

// TestServer_HandlerOptions_EnforcesKeyScopes proves handlerOptions wires the real authn.Interceptor, not just interceptor.Auth's JWT check.
func TestServer_HandlerOptions_EnforcesKeyScopes(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := apperror.NewReporter(log, false)

	store := fakes.NewAPIKey()
	orgID, projectID := uuid.New(), uuid.New()
	now := time.Now().UTC()

	readKey, readPlain, err := apikey.Scheme{}.Mint(orgID, projectID, "read-only", []string{authn.ScopePostsRead}, nil, nil, now)
	if err != nil {
		t.Fatalf("mint read-only key: %v", err)
	}
	store.Seed(readKey)

	adminKey, adminPlain, err := apikey.Scheme{}.Mint(orgID, projectID, "admin", []string{authn.ScopePostsAdmin}, nil, nil, now)
	if err != nil {
		t.Fatalf("mint admin key: %v", err)
	}
	store.Seed(adminKey)

	keyAuthn := apikey.NewAuthenticator(store, nil, apikey.Scheme{})
	chain := authn.Chain{keyAuthn, tokens.NewAuthenticator(alwaysOKVerifier{})}

	s := &Server{
		Kernel:    &platform.Kernel{Log: log, Reporter: reporter},
		Authn:     chain,
		KeyPrefix: apikey.DefaultPrefix,
	}

	// DeletePost requires ScopePostsAdmin in controlplane.ScopeTable().
	const procedure = blogv1connect.BlogServiceDeletePostProcedure
	var gotPrincipal session.Principal
	handler := connect.NewUnaryHandlerSimple(
		procedure,
		func(ctx context.Context, _ *blogv1.DeletePostRequest) (*blogv1.DeletePostResponse, error) {
			gotPrincipal = session.PrincipalFrom(ctx)
			return &blogv1.DeletePostResponse{}, nil
		},
		s.handlerOptions()...,
	)
	mux := http.NewServeMux()
	mux.Handle(procedure, handler)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	client := connect.NewClient[blogv1.DeletePostRequest, blogv1.DeletePostResponse](http.DefaultClient, ts.URL+procedure)

	t.Run("read-only key is denied", func(t *testing.T) {
		req := connect.NewRequest(&blogv1.DeletePostRequest{PostId: uuid.NewString()})
		req.Header().Set("Authorization", "Bearer "+readPlain)

		_, err := client.CallUnary(t.Context(), req)
		if err == nil {
			t.Fatal("expected the read-only key to be denied, got a successful delete")
		}
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("code = %v, want %v (denial: %v)", connect.CodeOf(err), connect.CodePermissionDenied, err)
		}
	})

	t.Run("admin key succeeds", func(t *testing.T) {
		req := connect.NewRequest(&blogv1.DeletePostRequest{PostId: uuid.NewString()})
		req.Header().Set("Authorization", "Bearer "+adminPlain)

		if _, err := client.CallUnary(t.Context(), req); err != nil {
			t.Fatalf("expected the admin-scoped key to succeed, got: %v", err)
		}
		if gotPrincipal.Source != session.SourceAPIKey {
			t.Errorf("principal source = %q, want %q", gotPrincipal.Source, session.SourceAPIKey)
		}
		if !slices.Contains(gotPrincipal.Scopes, authn.ScopePostsAdmin) {
			t.Errorf("principal scopes = %v, missing %q", gotPrincipal.Scopes, authn.ScopePostsAdmin)
		}
	})
}

// TestServer_HandlerOptions_RejectsUnscopedProcedure proves a procedure absent from ScopeTable denies every API-key caller.
func TestServer_HandlerOptions_RejectsUnscopedProcedure(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := apperror.NewReporter(log, false)

	store := fakes.NewAPIKey()
	orgID, projectID := uuid.New(), uuid.New()
	key, plain, err := apikey.Scheme{}.Mint(orgID, projectID, "everything", authn.AllScopes(), nil, nil, time.Now().UTC())
	if err != nil {
		t.Fatalf("mint key: %v", err)
	}
	store.Seed(key)

	chain := authn.Chain{apikey.NewAuthenticator(store, nil, apikey.Scheme{}), tokens.NewAuthenticator(alwaysOKVerifier{})}
	s := &Server{
		Kernel:    &platform.Kernel{Log: log, Reporter: reporter},
		Authn:     chain,
		KeyPrefix: apikey.DefaultPrefix,
	}

	const procedure = "/test.UndeclaredService/DoSomething"
	handler := connect.NewUnaryHandlerSimple(
		procedure,
		func(_ context.Context, _ *blogv1.DeletePostRequest) (*blogv1.DeletePostResponse, error) {
			return &blogv1.DeletePostResponse{}, nil
		},
		s.handlerOptions()...,
	)
	mux := http.NewServeMux()
	mux.Handle(procedure, handler)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	client := connect.NewClient[blogv1.DeletePostRequest, blogv1.DeletePostResponse](http.DefaultClient, ts.URL+procedure)
	req := connect.NewRequest(&blogv1.DeletePostRequest{PostId: uuid.NewString()})
	req.Header().Set("Authorization", "Bearer "+plain)

	if _, err := client.CallUnary(t.Context(), req); err == nil {
		t.Fatal("expected an undeclared procedure to deny a key with every scope, got success")
	}
}

// TestServer_HandlerOptions_AcceptsAnyConfiguredKeyPrefix proves the control-plane shape gate and the resolver share one prefix.
func TestServer_HandlerOptions_AcceptsAnyConfiguredKeyPrefix(t *testing.T) {
	for _, configured := range []string{"", "key_", "ak_", "sk_live_"} {
		t.Run("prefix="+configured, func(t *testing.T) {
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			scheme := apikey.NewScheme(configured)
			store := fakes.NewAPIKey()

			key, plain, err := scheme.Mint(uuid.New(), uuid.New(), "probe", []string{authn.ScopePostsAdmin}, nil, nil, time.Now().UTC())
			if err != nil {
				t.Fatalf("mint: %v", err)
			}
			store.Seed(key)

			keyAuthn := apikey.NewAuthenticator(store, nil, scheme)
			s := &Server{
				Kernel:    &platform.Kernel{Log: log, Reporter: apperror.NewReporter(log, false)},
				Authn:     authn.Chain{keyAuthn},
				KeyPrefix: keyAuthn.Scheme().Prefix(),
			}

			const procedure = blogv1connect.BlogServiceDeletePostProcedure
			mux := http.NewServeMux()
			mux.Handle(procedure, connect.NewUnaryHandlerSimple(
				procedure,
				func(_ context.Context, _ *blogv1.DeletePostRequest) (*blogv1.DeletePostResponse, error) {
					return &blogv1.DeletePostResponse{}, nil
				},
				s.handlerOptions()...,
			))
			ts := httptest.NewServer(mux)
			t.Cleanup(ts.Close)

			req := connect.NewRequest(&blogv1.DeletePostRequest{PostId: uuid.NewString()})
			req.Header().Set("Authorization", "Bearer "+plain)
			client := connect.NewClient[blogv1.DeletePostRequest, blogv1.DeletePostResponse](http.DefaultClient, ts.URL+procedure)
			if _, err := client.CallUnary(t.Context(), req); err != nil {
				t.Fatalf("api.keyPrefix %q: a key this deployment minted was refused at the boundary: %v", configured, err)
			}
		})
	}
}
