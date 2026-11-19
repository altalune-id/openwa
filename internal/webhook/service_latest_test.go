package webhook_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/platform/outbox"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/webhook"
)

func TestService_LatestDeliveries(t *testing.T) {
	h := newHarness(t)
	ctx, tc := svcCtx(t)
	pinged, _ := h.create(ctx, t, events.PostPublished)
	quiet, _ := h.create(ctx, t, events.PostPublished)
	require.NoError(t, h.svc.SendTest(ctx, pinged.ID))

	other := tenant.Into(t.Context(), tenant.Context{OrgID: tc.OrgID, ProjectID: uuid.New()})
	h.create(other, t, events.PostPublished)

	got, err := h.svc.LatestDeliveries(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	d, ok := got[pinged.ID]
	require.True(t, ok)
	assert.Equal(t, events.WebhookPing, d.EventType)
	assert.Equal(t, outbox.StatusPending, d.Status)
	_, ok = got[quiet.ID]
	assert.False(t, ok, "an endpoint with no delivery has no entry")
}

func TestDelivery_Failing(t *testing.T) {
	cases := []struct {
		name    string
		status  outbox.Status
		attempt int
		want    bool
	}{
		{"delivered", outbox.StatusDelivered, 1, false},
		{"failed", outbox.StatusFailed, outbox.MaxAttempts, true},
		{"queued, never tried", outbox.StatusPending, 0, false},
		{"pending after a failed try", outbox.StatusPending, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := webhook.Delivery{Entry: outbox.Entry{Status: tc.status, Attempt: tc.attempt}}
			assert.Equal(t, tc.want, d.Failing())
		})
	}
}

func TestService_Delivered(t *testing.T) {
	h := newHarness(t)
	ctx, tc := svcCtx(t)
	e, _ := h.create(ctx, t, events.MessageMatched)
	ok, err := h.svc.Delivered(ctx, events.MessageMatched)
	require.NoError(t, err)
	assert.False(t, ok)

	a := webhook.Attempt{ID: uuid.New(), OrgID: tc.OrgID, ProjectID: tc.ProjectID, EndpointID: e.ID, DeliveryID: uuid.New(), EventID: uuid.New(), EventType: events.MessageMatched, Attempt: 1, StatusCode: 200}
	require.NoError(t, h.store.SaveAttempt(ctx, a))
	ok, err = h.svc.Delivered(ctx, events.MessageMatched)
	require.NoError(t, err)
	assert.True(t, ok)
}
