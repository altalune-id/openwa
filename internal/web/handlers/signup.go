package handlers

import (
	"cmp"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/i18n"
	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/project"
	"altalune.id/openwa/internal/user"
	"altalune.id/openwa/internal/web"
	"altalune.id/openwa/internal/web/templates"
)

// SignupHandler owns /signup/complete — cloud-only workspace bootstrap for new OIDC users without a pre-existing membership.
type SignupHandler struct {
	Deps
	Users    *user.Service
	Orgs     *org.Service
	Projects *project.Service
}

// NewSignupHandler wires the handler.
func NewSignupHandler(d Deps, users *user.Service, orgs *org.Service, projects *project.Service) *SignupHandler {
	d.Orgs = orgs
	d.Projects = projects
	return &SignupHandler{Deps: d, Users: users, Orgs: orgs, Projects: projects}
}

// Register wires the /signup/complete routes onto mux.
func (h *SignupHandler) Register(mux web.Mux) {
	mux.HandleFunc("GET /signup/complete", h.GetSignup)
	mux.HandleFunc("POST /signup/complete", h.PostSignup)
}

// GetSignup renders the signup-complete form.
func (h *SignupHandler) GetSignup(w http.ResponseWriter, r *http.Request) {
	if h.Cfg.Mode != config.ModeCloud {
		h.ErrorPageKey(w, r, http.StatusNotFound, "error.not_found", nil)
		return
	}
	p, _, ok := h.LoadSession(r)
	if !ok {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	if h.hasMembership(r, p.UserID) {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/"), http.StatusSeeOther)
		return
	}
	Render(w, r, templates.SignupCompleteLayout(h.Base(r, pageTitle(r, "title.complete_signup")), h.defaultView(r, p)))
}

// PostSignup provisions an org and its first project under one workspace name, stamps terms, and redirects to the project overview.
func (h *SignupHandler) PostSignup(w http.ResponseWriter, r *http.Request) {
	if h.Cfg.Mode != config.ModeCloud {
		h.ErrorPageKey(w, r, http.StatusNotFound, "error.not_found", nil)
		return
	}
	p, sid, ok := h.LoadSession(r)
	if !ok {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/login"), http.StatusSeeOther)
		return
	}
	if h.hasMembership(r, p.UserID) {
		http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, "/"), http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPageKey(w, r, http.StatusBadRequest, "error.bad_request", err)
		return
	}
	view := h.defaultView(r, p)
	view.WorkspaceName = strings.TrimSpace(r.PostForm.Get("workspace_name"))
	if view.WorkspaceName == "" {
		view.FieldErrors["workspace_name"] = "signup.error.name_required"
		h.render(w, r, http.StatusUnprocessableEntity, view)
		return
	}
	if view.AskAccept && r.PostForm.Get("accept_terms") != "1" {
		view.Error = "signup.error.accept_terms"
		h.render(w, r, http.StatusUnprocessableEntity, view)
		return
	}

	ctx := r.Context()
	o, err := h.Orgs.Create(ctx, org.CreateRequest{Name: view.WorkspaceName, OwnerID: p.UserID})
	if err != nil {
		if org.IsInvalidNameError(err) {
			view.FieldErrors["workspace_name"] = "signup.error.name_invalid"
			h.render(w, r, http.StatusUnprocessableEntity, view)
			return
		}
		h.LogErr("signup: create org", err)
		view.Error = "signup.error.create_org"
		view.ErrorCode = ErrorRef(err)
		h.render(w, r, http.StatusInternalServerError, view)
		return
	}

	tctx := tenant.Into(ctx, tenant.Context{OrgID: o.ID, UserID: p.UserID})
	proj, err := h.Projects.Create(tctx, o.ID, "", view.WorkspaceName)
	if err != nil {
		h.LogErr("signup: create project", err)
		view.Error = "signup.error.create_project"
		view.ErrorCode = ErrorRef(err)
		h.render(w, r, http.StatusInternalServerError, view)
		return
	}

	if view.AskAccept {
		if err := h.Users.AcceptTerms(ctx, p.UserID); err != nil {
			h.LogErr("signup: accept terms", err)
		} else {
			p.TermsAcceptedAt = time.Now().UTC()
		}
	}

	p.ActiveOrgID = o.ID
	p.ActiveProjectID = proj.ID
	if err := h.UpdateSession(r, sid, p); err != nil {
		h.LogErr("signup: update session", err)
	}
	http.Redirect(w, r, web.Path(h.Cfg.HTTP.BasePath, projectPath(o.Slug, proj.Slug, "/overview")), http.StatusSeeOther) //nolint:gosec // G710: both slugs are generated and validated by their slug patterns
}

func (h *SignupHandler) hasMembership(r *http.Request, userID uuid.UUID) bool {
	orgs, err := h.Orgs.List(r.Context(), userID)
	if err != nil {
		h.LogErr("signup: list orgs", err)
		return false
	}
	return len(orgs) > 0
}

func (h *SignupHandler) defaultView(r *http.Request, p session.Principal) templates.SignupCompleteView {
	return templates.SignupCompleteView{
		Email:         p.Email,
		Name:          p.Name,
		WorkspaceName: defaultWorkspaceName(r, p.Name),
		AskAccept:     needsTerms(p, h.TermsUpdatedAt, h.Cfg.Compliance.RequireAcceptance),
		TermsURL:      cmp.Or(strings.TrimSpace(h.Cfg.Compliance.TermsURL), web.Path(h.Cfg.HTTP.BasePath, "/terms")),
		PrivacyURL:    cmp.Or(strings.TrimSpace(h.Cfg.Compliance.PrivacyURL), web.Path(h.Cfg.HTTP.BasePath, "/privacy")),
		FieldErrors:   map[string]string{},
	}
}

//i18n:use signup.workspace_default
//i18n:use signup.error.*

func defaultWorkspaceName(r *http.Request, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	const key = "signup.workspace_default"
	if out := i18n.TranslatorFrom(r.Context()).T(key, "Name", name); out != key {
		return out
	}
	return name
}

func (h *SignupHandler) render(w http.ResponseWriter, r *http.Request, status int, view templates.SignupCompleteView) {
	RenderStatus(w, r, status, templates.SignupCompleteLayout(h.Base(r, pageTitle(r, "title.complete_signup")), view))
}
