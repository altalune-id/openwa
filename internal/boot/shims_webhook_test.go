package boot

import (
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/webhook"
)

// SECURITY: the fake device store scopes by org only, so the service's project check behind the shim is the only thing refusing a sibling project's number.
func TestWebhookDevicesShim_SiblingProjectDeviceIsRefused(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	svc := device.NewService(fakes.NewDevice(), log, apperror.NewReporter(log, false).Unexpected, fakes.UnitOfWork, fakes.NewDeviceSessions(), "")
	org := uuid.New()
	sibling := tenant.Into(t.Context(), tenant.Context{OrgID: org, ProjectID: uuid.New()})
	d, err := svc.Create(sibling, "Sibling")
	require.NoError(t, err)
	shim := &webhookDevices{svc: svc}

	own := tenant.Into(t.Context(), tenant.Context{OrgID: org, ProjectID: uuid.New()})
	_, err = shim.ResolveID(own, d.PublicID)
	require.True(t, webhook.IsDeviceNotFoundError(err), "got %T: %v", err, err)
	_, err = shim.ResolveID(own, "not-a-public-id")
	require.True(t, webhook.IsDeviceNotFoundError(err), "got %T: %v", err, err)

	got, err := shim.ResolveID(sibling, d.PublicID)
	require.NoError(t, err)
	require.Equal(t, d.ID, got)
}
