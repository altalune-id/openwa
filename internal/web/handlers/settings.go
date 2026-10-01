package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/project"
	"altalune.id/openwa/internal/web"
	"altalune.id/openwa/internal/web/templates"
)

// SettingsHandler owns the project settings page.
type SettingsHandler struct {
	Deps
	Messages *message.Service
}

// NewSettingsHandler wires the handler.
func NewSettingsHandler(d Deps, projects *project.Service, messages *message.Service) *SettingsHandler {
	d.Projects = projects
	return &SettingsHandler{Deps: d, Messages: messages}
}

// Register wires the settings routes onto mux.
func (h *SettingsHandler) Register(mux web.Mux) {
	const base = "/orgs/{org}/projects/{project}/settings"
	mux.HandleFunc("GET "+base, h.GetSettings)
	mux.HandleFunc("POST "+base+"/retention", h.PostRetention)
}

// GetSettings renders the retention setting and how many messages tonight's run deletes.
func (h *SettingsHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	days, err := h.Messages.Retention(sc.req.Context())
	if err != nil {
		h.LogErr("web settings: retention", err)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return
	}
	h.render(w, sc, http.StatusOK, days, "")
}

// PostRetention saves the retention days and redirects with a flash.
func (h *SettingsHandler) PostRetention(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	if !h.canManage(sc) {
		h.ErrorPageKey(w, sc.req, http.StatusForbidden, "settings.error.forbidden", nil)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPageKey(w, sc.req, http.StatusBadRequest, "error.bad_request", err)
		return
	}
	days, err := strconv.Atoi(strings.TrimSpace(r.PostForm.Get("days")))
	if err == nil {
		err = h.Messages.SetRetention(sc.req.Context(), days)
	}
	if err != nil {
		var numErr *strconv.NumError
		if !message.IsInvalidRetentionError(err) && !errors.As(err, &numErr) {
			h.LogErr("web settings: save retention", err)
			h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.save_failed", err)
			return
		}
		current, rErr := h.Messages.Retention(sc.req.Context())
		if rErr != nil {
			h.LogErr("web settings: retention", rErr)
			h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", rErr)
			return
		}
		h.render(w, sc, http.StatusUnprocessableEntity, current, "settings.error.invalid_days")
		return
	}
	h.SetFlash(w, sc.req, web.FlashOK, "flash.retention_saved", "Days", strconv.Itoa(days))
	http.Redirect(w, sc.req, h.ProjectURL(sc, "/settings"), http.StatusSeeOther) //nolint:gosec // G710: both slugs come from resolved rows.
}

//i18n:use settings.error.*
func (h *SettingsHandler) render(w http.ResponseWriter, sc ProjectScope, status, days int, errKey string) {
	pending, err := h.Messages.PendingDeletion(sc.req.Context())
	if err != nil {
		h.LogErr("web settings: pending", err)
	}
	v := templates.SettingsView{ProjectSlug: sc.project.Slug, Days: days, Pending: pending, Min: message.MinRetentionDays, Max: message.MaxRetentionDays, Error: errKey, CanManage: h.canManage(sc)}
	RenderStatus(w, sc.req, status, templates.SettingsLayout(h.LayoutForProject(sc.req, "Project settings", sc.org.Slug, sc.project, "settings"), v))
}

// SECURITY: retention deletes the project's history, so only an org owner or admin may change it; a failed lookup refuses.
func (h *SettingsHandler) canManage(sc ProjectScope) bool {
	p := session.PrincipalFrom(sc.req.Context())
	ok, err := h.Orgs.IsManager(sc.req.Context(), sc.org.ID, p.UserID)
	return err == nil && ok
}
