package boot_test

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/boot"
)

func workerNames(srv *boot.Server) []string {
	var out []string
	for _, w := range srv.Supervisor.Workers() {
		out = append(out, w.Name())
	}
	return out
}

func TestBoot_RegistersTheWhatsAppRuntimeOnlyInFullServeMode(t *testing.T) {
	cfg := newSmokeCfg(t)
	full, err := boot.BootServer(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = full.Close() })
	require.Contains(t, workerNames(full), "whatsapp.runtime")
	require.NotNil(t, full.Devices)
	require.NotNil(t, full.WhatsApp)
	require.NotNil(t, full.Runtime)

	schedCfg := newSmokeCfg(t)
	schedCfg.Scheduler.Enabled = true
	schedOnly, err := boot.BootServer(context.Background(), schedCfg, boot.WithSchedulerOnly(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = schedOnly.Close() })
	require.False(t, slices.Contains(workerNames(schedOnly), "whatsapp.runtime"),
		"--scheduler-only must never open WhatsApp sessions")
}
