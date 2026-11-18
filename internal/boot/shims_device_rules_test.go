package boot

import (
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/dataplane"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
)

// An integrator's GET, edit allowed_senders, PATCH reaches the shim as a legacy-only write; on a builder-made device it is refused and the items stay.
func TestDeviceServiceForDataplane_LegacyPatchOnABuilderDeviceConflicts(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	svc := device.NewService(fakes.NewDevice(), log, apperror.NewReporter(log, false).Unexpected, fakes.UnitOfWork, fakes.NewDeviceSessions(), "")
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()})
	d, err := svc.Create(ctx, "Toko Sari Admin")
	require.NoError(t, err)
	built := device.Rules{IgnoreFromMe: true, Items: append(device.DefaultRules().Items, device.Rule{
		ID: "toko", Name: "Toko Admin and Sari", Enabled: true, Action: device.ActionMatch, Conditions: []device.Condition{
			{Field: device.FieldChatKind, Op: device.OpIs, Values: []string{"group"}},
			{Field: device.FieldChat, Op: device.OpIs, Values: []string{"120363001@g.us"}},
			{Field: device.FieldSender, Op: device.OpIs, Values: []string{"phone:6281111222333"}},
		},
	})}
	_, err = svc.Update(ctx, d.ID, device.Update{Rules: &built}, 0)
	require.NoError(t, err)
	shim := deviceServiceForDataplane{svc: svc}

	read, err := shim.Get(ctx, d.ID)
	require.NoError(t, err)
	require.Equal(t, "custom", read.Rules.GroupMode)
	patch := dataplane.RulesRef{GroupMode: read.Rules.GroupMode, AllowedSenders: []string{"628111222333"}, IgnoreFromMe: read.Rules.IgnoreFromMe}

	_, err = shim.Update(ctx, d.ID, nil, &patch, read.Version)
	require.True(t, device.IsRulesShapeConflictError(err), "got %v", err)
	after, err := shim.Get(ctx, d.ID)
	require.NoError(t, err)
	require.Equal(t, read.Version, after.Version)
	require.Equal(t, read.Rules.Items, after.Rules.Items, "items unchanged")

	patch.GroupMode = "open"
	_, err = shim.Update(ctx, d.ID, nil, &patch, read.Version)
	require.True(t, device.IsRulesShapeConflictError(err), "a client that also sets a group mode is refused too")
}
