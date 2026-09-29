// Command openwa is the multitenant WhatsApp gateway binary. See `openwa --help`.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"altalune.id/openwa/internal/boot"
	"altalune.id/openwa/internal/cli"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := cli.NewRootCmd(boot.BootServer, boot.BootClient)
	if err := root.ExecuteContext(ctx); err != nil {
		slog.ErrorContext(ctx, "openwa", "error", err)
		return cli.ExitCodeFor(err)
	}
	return cli.ExitOK
}
