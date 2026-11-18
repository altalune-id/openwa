package handlers

import (
	"cmp"
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/project"
	"altalune.id/openwa/internal/web"
	"altalune.id/openwa/internal/web/templates"
	"altalune.id/openwa/internal/whatsapp"
)

// DeviceHandler owns the project-scoped device console pages.
type DeviceHandler struct {
	Deps
	Devices  *device.Service
	Messages *message.Service
}

// NewDeviceHandler wires the handler; messages may be nil.
func NewDeviceHandler(d Deps, projects *project.Service, devices *device.Service, messages *message.Service) *DeviceHandler {
	d.Projects = projects
	return &DeviceHandler{Deps: d, Devices: devices, Messages: messages}
}

// Register wires the device routes onto mux.
func (h *DeviceHandler) Register(mux web.Mux) {
	const base = "/orgs/{org}/projects/{project}/devices"
	mux.HandleFunc("GET "+base, h.GetDevices)
	mux.HandleFunc("GET "+base+"/new", h.GetDeviceNew)
	mux.HandleFunc("POST "+base, h.PostDeviceCreate)
	mux.HandleFunc("GET "+base+"/{device}", h.GetDevice)
	mux.HandleFunc("POST "+base+"/{device}/links", h.PostDeviceLink)
	mux.HandleFunc("GET "+base+"/{device}/links/current", h.GetDeviceLinkCurrent)
	mux.HandleFunc("POST "+base+"/{device}/phone-links", h.PostDevicePhoneLink)
	mux.HandleFunc("POST "+base+"/{device}/unlink", h.PostDeviceUnlink)
	mux.HandleFunc("POST "+base+"/{device}/rules", h.PostDeviceRules)
	mux.HandleFunc("POST "+base+"/{device}/rename", h.PostDeviceRename)
	mux.HandleFunc("POST "+base+"/{device}/delete", h.PostDeviceDelete)
}

// GetDevices renders the devices list.
func (h *DeviceHandler) GetDevices(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	h.rememberProject(sc)
	views, err := h.Devices.List(sc.req.Context())
	if err != nil {
		h.LogErr("web device: list", err)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return
	}
	v := templates.DevicesView{ProjectSlug: sc.project.Slug, Total: len(views), Cards: make([]templates.DeviceCard, 0, len(views)), CanManage: h.canManageProject(sc)}
	for _, dv := range views {
		switch dv.Status.State {
		case device.SessionConnected:
			v.Connected++
		case device.SessionLoggedOut:
			v.Attention++
		}
		v.Cards = append(v.Cards, templates.DeviceCard{
			ID:       dv.Device.PublicID,
			Name:     dv.Device.Name,
			Phone:    dv.Status.Phone,
			PushName: dv.Status.PushName,
			StateKey: sessionStatus(dv.Status.State).Key,
			Tone:     sessionStatus(dv.Status.State).Tone,
			LastSeen: dv.Status.LastSeen,
		})
	}
	l := h.layout(sc, "")
	Render(w, sc.req, templates.DevicesLayout(l, v))
}

// GetDeviceNew renders the first-run page: the start button, the pairing of ?device, or its name step.
func (h *DeviceHandler) GetDeviceNew(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	if !h.requireManager(w, sc) {
		return
	}
	v := templates.DeviceConnectView{ProjectSlug: sc.project.Slug}
	if r.URL.Query().Get("device") == "" {
		h.writeConnect(w, sc, http.StatusOK, v)
		return
	}
	dv, ok := h.loadDevice(w, sc, r.URL.Query().Get("device"))
	if !ok {
		return
	}
	if r.URL.Query().Get("step") == "name" {
		v.Naming = nameStepView(dv, "")
		h.writeConnect(w, sc, http.StatusOK, v)
		return
	}
	st, err := h.Devices.LinkState(sc.req.Context(), dv.Device.ID)
	if err != nil {
		h.LogErr("web device: link state", err)
		st = device.LinkState{Outcome: device.LinkNone}
	}
	p := h.pairingView(sc, dv, st, pairingInput{flow: true}, true)
	p.FirstPaint = true
	v.Pairing = &p
	h.writeConnect(w, sc, http.StatusOK, v)
}

func (h *DeviceHandler) writeConnect(w http.ResponseWriter, sc ProjectScope, status int, v templates.DeviceConnectView) {
	l := h.layout(sc, "devices.connect.title")
	l.ActiveNav.Scope = web.NavScopeNone
	RenderStatus(w, sc.req, status, templates.DeviceConnectLayout(l, v))
}

