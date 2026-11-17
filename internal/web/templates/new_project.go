package templates

import (
	"github.com/a-h/templ"

	"altalune.id/openwa/internal/web"
)

func canCreateProject(d web.LayoutData) bool { return d.ActiveOrg != nil && d.ActiveOrg.CanManage }

func newProjectAction(d web.LayoutData, label string) templ.Component {
	if !canCreateProject(d) {
		return createProjectManagersOnly(d)
	}
	return AppButton(ButtonProps{Kind: ButtonPrimary, Icon: "plus", Label: label, Href: d.OrgPath("/projects/new")})
}

func newProjectCTA(d web.LayoutData) *ButtonProps {
	if !canCreateProject(d) {
		return nil
	}
	return &ButtonProps{Kind: ButtonPrimary, Label: d.Tr("projects.create_first"), Href: d.OrgPath("/projects/new")}
}
