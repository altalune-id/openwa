package boot

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/prototext"

	"altalune.id/openwa/internal/apikey"
	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/whatsapp"
)

func TestDeviceShims_MapEveryField(t *testing.T) {
	t.Parallel()
	seen := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	require.Equal(t,
		device.SessionStatus{State: device.SessionConnected, Phone: "628111", PushName: "Ops", Reason: "x", LastSeen: &seen, Live: true},
		toDeviceStatus(whatsapp.Status{State: whatsapp.StateConnected, Phone: "628111", PushName: "Ops", Reason: "x", LastSeen: &seen, Live: true}))

	exp := seen.Add(time.Minute)
	got := toDeviceLink(whatsapp.LinkState{ID: "lnk_V1StGXR8Z5jdHi6B", Method: whatsapp.LinkMethodPhone, Outcome: whatsapp.OutcomeFailed, QR: "2@x", PNG: []byte{1},
		PairingCode: "ABCD-EFGH", ExpiresAt: exp, StartedAt: seen, Err: errors.New("pair failed")})
	require.Equal(t, device.LinkState{ID: "lnk_V1StGXR8Z5jdHi6B", Method: device.LinkPhone, Outcome: device.LinkFailed, QR: "2@x", PNG: []byte{1},
		PairingCode: "ABCD-EFGH", ExpiresAt: exp, StartedAt: seen, Error: "pair failed"}, got)
	require.Empty(t, toDeviceLink(whatsapp.LinkState{Outcome: whatsapp.OutcomeNone}).Error)
}

// SECURITY: S3 answers from what the shim returns, so neither the error text nor its envelope may name the device UUID.
func TestDeviceServiceForDataplane_ErrorsNeverCarryTheDeviceUUID(t *testing.T) {
	t.Parallel()
	store, sessions := fakes.NewDevice(), fakes.NewDeviceSessions()
	log := slog.New(slog.DiscardHandler)
	svc := device.NewService(store, log, apperror.NewReporter(log, false).Unexpected, fakes.UnitOfWork, sessions)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()})
	d, err := svc.Create(ctx, "Sales")
	require.NoError(t, err)
	shim := deviceServiceForDataplane{svc: svc}

	sessions.LinkErr = &whatsapp.AlreadyLinkedError{ID: d.ID.String()}
	_, err = shim.StartLink(ctx, d.ID)
	require.True(t, whatsapp.IsAlreadyLinkedError(err))
	requireNoDeviceUUID(t, err, d.ID)

	store.ByIDFn = func(_ context.Context, id uuid.UUID) (*device.Device, error) {
		return nil, &device.NotFoundError{ID: id.String()}
	}
	err = shim.Unlink(ctx, d.ID)
	require.True(t, device.IsNotFoundError(err))
	requireNoDeviceUUID(t, err, d.ID)
}

func requireNoDeviceUUID(t *testing.T, err error, id uuid.UUID) {
	t.Helper()
	require.NotContains(t, err.Error(), id.String())
	carrier, ok := errors.AsType[interface {
		error
		ToAppError() *apperror.AppError
	}](err)
	require.True(t, ok, "a typed error with an envelope, got %T", err)
	for _, det := range carrier.ToAppError().Details() {
		require.NotContains(t, prototext.Format(det), id.String())
	}
}

func TestAPIKeyDevicesShimPinsThePrefix(t *testing.T) {
	t.Parallel()
	require.Equal(t, device.PublicIDPrefix, apikey.DevicePrefix, "apikey mirrors the device prefix without importing device")
}
