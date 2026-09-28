package boot

import (
	"context"
	"fmt"
	stdlog "log"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"

	"altalune.id/template/internal/apikey"
	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/auth"
	"altalune.id/template/internal/blog"
	"altalune.id/template/internal/blog/category"
	blogtag "altalune.id/template/internal/blog/tag"
	"altalune.id/template/internal/controlplane"
	"altalune.id/template/internal/dataplane"
	i18npkg "altalune.id/template/internal/i18n"
	"altalune.id/template/internal/ingest"
	"altalune.id/template/internal/invite"
	"altalune.id/template/internal/onboard"
	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform"
	"altalune.id/template/internal/platform/capabilities"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/project"
	"altalune.id/template/internal/todo"
	"altalune.id/template/internal/user"
	"altalune.id/template/internal/web"
	webhandlers "altalune.id/template/internal/web/handlers"
	webmw "altalune.id/template/internal/web/middleware"
	"altalune.id/template/internal/webhook"
)

func buildAPIHandler(cfg *config.Config, k *platform.Kernel, s *Services) (*controlplane.Server, http.Handler) {
	srv := controlplane.New(cfg, k, s.Auth, s.Users, s.Orgs, s.Projects, s.Todos, s.Invites, s.TodoStore, s.Posts, s.Categories, s.Tags)
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
	todos *todo.Service,
	invites *invite.Service,
	onboards *onboard.Service,
	posts *blog.Service,
	cats *category.Service,
	tags *blogtag.Service,
	apiKeys *apikey.Service,
	webhooks *webhook.Service,
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
	deps.Orgs = orgs
	deps.Projects = projects
	deps.I18n = bundle

	authHandler := webhandlers.NewAuthHandler(deps, auths, users, orgs, projects, kernel.AltAuth, required)
	onboardingHandler := webhandlers.NewOnboardingHandler(deps, users)
	onboardHandler := webhandlers.NewOnboardHandler(deps, users, orgs, projects, onboards, required, onComplete, setupToken)
	homeHandler := webhandlers.NewHomeHandler(deps, orgs, projects)
	orgHandler := webhandlers.NewOrgHandler(deps, orgs)
	projectHandler := webhandlers.NewProjectHandler(deps, projects)
	todoHandler := webhandlers.NewTodoHandler(deps, projects, todos)
	blogHandler := webhandlers.NewBlogHandler(deps, projects, posts, cats, tags)
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
			authHandler, onboardingHandler, onboardHandler, homeHandler, orgHandler, projectHandler, todoHandler, blogHandler, apiKeyHandler, webhookHandler, inviteHandler, localeHandler, welcomeHandler, signupHandler, legalHandler,
		},
		APIHandler:         apiHandler,
		DataHandler:        dataHandler,
		IngestHandler:      buildIngestHandler(cfg, slogger),
		MCPHandler:         mcp.Handler,
		MCPMetadataHandler: mcp.Metadata,
		MCPMetadataPath:    mcp.MetadataPath,
		MCPChallengeRoutes: mcp.ChallengeRoutes,
		RobotsCfg:          &struct{ RobotsTxt string }{RobotsTxt: cfg.HTTP.RobotsTxt},
		Chains:             surfaceChains(cfg, kernel, slogger, reporter, errTmpl, bundle, defaultLoc, required),
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
			webhandlers.WelcomeGate(cfg.HTTP.BasePath, cfg.Compliance.RequireAcceptance),
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
	}
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