func nameStepView(dv device.DeviceView, errText string) *templates.DeviceNameView {
	n := &templates.DeviceNameView{DeviceID: dv.Device.PublicID, Version: dv.Device.Version, Value: dv.Device.Name, Phone: dv.Status.Phone, Error: errText}
	if dv.Status.PushName != "" && device.DefaultName == dv.Device.Name {
		n.Value, n.FromProfile = dv.Status.PushName, true
	}
	return n
}

// PostDeviceCreate creates a device and redirects to its detail page.
func (h *DeviceHandler) PostDeviceCreate(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	if !h.requireManager(w, sc) {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPageKey(w, sc.req, http.StatusBadRequest, "error.bad_request", err)
		return
	}
	if !r.PostForm.Has("name") {
		h.createAndPair(w, sc)
		return
	}
	name := r.PostForm.Get("name")
	d, err := h.Devices.Create(sc.req.Context(), name)
	if err != nil {
		key, status := deviceFormError(err)
		if status >= http.StatusInternalServerError {
			h.LogErr("web device: create", err)
		}
		l := h.layout(sc, "devices.new")
		RenderStatus(w, sc.req, status, templates.DeviceNewLayout(l, templates.DeviceFormView{ProjectSlug: sc.project.Slug, Name: name, Error: l.Tr(key)}))
		return
	}
	h.SetFlash(w, sc.req, web.FlashOK, "flash.device_created", "Name", d.Name)
	h.redirect(w, sc, "/devices/"+d.PublicID)
}

func (h *DeviceHandler) createAndPair(w http.ResponseWriter, sc ProjectScope) {
	d, _, err := h.Devices.CreateDefault(sc.req.Context())
	if err != nil {
		key, status := deviceFormError(err)
		if status >= http.StatusInternalServerError {
			h.LogErr("web device: create default", err)
		}
		l := h.layout(sc, "devices.connect.title")
		h.writeConnect(w, sc, status, templates.DeviceConnectView{ProjectSlug: sc.project.Slug, Error: l.Tr(key)})
		return
	}
	_, err = h.Devices.StartLink(sc.req.Context(), d.ID)
	if err == nil {
		h.redirect(w, sc, "/devices/new?device="+d.PublicID)
		return
	}
	h.logUnless(err, "web device: create and pair")
	dv := device.DeviceView{Device: d, Status: device.SessionStatus{State: device.SessionUnlinked}}
	p := h.pairingView(sc, dv, device.LinkState{Outcome: device.LinkFailed}, pairingInput{flow: true}, true)
	p.ErrorKey = deviceErrorKey(err)
	p.FirstPaint = true
	h.writeConnect(w, sc, deviceWriteStatus(err), templates.DeviceConnectView{ProjectSlug: sc.project.Slug, Pairing: &p})
}

// GetDevice renders the detail page.
func (h *DeviceHandler) GetDevice(w http.ResponseWriter, r *http.Request) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	h.writeDetail(w, sc, dv, http.StatusOK, detailInput{settings: r.URL.Query().Get("tab") == "settings"})
}

// PostDeviceLink starts a QR attempt and returns the pairing fragment.
func (h *DeviceHandler) PostDeviceLink(w http.ResponseWriter, r *http.Request) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	if !h.requireManager(w, sc) {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPageKey(w, sc.req, http.StatusBadRequest, "error.bad_request", err)
		return
	}
	in := readPairingInput(r)
	st, err := h.Devices.StartLink(sc.req.Context(), dv.Device.ID)
	if !web.IsHTMXRequest(r) {
		if err != nil {
			h.SetFlash(w, sc.req, web.FlashErr, "flash.device_action_failed")
		}
		h.redirect(w, sc, in.returnPath(dv.Device.PublicID))
		return
	}
	h.writePairing(w, sc, dv, st, err, in)
}

// GetDeviceLinkCurrent returns the pairing fragment for the current attempt.
func (h *DeviceHandler) GetDeviceLinkCurrent(w http.ResponseWriter, r *http.Request) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	st, err := h.Devices.LinkState(sc.req.Context(), dv.Device.ID)
	h.writePairing(w, sc, dv, st, err, readPairingInput(r))
}

