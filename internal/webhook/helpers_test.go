package webhook_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/webhook"
)

func newSealedEndpoint(t *testing.T, tc tenant.Context, types ...events.Type) *webhook.Endpoint {
	t.Helper()
	if len(types) == 0 {
		types = []events.Type{events.PostPublished, events.PostDeleted}
	}
	e, err := webhook.New(tc.OrgID, tc.ProjectID, validURL, "orders", types)
	require.NoError(t, err)
	e.Secrets = webhook.SealedSecrets{Primary: []byte("sealed-primary")}
	e.CreatedAt = e.CreatedAt.Truncate(time.Microsecond)
	e.UpdatedAt = e.CreatedAt
	return e
}

// NOTE: timestamptz keeps microseconds, so a fixture time must not carry nanoseconds a round trip would drop.
func updateEndpoint(t *testing.T, e *webhook.Endpoint, rawURL, description string, types []events.Type, active bool) {
	t.Helper()
	require.NoError(t, e.Update(rawURL, description, types, active))
	e.UpdatedAt = e.UpdatedAt.Truncate(time.Microsecond)
}

func newAttempt(tc tenant.Context, endpointID, deliveryID uuid.UUID, n int, at time.Time) webhook.Attempt {
	return webhook.Attempt{
		ID:         uuid.Must(uuid.NewV7()),
		OrgID:      tc.OrgID,
		ProjectID:  tc.ProjectID,
		EndpointID: endpointID,
		DeliveryID: deliveryID,
		EventID:    uuid.New(),
		EventType:  events.PostPublished,
		Attempt:    n,
		StatusCode: 500,
		Error:      "boom",
		Duration:   1234 * time.Millisecond,
		CreatedAt:  at,

		ResponseBody:      `{"error":"<b>x</b>"}`,
		ResponseTruncated: true,
		ResponseHeaders:   []webhook.Header{{Name: "Content-Type", Value: "application/json"}, {Name: "X-Request-Id", Value: "req_1"}},
	}
}

func assertSameResponse(t *testing.T, want, got webhook.Attempt) {
	t.Helper()
	assert.Equal(t, want.ResponseBody, got.ResponseBody)
	assert.Equal(t, want.ResponseTruncated, got.ResponseTruncated)
	assert.Equal(t, want.ResponseHeaders, got.ResponseHeaders)
}

func noResponse(a webhook.Attempt) webhook.Attempt {
	a.StatusCode = 0
	a.Error = "dial tcp: connection refused"
	a.ResponseBody = ""
	a.ResponseTruncated = false
	a.ResponseHeaders = nil
	return a
}

func assertSameEndpoint(t *testing.T, want, got *webhook.Endpoint) {
	t.Helper()
	assert.Equal(t, want.ID, got.ID)
	assert.Equal(t, want.OrgID, got.OrgID)
	assert.Equal(t, want.ProjectID, got.ProjectID)
	assert.Equal(t, want.URL, got.URL)
	assert.Equal(t, want.Description, got.Description)
	assert.Equal(t, want.EventTypes, got.EventTypes)
	assert.Equal(t, want.Secrets, got.Secrets)
	assert.Equal(t, want.Active, got.Active)
	assert.True(t, want.CreatedAt.Equal(got.CreatedAt), "created_at: want %v got %v", want.CreatedAt, got.CreatedAt)
	assert.True(t, want.UpdatedAt.Equal(got.UpdatedAt), "updated_at: want %v got %v", want.UpdatedAt, got.UpdatedAt)
	assert.Equal(t, time.UTC, got.CreatedAt.Location())
}
