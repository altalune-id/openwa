package schema_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMessageMatchMigration_CheckIsValidatedAndIndexIsValid(t *testing.T) {
	sqlDB, sch, pfx := migrated(t)
	var validated bool
	require.NoError(t, sqlDB.QueryRowContext(t.Context(),
		`SELECT c.convalidated FROM pg_constraint c JOIN pg_namespace n ON n.oid = c.connamespace WHERE n.nspname = $1 AND c.conname = $2`,
		sch, pfx+"messages_match_outcome_check").Scan(&validated))
	require.True(t, validated)

	var valid bool
	var def string
	require.NoError(t, sqlDB.QueryRowContext(t.Context(),
		`SELECT i.indisvalid, pg_get_indexdef(i.indexrelid) FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND c.relname = $2`,
		sch, pfx+"messages_rule_hits_idx").Scan(&valid, &def))
	require.True(t, valid)
	require.Contains(t, def, "(project_id, device_id, matched_rule_id, wa_timestamp DESC)")
	require.Contains(t, def, "WHERE (matched_rule_id IS NOT NULL)")
}