// PostDevicePhoneLink asks for a pairing code and returns the pairing fragment.
func (h *DeviceHandler) PostDevicePhoneLink(w http.ResponseWriter, r *http.Request) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	if !h.requireManager(w, sc) {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPageKey(w, sc.req, http.StatusBadRequest, "error.bad_request", err)
		return
	}
	in := readPairingInput(r)
	in.tab = "code"
	st, err := h.Devices.LinkWithPhone(sc.req.Context(), dv.Device.ID, in.phone)
	if !web.IsHTMXRequest(r) && err == nil {
		h.redirect(w, sc, in.returnPath(dv.Device.PublicID))
		return
	}
	h.writePairing(w, sc, dv, st, err, in)
}

// PostDeviceUnlink logs the device's account out.
func (h *DeviceHandler) PostDeviceUnlink(w http.ResponseWriter, r *http.Request) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	if !h.requireManager(w, sc) {
		return
	}
	if err := h.Devices.Unlink(sc.req.Context(), dv.Device.ID); err != nil {
		h.logUnless(err, "web device: unlink")
		h.SetFlash(w, sc.req, web.FlashErr, "flash.device_action_failed")
		h.redirect(w, sc, "/devices/"+dv.Device.PublicID)
		return
	}
	h.SetFlash(w, sc.req, web.FlashOK, "flash.device_logged_out", "Name", dv.Device.Name)
	h.redirect(w, sc, "/devices/"+dv.Device.PublicID)
}

// PostDeviceRules replaces the inbound rules.
func (h *DeviceHandler) PostDeviceRules(w http.ResponseWriter, r *http.Request) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	if !h.requireManager(w, sc) {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPageKey(w, sc.req, http.StatusBadRequest, "error.bad_request", err)
		return
	}
	rules := rulesFromForm(r)
	version, err := formVersion(r, dv.Device.Version)
	if err == nil {
		_, err = h.Devices.UpdateLegacyRules(sc.req.Context(), dv.Device.ID, rules, version)
	}
	switch {
	case err == nil:
		h.SetFlash(w, sc.req, web.FlashOK, "flash.device_rules_saved")
	case device.IsRulesShapeConflictError(err):
		h.SetFlash(w, sc.req, web.FlashErr, "flash.rules_use_editor")
	default:
		h.logUnless(err, "web device: rules")
		h.SetFlash(w, sc.req, web.FlashErr, deviceErrorKey(err))
	}
	h.redirect(w, sc, "/devices/"+dv.Device.PublicID+"/rules")
}

// PostDeviceRename changes the device name.
func (h *DeviceHandler) PostDeviceRename(w http.ResponseWriter, r *http.Request) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPageKey(w, sc.req, http.StatusBadRequest, "error.bad_request", err)
		return
	}
	name := r.PostForm.Get("name")
	version, err := formVersion(r, dv.Device.Version)
	if err != nil {
		h.writeDetail(w, sc, dv, deviceWriteStatus(err), detailInput{name: &name, renameErr: err})
		return
	}
	if _, err := h.Devices.Rename(sc.req.Context(), dv.Device.ID, name, version); err != nil {
		h.logUnless(err, "web device: rename")
		if renameNext(r) != "" {
			l := h.layout(sc, "")
			v := templates.DeviceConnectView{ProjectSlug: sc.project.Slug, Naming: nameStepView(dv, l.Tr(deviceErrorKey(err)))}
			v.Naming.Value, v.Naming.FromProfile = name, false
			h.writeConnect(w, sc, deviceWriteStatus(err), v)
			return
		}
		h.writeDetail(w, sc, dv, deviceWriteStatus(err), detailInput{name: &name, renameErr: err})
		return
	}
	h.SetFlash(w, sc.req, web.FlashOK, "flash.device_renamed")
	h.redirect(w, sc, "/devices/"+dv.Device.PublicID+renameNext(r))
}

// PostDeviceDelete logs out and deletes the device.
func (h *DeviceHandler) PostDeviceDelete(w http.ResponseWriter, r *http.Request) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	if !h.requireManager(w, sc) {
		return
	}
	if err := h.Devices.Delete(sc.req.Context(), dv.Device.ID); err != nil {
		h.logUnless(err, "web device: delete")
		h.SetFlash(w, sc.req, web.FlashErr, "flash.device_action_failed")
		h.redirect(w, sc, "/devices/"+dv.Device.PublicID)
		return
	}
	h.SetFlash(w, sc.req, web.FlashOK, "flash.device_deleted", "Name", dv.Device.Name)
	h.redirect(w, sc, "/devices")
}

