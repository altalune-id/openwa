package handlers

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/web"
	"altalune.id/openwa/internal/web/templates"
	slugs "altalune.id/openwa/slug"
)

// OrgHandler owns /orgs, /orgs/new, /orgs/{slug} and members routes.
type OrgHandler struct{ Deps }

// NewOrgHandler wires the handler.
func NewOrgHandler(d Deps, orgs *org.Service) *OrgHandler {
	d.Orgs = orgs
	return &OrgHandler{Deps: d}
}

// GetList renders /orgs.
func (h *OrgHandler) GetList(w http.ResponseWriter, r *http.Request) {
	p, authed := h.requireAuth(w, r)
	if !authed {
		return
	}
	items, err := h.Orgs.List(r.Context(), p.UserID)
	if err != nil {
		h.LogErr("web org: list", err)
		h.ErrorPageKey(w, r, http.StatusInternalServerError, "error.load_failed", err)
		return
	}
	Render(w, r, templates.OrgsLayout(h.Layout(r, "Organizations", web.ActiveNav{Scope: web.NavScopeOrg}), templates.OrgsView{Orgs: orgSummaries(items)}))
}

// GetNew renders /orgs/new.
func (h *OrgHandler) GetNew(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAuth(w, r); !ok {
		return
	}
	if !h.Caps.OrgCreation {
		h.ErrorPageKey(w, r, http.StatusForbidden, "error.forbidden", nil)
		return
	}
	Render(w, r, templates.OrgNewLayout(h.Layout(r, "Create organization", web.ActiveNav{Scope: web.NavScopeOrg}), templates.OrgNewView{Slug: slugs.Generate()}))
}

