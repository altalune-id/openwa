package handlers

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/apikey"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/authn"
	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/platform/outbox"
	"altalune.id/openwa/internal/project"
	"altalune.id/openwa/internal/web"
	"altalune.id/openwa/internal/web/templates"
	"altalune.id/openwa/internal/webhook"
)

const maxConnectPolls = 10

// ConnectHandler owns the connect-an-agent card of one number.
type ConnectHandler struct {
	Deps
	Keys     *apikey.Service
	Devices  *device.Service
	Webhooks *webhook.Service
}

// NewConnectHandler wires the handler.
func NewConnectHandler(d Deps, projects *project.Service, keys *apikey.Service, devices *device.Service, hooks *webhook.Service) *ConnectHandler {
	d.Projects = projects
	return &ConnectHandler{Deps: d, Keys: keys, Devices: devices, Webhooks: hooks}
}

// Register wires the connect routes onto mux.
func (h *ConnectHandler) Register(mux web.Mux) {
	const base = "/orgs/{org}/projects/{project}/devices/{device}/connect"
	mux.HandleFunc("GET "+base, h.GetConnect)
	mux.HandleFunc("POST "+base+"/key", h.PostConnectKey)
	mux.HandleFunc("POST "+base+"/address", h.PostConnectAddress)
	mux.HandleFunc("POST "+base+"/test", h.PostConnectTest)
	mux.HandleFunc("GET "+base+"/test", h.GetConnectTest)
}

type connectScope struct {
	ProjectScope
	dv device.DeviceView
}

// GetConnect renders the card; it never carries a key or a secret.
func (h *ConnectHandler) GetConnect(w http.ResponseWriter, r *http.Request) {
	cs, ok := h.requireDevice(w, r, false)
	if !ok {
		return
	}
	v, ok := h.view(w, cs)
	if !ok {
		return
	}
	l := h.LayoutForProject(cs.req, pageTitle(cs.req, "connect.title"), cs.org.Slug, cs.project, "devices")
	l.Crumbs = append(l.Crumbs,
		web.Crumb{Label: l.Tr("nav.numbers"), Href: h.ProjectURL(cs.ProjectScope, "/devices")},
		web.Crumb{Label: cs.dv.Device.Name, Href: h.ProjectURL(cs.ProjectScope, "/devices/"+cs.dv.Device.PublicID)})
	Render(w, cs.req, templates.ConnectLayout(l, v))
}

// PostConnectKey mints a key bound to this number and renders the card with it, once.
func (h *ConnectHandler) PostConnectKey(w http.ResponseWriter, r *http.Request) {
	cs, ok := h.requireDevice(w, r, true)
	if !ok {
		return
	}
	scopes, _ := presetScopes(keyPresetSend)
	resourceIDs, err := h.Keys.ResourceIDs(cs.req.Context(), scopes, []string{cs.dv.Device.PublicID})
	var plaintext string
	if err == nil {
		expiresAt, _ := expiryFor(defaultExpiryDays, time.Now().UTC())
		_, plaintext, err = h.Keys.Mint(cs.req.Context(), cs.dv.Device.Name, scopes, resourceIDs, expiresAt)
	}
	if err != nil {
		h.LogErr("web connect: mint key", err)
		h.ErrorPageKey(w, cs.req, http.StatusInternalServerError, "error.load_failed", err)
		return
	}
	v, ok := h.view(w, cs)
	if !ok {
		return
	}
	// SECURITY: the plaintext key is rendered into this one no-store response and discarded.
	w.Header().Set("Cache-Control", "no-store")
	v.KeyPlaintext = plaintext
	Render(w, cs.req, templates.ConnectCard(h.ProjectFragmentBase(cs.ProjectScope), v))
}

