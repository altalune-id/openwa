package dataplane

import (
	"net/http"

	"altalune.id/openwa/internal/platform/authn"
)

func (h *Handler) listContacts(w http.ResponseWriter, r *http.Request) {
	req, ok := h.begin(w, r)
	if !ok {
		return
	}
	p, err := h.authorizeScope(req, authn.ScopeContactsRead)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	one, many, err := h.deviceFilter(req, r, p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	q := r.URL.Query()
	limit, err := limitOf(q.Get("limit"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items, next, err := h.contacts.List(req.ctx, ContactListOpts{DeviceID: one, DeviceIDs: many, Search: q.Get("q"), Cursor: q.Get("cursor"), Limit: limit})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := listEnvelope[contactView]{Data: make([]contactView, 0, len(items)), NextCursor: next}
	for _, c := range items {
		out.Data = append(out.Data, contactViewOf(c))
	}
	h.writeJSON(w, r, out)
}
