package handlers

import (
	"net/http"

	"altalune.id/openwa/internal/project"
	"altalune.id/openwa/internal/web"
	"altalune.id/openwa/internal/web/templates"
)

// ProjectOverviewHandler serves the project landing page.
type ProjectOverviewHandler struct{ Deps }

// NewProjectOverviewHandler wires the project overview page.
func NewProjectOverviewHandler(d Deps, projects *project.Service) *ProjectOverviewHandler {
	d.Projects = projects
	return &ProjectOverviewHandler{Deps: d}
}

// Register mounts the overview route.
func (h *ProjectOverviewHandler) Register(mux web.Mux) {
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/overview", h.GetOverview)
}

// GetOverview renders the project overview page.
func (h *ProjectOverviewHandler) GetOverview(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	h.rememberProject(sc)
	Render(w, sc.req, templates.OverviewLayout(
		h.LayoutForProject(sc.req, "Overview · "+sc.project.Name, sc.org.Slug, sc.project, "overview"),
		templates.OverviewView{
			OrgSlug:     sc.org.Slug,
			ProjectID:   sc.project.ID.String(),
			ProjectSlug: sc.project.Slug,
			ProjectName: sc.project.Name,
		},
	))
}
