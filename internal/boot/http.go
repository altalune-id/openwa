package boot

import (
	"context"
	"fmt"
	stdlog "log"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"altalune.id/openwa/internal/apikey"
	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/auth"
	"altalune.id/openwa/internal/controlplane"
	"altalune.id/openwa/internal/dataplane"
	"altalune.id/openwa/internal/device"
	i18npkg "altalune.id/openwa/internal/i18n"
	"altalune.id/openwa/internal/ingest"
	"altalune.id/openwa/internal/invite"
	"altalune.id/openwa/internal/legal"
	"altalune.id/openwa/internal/onboard"
	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/platform"
	"altalune.id/openwa/internal/platform/capabilities"
	"altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/project"
	"altalune.id/openwa/internal/user"
	"altalune.id/openwa/internal/web"
	webhandlers "altalune.id/openwa/internal/web/handlers"
	webmw "altalune.id/openwa/internal/web/middleware"
	"altalune.id/openwa/internal/webhook"
	"altalune.id/openwa/version"
)

func buildAPIHandler(cfg *config.Config, k *platform.Kernel, s *Services) (*controlplane.Server, http.Handler) {
	srv := controlplane.New(cfg, k, s.Auth, s.Users, s.Orgs, s.Projects, s.Todos, s.Invites, s.TodoStore, s.Posts, s.Categories, s.Tags, s.Devices)
	srv.Authn = s.Authn
	srv.KeyPrefix = s.KeyAuthn.Scheme().Prefix()
	srv.APIKeys = s.APIKeys
	if !cfg.API.Enabled {
		return srv, nil
	}
	h := srv.Handler(cfg.HTTP.BasePath)
	return srv, h
}

func buildDataHandler(cfg *config.Config, caps capabilities.Capabilities, slogger *slog.Logger, s *Services) http.Handler {
	if !caps.DataPlaneEnabled {
		return nil
	}
	return dataplane.NewHandler(dataplane.HandlerParams{
		BasePath: web.Path(cfg.HTTP.BasePath, "/api") + "/v1",
		Orgs:     orgServiceForDataplane{svc: s.Orgs},
		Projects: projectServiceForDataplane{svc: s.Projects},
		Posts:    blogServiceForDataplane{svc: s.Posts},
		Devices:  deviceServiceForDataplane{svc: s.Devices},
		Authz:    s.KeyAuthn,
		Caps:     caps,
		Log:      slogger,
	})
}

// NOTE: always mounted, so /hooks/ is reserved rather than reaching the console chain.
func buildIngestHandler(cfg *config.Config, log *slog.Logger) http.Handler {
	return ingest.NewHandler(ingest.HandlerParams{
		BasePath: web.Path(cfg.HTTP.BasePath, "/hooks"),
		Log:      log,
	})
}

func buildWebHandler(
	cfg *config.Config,
	kernel *platform.Kernel,
	caps capabilities.Capabilities,
	slogger *slog.Logger,
	reporter *apperror.Reporter,
	healthOK func() bool,
	auths *auth.Service,
	users *user.Service,
	orgs *org.Service,
	projects *project.Service,
	invites *invite.Service,
	onboards *onboard.Service,
	apiKeys *apikey.Service,
	webhooks *webhook.Service,
	devices *device.Service,
	required *atomic.Bool,
	onComplete func(ctx context.Context),
	setupToken string,
	apiHandler http.Handler,
	dataHandler http.Handler,
	mcp mcpSurface,
	bundle *i18npkg.Bundle,
	defaultLoc i18npkg.Locale,
) (handler http.Handler, routes []string) { //nolint:nonamedreturns // two return values differ in role
	deps := newWebDeps(cfg, caps, kernel.Sessions, slogger)
	termsSince := termsUpdatedAt(cfg, slogger)
	deps.TermsUpdatedAt = termsSince
	deps.Orgs = orgs
	deps.Projects = projects
	deps.I18n = bundle

	authHandler := webhandlers.NewAuthHandler(deps, auths, users, orgs, projects, kernel.AltAuth, required)
	onboardingHandler := webhandlers.NewOnboardingHandler(deps, users)
	onboardHandler := webhandlers.NewOnboardHandler(deps, users, orgs, projects, onboards, required, onComplete, setupToken)
	homeHandler := webhandlers.NewHomeHandler(deps, orgs, projects)
	orgHandler := webhandlers.NewOrgHandler(deps, orgs)
	projectHandler := webhandlers.NewProjectHandler(deps, projects)
	overviewHandler := webhandlers.NewProjectOverviewHandler(deps, projects, devices)
	deviceHandler := webhandlers.NewDeviceHandler(deps, projects, devices)
	apiKeyHandler := webhandlers.NewAPIKeyHandler(deps, projects, apiKeys)
	webhookHandler := webhandlers.NewWebhookHandler(deps, projects, webhooks)
	inviteHandler := webhandlers.NewInviteHandler(deps, orgs, invites)
	localeHandler := webhandlers.NewLocaleHandler(deps, users)
	welcomeHandler := webhandlers.NewWelcomeHandler(deps, users)
	signupHandler := webhandlers.NewSignupHandler(deps, users, orgs, projects)
	legalHandler := webhandlers.NewLegalHandler(deps)

	errTmpl := webmw.LogError{Log: slogger}

	return web.NewServerWithRoutes(web.ServerOpts{
		BasePath: cfg.HTTP.BasePath,
		HealthOK: healthOK,
		AppHandlers: []web.Register{
			authHandler, onboardingHandler, onboardHandler, homeHandler, orgHandler, projectHandler, overviewHandler, deviceHandler, apiKeyHandler, webhookHandler, inviteHandler, localeHandler, welcomeHandler, signupHandler, legalHandler,
		},
		APIHandler:         apiHandler,
		DataHandler:        dataHandler,
		IngestHandler:      buildIngestHandler(cfg, slogger),
		MCPHandler:         mcp.Handler,
		MCPMetadataHandler: mcp.Metadata,
		MCPMetadataPath:    mcp.MetadataPath,
		MCPChallengeRoutes: mcp.ChallengeRoutes,
		RobotsCfg:          &struct{ RobotsTxt string }{RobotsTxt: cfg.HTTP.RobotsTxt},
		Chains:             surfaceChains(cfg, kernel, slogger, reporter, errTmpl, bundle, defaultLoc, required, termsSince),
	})
}

