package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/internal/testutil/pgtest"
)

func TestMigrateUp_CreatesWhatsmeowSchema(t *testing.T) {
	h := pgtest.NewDatabase(t)
	cfg := config.Defaults()
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = "public"
	cfg.DB.TablePrefix = "openwa_"
	cfg.DB.AllowBypassRLS = true
	cfg.Security.EncryptionKey = strings.Repeat("ab", 32)
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	require.NoError(t, runCLIMigrateUp(t.Context(), cfg, stubServerBoot, cmd))
	db := h.OpenDB(t)
	var ok bool
	require.NoError(t, db.QueryRow(`SELECT to_regclass('whatsmeow_version') IS NOT NULL`).Scan(&ok))
	require.True(t, ok)
}
