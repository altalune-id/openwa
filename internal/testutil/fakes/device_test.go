package fakes_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
)

func TestDeviceFake_MatchesTheRealStoreVersionContract(t *testing.T) {
	t.Parallel()
	org, proj := uuid.New(), uuid.New()
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: org, ProjectID: proj})
	f := fakes.NewDevice()
	d, err := device.New(org, proj, fakes.DevicePublicID(), "x")
	require.NoError(t, err)
	require.NoError(t, f.Save(ctx, d, 0))
	require.NoError(t, f.Save(ctx, d, 1))
	got, err := f.ByID(ctx, d.ID)
	require.NoError(t, err)
	require.Equal(t, 2, got.Version)
	require.True(t, device.IsStaleVersionError(f.Save(ctx, d, 1)))

	other := tenant.Into(t.Context(), tenant.Context{OrgID: uuid.New(), ProjectID: proj})
	_, err = f.ByID(other, d.ID)
	require.True(t, device.IsNotFoundError(err))
	require.True(t, device.IsNotFoundError(f.Save(other, d, 0)))
}
