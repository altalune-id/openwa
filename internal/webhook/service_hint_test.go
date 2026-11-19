package webhook_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/events"
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
