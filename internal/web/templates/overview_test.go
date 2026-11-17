package templates

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOverviewPage_LabelsEachID(t *testing.T) {
	html := render(t, OverviewPage(projectShell(), OverviewView{
		OrgSlug: "acme", OrgID: "org-uuid", ProjectSlug: "alpha", ProjectID: "proj-uuid", ProjectName: "Alpha",
	}))
	for _, p := range [][2]string{
		{"overview.org_id", "acme"},
		{"settings.org_uuid", "org-uuid"},
		{"settings.project_id", "alpha"},
		{"settings.project_uuid", "proj-uuid"},
	} {
		re := `<dt[^>]*>` + regexp.QuoteMeta(p[0]) + `</dt><dd[^>]*><button[^>]*data-copy="` + regexp.QuoteMeta(p[1]) + `"`
		require.Regexp(t, re, html, "%s labels %s", p[0], p[1])
	}
}
