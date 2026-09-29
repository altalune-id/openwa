package webhook_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/rlsfixture"
	"altalune.id/openwa/internal/webhook"
)

type hijackFixture struct {
	store webhook.Store
	orgA  tenant.Context
	orgB  tenant.Context
}

func newHijackFixture(t *testing.T) hijackFixture {
	t.Helper()
	f := rlsfixture.Open(t)
	return hijackFixture{
		store: webhook.NewStore(f.DBCfg, f.Pool(), f.PgConn()),
		orgA:  f.SeedTenant(t),
		orgB:  f.SeedTenant(t),
	}
}

func isBlocked(err error) bool {
	return webhook.IsNotFoundError(err) || rlsfixture.IsRLSViolation(err)
}

func TestPostgres_Hijack_ByIDOfAnotherOrgIsNotFound(t *testing.T) {
	f := newHijackFixture(t)
	victim := newSealedEndpoint(t, f.orgA)
	require.NoError(t, f.store.Save(tenant.Into(t.Context(), f.orgA), victim))

	_, err := f.store.ByID(tenant.Into(t.Context(), f.orgB), victim.ID)
	assert.True(t, webhook.IsNotFoundError(err), "org B must not read org A's endpoint, got %T: %v", err, err)
}

func TestPostgres_Hijack_ListNamingAnotherOrgIsEmpty(t *testing.T) {
	f := newHijackFixture(t)
	require.NoError(t, f.store.Save(tenant.Into(t.Context(), f.orgA), newSealedEndpoint(t, f.orgA)))

	got, err := f.store.List(tenant.Into(t.Context(), f.orgB), f.orgA.OrgID, f.orgA.ProjectID)
	require.NoError(t, err)
	assert.Empty(t, got, "naming org A's scope from org B's context must list nothing")
}

func TestPostgres_Hijack_SaveOntoAnotherOrgsRowIsBlocked(t *testing.T) {
	t.Run("reskinned with the caller's own scope but another org's row id", func(t *testing.T) {
		f := newHijackFixture(t)
		ownerCtx := tenant.Into(t.Context(), f.orgA)
		otherCtx := tenant.Into(t.Context(), f.orgB)
		victim := newSealedEndpoint(t, f.orgA)
		require.NoError(t, f.store.Save(ownerCtx, victim))

		hijack := newSealedEndpoint(t, f.orgB)
		hijack.ID = victim.ID
		hijack.URL = "https://attacker.example/steal"

		err := f.store.Save(otherCtx, hijack)
		assert.True(t, isBlocked(err), "got %T: %v", err, err)

		got, err := f.store.ByID(ownerCtx, victim.ID)
		require.NoError(t, err)
		assert.Equal(t, validURL, got.URL, "org A's endpoint must be untouched")
		assert.Equal(t, f.orgA.OrgID, got.OrgID)

		listed, err := f.store.List(otherCtx, f.orgB.OrgID, f.orgB.ProjectID)
		require.NoError(t, err)
		assert.Empty(t, listed, "the refused upsert must not have landed in org B either")
	})

	t.Run("a fresh row naming another org", func(t *testing.T) {
		f := newHijackFixture(t)
		planted := newSealedEndpoint(t, f.orgA)

		err := f.store.Save(tenant.Into(t.Context(), f.orgB), planted)
		assert.True(t, isBlocked(err), "got %T: %v", err, err)

		_, err = f.store.ByID(tenant.Into(t.Context(), f.orgA), planted.ID)
		assert.True(t, webhook.IsNotFoundError(err), "org B must not plant an endpoint in org A")
	})
}

func TestPostgres_Hijack_SaveSecretsOfAnotherOrgIsNotFound(t *testing.T) {
	f := newHijackFixture(t)
	ownerCtx := tenant.Into(t.Context(), f.orgA)
	victim := newSealedEndpoint(t, f.orgA)
	require.NoError(t, f.store.Save(ownerCtx, victim))

	err := f.store.SaveSecrets(tenant.Into(t.Context(), f.orgB), victim.ID, victim.Secrets,
		webhook.SealedSecrets{Primary: []byte("attacker-primary")})
	assert.True(t, webhook.IsNotFoundError(err), "got %T: %v", err, err)

	got, err := f.store.ByID(ownerCtx, victim.ID)
	require.NoError(t, err)
	assert.Equal(t, victim.Secrets, got.Secrets, "org A's secrets must be untouched")
}

func TestPostgres_Hijack_DeleteOfAnotherOrgsRowIsNotFound(t *testing.T) {
	f := newHijackFixture(t)
	ownerCtx := tenant.Into(t.Context(), f.orgA)
	victim := newSealedEndpoint(t, f.orgA)
	require.NoError(t, f.store.Save(ownerCtx, victim))

	err := f.store.Delete(tenant.Into(t.Context(), f.orgB), victim.ID)
	assert.True(t, webhook.IsNotFoundError(err), "got %T: %v", err, err)

	_, err = f.store.ByID(ownerCtx, victim.ID)
	require.NoError(t, err, "org A's endpoint must survive org B's delete")
}

func TestPostgres_Hijack_ListAttemptsOfAnotherOrgIsEmpty(t *testing.T) {
	f := newHijackFixture(t)
	ownerCtx := tenant.Into(t.Context(), f.orgA)
	e := newSealedEndpoint(t, f.orgA)
	require.NoError(t, f.store.Save(ownerCtx, e))
	deliveryID := uuid.New()
	require.NoError(t, f.store.SaveAttempt(ownerCtx, newAttempt(f.orgA, e.ID, deliveryID, 1, time.Now().UTC().Truncate(time.Microsecond))))

	got, err := f.store.ListAttempts(tenant.Into(t.Context(), f.orgB), e.ID, deliveryID)
	require.NoError(t, err)
	assert.Empty(t, got, "org B must not read org A's attempts")
}

func TestPostgres_TenantMissing(t *testing.T) {
	f := newHijackFixture(t)
	e := newSealedEndpoint(t, f.orgA)

	assert.Error(t, f.store.Save(t.Context(), e))
	_, err := f.store.ByID(t.Context(), e.ID)
	assert.Error(t, err)
	_, err = f.store.List(t.Context(), f.orgA.OrgID, f.orgA.ProjectID)
	assert.Error(t, err)
	assert.Error(t, f.store.Delete(t.Context(), e.ID))
	assert.Error(t, f.store.SaveAttempt(t.Context(), newAttempt(f.orgA, e.ID, uuid.New(), 1, time.Now())))
	_, err = f.store.ListAttempts(t.Context(), e.ID, uuid.New())
	assert.Error(t, err)
}
