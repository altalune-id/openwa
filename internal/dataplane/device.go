package dataplane

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/authn"
	"altalune.id/openwa/internal/platform/publicid"
	"altalune.id/openwa/internal/platform/session"
)

type rulesView struct {
	GroupMode      string   `json:"group_mode"`
	AllowedSenders []string `json:"allowed_senders"`
	AllowedGroups  []string `json:"allowed_groups"`
	TriggerPrefix  string   `json:"trigger_prefix"`
	IgnoreFromMe   bool     `json:"ignore_from_me"`
}

type deviceView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Rules      rulesView  `json:"rules"`
	Version    int        `json:"version"`
	State      string     `json:"state"`
	Phone      string     `json:"phone"`
	PushName   string     `json:"push_name"`
	LastSeenAt *time.Time `json:"last_seen_at"`
}

type deviceListView struct {
	Devices []deviceView `json:"devices"`
}

type linkView struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	Method      string     `json:"method"`
	QRPNGBase64 string     `json:"qr_png_base64,omitempty"`
	Code        string     `json:"code,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	URL         string     `json:"url"`
}

type rulesPatch struct {
	GroupMode      *string   `json:"group_mode"`
	AllowedSenders *[]string `json:"allowed_senders"`
	AllowedGroups  *[]string `json:"allowed_groups"`
	TriggerPrefix  *string   `json:"trigger_prefix"`
	IgnoreFromMe   *bool     `json:"ignore_from_me"`
}

type deviceInput struct {
	Name  *string     `json:"name"`
	Rules *rulesPatch `json:"rules"`
}

type linkInput struct {
	Method string `json:"method"`
	Phone  string `json:"phone"`
}

func (h *Handler) listDevices(w http.ResponseWriter, r *http.Request) {
	req, ok := h.beginWrite(w, r)
	if !ok {
		return
	}
	p, err := h.authorizeScope(req, authn.ScopeDevicesRead)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	devs, err := h.devices.List(req.ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	all := p.ReachesWholeProject(req.scope.orgID, req.scope.projectID)
	out := deviceListView{Devices: make([]deviceView, 0, len(devs))}
	for _, d := range devs {
		if all || slices.Contains(p.ResourceIDs, d.ID) {
			out.Devices = append(out.Devices, deviceViewOf(d))
		}
	}
	h.writeJSON(w, r, out)
}

func (h *Handler) getDevice(w http.ResponseWriter, r *http.Request) {
	req, ref, ok := h.beginDevice(w, r, authn.ScopeDevicesRead)
	if !ok {
		return
	}
	id := ref.ID
	d, err := h.devices.Get(req.ctx, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	tag := etagFor(d.Version)
	w.Header().Set("ETag", tag)
	if matchesETag(r.Header.Get("If-None-Match"), tag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.writeJSON(w, r, deviceViewOf(d))
}

// SECURITY: the body is decoded but held until the key is authorized, so a bad key cannot tell 400 from the masked not-found.
func (h *Handler) createDevice(w http.ResponseWriter, r *http.Request) {
	req, ok := h.beginWrite(w, r)
	if !ok {
		return
	}
	raw, in, shapeErr := decodeJSON[deviceInput](r)
	if authErr := h.authorizeProject(req, authn.ScopeDevicesWrite); authErr != nil {
		h.fail(w, r, authErr)
		return
	}
	if shapeErr == nil && in.Name == nil {
		shapeErr = &BadRequestError{}
	}
	if shapeErr != nil {
		h.fail(w, r, shapeErr)
		return
	}

	h.createIdempotently(w, r, req.scope.projectID, idempotencyKeyOf(r, "devices:"), raw, func() (string, []byte, error) {
		d, err := h.devices.Create(req.ctx, *in.Name)
		if err != nil {
			return "", nil, err
		}
		body, err := json.Marshal(deviceViewOf(d))
		if err != nil {
			return "", nil, err
		}
		return etagFor(d.Version), body, nil
	})
}

func (h *Handler) patchDevice(w http.ResponseWriter, r *http.Request) {
	req, ref, ok := h.beginDevice(w, r, authn.ScopeDevicesWrite)
	if !ok {
		return
	}
	id := ref.ID
	ifVersion, err := ifMatchVersion(r.Header.Get("If-Match"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	_, in, err := decodeJSON[deviceInput](r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var rules *RulesRef
	if in.Rules != nil {
		rr := mergeRules(ref.Rules, *in.Rules)
		rules = &rr
	}
	d, err := h.devices.Update(req.ctx, id, in.Name, rules, ifVersion)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("ETag", etagFor(d.Version))
	h.writeJSON(w, r, deviceViewOf(d))
}

func (h *Handler) deleteDevice(w http.ResponseWriter, r *http.Request) {
	req, ref, ok := h.beginDevice(w, r, authn.ScopeDevicesWrite)
	if !ok {
		return
	}
	id := ref.ID
	if err := h.devices.Delete(req.ctx, id); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) unlinkDevice(w http.ResponseWriter, r *http.Request) {
	req, ref, ok := h.beginDevice(w, r, authn.ScopeDevicesWrite)
	if !ok {
		return
	}
	id := ref.ID
	if err := h.devices.Unlink(req.ctx, id); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) createLink(w http.ResponseWriter, r *http.Request) {
	req, ref, ok := h.beginDevice(w, r, authn.ScopeDevicesWrite)
	if !ok {
		return
	}
	id := ref.ID
	_, in, err := decodeJSON[linkInput](r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var link LinkRef
	switch in.Method {
	case "", "qr":
		link, err = h.devices.StartLink(req.ctx, id)
	case "phone":
		if strings.TrimSpace(in.Phone) == "" {
			h.fail(w, r, &BadRequestError{})
			return
		}
		link, err = h.devices.LinkWithPhone(req.ctx, id, in.Phone)
	default:
		h.fail(w, r, &BadRequestError{})
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	body, err := json.Marshal(h.linkViewOf(r, ref.PublicID, link))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeBytes(w, http.StatusCreated, body)
}

func (h *Handler) getLink(w http.ResponseWriter, r *http.Request) {
	req, ref, ok := h.beginDevice(w, r, authn.ScopeDevicesRead)
	if !ok {
		return
	}
	id := ref.ID
	link, err := h.devices.LinkState(req.ctx, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if link.Outcome == "none" || link.Outcome == "" {
		h.fail(w, r, &NotFoundError{})
		return
	}
	h.writeJSON(w, r, h.linkViewOf(r, ref.PublicID, link))
}

// SECURITY: a malformed public id is the masked 404 before any query; the key is authorized for the project and scope before the id is looked up, and its ResourceIDs are then checked against the resolved UUID.
func (h *Handler) beginDevice(w http.ResponseWriter, r *http.Request, scopeName string) (request, DeviceRef, bool) {
	req, ok := h.beginWrite(w, r)
	if !ok {
		return request{}, DeviceRef{}, false
	}
	raw := r.PathValue("device")
	if !publicid.Valid(device.PublicIDPrefix, raw) {
		h.fail(w, r, &NotFoundError{})
		return request{}, DeviceRef{}, false
	}
	p, err := h.authorizeScope(req, scopeName)
	if err != nil {
		h.fail(w, r, err)
		return request{}, DeviceRef{}, false
	}
	ref, err := h.devices.Resolve(req.ctx, raw)
	if err != nil {
		h.fail(w, r, err)
		return request{}, DeviceRef{}, false
	}
	if !p.ReachesResource(req.scope.orgID, req.scope.projectID, ref.ID) {
		h.fail(w, r, &NotFoundError{})
		return request{}, DeviceRef{}, false
	}
	return req, ref, true
}

// SECURITY: the key is admitted with its principal, never widened; the caller narrows by ReachesResource/ReachesWholeProject, and every failure is the masked not-found.
func (h *Handler) authorizeScope(req request, scopeName string) (session.Principal, error) {
	if h.authz == nil {
		return session.Principal{}, &NotFoundError{}
	}
	p, err := h.authz.AuthorizeScope(req.ctx, req.raw, scopeName, req.scope.orgID, req.scope.projectID)
	if err != nil {
		return session.Principal{}, &NotFoundError{}
	}
	return p, nil
}

func (h *Handler) linkViewOf(r *http.Request, devicePublicID string, l LinkRef) linkView {
	v := linkView{
		ID:     l.ID,
		Status: linkStatus(l.Outcome),
		Method: l.Method,
		Code:   l.PairingCode,
		URL:    h.basePath + "/orgs/" + r.PathValue("org") + "/projects/" + r.PathValue("project") + "/devices/" + devicePublicID + "/links/current",
	}
	if len(l.PNG) > 0 {
		v.QRPNGBase64 = base64.StdEncoding.EncodeToString(l.PNG)
	}
	if !l.ExpiresAt.IsZero() {
		exp := l.ExpiresAt.UTC()
		v.ExpiresAt = &exp
	}
	return v
}

func linkStatus(outcome string) string {
	switch outcome {
	case "timeout":
		return "expired"
	case "":
		return "failed"
	}
	return outcome
}

func deviceViewOf(d DeviceRef) deviceView {
	return deviceView{
		ID:   d.PublicID,
		Name: d.Name,
		Rules: rulesView{
			GroupMode:      d.Rules.GroupMode,
			AllowedSenders: nonNil(d.Rules.AllowedSenders),
			AllowedGroups:  nonNil(d.Rules.AllowedGroups),
			TriggerPrefix:  d.Rules.TriggerPrefix,
			IgnoreFromMe:   d.Rules.IgnoreFromMe,
		},
		Version:    d.Version,
		State:      d.State,
		Phone:      d.Phone,
		PushName:   d.PushName,
		LastSeenAt: d.LastSeenAt,
	}
}

// NOTE: mergeRules overlays only the fields the patch named onto the device's current rules, so an omitted field is kept, never zeroed.
func mergeRules(cur RulesRef, p rulesPatch) RulesRef {
	if p.GroupMode != nil {
		cur.GroupMode = *p.GroupMode
	}
	if p.AllowedSenders != nil {
		cur.AllowedSenders = *p.AllowedSenders
	}
	if p.AllowedGroups != nil {
		cur.AllowedGroups = *p.AllowedGroups
	}
	if p.TriggerPrefix != nil {
		cur.TriggerPrefix = *p.TriggerPrefix
	}
	if p.IgnoreFromMe != nil {
		cur.IgnoreFromMe = *p.IgnoreFromMe
	}
	return cur
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
