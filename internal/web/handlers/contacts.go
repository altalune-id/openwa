package handlers

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/contact"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/keyset"
	"altalune.id/openwa/internal/project"
	"altalune.id/openwa/internal/web"
	"altalune.id/openwa/internal/web/templates"
)

// ContactsHandler owns the project-scoped contacts page.
type ContactsHandler struct {
	Deps
	Contacts *contact.Service
	Devices  *device.Service
}

// NewContactsHandler wires the handler.
func NewContactsHandler(d Deps, projects *project.Service, contacts *contact.Service, devices *device.Service) *ContactsHandler {
	d.Projects = projects
	return &ContactsHandler{Deps: d, Contacts: contacts, Devices: devices}
}

// Register wires the contacts route onto mux.
func (h *ContactsHandler) Register(mux web.Mux) {
	mux.HandleFunc("GET /orgs/{org}/projects/{project}/contacts", h.GetContacts)
}

// GetContacts renders one page of the project's contacts.
func (h *ContactsHandler) GetContacts(w http.ResponseWriter, r *http.Request) {
	sc, ok := h.RequireProject(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	views, err := h.Devices.List(sc.req.Context())
	if err != nil {
		h.LogErr("web contacts: devices", err)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return
	}
	// NOTE: the one device list feeds the picker, the device_id filter and every row's public id, so no row costs a query.
	devices := make([]templates.InboxDevice, 0, len(views))
	pubs := make(map[uuid.UUID]string, len(views))
	opts := contact.ListOpts{Search: q.Get("q"), Cursor: q.Get("cursor")}
	for _, v := range views {
		id := v.Device.ID
		pubs[id] = v.Device.PublicID
		selected := v.Device.PublicID == q.Get("device")
		if selected {
			opts.DeviceID = &id
		}
		devices = append(devices, templates.InboxDevice{ID: v.Device.PublicID, Name: v.Device.Name, Selected: selected})
	}
	items, next, err := h.Contacts.List(sc.req.Context(), opts)
	if err != nil {
		h.LogErr("web contacts: list", err)
		status := http.StatusInternalServerError
		if keyset.IsInvalidCursorError(err) {
			status = http.StatusBadRequest
		}
		h.ErrorPageKey(w, sc.req, status, "error.load_failed", err)
		return
	}
	rows := make([]templates.ContactRowView, 0, len(items))
	for i, c := range items {
		rows = append(rows, templates.ContactRowView{ID: strconv.Itoa(i), Name: c.DisplayName(), Phone: c.Phone, JID: c.JID, DeviceID: pubs[c.DeviceID]})
	}
	Render(w, sc.req, templates.ContactsLayout(h.LayoutForProject(sc.req, "Contacts", sc.org.Slug, sc.project, "contacts"), templates.ContactsView{
		ProjectSlug: sc.project.Slug, Devices: devices, DeviceID: q.Get("device"), Query: q.Get("q"), Rows: rows, NextCursor: next,
	}))
}
