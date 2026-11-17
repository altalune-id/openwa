package handlers

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/project"
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
	return d.RequireOrgFrom(w, r, func(r *http.Request) string { return r.PathValue("org") })
}

// RequireOrgFrom is RequireOrg for a route that names its org elsewhere, such as a form field; slug is read only once the caller is signed in.
func (d Deps) RequireOrgFrom(w http.ResponseWriter, r *http.Request, slug func(*http.Request) string) (OrgScope, bool) {
	p, sid, ok := d.RequireSignedIn(w, r)
	if !ok {
		return OrgScope{}, false
	}
	o, r, ok := d.OrgScopeFor(w, r, p, slug(r))
	if !ok {
		return OrgScope{}, false
	}
	return OrgScope{principal: p, sid: sid, org: o, req: r}, true
}

// RequireSignedIn loads the caller's session, redirecting to login when there is none.
func (d Deps) RequireSignedIn(w http.ResponseWriter, r *http.Request) (session.Principal, string, bool) {
	p, sid, ok := d.LoadSession(r)
	if !ok || p.UserID == uuid.Nil {
		http.Redirect(w, r, ResolveReturnTo(d.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return session.Principal{}, "", false
	}
	return p, sid, true
}

// MemberOrgScopes returns an OrgScope for every org the caller belongs to. SECURITY: org.Service.List joins the caller's memberships, so every org here is one the caller is a member of right now.
func (d Deps) MemberOrgScopes(r *http.Request, p session.Principal, sid string) ([]OrgScope, error) {
	orgs, err := d.Orgs.List(r.Context(), p.UserID)
	if err != nil {
		return nil, err
	}
	out := make([]OrgScope, 0, len(orgs))
	for _, o := range orgs {
		out = append(out, OrgScope{principal: p, sid: sid, org: o, req: orgScopedRequest(r, p, o)})
	}
	return out, nil
}

func orgScopedRequest(r *http.Request, p session.Principal, o *org.Org) *http.Request {
	return r.WithContext(orgScopedCtx(r.Context(), p, o))
}

func orgScopedCtx(ctx context.Context, p session.Principal, o *org.Org) context.Context {
	return tenant.Into(ctx, tenant.Context{OrgID: o.ID, UserID: p.UserID})
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

func (d Deps) rememberProject(sc ProjectScope) {
	p := sc.principal
	if p.ActiveOrgID == sc.org.ID && p.ActiveProjectID == sc.project.ID && p.LastProjectID == sc.project.ID {
		return
	}
	updated := p
	updated.ActiveOrgID, updated.ActiveProjectID, updated.LastProjectID = sc.org.ID, sc.project.ID, sc.project.ID
	if d.Users != nil && p.LastProjectID != sc.project.ID {
		if err := d.Users.SetLastProject(sc.req.Context(), p.UserID, sc.project.ID); err != nil {
			d.LogErr("web project: remember last project", err)
		}
	}
	if err := d.UpdateSession(sc.req, sc.sid, updated); err != nil {
		d.LogErr("web project: update session", err)
	}
}

// SECURITY: the remembered project counts only when the store finds it inside one of orgs, the caller's current memberships, so a stale or foreign id never names a target.
func (d Deps) lastProject(ctx context.Context, p session.Principal, orgs []*org.Org) (*org.Org, *project.Project) {
	if p.LastProjectID == uuid.Nil || d.Projects == nil {
		return nil, nil
	}
	for _, o := range orgs {
		pr, err := d.Projects.ByID(orgScopedCtx(ctx, p, o), p.LastProjectID)
		if err != nil {
			if !project.IsNotFoundError(err) {
				d.LogErr("web project: last project", err)
			}
			continue
		}
		if pr.OrgID == o.ID {
			return o, pr
		}
	}
	return nil, nil
}

// SECURITY: project configuration writes need an org owner or admin; a failed lookup refuses.
func (d Deps) canManageProject(sc ProjectScope) bool {
	return d.isManager(sc.req, sc.org, sc.principal)
}

func (d Deps) isManager(r *http.Request, o *org.Org, p session.Principal) bool {
	ok, err := d.Orgs.IsManager(r.Context(), o.ID, p.UserID)
	return err == nil && ok
}

//i18n:use common.managers_only
func (d Deps) requireManager(w http.ResponseWriter, sc ProjectScope) bool {
	return d.requireManagerOf(w, sc.req, sc.org, sc.principal)
}

func (d Deps) requireOrgManager(w http.ResponseWriter, sc OrgScope) bool {
	return d.requireManagerOf(w, sc.req, sc.org, sc.principal)
}

// SECURITY: the one manager gate for console writes; a failed lookup refuses.
func (d Deps) requireManagerOf(w http.ResponseWriter, r *http.Request, o *org.Org, p session.Principal) bool {
	if d.isManager(r, o, p) {
		return true
	}
	d.ErrorPageKey(w, r, http.StatusForbidden, "error.managers_only", nil)
	return false
}
