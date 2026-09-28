package boot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/controlplane"
	mcpinternal "altalune.id/template/internal/mcp"
	"altalune.id/template/internal/mcp/ui"
	"altalune.id/template/internal/platform/authn"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/tokens"
	"altalune.id/template/internal/user"
	rootmcp "altalune.id/template/mcp"
	"altalune.id/template/version"
)

const mcpServerName = "altempl"

type mcpSurface struct {
	Handler         http.Handler
	Metadata        http.Handler
	MetadataPath    string
	ChallengeRoutes map[string]http.Handler
	Server          *rootmcp.Server
}

// SECURITY: the JWT link is built on a second tokens.Verifier pinned to mcp.audience, never the kernel's control-plane verifier, so a token minted for one resource is refused at the other (RFC 8707).
func buildMCPSurface(ctx context.Context, cfg *config.Config, log *slog.Logger, s *Services, apiSrv *controlplane.Server) (mcpSurface, error) {
	if !cfg.MCP.Enabled {
		return mcpSurface{}, nil
	}
	surface, err := mcpinternal.NewSurface(cfg.MCP.Audience)
	if err != nil {
		return mcpSurface{}, fmt.Errorf("boot: mcp surface: %w", err)
	}
	verifier, err := tokens.NewVerifier(ctx, mcpTokensConfig(cfg))
	if err != nil {
		return mcpSurface{}, fmt.Errorf("boot: mcp verifier: %w", err)
	}
	chain := authn.Chain{
		s.KeyAuthn,
		user.NewAuthenticator(
			tokens.NewAuthenticator(verifier),
			s.UserStore,
			user.WithTenantResolution(
				orgStoreForOnboard{store: s.OrgStore},
				projectStoreForOnboard{store: s.ProjectStore},
			),
		),
	}

	registry := rootmcp.NewRegistry()
	if err := registerTools(registry, apiSrv); err != nil {
		return mcpSurface{}, fmt.Errorf("boot: mcp tools: %w", err)
	}

	opts := []rootmcp.Option{
		rootmcp.WithImplementation(mcpServerName, version.Default()),
		rootmcp.WithRegistry(registry),
		rootmcp.WithScopes(mcpinternal.ScopesFromContext),
		rootmcp.WithErrorMapper(mcpErrorPayload),
		rootmcp.WithLogger(log),
		rootmcp.WithUI(cfg.MCP.AppsUI),
	}
	srv := rootmcp.NewServer(opts...)
	if cfg.MCP.AppsUI {
		srv.AddUIResource(blogListResource())
	}

	challenge, err := mcpinternal.ChallengeRoutes(cfg.HTTP.BasePath, cfg.MCP.ChallengePrefix, cfg.MCP.ChallengeToken)
	if err != nil {
		return mcpSurface{}, fmt.Errorf("boot: mcp challenge: %w", err)
	}
	if len(challenge) == 0 {
		// NOTE: the authorization server re-checks the proof on a schedule, so a missing token
		// fails admission long after boot looks healthy; say so now rather than at a support desk.
		log.Warn("boot: mcp challenge token unset — host control cannot be verified",
			slog.String("set", "ALT_MCP_CHALLENGE_TOKEN"))
	}

	log.Info("boot: mcp surface mounted",
		slog.String("audience", cfg.MCP.Audience),
		slog.String("metadata", surface.MetadataPath),
		slog.Int("tools", len(registry.Names())),
		slog.Bool("ui", cfg.MCP.AppsUI))

	guard := mcpinternal.Authenticate(chain, s.KeyAuthn.Scheme().Authn(), surface.MetadataURL, log)
	return mcpSurface{
		Handler:         guard(srv.Handler()),
		Metadata:        mcpinternal.MetadataHandler(surface.Resource, []string{cfg.Tokens.Issuer}, registeredScopes(registry)),
		MetadataPath:    surface.MetadataPath,
		ChallengeRoutes: challenge,
		Server:          srv,
	}, nil
}

// NOTE: app.css paints a transparent body and .app-card draws its own border, so a host-drawn frame would double up on every card.
func blogListResource() rootmcp.UIResource {
	return rootmcp.UIResource{
		URI:           ui.ResourceURI,
		Name:          "Altempl app",
		Body:          ui.Document(),
		PrefersBorder: new(bool),
	}
}

func mcpTokensConfig(cfg *config.Config) tokens.Config {
	c := cfg.Tokens
	c.Audience = cfg.MCP.Audience
	return c
}

func registeredScopes(reg *rootmcp.Registry) []string {
	scopes := map[string]struct{}{}
	for _, name := range reg.Names() {
		spec, ok := reg.Spec(name)
		if !ok || spec.Scope == "" {
			continue
		}
		scopes[spec.Scope] = struct{}{}
	}
	return slices.Sorted(maps.Keys(scopes))
}

// SECURITY: anything TranslateError does not claim stays unmapped, so the root server logs the cause and answers a generic payload instead of handing the caller a raw Go error.
func mcpErrorPayload(ctx context.Context, err error) rootmcp.ErrorPayload {
	appErr, ok := apperror.AsAppError(mcpinternal.TranslateError(err))
	if !ok {
		return rootmcp.ErrorPayload{}
	}
	payload := rootmcp.ErrorPayload{Code: appErr.Code(), Message: appErr.Message()}
	if denied, ok := errors.AsType[*rootmcp.ScopeDeniedError](err); ok {
		payload.Meta = map[string]string{"tool": denied.Tool, "scope": denied.Scope}
	}
	return mcpinternal.AttachContext(ctx, payload)
}
