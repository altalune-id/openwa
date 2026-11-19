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

func TestPostgres_SecretHintsRoundTripThroughSaveAndSaveSecrets(t *testing.T) {
	f := newHijackFixture(t)
	ctx := tenant.Into(t.Context(), f.orgA)
	e := newSealedEndpoint(t, f.orgA)
	e.Secrets.PrimaryHint = "a1b2"
	require.NoError(t, f.store.Save(ctx, e))

	next := webhook.SealedSecrets{Primary: []byte("sealed-new"), PrimaryHint: "c3d4", Secondary: []byte("sealed-primary"), SecondaryHint: "a1b2"}
	require.NoError(t, f.store.SaveSecrets(ctx, e.ID, e.Secrets, next))
	got, err := f.store.ByID(ctx, e.ID)
	require.NoError(t, err)
	assert.Equal(t, "c3d4", got.Secrets.PrimaryHint)
	assert.Equal(t, "a1b2", got.Secrets.SecondaryHint)
}

func TestPostgres_DeliveredReadsSuccessfulAttemptsOfOneTypeInTheProject(t *testing.T) {
	f := newHijackFixture(t)
	ctxA := tenant.Into(t.Context(), f.orgA)
	e := newSealedEndpoint(t, f.orgA)
	require.NoError(t, f.store.Save(ctxA, e))
	save := func(typ events.Type, status int, errText string) {
		a := newAttempt(f.orgA, e.ID, uuid.New(), 1, time.Now().UTC().Truncate(time.Microsecond))
		a.EventType, a.StatusCode, a.Error = typ, status, errText
		require.NoError(t, f.store.SaveAttempt(ctxA, a))
	}
	save(events.WebhookPing, 200, "")
	save(events.MessageMatched, 500, "status 500")
	save(events.MessageMatched, 500, "")
	got, err := f.store.Delivered(ctxA, events.MessageMatched)
	require.NoError(t, err)
	assert.False(t, got, "a ping, a failure or a non-2xx answer is not a delivered message.matched")

	save(events.MessageMatched, 204, "")
	save(events.MessageMatched, 500, "status 500")
	got, err = f.store.Delivered(ctxA, events.MessageMatched)
	require.NoError(t, err)
	assert.True(t, got, "a later failure does not undo a success")

	sibling := tenant.Into(t.Context(), tenant.Context{OrgID: f.orgA.OrgID, ProjectID: uuid.New(), UserID: f.orgA.UserID})
	got, err = f.store.Delivered(sibling, events.MessageMatched)
	require.NoError(t, err)
	assert.False(t, got, "another project of the org sees nothing")
	got, err = f.store.Delivered(tenant.Into(t.Context(), f.orgB), events.MessageMatched)
	require.NoError(t, err)
	assert.False(t, got, "another org sees nothing")
}
