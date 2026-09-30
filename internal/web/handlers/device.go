package handlers

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/project"
	"altalune.id/openwa/internal/web"
	"altalune.id/openwa/internal/web/templates"
	"altalune.id/openwa/internal/whatsapp"
)

// DeviceHandler owns the project-scoped device console pages.
type DeviceHandler struct {
	Deps
	Devices *device.Service
}

// NewDeviceHandler wires the handler.
func NewDeviceHandler(d Deps, projects *project.Service, devices *device.Service) *DeviceHandler {
	d.Projects = projects
	return &DeviceHandler{Deps: d, Devices: devices}
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
	v := templates.DevicesView{ProjectSlug: sc.project.Slug, Total: len(views), Cards: make([]templates.DeviceCard, 0, len(views))}
	for _, dv := range views {
		switch dv.Status.State {
		case device.SessionConnected:
			v.Connected++
		case device.SessionDisconnected, device.SessionLoggedOut:
			v.Attention++
		}
		v.Cards = append(v.Cards, templates.DeviceCard{
			ID:       dv.Device.PublicID,
			Name:     dv.Device.Name,
			Phone:    dv.Status.Phone,
			PushName: dv.Status.PushName,
			StateKey: deviceStateKey(dv.Status.State),
			Tone:     deviceTone(dv.Status.State),
			LastSeen: dv.Status.LastSeen,
		})
	}
	l := h.layout(sc, "")
	Render(w, sc.req, templates.DevicesLayout(l, v))
}

// GetDeviceNew renders the empty form.
func (h *DeviceHandler) GetDeviceNew(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	l := h.layout(sc, "devices.new")
	Render(w, sc.req, templates.DeviceNewLayout(l, templates.DeviceFormView{ProjectSlug: sc.project.Slug}))
}

// PostDeviceCreate creates a device and redirects to its detail page.
func (h *DeviceHandler) PostDeviceCreate(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPageKey(w, sc.req, http.StatusBadRequest, "error.bad_request", err)
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

// GetDevice renders the detail page.
func (h *DeviceHandler) GetDevice(w http.ResponseWriter, r *http.Request) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	h.writeDetail(w, sc, dv, http.StatusOK, detailInput{})
}

// PostDeviceLink starts a QR attempt and returns the pairing fragment.
func (h *DeviceHandler) PostDeviceLink(w http.ResponseWriter, r *http.Request) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	st, err := h.Devices.StartLink(sc.req.Context(), dv.Device.ID)
	if !web.IsHTMXRequest(r) {
		if err != nil {
			h.SetFlash(w, sc.req, web.FlashErr, "flash.device_action_failed")
		}
		h.redirect(w, sc, "/devices/"+dv.Device.PublicID)
		return
	}
	h.writePairing(w, sc, dv, st, err, "")
}

// GetDeviceLinkCurrent returns the pairing fragment for the current attempt.
func (h *DeviceHandler) GetDeviceLinkCurrent(w http.ResponseWriter, r *http.Request) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	st, err := h.Devices.LinkState(sc.req.Context(), dv.Device.ID)
	h.writePairing(w, sc, dv, st, err, "")
}

// PostDevicePhoneLink asks for a pairing code and returns the pairing fragment.
func (h *DeviceHandler) PostDevicePhoneLink(w http.ResponseWriter, r *http.Request) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPageKey(w, sc.req, http.StatusBadRequest, "error.bad_request", err)
		return
	}
	phone := strings.TrimSpace(r.PostForm.Get("phone"))
	st, err := h.Devices.LinkWithPhone(sc.req.Context(), dv.Device.ID, phone)
	h.writePairing(w, sc, dv, st, err, phone)
}

// PostDeviceUnlink logs the device's account out.
func (h *DeviceHandler) PostDeviceUnlink(w http.ResponseWriter, r *http.Request) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	if err := h.Devices.Unlink(sc.req.Context(), dv.Device.ID); err != nil {
		h.logUnless(err, "web device: unlink")
		h.SetFlash(w, sc.req, web.FlashErr, "flash.device_action_failed")
		h.redirect(w, sc, "/devices/"+dv.Device.PublicID)
		return
	}
	h.SetFlash(w, sc.req, web.FlashOK, "flash.device_logged_out")
	h.redirect(w, sc, "/devices/"+dv.Device.PublicID)
}

