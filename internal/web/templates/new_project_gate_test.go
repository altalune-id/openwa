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

func TestNewProjectEntryPoints_MembersSeeManagersOnlyLine(t *testing.T) {
	shell := projectShell()
	shell.ActiveProject = nil
	shell.Content = templ.Raw("")
	pages := map[string]func(web.LayoutData) templ.Component{
		"switcher": func(d web.LayoutData) templ.Component { return Layout(d) },
		"dashboard": func(d web.LayoutData) templ.Component {
			return DashboardPage(d, DashboardView{OrgSlug: "acme", Projects: []ProjectSummary{{Slug: "a", Name: "A"}}})
		},
		"dashboard-empty": func(d web.LayoutData) templ.Component { return DashboardPage(d, DashboardView{OrgSlug: "acme"}) },
	}
	for name, page := range pages {
		for _, canManage := range []bool{true, false} {
			d := shell
			org := *d.ActiveOrg
			org.CanManage = canManage
			d.ActiveOrg = &org
			html := render(t, page(d))
			if canManage {
				require.NotContains(t, html, "projects.create_managers_only", "manager: %s", name)
				continue
			}
			require.Contains(t, html, "projects.create_managers_only", "member: %s", name)
		}
	}
}
