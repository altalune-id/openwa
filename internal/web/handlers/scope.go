package handlers

import (
	"net/http"

	"github.com/google/uuid"

	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/project"
)

// OrgScope is the org an org-scoped route acts on, with a request already carrying the tenant scope.
type OrgScope struct {
	principal session.Principal
	sid       string
	org       *org.Org
	req       *http.Request
}

// ProjectScope is the org and project a project-scoped route acts on, with a request already carrying both scopes.
type ProjectScope struct {
	principal session.Principal
	sid       string
	org       *org.Org
	project   *project.Project
	req       *http.Request
}

// RequireOrg resolves the org the path names, gating membership before anything reads its rows. SECURITY: the slug is attacker-supplied and RLS cannot gate it, so this membership check is the only thing separating one org's members from another's rows.
func (d Deps) RequireOrg(w http.ResponseWriter, r *http.Request) (OrgScope, bool) {
	p, sid, ok := d.LoadSession(r)
	if !ok || p.UserID == uuid.Nil {
		http.Redirect(w, r, ResolveReturnTo(d.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return OrgScope{}, false
	}
	o, r, ok := d.OrgScopeFor(w, r, p, r.PathValue("org"))
	if !ok {
		return OrgScope{}, false
	}
	return OrgScope{principal: p, sid: sid, org: o, req: r}, true
}

// RequireProject resolves the org and project the path names, gating org membership before the project is looked up. SECURITY: membership is org-level by design, so this admits ANY project in an org the caller belongs to — see ../../../docs/multitenancy/request-scope.md.
func (d Deps) RequireProject(w http.ResponseWriter, r *http.Request) (ProjectScope, bool) {
	sc, ok := d.RequireOrg(w, r)
	if !ok {
		return ProjectScope{}, false
	}
	proj, req, ok := d.ProjectScopeFor(w, sc.req, sc.org.ID, sc.req.PathValue("project"))
	if !ok {
		return ProjectScope{}, false
	}
	return ProjectScope{principal: sc.principal, sid: sc.sid, org: sc.org, project: proj, req: req}, true
}
