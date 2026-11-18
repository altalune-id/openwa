package handlers

import (
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/contact"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/web"
	"altalune.id/openwa/internal/web/templates"
)

const pickLimit = 20

// RulesHandler owns a device's rules list, rule builder and test box.
type RulesHandler struct {
	*DeviceHandler
	Messages *message.Service
	Chats    *chat.Service
	Contacts *contact.Service
}

// NewRulesHandler wires the handler onto the device handler it shares device loading with.
func NewRulesHandler(devices *DeviceHandler, messages *message.Service, chats *chat.Service, contacts *contact.Service) *RulesHandler {
	return &RulesHandler{DeviceHandler: devices, Messages: messages, Chats: chats, Contacts: contacts}
}

// Register wires the rules routes onto mux.
func (h *RulesHandler) Register(mux web.Mux) {
	const base = "/orgs/{org}/projects/{project}/devices/{device}/rules"
	mux.HandleFunc("GET "+base, h.GetRules)
	mux.HandleFunc("GET "+base+"/{rule}", h.GetRule)
	mux.HandleFunc("POST "+base+"/{rule}", h.PostRule)
	mux.HandleFunc("POST "+base+"/{rule}/preview", h.PostRulePreview)
	mux.HandleFunc("POST "+base+"/{rule}/test", h.PostRuleTest)
	mux.HandleFunc("POST "+base+"/{rule}/toggle", h.PostRuleToggle)
	mux.HandleFunc("POST "+base+"/{rule}/move", h.PostRuleMove)
	mux.HandleFunc("POST "+base+"/{rule}/delete", h.PostRuleDelete)
	mux.HandleFunc("GET "+base+"/{rule}/pick", h.GetRulePick)
}

// GetRules renders the rules list.
func (h *RulesHandler) GetRules(w http.ResponseWriter, r *http.Request) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	v, err := h.listView(sc, dv)
	if err != nil {
		h.LogErr("web rules: list", err)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return
	}
	Render(w, sc.req, templates.RulesLayout(h.rulesLayout(sc, dv, ""), v))
}

// GetRule renders the builder for a stored rule, or a blank draft for an id no rule has yet.
func (h *RulesHandler) GetRule(w http.ResponseWriter, r *http.Request) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	id, ok := h.ruleID(w, sc, r)
	if !ok {
		return
	}
	f, custom, found := storedForm(dv.Device.Rules.Items, id)
	if !found {
		f = blankForm(id)
		if sender := strings.TrimSpace(r.URL.Query().Get("sender")); sender != "" {
			f.Who, f.Senders = whoOnly, []string{sender}
		}
	}
	h.writeBuilder(w, sc, dv, http.StatusOK, builderInput{form: f, isNew: !found, custom: custom})
}

// PostRule saves one rule, or its pair, in one conditional write of the whole list.
func (h *RulesHandler) PostRule(w http.ResponseWriter, r *http.Request) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	id, ok := h.ruleID(w, sc, r)
	if !ok || !h.requireManager(w, sc) || !h.parseForm(w, sc) {
		return
	}
	f := parseRuleForm(id, sc.req.PostForm)
	_, custom, found := storedForm(dv.Device.Rules.Items, id)
	in := builderInput{form: f, isNew: !found}
	if custom {
		in.custom = true
		h.writeBuilder(w, sc, dv, http.StatusConflict, in)
		return
	}
	if f.Name == "" {
		labels, err := h.labels(sc.req.Context(), dv)
		if err != nil {
			h.LogErr("web rules: labels", err)
			h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
			return
		}
		f.Name = sentenceName(h.ProjectFragmentBase(sc).Tr, f, labels)
	}
	rules, ferr := f.pairable(newRuleID).rules()
	if ferr != nil {
		in.formErr = ferr
		h.writeBuilder(w, sc, dv, http.StatusUnprocessableEntity, in)
		return
	}
	if err := h.writeItems(sc, dv, putGroup(dv.Device.Rules.Items, id, rules)); err != nil {
		in.saveErr = err
		h.writeBuilder(w, sc, dv, deviceWriteStatus(err), in)
		return
	}
	h.SetFlash(w, sc.req, web.FlashOK, "flash.rule_saved")
	h.redirect(w, sc, rulesPath(dv))
}

// PostRulePreview re-renders the builder body for the posted draft without saving.
func (h *RulesHandler) PostRulePreview(w http.ResponseWriter, r *http.Request) {
	h.builderFragment(w, r, false)
}

