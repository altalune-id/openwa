package webhook_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/webhook"
)

func tail(secret string) string { return secret[len(secret)-webhook.SecretHintLen:] }

func TestService_SecretHintsFollowCreateRotateAndRetire(t *testing.T) {
	h := newHarness(t)
	ctx, _ := svcCtx(t)
	e, first := h.create(ctx, t, events.PostPublished)

	stored, err := h.store.ByID(ctx, e.ID)
	require.NoError(t, err)
	assert.Equal(t, tail(first), stored.Secrets.PrimaryHint)
	assert.Empty(t, stored.Secrets.SecondaryHint)

	second, err := h.svc.Rotate(ctx, e.ID)
	require.NoError(t, err)
	stored, err = h.store.ByID(ctx, e.ID)
	require.NoError(t, err)
	assert.Equal(t, tail(second), stored.Secrets.PrimaryHint)
	assert.Equal(t, tail(first), stored.Secrets.SecondaryHint)

	require.NoError(t, h.svc.RetireSecondary(ctx, e.ID))
	stored, err = h.store.ByID(ctx, e.ID)
	require.NoError(t, err)
	assert.Equal(t, tail(second), stored.Secrets.PrimaryHint)
	assert.Empty(t, stored.Secrets.SecondaryHint, "ending the rotation clears the old hint")
}

func TestService_BackfillSecretHints(t *testing.T) {
	h := newHarness(t)
	ctx, tc := svcCtx(t)
	e, secret := h.create(ctx, t, events.PostPublished)
	rotating, _ := h.create(ctx, t, events.PostPublished)
	_, err := h.svc.Rotate(ctx, rotating.ID)
	require.NoError(t, err)
	broken := newSealedEndpoint(t, tc)
	h.store.Seed(broken)

	for _, id := range []uuid.UUID{e.ID, rotating.ID} {
		stored, err := h.store.ByID(ctx, id)
		require.NoError(t, err)
		stored.Secrets.PrimaryHint, stored.Secrets.SecondaryHint = "", ""
		h.store.Seed(stored)
	}

	require.NoError(t, h.svc.BackfillSecretHints(tenant.Into(t.Context(), tenant.Context{OrgID: tc.OrgID})))
	got, err := h.store.ByID(ctx, e.ID)
	require.NoError(t, err)
	assert.Equal(t, tail(secret), got.Secrets.PrimaryHint)
	got, err = h.store.ByID(ctx, rotating.ID)
	require.NoError(t, err)
	assert.Len(t, got.Secrets.PrimaryHint, webhook.SecretHintLen)
	assert.Len(t, got.Secrets.SecondaryHint, webhook.SecretHintLen)
	got, err = h.store.ByID(ctx, broken.ID)
	require.NoError(t, err)
	assert.Empty(t, got.Secrets.PrimaryHint, "an unreadable secret keeps no hint")
	assert.Equal(t, 1, *h.unexpected, "one report per run for the unreadable rows")

	require.NoError(t, h.svc.BackfillSecretHints(tenant.Into(t.Context(), tenant.Context{OrgID: tc.OrgID})))
	assert.Equal(t, 2, *h.unexpected, "a second run reports the still-unreadable row again, and nothing else changes")
}