// PostDeviceRules replaces the inbound rules.
func (h *DeviceHandler) PostDeviceRules(w http.ResponseWriter, r *http.Request) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.ErrorPageKey(w, sc.req, http.StatusBadRequest, "error.bad_request", err)
		return
	}
	rules := rulesFromForm(r)
	version, err := formVersion(r, dv.Device.Version)
	if err != nil {
		h.writeDetail(w, sc, dv, deviceWriteStatus(err), detailInput{rules: &rules, rulesErr: err})
		return
	}
	if _, err := h.Devices.UpdateRules(sc.req.Context(), dv.Device.ID, rules, version); err != nil {
		h.logUnless(err, "web device: rules")
		h.writeDetail(w, sc, dv, deviceWriteStatus(err), detailInput{rules: &rules, rulesErr: err})
		return
	}
	h.SetFlash(w, sc.req, web.FlashOK, "flash.device_rules_saved")
	h.redirect(w, sc, "/devices/"+dv.Device.PublicID)
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
		h.writeDetail(w, sc, dv, deviceWriteStatus(err), detailInput{name: &name, renameErr: err})
		return
	}
	h.SetFlash(w, sc.req, web.FlashOK, "flash.device_renamed")
	h.redirect(w, sc, "/devices/"+dv.Device.PublicID)
}

// PostDeviceDelete logs out and deletes the device.
func (h *DeviceHandler) PostDeviceDelete(w http.ResponseWriter, r *http.Request) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	if err := h.Devices.Delete(sc.req.Context(), dv.Device.ID); err != nil {
		h.logUnless(err, "web device: delete")
		h.SetFlash(w, sc.req, web.FlashErr, "flash.device_action_failed")
		h.redirect(w, sc, "/devices/"+dv.Device.PublicID)
		return
	}
	h.SetFlash(w, sc.req, web.FlashOK, "flash.device_deleted")
	h.redirect(w, sc, "/devices")
}

type detailInput struct {
	rules     *device.Rules
	rulesErr  error
	name      *string
	renameErr error
}

func (h *DeviceHandler) requireDevice(w http.ResponseWriter, r *http.Request) (ProjectScope, device.DeviceView, bool) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return ProjectScope{}, device.DeviceView{}, false
	}
	d, err := h.Devices.Resolve(sc.req.Context(), r.PathValue("device"))
	if err != nil {
		if device.IsNotFoundError(err) {
			h.ErrorPageKey(w, sc.req, http.StatusNotFound, "error.not_found", err)
			return ProjectScope{}, device.DeviceView{}, false
		}
		h.LogErr("web device: resolve", err)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return ProjectScope{}, device.DeviceView{}, false
	}
	dv, err := h.Devices.Get(sc.req.Context(), d.ID)
	if err != nil {
		if device.IsNotFoundError(err) {
			h.ErrorPageKey(w, sc.req, http.StatusNotFound, "error.not_found", err)
			return ProjectScope{}, device.DeviceView{}, false
		}
		h.LogErr("web device: get", err)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return ProjectScope{}, device.DeviceView{}, false
	}
	return sc, dv, true
}

