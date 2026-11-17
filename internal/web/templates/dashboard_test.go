package templates

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDashboardPage_BadgesWrapInsteadOfSqueezingTheName(t *testing.T) {
	d := projectShell()
	html := render(t, DashboardPage(d, DashboardView{
		OrgSlug:      "acme",
		ActiveProjID: "p1",
		Projects:     []ProjectSummary{{ID: "p1", Slug: "default", Name: "Default project", System: true}},
	}))
	row := regexp.MustCompile(`<div class="([^"]*)"><p class="([^"]*)">Default project</p>`).FindStringSubmatch(html)
	require.NotNil(t, row, "project name row")
	require.Regexp(t, `\bflex-wrap\b`, row[1], "badges wrap to a new line")
	require.Regexp(t, `\bmin-w-\[12ch\]`, row[2], "name keeps at least 12ch")
	require.Regexp(t, `\bflex-1\b`, row[2], "name takes the free space")
	card := regexp.MustCompile(`<a href="/orgs/acme/projects/default/overview" class="([^"]*)"`).FindStringSubmatch(html)
	require.NotNil(t, card, "project card")
	require.Regexp(t, `\bh-full\b`, card[1], "cards in one grid row share a height when a badge wraps")
}

func TestDashboardPage_ManagersOnlyLineCentersOnTheTitleRow(t *testing.T) {
	d := projectShell()
	html := render(t, DashboardPage(d, DashboardView{OrgSlug: "acme"}))
	line := regexp.MustCompile(`<span class="([^"]*)">projects\.create_managers_only</span>`).FindStringSubmatch(html)
	require.NotNil(t, line, "managers-only line")
	for _, c := range []string{"flex", "items-center", "min-h-11", "lg:min-h-8"} {
		require.Regexp(t, `(^|\s)`+regexp.QuoteMeta(c)+`(\s|$)`, line[1], "line matches the title row height: %s", c)
	}
}
