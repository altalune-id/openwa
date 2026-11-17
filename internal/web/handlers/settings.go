package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"altalune.id/openwa/internal/message"
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
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/rename", h.PostRename)
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/usage", h.GetUsage)
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
	v := h.view(sc, days)
	v.NameSaved = sc.req.URL.Query().Get("saved") == "name"
	h.renderView(w, sc, http.StatusOK, v)
}

// GetUsage renders the usage placeholder with the project's retention.
func (h *SettingsHandler) GetUsage(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	days, err := h.Messages.Retention(sc.req.Context())
	if err != nil {
		h.LogErr("web usage: retention", err)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return
	}
	Render(w, sc.req, templates.UsageLayout(h.LayoutForProject(sc.req, pageTitle(sc.req, "usage.title"), sc.org.Slug, sc.project, "usage"), templates.UsageView{RetentionDays: days}))
}

//i18n:use settings.name_error
//i18n:use settings.name_locked

// PostRename renames the project for a manager and returns to settings, re-rendering it with a field error on refusal.
func (h *SettingsHandler) PostRename(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	if !h.requireManager(w, sc) {
		return
	}
	if err := sc.req.ParseForm(); err != nil {
		h.ErrorPageKey(w, sc.req, http.StatusBadRequest, "error.bad_request", err)
		return
	}
	name := strings.TrimSpace(sc.req.PostForm.Get("name"))
	_, err := h.Projects.Rename(sc.req.Context(), sc.project.ID, name)
	if err == nil {
		http.Redirect(w, sc.req, h.ProjectURL(sc, "/settings?saved=name"), http.StatusSeeOther) //nolint:gosec // G710: both slugs come from resolved rows.
		return
	}
	status, key := http.StatusUnprocessableEntity, "settings.name_error"
	switch {
	case project.IsSystemProtectedError(err):
		status, key = http.StatusConflict, "settings.name_locked"
	case !project.IsInvalidNameError(err):
		h.LogErr("web settings: rename", err)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.save_failed", err)
		return
	}
	days, rErr := h.Messages.Retention(sc.req.Context())
	if rErr != nil {
		h.LogErr("web settings: retention", rErr)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", rErr)
		return
	}
	v := h.view(sc, days)
	v.Name, v.NameError = name, key
	h.renderView(w, sc, status, v)
}

// PostRetention saves the retention days and redirects with a flash.
func (h *SettingsHandler) PostRetention(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	if !h.canManageProject(sc) {
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
	v := h.view(sc, days)
	v.Error = errKey
	h.renderView(w, sc, status, v)
}

func (h *SettingsHandler) view(sc ProjectScope, days int) templates.SettingsView {
	pending, err := h.Messages.PendingDeletion(sc.req.Context())
	if err != nil {
		h.LogErr("web settings: pending", err)
	}
	canManage := h.canManageProject(sc)
	v := templates.SettingsView{
		OrgSlug: sc.org.Slug, OrgID: sc.org.ID.String(),
		ProjectSlug: sc.project.Slug, ProjectID: sc.project.ID.String(),
		Name: sc.project.Name, System: sc.project.System,
		CanManage: canManage, CanRename: canManage && !sc.project.System,
		Days: days, Pending: pending, Min: message.MinRetentionDays, Max: message.MaxRetentionDays,
	}
	v.RetentionAt, v.RetentionZone = h.nextRetention()
	return v
}

func (h *SettingsHandler) nextRetention() (at time.Time, zone string) { //nolint:nonamedreturns // the two results differ in role
	locs, err := h.Cfg.Scheduler.Locations()
	if err != nil {
		h.LogErr("web settings: scheduler zone", err)
		return time.Time{}, ""
	}
	loc := locs(message.RetentionJobName)
	return message.RetentionSchedule(loc).Next(time.Now()).UTC(), loc.String()
}

func (h *SettingsHandler) renderView(w http.ResponseWriter, sc ProjectScope, status int, v templates.SettingsView) {
	RenderStatus(w, sc.req, status, templates.SettingsLayout(h.LayoutForProject(sc.req, pageTitle(sc.req, "settings.title"), sc.org.Slug, sc.project, "settings"), v))
}