type detailInput struct {
	name      *string
	renameErr error
	settings  bool
}

// SECURITY: next is a fixed step name mapped to a path suffix, never a URL, so rename cannot redirect off the device.
func renameNext(r *http.Request) string {
	if r.PostForm.Get("next") == "rules" {
		return "/rules"
	}
	return ""
}

type pairingInput struct {
	flow  bool
	tab   string
	phone string
}

func readPairingInput(r *http.Request) pairingInput {
	return pairingInput{flow: r.FormValue("flow") == "new", tab: r.FormValue("pair_tab"), phone: strings.TrimSpace(r.FormValue("phone"))}
}

func (in pairingInput) returnPath(publicID string) string {
	if in.flow {
		return "/devices/new?device=" + publicID
	}
	return "/devices/" + publicID
}

func (h *DeviceHandler) requireDevice(w http.ResponseWriter, r *http.Request) (ProjectScope, device.DeviceView, bool) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return ProjectScope{}, device.DeviceView{}, false
	}
	dv, ok := h.loadDevice(w, sc, r.PathValue("device"))
	return sc, dv, ok
}

func (h *DeviceHandler) loadDevice(w http.ResponseWriter, sc ProjectScope, publicID string) (device.DeviceView, bool) {
	d, err := h.Devices.Resolve(sc.req.Context(), publicID)
	if err != nil {
		if device.IsNotFoundError(err) {
			h.ErrorPageKey(w, sc.req, http.StatusNotFound, "error.not_found", err)
			return device.DeviceView{}, false
		}
		h.LogErr("web device: resolve", err)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return device.DeviceView{}, false
	}
	dv, err := h.Devices.Get(sc.req.Context(), d.ID)
	if err != nil {
		if device.IsNotFoundError(err) {
			h.ErrorPageKey(w, sc.req, http.StatusNotFound, "error.not_found", err)
			return device.DeviceView{}, false
		}
		h.LogErr("web device: get", err)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return device.DeviceView{}, false
	}
	return dv, true
}

func (h *DeviceHandler) writeDetail(w http.ResponseWriter, sc ProjectScope, dv device.DeviceView, status int, in detailInput) {
	ctx := sc.req.Context()
	st, err := h.Devices.LinkState(ctx, dv.Device.ID)
	if err != nil {
		h.LogErr("web device: link state", err)
		st = device.LinkState{Outcome: device.LinkNone}
	}
	canManage := h.canManageProject(sc)
	l := h.layout(sc, "")
	l.Crumbs[len(l.Crumbs)-1].Href = h.ProjectURL(sc, "/devices")
	l.Crumbs = append(l.Crumbs, web.Crumb{Label: dv.Device.Name})
	l.ActiveNav.Parent = "devices"

	d := dv.Device
	v := templates.DeviceDetailView{
		ProjectSlug:  sc.project.Slug,
		ID:           d.PublicID,
		Name:         d.Name,
		Version:      d.Version,
		State:        string(cmp.Or(dv.Status.State, device.SessionUnlinked)),
		Status:       sessionStatus(dv.Status.State),
		Connected:    dv.Status.State == device.SessionConnected,
		Reconnecting: dv.Status.State == device.SessionDisconnected,
		SignedOut:    dv.Status.State == device.SessionLoggedOut,
		Phone:        dv.Status.Phone,
		PushName:     dv.Status.PushName,
		Reason:       dv.Status.Reason,
		ChangedAt:    dv.Status.UpdatedAt,
		Timeline:     numberTimeline(dv),
		Settings:     in.settings || in.renameErr != nil,
		RenameValue:  d.Name,
		Pairing:      h.pairingView(sc, dv, st, pairingInput{}, canManage),
		CanManage:    canManage,
	}
	if h.Messages != nil {
		act, err := h.Messages.Activity(ctx, []uuid.UUID{d.ID})
		if err != nil {
			h.LogErr("web device: activity", err)
		}
		v.LastMessage = act[d.ID].LastAt
	}
	if in.name != nil {
		v.RenameValue = *in.name
	}
	if in.renameErr != nil {
		v.RenameError = l.Tr(deviceErrorKey(in.renameErr))
	}
	RenderStatus(w, sc.req, status, templates.DeviceDetailLayout(l, v))
}

