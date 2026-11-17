package handlers

import (
	"net/http"
	"strings"

	"altalune.id/openwa/internal/project"
	"altalune.id/openwa/internal/web"
	"altalune.id/openwa/internal/web/templates"
	slugs "altalune.id/openwa/slug"
)

// ProjectHandler owns the /projects routes.
type ProjectHandler struct{ Deps }

// NewProjectHandler wires the handler.
func NewProjectHandler(d Deps, projects *project.Service) *ProjectHandler {
	d.Projects = projects
	return &ProjectHandler{Deps: d}
}

// GetNew renders /orgs/{org}/projects/new.
func (h *ProjectHandler) GetNew(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireOrg(w, r)
	if !ok || !h.requireOrgManager(w, sc) {
		return
	}
	o := sc.org
	Render(w, r, templates.ProjectNewLayout(
		h.LayoutForOrg(r, pageTitle(r, "nav.create_project"), o.Slug, "overview"),
		templates.ProjectNewView{OrgSlug: o.Slug, Slug: slugs.Generate()},
	))
}

// PostCreate handles POST /orgs/{org}/projects.
func (h *ProjectHandler) PostCreate(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireOrg(w, r)
	if !ok || !h.requireOrgManager(w, sc) {
		return
	}
	p, sid, o, r := sc.principal, sc.sid, sc.org, sc.req
	if err := r.ParseForm(); err != nil {
		h.ErrorPageKey(w, r, http.StatusBadRequest, "error.bad_request", nil)
		return
	}
	slug := strings.TrimSpace(r.PostForm.Get("slug"))
	name := strings.TrimSpace(r.PostForm.Get("name"))
	created, err := h.Projects.Create(r.Context(), o.ID, slug, name)
	if err != nil {
		h.LogErr("web project: create", err)
		msg := err.Error()
		if project.IsAlreadyExistsError(err) {
			msg = "Slug is already taken."
		}
		if slug == "" {
			slug = slugs.Generate()
		}
		Render(w, r, templates.ProjectNewLayout(
			h.LayoutForOrg(r, pageTitle(r, "nav.create_project"), o.Slug, "overview"),
			templates.ProjectNewView{OrgSlug: o.Slug, Slug: slug, Name: name, Error: msg, ErrorCode: ErrorRef(err)},
		))
		return
	}
	updated := p
	updated.ActiveOrgID = o.ID
	updated.ActiveProjectID = created.ID
	if err := h.UpdateSession(r, sid, updated); err != nil {
		h.LogErr("web project: update session", err)
	}
	h.SetFlash(w, r, web.FlashOK, "flash.project_created", "Name", created.Name)
	http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, projectPath(o.Slug, created.Slug, "/overview")), http.StatusSeeOther) //nolint:gosec // G710: both slugs are validated by their own slug patterns
}

func projectPath(orgSlug, projectSlug, suffix string) string {
	return "/orgs/" + orgSlug + "/projects/" + projectSlug + suffix
}

// Register wires the project routes onto mux.
func (h *ProjectHandler) Register(mux web.Mux) {
	mux.HandleFunc("GET /orgs/{org}/projects/new", h.GetNew)
	mux.HandleFunc("POST /orgs/{org}/projects", h.PostCreate)
}
