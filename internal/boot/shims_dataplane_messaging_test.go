package boot

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
)

// SECURITY: the S3 hijack tests model a sibling-project device as not found; this pins that the real service behind the shim agrees.
func TestDataplaneShims_SiblingProjectDeviceIsNotFound(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	svc := device.NewService(fakes.NewDevice(), log, apperror.NewReporter(log, false).Unexpected, fakes.UnitOfWork, fakes.NewDeviceSessions())
	org := uuid.New()
	sibling := tenant.Into(t.Context(), tenant.Context{OrgID: org, ProjectID: uuid.New()})
	d, err := svc.Create(sibling, "Sibling")
	require.NoError(t, err)
	shim := deviceServiceForDataplane{svc: svc}

	own := tenant.Into(t.Context(), tenant.Context{OrgID: org, ProjectID: uuid.New()})
	_, err = shim.Resolve(own, d.PublicID)
	require.True(t, device.IsNotFoundError(err), "another project of the same org never resolves the device")
	got, err := shim.Resolve(sibling, d.PublicID)
	require.NoError(t, err)
	require.Equal(t, d.ID, got.ID)
}

type countingLookup struct {
	calls int
	got   []uuid.UUID
}

func (c *countingLookup) PublicIDs(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	c.calls++
	c.got = ids
	out := map[uuid.UUID]string{}
	for _, id := range ids {
		out[id] = "dev_" + id.String()[:8]
	}
	return out, nil
}

func TestPublicIDsOf_OneDedupedReadAndNoneForAnEmptyPage(t *testing.T) {
	l := &countingLookup{}
	a, b := uuid.New(), uuid.New()
	got, err := publicIDsOf(t.Context(), l, []uuid.UUID{a, b, a, a})
	require.NoError(t, err)
	require.Equal(t, 1, l.calls, "a page costs one batch read, never one per row")
	require.Len(t, l.got, 2)
	require.Len(t, got, 2)

	empty, err := publicIDsOf(t.Context(), l, nil)
	require.NoError(t, err)
	require.Empty(t, empty)
	require.Equal(t, 1, l.calls, "an empty page reads nothing")
}