// PostConnectAddress creates this number's message.matched webhook and renders the card with its secret, once.
func (h *ConnectHandler) PostConnectAddress(w http.ResponseWriter, r *http.Request) {
	cs, ok := h.requireDevice(w, r, true)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPageKey(w, cs.req, http.StatusBadRequest, "error.bad_request", nil)
		return
	}
	v, ok := h.view(w, cs)
	if !ok {
		return
	}
	if v.Endpoint != nil {
		Render(w, cs.req, templates.ConnectCard(h.ProjectFragmentBase(cs.ProjectScope), v))
		return
	}
	rawURL := strings.TrimSpace(r.PostForm.Get("url"))
	e, secret, err := h.Webhooks.Create(cs.req.Context(), rawURL, "", []events.Type{events.MessageMatched}, cs.dv.Device.PublicID)
	if webhook.IsDuplicateEndpointError(err) {
		if v, ok = h.view(w, cs); ok {
			Render(w, cs.req, templates.ConnectCard(h.ProjectFragmentBase(cs.ProjectScope), v))
		}
		return
	}
	if err != nil {
		if !isWebhookRefusal(err) {
			h.LogErr("web connect: create webhook", err)
		}
		werr := webhookError(err)
		v.AddressValue, v.AddressError, v.AddressMax = rawURL, werr.Key, werr.Max
		Render(w, cs.req, templates.ConnectCard(h.ProjectFragmentBase(cs.ProjectScope), v))
		return
	}
	// SECURITY: the signing secret is rendered into this one no-store response and discarded.
	w.Header().Set("Cache-Control", "no-store")
	v.Endpoint, v.Secret = &templates.ConnectEndpoint{ID: e.ID.String(), URL: e.URL}, secret
	Render(w, cs.req, templates.ConnectCard(h.ProjectFragmentBase(cs.ProjectScope), v))
}

// PostConnectTest queues a webhook.ping to this number's webhook and starts the poll.
func (h *ConnectHandler) PostConnectTest(w http.ResponseWriter, r *http.Request) {
	cs, ok := h.requireDevice(w, r, true)
	if !ok {
		return
	}
	v, ok := h.view(w, cs)
	if !ok {
		return
	}
	if v.Endpoint == nil {
		Render(w, cs.req, templates.ConnectTestResult(h.ProjectFragmentBase(cs.ProjectScope), v))
		return
	}
	id, err := uuid.Parse(v.Endpoint.ID)
	if err == nil {
		err = h.Webhooks.SendTest(cs.req.Context(), id)
	}
	if err != nil {
		h.logTestFailure(err)
		h.ErrorPageKey(w, cs.req, http.StatusConflict, webhookActionError(err).Key, err)
		return
	}
	v.Test = templates.ConnectTest{State: "waiting", PollURL: h.pollURL(cs, 1)}
	Render(w, cs.req, templates.ConnectTestResult(h.ProjectFragmentBase(cs.ProjectScope), v))
}

// GetConnectTest renders the latest delivery of this number's webhook in plain words, polling a bounded number of times.
func (h *ConnectHandler) GetConnectTest(w http.ResponseWriter, r *http.Request) {
	cs, ok := h.requireDevice(w, r, true)
	if !ok {
		return
	}
	v, ok := h.view(w, cs)
	if !ok {
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if v.Endpoint != nil {
		t, err := h.testState(cs, v.Endpoint.ID)
		if err != nil {
			h.LogErr("web connect: test state", err)
			h.ErrorPageKey(w, cs.req, http.StatusInternalServerError, "error.load_failed", err)
			return
		}
		if t.State == "waiting" && n >= 0 && n < maxConnectPolls {
			t.PollURL, t.Polls = h.pollURL(cs, n+1), n
		}
		v.Test = t
	}
	Render(w, cs.req, templates.ConnectTestResult(h.ProjectFragmentBase(cs.ProjectScope), v))
}

func (h *ConnectHandler) logTestFailure(err error) {
	if !isWebhookRefusal(err) {
		h.LogErr("web connect: send test", err)
	}
}

func (h *ConnectHandler) requireDevice(w http.ResponseWriter, r *http.Request, write bool) (connectScope, bool) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return connectScope{}, false
	}
	d, err := h.Devices.Resolve(sc.req.Context(), r.PathValue("device"))
	if err == nil {
		var dv device.DeviceView
		if dv, err = h.Devices.Get(sc.req.Context(), d.ID); err == nil {
			if write && !h.requireManager(w, sc) {
				return connectScope{}, false
			}
			return connectScope{ProjectScope: sc, dv: dv}, true
		}
	}
	if device.IsNotFoundError(err) {
		h.ErrorPageKey(w, sc.req, http.StatusNotFound, "error.not_found", err)
		return connectScope{}, false
	}
	h.LogErr("web connect: device", err)
	h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
	return connectScope{}, false
}