//i18n:use devices.timeline.*
func numberTimeline(dv device.DeviceView) []templates.TimelineEntry {
	st := dv.Status
	out := []templates.TimelineEntry{{At: dv.Device.CreatedAt, Key: "devices.timeline.added"}}
	if st.LastConnectedAt != nil {
		e := templates.TimelineEntry{At: *st.LastConnectedAt, Key: "devices.timeline.connected"}
		if st.Phone != "" && st.State != device.SessionLoggedOut {
			e.Key, e.Phone = "devices.timeline.connected_as", st.Phone
		}
		out = append(out, e)
	}
	key := ""
	switch st.State {
	case device.SessionLoggedOut:
		key = "devices.timeline.signed_out"
	case device.SessionDisconnected:
		key = "devices.timeline.reconnecting"
	case device.SessionUnlinked:
		if st.LastConnectedAt != nil {
			key = "devices.timeline.unlinked"
		}
	}
	if key != "" && !st.UpdatedAt.IsZero() {
		out = append(out, templates.TimelineEntry{At: st.UpdatedAt, Key: key})
	}
	slices.SortStableFunc(out, func(a, b templates.TimelineEntry) int { return a.At.Compare(b.At) })
	return out
}

func (h *DeviceHandler) writePairing(w http.ResponseWriter, sc ProjectScope, dv device.DeviceView, st device.LinkState, err error, in pairingInput) {
	base := h.ProjectFragmentBase(sc)
	canManage := h.canManageProject(sc)
	if err == nil {
		Render(w, sc.req, templates.DevicePairing(base, h.pairingView(sc, dv, st, in, canManage)))
		return
	}
	h.logUnless(err, "web device: link")
	status := deviceWriteStatus(err)
	v := h.pairingView(sc, dv, device.LinkState{Outcome: device.LinkFailed}, in, canManage)
	v.ErrorKey = deviceErrorKey(err)
	if whatsapp.IsInvalidPhoneError(err) {
		v.ErrorKey = ""
		v.PhoneError = base.Tr("devices.error.invalid_phone")
	}
	RenderStatus(w, sc.req, status, templates.DevicePairing(base, v))
}

func (h *DeviceHandler) pairingView(sc ProjectScope, dv device.DeviceView, st device.LinkState, in pairingInput, canManage bool) templates.DevicePairingView {
	v := templates.DevicePairingView{
		ProjectSlug: sc.project.Slug,
		DeviceID:    dv.Device.PublicID,
		Name:        dv.Device.Name,
		Outcome:     string(st.Outcome),
		PairingCode: st.PairingCode,
		ExpiresAt:   st.ExpiresAt,
		Linked:      dv.Status.State == device.SessionConnected || dv.Status.State == device.SessionDisconnected,
		Phone:       dv.Status.Phone,
		PushName:    dv.Status.PushName,
		PhoneValue:  in.phone,
		CanManage:   canManage,
		Flow:        in.flow,
		Relink:      dv.Status.State == device.SessionLoggedOut,
		LocalPhones: h.Cfg != nil && h.Cfg.WhatsApp.DefaultCountryCode != "",
		Tab:         in.tab,
	}
	if st.Method == device.LinkPhone && in.tab == "" {
		v.Tab = "code"
	}
	if len(st.PNG) > 0 {
		v.PNGBase64 = base64.StdEncoding.EncodeToString(st.PNG)
	}
	// SECURITY: a pending QR or pairing code lets whoever scans it first take the number, so only a manager sees it.
	if !canManage {
		v.PNGBase64, v.PairingCode = "", ""
	}
	//i18n:use devices.link_expired
	//i18n:use devices.link_failed
	switch st.Outcome {
	case device.LinkTimeout:
		v.ErrorKey = "devices.link_expired"
	case device.LinkFailed:
		v.ErrorKey = "devices.link_failed"
	}
	return v
}

// NOTE: leafKey names a nested page's last crumb; the Devices crumb then links back to the list.
func (h *DeviceHandler) layout(sc ProjectScope, leafKey string) web.LayoutData {
	l := h.LayoutForProject(sc.req, "", sc.org.Slug, sc.project, "devices")
	l.Title = l.Tr("nav.devices")
	devices := web.Crumb{Label: l.Tr("nav.devices")}
	if leafKey == "" {
		l.Crumbs = append(l.Crumbs, devices)
		return l
	}
	devices.Href = h.ProjectURL(sc, "/devices")
	l.Crumbs = append(l.Crumbs, devices, web.Crumb{Label: l.Tr(leafKey)})
	return l
}