func (h *DeviceHandler) writeDetail(w http.ResponseWriter, sc ProjectScope, dv device.DeviceView, status int, in detailInput) {
	st, err := h.Devices.LinkState(sc.req.Context(), dv.Device.ID)
	if err != nil {
		h.LogErr("web device: link state", err)
		st = device.LinkState{Outcome: device.LinkNone}
	}
	l := h.layout(sc, "")
	l.Crumbs[len(l.Crumbs)-1].Href = h.ProjectURL(sc, "/devices")
	l.Crumbs = append(l.Crumbs, web.Crumb{Label: dv.Device.Name})
	l.ActiveNav.Parent = "devices"

	d := dv.Device
	rules := d.Rules
	if in.rules != nil {
		rules = *in.rules
	}
	v := templates.DeviceDetailView{
		ProjectSlug: sc.project.Slug,
		ID:          d.PublicID,
		Name:        d.Name,
		Version:     d.Version,
		StateKey:    deviceStateKey(dv.Status.State),
		Tone:        deviceTone(dv.Status.State),
		Phone:       dv.Status.Phone,
		PushName:    dv.Status.PushName,
		Reason:      dv.Status.Reason,
		LastSeen:    dv.Status.LastSeen,
		RenameValue: d.Name,
		Pairing:     pairingView(sc, dv, st, ""),
		Rules: templates.DeviceRulesView{
			GroupMode:      string(rules.GroupMode),
			AllowedSenders: strings.Join(rules.AllowedSenders, "\n"),
			AllowedGroups:  strings.Join(rules.AllowedGroups, "\n"),
			TriggerPrefix:  rules.TriggerPrefix,
			IgnoreFromMe:   rules.IgnoreFromMe,
			Errors:         map[string]string{},
		},
	}
	if in.name != nil {
		v.RenameValue = *in.name
	}
	if in.renameErr != nil {
		v.RenameError = l.Tr(deviceErrorKey(in.renameErr))
	}
	if in.rulesErr != nil {
		if ir, ok := errorsAsInvalidRules(in.rulesErr); ok {
			v.Rules.Errors[ir.Field] = l.Tr(deviceErrorKey(in.rulesErr))
		} else {
			v.Rules.FormError = l.Tr(deviceErrorKey(in.rulesErr))
		}
	}
	RenderStatus(w, sc.req, status, templates.DeviceDetailLayout(l, v))
}

func (h *DeviceHandler) writePairing(w http.ResponseWriter, sc ProjectScope, dv device.DeviceView, st device.LinkState, err error, phone string) {
	base := h.ProjectFragmentBase(sc)
	if err == nil {
		Render(w, sc.req, templates.DevicePairing(base, pairingView(sc, dv, st, phone)))
		return
	}
	h.logUnless(err, "web device: link")
	status := deviceWriteStatus(err)
	v := pairingView(sc, dv, device.LinkState{Outcome: device.LinkFailed}, phone)
	v.ErrorKey = deviceErrorKey(err)
	if whatsapp.IsInvalidPhoneError(err) {
		v.ErrorKey = ""
		v.PhoneError = base.Tr("devices.error.invalid_phone")
	}
	RenderStatus(w, sc.req, status, templates.DevicePairing(base, v))
}

func pairingView(sc ProjectScope, dv device.DeviceView, st device.LinkState, phone string) templates.DevicePairingView {
	v := templates.DevicePairingView{
		ProjectSlug: sc.project.Slug,
		DeviceID:    dv.Device.PublicID,
		Outcome:     string(st.Outcome),
		PairingCode: st.PairingCode,
		ExpiresAt:   st.ExpiresAt,
		Linked:      dv.Status.State == device.SessionConnected || dv.Status.State == device.SessionDisconnected,
		Phone:       dv.Status.Phone,
		PhoneValue:  phone,
	}
	if len(st.PNG) > 0 {
		v.PNGBase64 = base64.StdEncoding.EncodeToString(st.PNG)
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

func rulesFromForm(r *http.Request) device.Rules {
	senders := splitList(r.PostForm.Get("allowed_senders"))
	for i, s := range senders {
		senders[i] = strings.NewReplacer("+", "", " ", "", "-", "", "(", "", ")", "").Replace(s)
	}
	return device.Rules{
		GroupMode:      device.GroupMode(r.PostForm.Get("group_mode")),
		AllowedSenders: senders,
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
func deviceStateKey(s device.SessionState) string {
	if s == "" {
		s = device.SessionUnlinked
	}
	return "devices.state." + string(s)
}

func deviceTone(s device.SessionState) templates.Tone {
	switch s {
	case device.SessionConnected:
		return templates.ToneSuccess
	case device.SessionLinking:
		return templates.ToneInfo
	case device.SessionDisconnected:
		return templates.ToneWarning
	case device.SessionLoggedOut:
		return templates.ToneDanger
	}
	return templates.ToneNeutral
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
	case device.IsStaleVersionError(err), whatsapp.IsAlreadyLinkedError(err), whatsapp.IsLinkTimeoutError(err), whatsapp.IsNotConnectedError(err):
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