func surfaceChains(
	cfg *config.Config,
	kernel *platform.Kernel,
	slogger *slog.Logger,
	reporter *apperror.Reporter,
	errTmpl webmw.ErrorTemplate,
	bundle *i18npkg.Bundle,
	defaultLoc i18npkg.Locale,
	required *atomic.Bool,
	termsSince time.Time,
) web.SurfaceChains {
	edge := []web.Middleware{
		webmw.RequestID,
		webmw.RequestLog(slogger),
		webmw.OTel,
	}
	return web.SurfaceChains{
		Probes: slices.Concat(edge, []web.Middleware{
			webmw.Recover(reporter.Unexpected, nil),
		}),
		Console: slices.Concat(edge, []web.Middleware{
			webmw.CSP(cspOptions(cfg.HTTP.CSP)),
			webmw.Recover(reporter.Unexpected, errTmpl),
			webmw.Session(webmw.SessionConfig{
				Store:  kernel.Sessions,
				Secret: []byte(cfg.HTTP.StateSecret),
			}),
			webmw.Tenant,
			i18npkg.Middleware(i18npkg.MiddlewareOpts{
				Bundle:     bundle,
				Default:    defaultLoc,
				UserLookup: sessionLocaleLookup,
			}),
			webhandlers.OnboardingGate(cfg.HTTP.BasePath, required),
			webhandlers.WelcomeGate(cfg.HTTP.BasePath, cfg.Compliance.RequireAcceptance, termsSince),
			webmw.Flash([]byte(cfg.HTTP.StateSecret), cfg.HTTP.BasePath, cfg.HTTP.CookieSecure),
		}),
		Control: edge,
		Data: slices.Concat(edge, []web.Middleware{
			webmw.RecoverJSON(reporter.Unexpected),
		}),
		Ingest: slices.Concat(edge, []web.Middleware{
			webmw.RecoverJSON(reporter.Unexpected),
		}),
		MCP: slices.Concat(edge, []web.Middleware{
			webmw.RecoverJSON(reporter.Unexpected),
		}),
	}
}

func healthOnlyHandler(cfg *config.Config, healthOK func() bool) http.Handler {
	return web.NewServer(web.ServerOpts{
		BasePath:  cfg.HTTP.BasePath,
		HealthOK:  healthOK,
		RobotsCfg: &struct{ RobotsTxt string }{RobotsTxt: cfg.HTTP.RobotsTxt},
	})
}

func buildI18nBundle(cfg *config.Config) (*i18npkg.Bundle, i18npkg.Locale, error) {
	tag := cfg.I18n.DefaultLocale
	if tag == "" {
		tag = string(i18npkg.EnUS)
	}
	tmp := i18npkg.NewEmbeddedBundle(i18npkg.EnUS)
	loc, err := tmp.Parse(tag)
	if err != nil {
		return nil, "", fmt.Errorf("i18n: default locale %q not among embedded locales", tag)
	}
	return i18npkg.NewEmbeddedBundle(loc), loc, nil
}

func sessionLocaleLookup(ctx context.Context) string {
	return session.PrincipalFrom(ctx).Locale
}

func newWebDeps(cfg *config.Config, caps capabilities.Capabilities, sessions session.Store, slogger *slog.Logger) webhandlers.Deps {
	return webhandlers.Deps{
		Cfg:      cfg,
		Caps:     caps,
		Sessions: sessions,
		Logger:   stdlog.New(logSlogWriter{log: slogger}, "", 0),

		AssetVersion: assetVersion(),
	}
}

func assetVersion() string {
	if version.Version == "" {
		return strconv.FormatInt(time.Now().Unix(), 10)
	}
	return version.Default()
}

type logSlogWriter struct{ log *slog.Logger }

func (w logSlogWriter) Write(p []byte) (int, error) {
	if w.log != nil {
		w.log.Info(strings.TrimRight(string(p), "\n"))
	}
	return len(p), nil
}

func cspOptions(cfg config.CSPConfig) webmw.CSPOptions {
	return webmw.CSPOptions{
		Enabled:    cfg.Enabled,
		ReportOnly: cfg.ReportOnly,
		ReportURI:  cfg.ReportURI,
	}
}

func termsUpdatedAt(cfg *config.Config, log *slog.Logger) time.Time {
	if strings.TrimSpace(cfg.Compliance.TermsURL) != "" {
		return time.Time{}
	}
	doc, err := legal.Terms()
	if err != nil {
		log.Error("boot: terms document failed to parse; re-acceptance disabled", "err", err)
		return time.Time{}
	}
	return doc.UpdatedAt
}