// PostCreate handles POST /orgs (org creation is capability-gated).
func (h *OrgHandler) PostCreate(w http.ResponseWriter, r *http.Request) {
	p, sid, ok := h.LoadSession(r)
	if !ok {
		http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	if !h.Caps.OrgCreation {
		h.ErrorPageKey(w, r, http.StatusForbidden, "error.forbidden", nil)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPageKey(w, r, http.StatusBadRequest, "error.bad_request", nil)
		return
	}
	slug := strings.TrimSpace(r.PostForm.Get("slug"))
	name := strings.TrimSpace(r.PostForm.Get("name"))
	created, err := h.Orgs.Create(r.Context(), org.CreateRequest{Slug: slug, Name: name, OwnerID: p.UserID})
	if err != nil {
		h.LogErr("web org: create", err)
		h.renderNewErr(w, r, slug, name, err)
		return
	}
	updated := p
	updated.ActiveOrgID = created.ID
	updated.ActiveProjectID = uuid.Nil
	if err := h.UpdateSession(r, sid, updated); err != nil {
		h.LogErr("web org: update session", err)
	}
	h.SetFlash(w, r, web.FlashOK, "flash.org_created", "Name", created.Name)
	http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/orgs/"+created.Slug+"/projects"), http.StatusSeeOther)
}

// PostRename handles POST /orgs/{slug}/rename.
func (h *OrgHandler) PostRename(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireOrg(w, r)
	if !ok {
		return
	}
	p, o, r := sc.principal, sc.org, sc.req
	if err := r.ParseForm(); err != nil {
		h.ErrorPageKey(w, r, http.StatusBadRequest, "error.bad_request", nil)
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("name"))
	// SECURITY: the slug comes from the URL, so membership in that org must be checked here — RLS no longer narrows this to the active org.
	canManage, mErr := h.Orgs.IsManager(r.Context(), o.ID, p.UserID)
	if mErr != nil || !canManage {
		h.ErrorPageKey(w, r, http.StatusForbidden, "error.forbidden", nil)
		return
	}
	if _, err := h.Orgs.Rename(r.Context(), o.ID, name); err != nil {
		h.LogErr("web org: rename", err)
		if org.IsSystemProtectedError(err) {
			h.ErrorPageKey(w, r, http.StatusConflict, "error.forbidden", err)
			return
		}
		h.ErrorPageKey(w, r, http.StatusBadRequest, "error.save_failed", err)
		return
	}
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/orgs/"+o.Slug+"/members"), http.StatusSeeOther) //nolint:gosec // G710: destination sanitized via ResolveReturnTo → SanitizeReturnTo
}

// GetShow renders /orgs/{slug} — the members view.
func (h *OrgHandler) GetShow(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireOrg(w, r)
	if !ok {
		return
	}
	p, o, r := sc.principal, sc.org, sc.req
	profiles, err := h.Orgs.ListMemberProfiles(r.Context(), o.ID)
	if err != nil {
		h.LogErr("web org: members", err)
		h.ErrorPageKey(w, r, http.StatusInternalServerError, "error.load_failed", err)
		return
	}
	canManage, _ := h.Orgs.IsManager(r.Context(), o.ID, p.UserID)
	Render(w, r, templates.MembersLayout(h.LayoutForOrg(r, "Members", o.Slug, "members"), templates.MembersView{
		OrgSlug:   o.Slug,
		Members:   memberProfileRows(profiles, o.ID, p.UserID),
		CanManage: canManage,
	}))
}

// PostRemoveMember handles POST /orgs/{slug}/members/{user}/remove.
func (h *OrgHandler) PostRemoveMember(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireOrg(w, r)
	if !ok {
		return
	}
	p, o, r := sc.principal, sc.org, sc.req
	canManage, mErr := h.Orgs.IsManager(r.Context(), o.ID, p.UserID)
	if mErr != nil || !canManage {
		h.ErrorPageKey(w, r, http.StatusForbidden, "error.forbidden", nil)
		return
	}
	userID, err := uuid.Parse(r.PathValue("user"))
	if err != nil {
		h.ErrorPageKey(w, r, http.StatusBadRequest, "error.bad_id", nil)
		return
	}
	if err := h.Orgs.RemoveMember(r.Context(), o.ID, userID); err != nil {
		h.LogErr("web org: remove member", err)
		if org.IsSystemProtectedError(err) {
			h.ErrorPageKey(w, r, http.StatusConflict, "error.forbidden", err)
			return
		}
		if org.IsMembershipMissingError(err) || org.IsNotFoundError(err) {
			h.ErrorPageKey(w, r, http.StatusNotFound, "error.not_found", nil)
			return
		}
		if org.IsSelfRemovalError(err) {
			h.ErrorPageKey(w, r, http.StatusConflict, "error.forbidden", nil)
			return
		}
		if org.IsOwnerRemovalError(err) {
			h.ErrorPageKey(w, r, http.StatusConflict, "error.forbidden", nil)
			return
		}
		// SECURITY: err.Error() names internal ids, so it stays in the log and never reaches the page.
		h.ErrorPageKey(w, r, http.StatusInternalServerError, "error.save_failed", nil)
		return
	}
	h.SetFlash(w, r, web.FlashOK, "flash.member_removed")
	http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/orgs/"+o.Slug+"/members"), http.StatusSeeOther) //nolint:gosec // G710: destination sanitized via ResolveReturnTo → SanitizeReturnTo
}

func (h *OrgHandler) requireAuth(w http.ResponseWriter, r *http.Request) (session.Principal, bool) {
	p, _, ok := h.LoadSession(r)
	if !ok {
		http.Redirect(w, r, ResolveReturnTo(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return session.Principal{}, false
	}
	return p, true
}

func (h *OrgHandler) renderNewErr(w http.ResponseWriter, r *http.Request, slug, name string, err error) {
	if slug == "" {
		slug = slugs.Generate()
	}
	msg := err.Error()
	switch {
	case org.IsAlreadyExistsError(err):
		msg = "Slug is already taken."
	case org.IsCreationDisabledError(err):
		msg = "Organization creation is disabled."
	}
	Render(w, r, templates.OrgNewLayout(
		h.Layout(r, "Create organization", web.ActiveNav{Scope: web.NavScopeOrg}),
		templates.OrgNewView{Slug: slug, Name: name, Error: msg, ErrorCode: ErrorRef(err)},
	))
}

func orgSummaries(items []*org.Org) []templates.OrgSummary {
	out := make([]templates.OrgSummary, 0, len(items))
	for _, o := range items {
		out = append(out, templates.OrgSummary{ID: o.ID.String(), Slug: o.Slug, Name: o.Name, System: o.System})
	}
	return out
}

func memberProfileRows(items []*org.MemberProfile, orgID, viewer uuid.UUID) []templates.MemberRow {
	// NOTE: the viewer is always a member of the org OrgScopeFor resolved, so their role is in this same list.
	var viewerRole org.Role
	for _, m := range items {
		if m.UserID == viewer {
			viewerRole = m.Role
			break
		}
	}
	out := make([]templates.MemberRow, 0, len(items))
	for _, m := range items {
		out = append(out, templates.MemberRow{
			UserID:    m.UserID.String(),
			Email:     m.Email,
			Name:      m.Name,
			Role:      string(m.Role),
			System:    m.System,
			IsSelf:    m.UserID == viewer,
			Removable: org.RemovalRefusal(orgID, viewer, m.UserID, viewerRole, m.Role, m.System) == nil,
		})
	}
	return out
}

// Register wires all org routes onto the mux.
func (h *OrgHandler) Register(mux web.Mux) {
	mux.HandleFunc("GET /orgs", h.GetList)
	mux.HandleFunc("GET /orgs/new", h.GetNew)
	mux.HandleFunc("POST /orgs", h.PostCreate)
	mux.HandleFunc("GET /orgs/{org}/members", h.GetShow)
	mux.HandleFunc("POST /orgs/{org}/rename", h.PostRename)
	mux.HandleFunc("POST /orgs/{org}/members/{user}/remove", h.PostRemoveMember)
}
