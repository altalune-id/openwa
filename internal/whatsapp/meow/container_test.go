package meow_test

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/testutil/pgtest"
	"altalune.id/openwa/internal/whatsapp/meow"
)

func TestUpgrade_CreatesSchemaAndAssertUpgradedPasses(t *testing.T) {
	h := pgtest.NewDatabase(t)
	db := h.OpenDB(t)
	log := slog.New(slog.DiscardHandler)
	require.True(t, meow.IsNotUpgradedError(meow.AssertUpgraded(t.Context(), db)))
	require.NoError(t, meow.Upgrade(t.Context(), db, log))
	require.NoError(t, meow.AssertUpgraded(t.Context(), db))
	c, err := meow.NewContainer(db, log, "OpenWA")
	require.NoError(t, err)
	require.NoError(t, c.Close())
}
