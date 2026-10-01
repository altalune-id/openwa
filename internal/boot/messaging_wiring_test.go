package boot_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/boot"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/whatsapp"
)

type nopInbound struct{}

func (nopInbound) RecordInbound(context.Context, whatsapp.SessionRef, whatsapp.InboundMessage) error {
	return nil
}
func (nopInbound) RecordReceipt(context.Context, whatsapp.SessionRef, whatsapp.Receipt) error {
	return nil
}
func (nopInbound) UpsertContact(context.Context, whatsapp.SessionRef, whatsapp.ContactUpdate) error {
	return nil
}

func TestBootServer_WiresMessaging(t *testing.T) {
	cfg := newSmokeCfg(t)
	cfg.Scheduler = config.SchedulerConfig{Enabled: true, Timezone: "UTC", ShutdownGrace: 5 * time.Second}
	srv, err := boot.BootServer(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })

	require.NotNil(t, srv.Chats)
	require.NotNil(t, srv.Contacts)
	require.NotNil(t, srv.Messages)
	require.NotNil(t, srv.Messages.Recorder())
	require.True(t, whatsapp.IsSetupError(srv.Runtime.SetOutbound(&fakes.Outbound{})), "boot already set the outbound port")
	require.True(t, whatsapp.IsSetupError(srv.WhatsApp.SetInbound(nopInbound{})), "boot already set the inbound port")

	var names []string
	for _, j := range srv.Scheduler.Jobs() {
		names = append(names, j.Name)
	}
	require.Contains(t, names, message.RetentionJobName)
}
