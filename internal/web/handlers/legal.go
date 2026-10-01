package handlers

import (
	"net/http"

	"altalune.id/openwa/internal/legal"
	"altalune.id/openwa/internal/web"
	"altalune.id/openwa/internal/web/templates"
)

// LegalHandler serves the embedded Terms and Privacy documents at /terms and /privacy.
type LegalHandler struct{ Deps }

// NewLegalHandler wires the handler.
func NewLegalHandler(d Deps) *LegalHandler { return &LegalHandler{Deps: d} }

// Register mounts the /terms and /privacy routes on mux.
func (h *LegalHandler) Register(mux web.Mux) {
	mux.HandleFunc("GET /terms", h.get(legal.TermsSlug, "Terms of Service"))
	mux.HandleFunc("GET /privacy", h.get(legal.PrivacySlug, "Privacy Policy"))
}

func (h *LegalHandler) get(slug, fallbackTitle string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		doc, err := legal.BySlug(slug)
		if err != nil {
			h.LogErr("legal: load", err)
			h.ErrorPageKey(w, r, http.StatusInternalServerError, "error.load_failed", err)
			return
		}
		title := doc.Title
		if title == "" {
			title = fallbackTitle
		}
		Render(w, r, templates.LegalLayout(h.Base(r, title), templates.LegalView{Doc: doc}))
	}
}
