package handlers

import (
	"net/http"

	"altalune.id/openwa/internal/alert"
	"altalune.id/openwa/internal/web"
	"altalune.id/openwa/internal/web/templates"
)

// UnsubscribeHandler owns the public opt-out page a disconnect email links to.
type UnsubscribeHandler struct {
	Deps
	Alerts *alert.Service
}

// NewUnsubscribeHandler wires the handler.
func NewUnsubscribeHandler(d Deps, alerts *alert.Service) *UnsubscribeHandler {
	return &UnsubscribeHandler{Deps: d, Alerts: alerts}
}

// Register wires the unsubscribe routes; they take no session, since the sealed token is the credential (RFC 8058).
func (h *UnsubscribeHandler) Register(mux web.Mux) {
	mux.HandleFunc("GET /alerts/unsubscribe", h.Get)
	mux.HandleFunc("POST /alerts/unsubscribe", h.Post)
}

// Get shows the confirm form and records nothing, so a link prefetcher cannot unsubscribe anyone.
func (h *UnsubscribeHandler) Get(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("t")
	target, err := h.Alerts.UnsubscribeTarget(r.Context(), token)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, templates.UnsubscribeView{State: "confirm", Token: token, Address: target.Email, Project: target.Project})
}

// Post records the opt-out, from the confirm form or a mail client's one-click POST.
func (h *UnsubscribeHandler) Post(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.ErrorPageKey(w, r, http.StatusBadRequest, "error.bad_request", err)
		return
	}
	target, err := h.Alerts.Unsubscribe(r.Context(), r.Form.Get("t"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, http.StatusOK, templates.UnsubscribeView{State: "done", Address: target.Email, Project: target.Project})
}

func (h *UnsubscribeHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if alert.IsInvalidUnsubscribeTokenError(err) {
		h.render(w, r, http.StatusBadRequest, templates.UnsubscribeView{State: "invalid"})
		return
	}
	h.LogErr("web unsubscribe", err)
	h.ErrorPageKey(w, r, http.StatusInternalServerError, "error.load_failed", err)
}

func (h *UnsubscribeHandler) render(w http.ResponseWriter, r *http.Request, status int, v templates.UnsubscribeView) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	RenderStatus(w, r, status, templates.UnsubscribeLayout(h.Base(r, pageTitle(r, "unsubscribe.title")), v))
}
