package handlers

import (
	"net/http"

	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/project"
	"altalune.id/openwa/internal/web"
	"altalune.id/openwa/internal/web/templates"
)

// ProjectOverviewHandler serves the project landing page.
type ProjectOverviewHandler struct {
	Deps
	Devices *device.Service
}

// NewProjectOverviewHandler wires the project overview page; devices may be nil.
func NewProjectOverviewHandler(d Deps, projects *project.Service, devices *device.Service) *ProjectOverviewHandler {
	d.Projects = projects
	return &ProjectOverviewHandler{Deps: d, Devices: devices}
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
	v := templates.OverviewView{
		OrgSlug:     sc.org.Slug,
		ProjectID:   sc.project.ID.String(),
		ProjectSlug: sc.project.Slug,
		ProjectName: sc.project.Name,
	}
	if h.Devices != nil {
		views, err := h.Devices.List(sc.req.Context())
		if err != nil {
			h.LogErr("web overview: devices", err)
		}
		v.Devices = len(views)
		for _, dv := range views {
			if dv.Status.State == device.SessionConnected {
				v.DevicesConnected++
			}
		}
	}
	Render(w, sc.req, templates.OverviewLayout(
		h.LayoutForProject(sc.req, "Overview", sc.org.Slug, sc.project, "overview"),
		v,
	))
}