func (h *DeviceHandler) redirect(w http.ResponseWriter, sc ProjectScope, suffix string) {
	http.Redirect(w, sc.req, h.ProjectURL(sc, suffix), http.StatusSeeOther) //nolint:gosec // G710: both slugs come from resolved rows and suffix is built from a stored public id
}

func (h *DeviceHandler) logUnless(err error, msg string) {
	if deviceWriteStatus(err) >= http.StatusInternalServerError {
		h.LogErr(msg, err)
	}
}

func formVersion(r *http.Request, current int) (int, error) {
	v, err := strconv.Atoi(r.PostForm.Get("version"))
	if err != nil || v < 1 {
		return 0, &device.StaleVersionError{Got: current}
	}
	return v, nil
}

func rulesFromForm(r *http.Request) device.Legacy {
	return device.Legacy{
		GroupMode:      device.GroupMode(r.PostForm.Get("group_mode")),
		AllowedSenders: splitList(r.PostForm.Get("allowed_senders")),
		AllowedGroups:  splitList(r.PostForm.Get("allowed_groups")),
		TriggerPrefix:  r.PostForm.Get("trigger_prefix"),
		IgnoreFromMe:   r.PostForm.Get("ignore_from_me") == "1",
	}
}

func splitList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(c rune) bool { return c == '\n' || c == '\r' || c == ',' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

//i18n:use devices.state.*
func sessionStatus(s device.SessionState) templates.SessionStatus {
	switch s {
	case device.SessionConnected:
		return templates.SessionStatus{Key: "devices.state.connected", Tone: templates.ToneSuccess, Icon: "circle-check"}
	case device.SessionLinking:
		return templates.SessionStatus{Key: "devices.state.linking", Tone: templates.ToneInfo, Icon: "qr-code"}
	case device.SessionDisconnected:
		return templates.SessionStatus{Key: "devices.state.disconnected", Tone: templates.ToneWarning, Icon: "refresh-cw"}
	case device.SessionLoggedOut:
		return templates.SessionStatus{Key: "devices.state.logged_out", Tone: templates.ToneDanger, Icon: "triangle-exclamation-point"}
	}
	return templates.SessionStatus{Key: "devices.state.unlinked", Tone: templates.ToneNeutral, Icon: "unlink"}
}

func errorsAsInvalidRules(err error) (*device.InvalidRulesError, bool) {
	ir, ok := errors.AsType[*device.InvalidRulesError](err)
	return ir, ok
}

//i18n:use devices.error.*
func deviceErrorKey(err error) string {
	switch {
	case device.IsInvalidNameError(err):
		return "devices.error.invalid_name"
	case device.IsNameTakenError(err):
		return "devices.error.name_taken"
	case device.IsStaleVersionError(err):
		return "devices.error.stale_version"
	case device.IsRulesShapeConflictError(err):
		return "devices.error.rules_shape_conflict"
	case device.IsInvalidRulesError(err):
		ir, _ := errorsAsInvalidRules(err)
		return "devices.error.invalid_" + ir.Field
	case whatsapp.IsInvalidPhoneError(err):
		return "devices.error.invalid_phone"
	case whatsapp.IsAlreadyLinkedError(err):
		return "devices.error.already_linked"
	case whatsapp.IsNotOwnedError(err):
		return "devices.error.not_owned"
	}
	return "devices.error.link_failed"
}

func deviceFormError(err error) (key string, status int) {
	return deviceErrorKey(err), deviceWriteStatus(err)
}

func deviceWriteStatus(err error) int {
	switch {
	case device.IsInvalidNameError(err), device.IsInvalidRulesError(err), whatsapp.IsInvalidPhoneError(err), device.IsNameTakenError(err):
		return http.StatusUnprocessableEntity
	case device.IsStaleVersionError(err), device.IsRulesShapeConflictError(err), whatsapp.IsAlreadyLinkedError(err), whatsapp.IsLinkTimeoutError(err), whatsapp.IsNotConnectedError(err):
		return http.StatusConflict
	case whatsapp.IsUnsupportedError(err):
		return http.StatusNotImplemented
	case whatsapp.IsNotOwnedError(err):
		return http.StatusServiceUnavailable
	case device.IsNotFoundError(err):
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}
