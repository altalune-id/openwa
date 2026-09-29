// Package cli builds the cobra command tree — every command is a factory so tests get a fresh, isolated tree.
package cli

import (
	"context"

	"github.com/spf13/cobra"

	"altalune.id/openwa/internal/boot"
	"altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/reqid"
	"altalune.id/openwa/version"
)

// ServerBootFn boots the fully-wired server graph (DB, migrations, services).
type ServerBootFn func(ctx context.Context, cfg *config.Config, opts ...boot.Option) (*boot.Server, error)

// ClientBootFn boots the minimal client graph used by remote-facing subcommands.
type ClientBootFn func(ctx context.Context, cfg *config.Config, token string) (*boot.Client, error)

type ctxKey int

const (
	ctxKeyConfig ctxKey = iota + 1
	ctxKeyPrincipal
)

// NewRootCmd builds a fresh command tree bound to the given boot fns.
func NewRootCmd(bootServer ServerBootFn, bootClient ClientBootFn) *cobra.Command {
	var (
		configPath    string
		url           string
		token         string
		tokenFile     string
		output        string
		orgSlug       string
		projectSlug   string
		noInteractive bool
		logLevel      string
		logFormat     string
	)

	root := &cobra.Command{
		Use:           "openwa",
		Short:         "openwa is a multitenant WhatsApp gateway",
		Long:          "openwa is a multitenant WhatsApp gateway — Templ + HTMX SSR and Connect-RPC over a single HTTP listener.",
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	p := root.PersistentFlags()
	p.StringVarP(&configPath, "config", "c", "", "config file (yaml). Env (OPENWA_*) still applies; -c makes yaml explicit.")
	p.StringVar(&url, "url", "", "base URL of the openwa instance to talk to (also: OPENWA_URL)")
	p.StringVar(&token, "token", "", "bearer token (also: OPENWA_TOKEN)")
	p.StringVar(&tokenFile, "token-file", "", "path to file containing the bearer token (0600) (also: OPENWA_TOKEN_FILE)")
	p.StringVar(&output, "output", "", "output format: text|json|ndjson (also: OPENWA_OUTPUT)")
	p.StringVar(&orgSlug, "org", "", "override active org (slug) (also: OPENWA_ORG)")
	p.StringVar(&projectSlug, "project", "", "override active project (slug) (also: OPENWA_PROJECT)")
	p.BoolVar(&noInteractive, "no-interactive", false, "never prompt; fail if a prompt would be needed")
	p.StringVar(&logLevel, "log-level", "info", "log level: debug|info|warn|error (also: OPENWA_LOG_LEVEL)")
	p.StringVar(&logFormat, "log-format", "json", "log format: json|text (also: OPENWA_LOG_FORMAT)")

	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if cmd.Name() == "help" || cmd.Name() == "completion" {
			return nil
		}
		cfg, err := config.Load(configPath)
		if err != nil {
			return err
		}
		ctx, _ := reqid.Ensure(cmd.Context())
		ctx = ctxWithConfig(ctx, cfg)
		cmd.SetContext(ctx)
		return nil
	}

	root.AddGroup(
		&cobra.Group{ID: "runtime", Title: "Runtime:"},
		&cobra.Group{ID: "auth", Title: "Auth:"},
		&cobra.Group{ID: "tenancy", Title: "Tenancy:"},
		&cobra.Group{ID: "domain", Title: "Domain:"},
		&cobra.Group{ID: "meta", Title: "Meta:"},
	)

	root.AddCommand(
		newServeCmd(bootServer),
		newInitCmd(bootServer),
		newMigrateCmd(bootServer),
		newSchedulerCmd(bootServer),
		newAuthCmd(bootServer, bootClient),
		newOrgCmd(bootServer, bootClient),
		newProjectCmd(bootServer, bootClient),
		newInviteCmd(bootServer, bootClient),
		newVersionCmd(),
		newHealthzCmd(),
		newCompletionCmd(),
	)
	registerURLFlagCompletion(root)
	return root
}

func ctxWithConfig(ctx context.Context, cfg *config.Config) context.Context {
	return context.WithValue(ctx, ctxKeyConfig, cfg)
}

func configFromCtx(ctx context.Context) *config.Config {
	cfg, _ := ctx.Value(ctxKeyConfig).(*config.Config)
	return cfg
}

func ctxWithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKeyPrincipal, p)
}

func principalFromCtx(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKeyPrincipal).(Principal)
	return p, ok
}
