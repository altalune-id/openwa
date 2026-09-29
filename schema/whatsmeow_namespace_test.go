package schema_test

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/schema"
)

var createTable = regexp.MustCompile(`(?i)CREATE TABLE(?: IF NOT EXISTS)?\s+(?:\{\{\.Schema\}\}\.)?\{\{\.TablePrefix\}\}([a-z0-9_]+)`)

// TestNoMigrationCreatesAWhatsmeowTable pins the namespace whatsmeow's fixed table names live in.
func TestNoMigrationCreatesAWhatsmeowTable(t *testing.T) {
	t.Parallel()
	entries, err := fs.ReadDir(schema.MigrationsFS(), "migrations/postgres")
	require.NoError(t, err)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		b, err := fs.ReadFile(schema.MigrationsFS(), "migrations/postgres/"+e.Name())
		require.NoError(t, err)
		for _, m := range createTable.FindAllStringSubmatch(string(b), -1) {
			require.False(t, strings.HasPrefix(m[1], "whatsmeow_"), "%s creates %s inside whatsmeow's namespace", e.Name(), m[1])
		}
	}
}
