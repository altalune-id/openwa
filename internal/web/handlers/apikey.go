package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/apikey"
	"altalune.id/template/internal/platform/authn"
	"altalune.id/template/internal/project"
	"altalune.id/template/internal/web"
	"altalune.id/template/internal/web/templates"
)

// APIKeyHandler owns the project-scoped API key console pages.
type APIKeyHandler struct {
	Deps
	Keys *apikey.Service
}

// NewAPIKeyHandler wires the handler.
func NewAPIKeyHandler(d Deps, projects *project.Service, keys *apikey.Service) *APIKeyHandler {
	d.Projects = projects
	return &APIKeyHandler{Deps: d, Keys: keys}
}

// GetKeys renders the API key list page.
func (h *APIKeyHandler) GetKeys(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	v, err := h.keysView(sc, nil, "", "")
	if err != nil {
		h.LogErr("web apikey: list", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "List failed", "Could not load API keys.", err)
		return
	}
	Render(w, sc.req, templates.APIKeysLayout(
		h.LayoutForProject(sc.req, "API keys · "+sc.project.Name, sc.org.Slug, sc.project, "apikeys"),
		v,
	))
}

// PostKeyCreate mints a key and renders the refreshed list with the one-time plaintext reveal.
func (h *APIKeyHandler) PostKeyCreate(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPage(w, sc.req, http.StatusBadRequest, "Bad request", "Could not parse form body.")
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("name"))
	scopes := selectedScopes(r.PostForm["scopes"])
	expiresAt, err := parseExpiry(r.PostForm.Get("expires_at"))
	if err != nil {
		h.writeKeyList(w, sc, nil, templates.APIKeyErrorInvalidExpiry, "")
		return
	}
	k, plaintext, err := h.Keys.Mint(sc.req.Context(), name, scopes, nil, expiresAt)
	if err != nil {
		h.LogErr("web apikey: mint", err)
		h.writeKeyList(w, sc, nil, apiKeyErrorKind(err), ErrorRef(err))
		return
	}
	// SECURITY: the plaintext is rendered once and discarded — never logged, stored on a row, or put in the session.
	minted := &templates.APIKeyMinted{Name: k.Name, Plaintext: plaintext}
	h.writeKeyList(w, sc, minted, "", "")
}

// PostKeyRevoke revokes a key and returns the refreshed list fragment.
func (h *APIKeyHandler) PostKeyRevoke(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.ErrorPage(w, sc.req, http.StatusBadRequest, "Bad id", "Malformed API key id.")
		return
	}
	if err := h.Keys.Revoke(sc.req.Context(), id); err != nil && !apikey.IsNotFoundError(err) {
		h.LogErr("web apikey: revoke", err)
		h.writeKeyList(w, sc, nil, apiKeyErrorKind(err), ErrorRef(err))
		return
	}
	h.writeKeyList(w, sc, nil, "", "")
}

func (h *APIKeyHandler) keysView(sc ProjectScope, minted *templates.APIKeyMinted, errKind, code string) (templates.APIKeysView, error) {
	items, err := h.Keys.List(sc.req.Context(), sc.project.ID)
	if err != nil {
		return templates.APIKeysView{}, err
	}
	rows := make([]templates.APIKeyRow, 0, len(items))
	for _, k := range items {
		rows = append(rows, apiKeyRow(k))
	}
	return templates.APIKeysView{
		OrgSlug:     sc.org.Slug,
		ProjectSlug: sc.project.Slug,
		ProjectName: sc.project.Name,
		Items:       rows,
		Scopes:      scopeOptions(),
		Minted:      minted,
		ErrorKind:   errKind,
		ErrorCode:   code,
	}, nil
}

func (h *APIKeyHandler) writeKeyList(w http.ResponseWriter, sc ProjectScope, minted *templates.APIKeyMinted, errKind, code string) {
	v, err := h.keysView(sc, minted, errKind, code)
	if err != nil {
		h.LogErr("web apikey: list", err)
		h.ErrorPage(w, sc.req, http.StatusInternalServerError, "List failed", "Could not load API keys.", err)
		return
	}
	Render(w, sc.req, templates.APIKeyList(h.ProjectFragmentBase(sc), v))
}

// Register wires the API key routes onto mux.
func (h *APIKeyHandler) Register(mux web.Mux) {
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/apikeys", h.GetKeys)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/apikeys", h.PostKeyCreate)
	mux.HandleFunc("POST /orgs/{org}/projects/{project}/apikeys/{id}/revoke", h.PostKeyRevoke)
}

// SECURITY: an unrecognized or duplicated scope value is dropped rather than reaching Mint.
func selectedScopes(raw []string) []string {
	set := make(map[string]bool, len(raw))
	for _, s := range raw {
		set[strings.TrimSpace(s)] = true
	}
	out := make([]string, 0, len(raw))
	for _, s := range authn.AllScopes() {
		if set[s] {
			out = append(out, s)
		}
	}
	return out
}

func parseExpiry(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		return nil, err
	}
	t = t.UTC()
	return &t, nil
}

func apiKeyErrorKind(err error) string {
	switch {
	case apikey.IsUnknownScopeError(err):
		return templates.APIKeyErrorScope
	case apikey.IsNotFoundError(err):
		return templates.APIKeyErrorNotFound
	default:
		return templates.APIKeyErrorFailed
	}
}

func apiKeyRow(k *apikey.APIKey) templates.APIKeyRow {
	labels := make([]string, 0, len(k.Scopes))
	for _, s := range k.Scopes {
		labels = append(labels, scopeLabelKey(s))
	}
	row := templates.APIKeyRow{
		ID:             k.ID.String(),
		Name:           k.Name,
		ScopeLabelKeys: labels,
		CreatedAt:      k.CreatedAt.Format(time.RFC3339),
		Revoked:        k.RevokedAt != nil,
	}
	if k.ExpiresAt != nil {
		row.HasExpiry = true
		row.ExpiresAt = k.ExpiresAt.Format(time.RFC3339)
	}
	if k.LastUsedAt != nil {
		row.HasLastUsed = true
		row.LastUsedAt = k.LastUsedAt.Format(time.RFC3339)
	}
	if k.RevokedAt != nil {
		row.RevokedAt = k.RevokedAt.Format(time.RFC3339)
	}
	return row
}

func scopeOptions() []templates.APIKeyScopeOption {
	all := authn.AllScopes()
	out := make([]templates.APIKeyScopeOption, 0, len(all))
	for _, s := range all {
		out = append(out, templates.APIKeyScopeOption{Value: s, LabelKey: scopeLabelKey(s)})
	}
	return out
}

//i18n:use apikey.scope.*
func scopeLabelKey(scope string) string {
	return "apikey.scope." + strings.ReplaceAll(scope, ":", "_")
}