// PostRuleTest runs the posted draft, placed among the saved rules, against a sample message; it never sends.
func (h *RulesHandler) PostRuleTest(w http.ResponseWriter, r *http.Request) {
	h.builderFragment(w, r, true)
}

func (h *RulesHandler) builderFragment(w http.ResponseWriter, r *http.Request, test bool) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	id, ok := h.ruleID(w, sc, r)
	if !ok || !h.parseForm(w, sc) {
		return
	}
	stored, custom, found := storedForm(dv.Device.Rules.Items, id)
	in := builderInput{form: parseRuleForm(id, sc.req.PostForm), isNew: !found, custom: custom, test: test}
	if custom || !h.canManageProject(sc) {
		in.form = stored
		if !found {
			in.form = blankForm(id)
		}
	}
	if _, ferr := in.form.rules(); ferr != nil && !custom {
		in.formErr = ferr
	}
	h.writeBuilder(w, sc, dv, http.StatusOK, in)
}

// PostRuleToggle pauses or resumes a rule and its pair.
func (h *RulesHandler) PostRuleToggle(w http.ResponseWriter, r *http.Request) {
	h.listAction(w, r, func(items []device.Rule, id string, _ url.Values) ([]device.Rule, bool) {
		return toggleGroup(items, id)
	})
}

// PostRuleMove moves a rule and its pair one place up or down.
func (h *RulesHandler) PostRuleMove(w http.ResponseWriter, r *http.Request) {
	h.listAction(w, r, func(items []device.Rule, id string, form url.Values) ([]device.Rule, bool) {
		return moveGroup(items, id, form.Get("to"))
	})
}

// PostRuleDelete deletes a rule and its pair.
func (h *RulesHandler) PostRuleDelete(w http.ResponseWriter, r *http.Request) {
	h.listAction(w, r, func(items []device.Rule, id string, _ url.Values) ([]device.Rule, bool) {
		return deleteGroup(items, id)
	})
}

func (h *RulesHandler) listAction(w http.ResponseWriter, r *http.Request, change func([]device.Rule, string, url.Values) ([]device.Rule, bool)) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	id, ok := h.ruleID(w, sc, r)
	if !ok || !h.requireManager(w, sc) || !h.parseForm(w, sc) {
		return
	}
	items, changed := change(dv.Device.Rules.Items, id, sc.req.PostForm)
	if !changed {
		h.ErrorPageKey(w, sc.req, http.StatusNotFound, "error.not_found", nil)
		return
	}
	err := h.writeItems(sc, dv, items)
	if !web.IsHTMXRequest(r) {
		if err != nil {
			h.SetFlash(w, sc.req, web.FlashErr, deviceErrorKey(err))
		}
		h.redirect(w, sc, rulesPath(dv))
		return
	}
	status := http.StatusOK
	if err != nil {
		status = deviceWriteStatus(err)
	}
	fresh, ok := h.reload(w, sc, dv)
	if !ok {
		return
	}
	v, lerr := h.listView(sc, fresh)
	if lerr != nil {
		h.LogErr("web rules: list", lerr)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", lerr)
		return
	}
	if err != nil {
		v.Error = h.ProjectFragmentBase(sc).Tr(deviceErrorKey(err))
	}
	RenderStatus(w, sc.req, status, templates.RulesList(h.ProjectFragmentBase(sc), v))
}

// GetRulePick lists up to 20 groups or contacts for the chip picker.
func (h *RulesHandler) GetRulePick(w http.ResponseWriter, r *http.Request) {
	sc, dv, ok := h.requireDevice(w, r)
	if !ok {
		return
	}
	if _, ok := h.ruleID(w, sc, r); !ok {
		return
	}
	field := r.URL.Query().Get("field")
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	v := templates.RulePickView{Field: field, Query: q}
	switch field {
	case "chat":
		kind := chat.KindGroup
		chats, _, err := h.Chats.List(sc.req.Context(), chat.ListOpts{DeviceID: &dv.Device.ID, Kind: &kind, Search: q, Limit: pickLimit})
		if err != nil {
			h.LogErr("web rules: pick chats", err)
			h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
			return
		}
		for _, c := range chats {
			v.Options = append(v.Options, templates.RuleChip{Value: c.JID, Label: c.DisplayName()})
		}
	case "sender":
		contacts, _, err := h.Contacts.List(sc.req.Context(), contact.ListOpts{DeviceID: &dv.Device.ID, Search: q, Limit: pickLimit})
		if err != nil {
			h.LogErr("web rules: pick contacts", err)
			h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
			return
		}
		for _, c := range contacts {
			v.Options = append(v.Options, templates.RuleChip{Value: senderValue(c), Label: contactLabel(h.ProjectFragmentBase(sc).Tr, c)})
		}
		if n, ok := h.Devices.NormalizePhone(q); ok {
			v.Typed = n
		}
	default:
		h.ErrorPageKey(w, sc.req, http.StatusBadRequest, "error.bad_request", nil)
		return
	}
	Render(w, sc.req, templates.RulePickOptions(h.ProjectFragmentBase(sc), v))
}