func (h *ConnectHandler) view(w http.ResponseWriter, cs connectScope) (templates.ConnectView, bool) {
	dev, st := cs.dv.Device, cs.dv.Status
	v := templates.ConnectView{
		ProjectSlug: cs.project.Slug,
		OrgSlug:     cs.org.Slug,
		BaseURL:     strings.TrimRight(h.Cfg.HTTP.BaseURL, "/") + web.Path(h.Cfg.HTTP.BasePath, "/api/v1/orgs/"+cs.org.Slug+"/projects/"+cs.project.Slug),
		DeviceID:    dev.PublicID,
		DeviceName:  dev.Name,
		Phone:       st.Phone,
		NeverLinked: st.Phone == "" && st.LastConnectedAt == nil,
		CanManage:   h.canManageProject(cs.ProjectScope),
		Test:        templates.ConnectTest{State: "idle"},
	}
	if a := h.Cfg.Connect.RecommendedAgent; a.Name != "" {
		v.Recommended = &templates.RecommendedAgent{Name: a.Name, ConsoleURL: a.ConsoleURL, GuideURL: a.GuideURL}
	}
	keys, err := h.Keys.List(cs.req.Context(), cs.project.ID)
	if err == nil {
		v.KeyCreated = slices.ContainsFunc(keys, func(k *apikey.APIKey) bool { return isConnectKey(k, dev.ID) })
		var hooks []*webhook.Endpoint
		if hooks, err = h.Webhooks.List(cs.req.Context()); err == nil {
			if i := slices.IndexFunc(hooks, func(e *webhook.Endpoint) bool { return isConnectEndpoint(e, dev.ID) }); i >= 0 {
				v.Endpoint = &templates.ConnectEndpoint{ID: hooks[i].ID.String(), URL: hooks[i].URL}
			}
		}
	}
	if err != nil {
		h.LogErr("web connect: load", err)
		h.ErrorPageKey(w, cs.req, http.StatusInternalServerError, "error.load_failed", err)
		return templates.ConnectView{}, false
	}
	return v, true
}

func (h *ConnectHandler) testState(cs connectScope, endpointID string) (templates.ConnectTest, error) {
	id, err := uuid.Parse(endpointID)
	if err != nil {
		return templates.ConnectTest{}, err
	}
	ds, err := h.Webhooks.Deliveries(cs.req.Context(), id, webhook.MaxDeliveriesListed)
	if err != nil {
		return templates.ConnectTest{}, err
	}
	i := slices.IndexFunc(ds, func(d webhook.Delivery) bool { return d.EventType == events.WebhookPing })
	if i < 0 {
		return templates.ConnectTest{State: "waiting"}, nil
	}
	d := ds[i]
	switch {
	case d.Status == outbox.StatusDelivered:
		return templates.ConnectTest{State: "delivered"}, nil
	case d.Status == outbox.StatusPending && d.Attempt == 0:
		return templates.ConnectTest{State: "waiting"}, nil
	}
	t := templates.ConnectTest{State: "failed", Reason: "unreachable"}
	if d.Status == outbox.StatusPending {
		t.State = "retrying"
	}
	attempts, err := h.Webhooks.Attempts(cs.req.Context(), id, d.ID)
	if err != nil {
		return templates.ConnectTest{}, err
	}
	if len(attempts) > 0 && attempts[0].StatusCode > 0 {
		t.Reason, t.Code = "status", attempts[0].StatusCode
	}
	return t, nil
}

func (h *ConnectHandler) pollURL(cs connectScope, n int) string {
	return h.ProjectURL(cs.ProjectScope, "/devices/"+cs.dv.Device.PublicID+"/connect/test?n="+strconv.Itoa(n))
}

func isConnectKey(k *apikey.APIKey, deviceID uuid.UUID) bool {
	return k.Usable(time.Now()) && slices.Contains(k.ResourceIDs, deviceID) && slices.Contains(k.Scopes, authn.ScopeMessagesWrite)
}

func isConnectEndpoint(e *webhook.Endpoint, deviceID uuid.UUID) bool {
	return e.DeviceID != nil && *e.DeviceID == deviceID && e.Subscribes(events.MessageMatched)
}
