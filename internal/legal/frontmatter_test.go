package legal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSplitFrontmatter_ParsesDate(t *testing.T) {
	t.Parallel()
	fm, body, err := splitFrontmatter([]byte("---\ntitle: T\nupdated: 2026-09-28\n---\n# T\n"))
	require.NoError(t, err)
	require.Equal(t, "T", fm.Title)
	require.True(t, fm.Updated.Equal(time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)))
	require.Equal(t, "# T\n", string(body))
}

func TestSplitFrontmatter_RejectsBadDate(t *testing.T) {
	t.Parallel()
	_, _, err := splitFrontmatter([]byte("---\ntitle: T\nupdated: 28/09/2026\n---\n# T\n"))
	require.Error(t, err)
}

func TestHeadings_ReadsLevelTwoOnly(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"1. A", "2. B"}, headings([]byte("# Title\n\n## 1. A\ntext\n### sub\n## 2. B\n")))
}