// SECURITY: the id is echoed into form actions and links, so anything but a valid rule id (a decoded "../delete") is refused before rendering.
func (h *RulesHandler) ruleID(w http.ResponseWriter, sc ProjectScope, r *http.Request) (string, bool) {
	id := r.PathValue("rule")
	if device.ValidRuleID(id) {
		return id, true
	}
	h.ErrorPageKey(w, sc.req, http.StatusNotFound, "error.not_found", nil)
	return "", false
}

func (h *RulesHandler) parseForm(w http.ResponseWriter, sc ProjectScope) bool {
	if err := sc.req.ParseForm(); err != nil {
		h.ErrorPageKey(w, sc.req, http.StatusBadRequest, "error.bad_request", err)
		return false
	}
	return true
}

func (h *RulesHandler) writeItems(sc ProjectScope, dv device.DeviceView, items []device.Rule) error {
	version, err := formVersion(sc.req, dv.Device.Version)
	if err != nil {
		return err
	}
	next := device.Rules{IgnoreFromMe: dv.Device.Rules.IgnoreFromMe, Items: items}
	_, err = h.Devices.Update(sc.req.Context(), dv.Device.ID, device.Update{Rules: &next}, version)
	h.logUnless(err, "web rules: write")
	return err
}

func (h *RulesHandler) reload(w http.ResponseWriter, sc ProjectScope, dv device.DeviceView) (device.DeviceView, bool) {
	fresh, err := h.Devices.Get(sc.req.Context(), dv.Device.ID)
	if err != nil {
		h.LogErr("web rules: reload", err)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return device.DeviceView{}, false
	}
	return fresh, true
}

func rulesPath(dv device.DeviceView) string { return "/devices/" + dv.Device.PublicID + "/rules" }

func storedForm(items []device.Rule, id string) (f ruleForm, custom, found bool) {
	g := ruleGroups(items)
	i := groupIndex(g, id)
	if i < 0 {
		return ruleForm{}, false, false
	}
	if !g[i].OK {
		r := items[g[i].At]
		return ruleForm{ID: r.ID, Name: r.Name, Action: r.Action, Enabled: r.Enabled}, true, true
	}
	return g[i].Form, false, true
}

func blankForm(id string) ruleForm {
	return ruleForm{ID: id, Action: device.ActionMatch, Enabled: true, DM: true, Groups: groupsAll, Who: whoEveryone}
}

func newRuleID() string { return uuid.NewString() }

func senderValue(c *contact.Contact) string {
	if c.Phone != "" {
		return device.PhoneSenderPrefix + c.Phone
	}
	if c.LID != "" && !strings.HasSuffix(c.JID, "@s.whatsapp.net") {
		return c.LID
	}
	return c.JID
}

func earlierAllDMRule(items []device.Rule, id string) string {
	for _, g := range ruleGroups(items) {
		if g.ID == id {
			return ""
		}
		r := items[g.At+g.N-1]
		if !r.Enabled || r.Action != device.ActionMatch || len(r.Conditions) != 1 {
			continue
		}
		if c := r.Conditions[0]; c.Field == device.FieldChatKind && c.Op == device.OpIs && slices.Contains(c.Values, "dm") {
			return r.Name
		}
	}
	return ""
}

//i18n:use rules.hidden_number
func contactLabel(tr func(string, ...any) string, c *contact.Contact) string {
	if c.Name == "" && c.PushName == "" && c.BusinessName == "" && c.Phone == "" {
		return tr("rules.hidden_number")
	}
	return c.DisplayName()
}
