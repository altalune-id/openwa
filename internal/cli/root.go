// Package cli builds the cobra command tree — every command is a factory so tests get a fresh, isolated tree.
package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"altalune.id/openwa/internal/boot"
	"altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/reqid"
	"altalune.id/openwa/version"
)

// ServerBootFn boots the fully-wired server graph (DB, migrations, services).
type ServerBootFn func(ctx context.Context, cfg *config.Config, opts ...boot.Option) (*boot.Server, error)

// ClientBootFn boots the minimal client graph remote subcommands use to call the server at baseURL with token.
type ClientBootFn func(ctx context.Context, cfg *config.Config, baseURL, token string) (*boot.Client, error)

type ctxKey int

const ctxKeyConfig ctxKey = iota + 1

const operatorAnnotation = "openwa/operator"

// NewRootCmd builds a fresh command tree bound to the given boot fns.
func NewRootCmd(bootServer ServerBootFn, bootClient ClientBootFn) *cobra.Command {
	var (
		configPath    string
		url           string
		token         string
		tokenFile     string
		profile       string
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
	p.StringVarP(&configPath, "config", "c", "", "config file (yaml). Env ("+config.EnvPrefix+"_*) still applies; -c makes yaml explicit.")
	p.StringVar(&url, "url", "", "base URL of the openwa instance to talk to (also: "+config.EnvVar("url")+")")
	p.StringVar(&token, "token", "", "bearer token (also: "+config.EnvVar("token")+")")
	p.StringVar(&tokenFile, "token-file", "", "read the bearer token from a file")
	p.StringVar(&profile, "profile", "", "signed-in profile to use (also: "+config.EnvVar("profile")+"; default \"default\")")
	p.StringVar(&output, "output", "", "output format: text|json|ndjson (also: "+config.EnvVar("output")+")")
	p.StringVar(&orgSlug, "org", "", "override active org (slug) (also: "+config.EnvVar("org")+")")
	p.StringVar(&projectSlug, "project", "", "override active project (slug) (also: "+config.EnvVar("project")+")")
	p.BoolVar(&noInteractive, "no-interactive", false, "never prompt; fail if a prompt would be needed")
	p.StringVar(&logLevel, "log-level", "info", "log level: debug|info|warn|error (also: "+config.EnvVar("log.level")+")")
	p.StringVar(&logFormat, "log-format", "json", "log format: json|text (also: "+config.EnvVar("log.format")+")")

	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if cmd.Name() == "help" || cmd.Name() == "completion" {
			return nil
		}
		if err := refuseRemoteFlags(cmd); err != nil {
			return err
		}
		var opts []config.Option
		if cmd.Annotations[operatorAnnotation] != "true" {
			opts = append(opts, config.WithoutValidation())
		}
		cfg, err := config.Load(configPath, opts...)
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
		markOperator(newServeCmd(bootServer)),
		markOperator(newInitCmd(bootServer)),
		markOperator(newMigrateCmd(bootServer)),
		markOperator(newSchedulerCmd(bootServer)),
		newAuthCmd(bootServer, bootClient),
		markOperator(newOrgCmd(bootServer)),
		markOperator(newProjectCmd(bootServer)),
		markOperator(newInviteCmd(bootServer)),
		newDeviceCmd(bootClient),
		newSendCmd(bootClient),
		newChatCmd(bootClient),
		newMessageCmd(bootClient),
		newContactCmd(bootClient),
		newGroupCmd(bootClient),
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

func markOperator(cmd *cobra.Command) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[operatorAnnotation] = "true"
	for _, sub := range cmd.Commands() {
		markOperator(sub)
	}
	return cmd
}

// NOTE: --profile stays allowed on operator commands, because it picks the local sign-in.
func refuseRemoteFlags(cmd *cobra.Command) error {
	if cmd.Annotations[operatorAnnotation] != "true" {
		return nil
	}
	for _, name := range []string{"url", "token", "token-file"} {
		if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
			return &RemoteFlagError{Command: strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" "), Flag: name}
		}
	}
	return nil
}
