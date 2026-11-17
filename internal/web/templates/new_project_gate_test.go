package templates

import (
	"testing"

	"github.com/a-h/templ"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/web"
)

func TestNewProjectEntryPoints_OnlyManagersSeeThem(t *testing.T) {
	const href = `href="/orgs/acme/projects/new"`
	pages := func(d web.LayoutData) map[string]templ.Component {
		orgShell := d
		orgShell.ActiveProject = nil
		orgShell.Content = templ.Raw("")
		return map[string]templ.Component{
			"shell":           Layout(orgShell),
			"dashboard":       DashboardPage(d, DashboardView{OrgSlug: "acme", Projects: []ProjectSummary{{Slug: "a", Name: "A"}}}),
			"dashboard-empty": DashboardPage(d, DashboardView{OrgSlug: "acme"}),
		}
	}
	for _, canManage := range []bool{true, false} {
		d := projectShell()
		d.ActiveOrg.CanManage = canManage
		for name, c := range pages(d) {
			html := render(t, c)
			if canManage {
				require.Contains(t, html, href, "manager: %s shows New project", name)
			} else {
				require.NotContains(t, html, href, "member: %s hides New project", name)
			}
		}
	}
}
