package handlers

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"text/template"

	"altalune.id/openwa/internal/apikey"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/project"
	"altalune.id/openwa/internal/web"
	"altalune.id/openwa/internal/web/templates"
	"altalune.id/openwa/internal/webhook"
)

//go:embed quickstart/*
var quickstartFS embed.FS

// DevelopersHandler owns the project quickstart page.
type DevelopersHandler struct {
	Deps
	Keys     *apikey.Service
	Devices  *device.Service
	Webhooks *webhook.Service
}

// NewDevelopersHandler wires the handler.
func NewDevelopersHandler(d Deps, projects *project.Service, keys *apikey.Service, devices *device.Service, hooks *webhook.Service) *DevelopersHandler {
	d.Projects = projects
	return &DevelopersHandler{Deps: d, Keys: keys, Devices: devices, Webhooks: hooks}
}

// Register wires the quickstart routes onto mux.
func (h *DevelopersHandler) Register(mux web.Mux) {
	const base = "/orgs/{org}/projects/{project}/developers"
	mux.HandleFunc("GET "+base, h.GetDevelopers)
}

// GetDevelopers renders the quickstart, or only its send section when htmx switches the number.
func (h *DevelopersHandler) GetDevelopers(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	v, err := h.view(sc, strings.TrimSpace(r.URL.Query().Get("device")))
	if err != nil {
		h.LogErr("web developers: view", err)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return
	}
	if web.IsHTMXRequest(r) && r.URL.Query().Has("device") {
		Render(w, sc.req, templates.DevelopersSend(h.ProjectFragmentBase(sc), v))
		return
	}
	Render(w, sc.req, templates.DevelopersLayout(h.LayoutForProject(sc.req, pageTitle(sc.req, "developers.title"), sc.org.Slug, sc.project, "quickstart"), v))
}

func (h *DevelopersHandler) view(sc ProjectScope, selected string) (templates.DevelopersView, error) {
	views, err := h.Devices.List(sc.req.Context())
	if err != nil {
		return templates.DevelopersView{}, err
	}
	hooks, err := h.Webhooks.List(sc.req.Context())
	if err != nil {
		return templates.DevelopersView{}, err
	}
	v := templates.DevelopersView{
		ProjectSlug: sc.project.Slug,
		BaseURL:     strings.TrimRight(h.Cfg.HTTP.BaseURL, "/") + web.Path(h.Cfg.HTTP.BasePath, "/api/v1/orgs/"+sc.org.Slug+"/projects/"+sc.project.Slug),
		CanManage:   h.canManageProject(sc),
		KeyPrefix:   h.Keys.Scheme().Prefix(),
	}
	for _, dv := range views {
		v.Devices = append(v.Devices, templates.WebhookDeviceOption{PublicID: dv.Device.PublicID, Name: dv.Device.Name, Phone: dv.Status.Phone})
	}
	// SECURITY: only a number listed for this project is ever echoed back into the snippets.
	if !slices.ContainsFunc(v.Devices, func(o templates.WebhookDeviceOption) bool { return o.PublicID == selected }) {
		selected = ""
		if len(v.Devices) > 0 {
			selected = v.Devices[0].PublicID
		}
	}
	v.Selected = selected
	for i := range v.Devices {
		v.Devices[i].Selected = v.Devices[i].PublicID == selected
	}
	if e := testEndpoint(hooks); e != nil {
		v.TestEndpointID = e.ID.String()
	}
	if v.Send, v.Verify, v.Payload, err = quickstartSnippets(v.BaseURL, snippetDevice(selected)); err != nil {
		return templates.DevelopersView{}, err
	}
	return v, nil
}

func snippetDevice(publicID string) string {
	if publicID == "" {
		return device.PublicIDPrefix + "_..."
	}
	return publicID
}

func testEndpoint(hooks []*webhook.Endpoint) *webhook.Endpoint {
	if i := slices.IndexFunc(hooks, func(e *webhook.Endpoint) bool { return e.Active && e.Subscribes(events.MessageMatched) }); i >= 0 {
		return hooks[i]
	}
	if i := slices.IndexFunc(hooks, func(e *webhook.Endpoint) bool { return e.Active }); i >= 0 {
		return hooks[i]
	}
	return nil
}

func quickstartSnippets(base, deviceID string) (send, verify []templates.WebhookSnippet, payload string, err error) { //nolint:nonamedreturns // three results differ in role
	data := struct{ Base, Device string }{Base: base, Device: deviceID}
	for _, src := range []struct{ lang, label, file string }{
		{"curl", "curl", "send.sh.tmpl"},
		{"js", "JavaScript", "send.mjs.tmpl"},
		{"python", "Python", "send.py.tmpl"},
	} {
		tmpl, err := template.ParseFS(quickstartFS, "quickstart/"+src.file)
		if err != nil {
			return nil, nil, "", fmt.Errorf("quickstart: parse %s: %w", src.file, err)
		}
		var b strings.Builder
		if err := tmpl.Execute(&b, data); err != nil {
			return nil, nil, "", fmt.Errorf("quickstart: render %s: %w", src.file, err)
		}
		send = append(send, templates.WebhookSnippet{Lang: src.lang, Label: src.label, Code: b.String()})
	}
	verifiers, err := webhook.VerifySnippets()
	if err != nil {
		return nil, nil, "", err
	}
	for _, s := range verifiers {
		if s.Lang == webhook.SnippetNode || s.Lang == webhook.SnippetPython {
			verify = append(verify, templates.WebhookSnippet{Lang: s.Lang, Label: s.Label, Code: s.Code})
		}
	}
	raw, err := quickstartFS.ReadFile("quickstart/message_matched.json")
	if err != nil {
		return nil, nil, "", fmt.Errorf("quickstart: payload: %w", err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return nil, nil, "", fmt.Errorf("quickstart: payload: %w", err)
	}
	return send, verify, pretty.String(), nil
}
