package webhook_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/webhook"
)

func (f pgFixture) seedDevice(t *testing.T, projectID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := f.sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+f.prefix+"devices (id, public_id, org_id, project_id, name, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $6)",
		id, "dev_"+id.String()[:12], f.tc.OrgID, projectID, id.String()[:8], time.Now().UTC())
	require.NoError(t, err)
	return id
}

func TestPostgresStore_DeviceIDRoundTripsAndNeverChanges(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	device := f.seedDevice(t, f.tc.ProjectID)

	e, err := webhook.New(f.tc.OrgID, f.tc.ProjectID, &device, validURL, "", []events.Type{events.MessageMatched})
	require.NoError(t, err)
	e.Secrets = webhook.SealedSecrets{Primary: []byte("sealed")}
	require.NoError(t, f.store.Save(ctx, e))

	wide := newSealedEndpoint(t, f.tc)
	require.NoError(t, f.store.Save(ctx, wide))

	got, err := f.store.ByID(ctx, e.ID)
	require.NoError(t, err)
	require.NotNil(t, got.DeviceID)
	require.Equal(t, device, *got.DeviceID)
	got, err = f.store.ByID(ctx, wide.ID)
	require.NoError(t, err)
	require.Nil(t, got.DeviceID)

	other := f.seedDevice(t, f.tc.ProjectID)
	e.DeviceID = &other
	updateEndpoint(t, e, validURL, "changed", []events.Type{events.MessageReceived}, true)
	require.NoError(t, f.store.Save(ctx, e))
	got, err = f.store.ByID(ctx, e.ID)
	require.NoError(t, err)
	require.Equal(t, "changed", got.Description)
	require.Equal(t, device, *got.DeviceID, "an update never moves an endpoint to another number")
}

func TestPostgresStore_OneScopedEndpointPerNumberAndURL(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), f.tc)
	device := f.seedDevice(t, f.tc.ProjectID)
	scoped := func(rawURL string) *webhook.Endpoint {
		e, err := webhook.New(f.tc.OrgID, f.tc.ProjectID, &device, rawURL, "", []events.Type{events.MessageMatched})
		require.NoError(t, err)
		e.Secrets = webhook.SealedSecrets{Primary: []byte("sealed")}
		return e
	}

	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() { errs[i] = f.store.Save(ctx, scoped(validURL)) })
	}
	wg.Wait()
	dup := 0
	for _, err := range errs {
		if err != nil {
			require.True(t, webhook.IsDuplicateEndpointError(err), "got %T: %v", err, err)
			dup++
		}
	}
	assert.Equal(t, 1, dup, "two concurrent saves of one number and URL make one endpoint")

	require.NoError(t, f.store.Save(ctx, scoped("https://other.example.com/hook")), "another URL for the same number is fine")
	wide := newSealedEndpoint(t, f.tc)
	wide.URL = validURL
	require.NoError(t, f.store.Save(ctx, wide), "a project-wide endpoint is not limited")
}
