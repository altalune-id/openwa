package dataplane

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/authn"
	"altalune.id/openwa/internal/platform/publicid"
	"altalune.id/openwa/internal/platform/session"
)

// SECURITY: a device-bound key always lists through its ResourceIDs, and one with an empty set sees nothing, so an absent filter never widens it to the project; device_id is a public id resolved in the project.
func (h *Handler) deviceFilter(req request, r *http.Request, p session.Principal) (one *uuid.UUID, many []uuid.UUID, err error) {
	all := p.ReachesWholeProject(req.scope.orgID, req.scope.projectID)
	resources := p.ResourceIDs
	if !all && len(resources) == 0 {
		return nil, nil, &NotFoundError{}
	}
	raw := r.URL.Query().Get("device_id")
	if raw == "" {
		if all {
			return nil, nil, nil
		}
		return nil, resources, nil
	}
	if !publicid.Valid(device.PublicIDPrefix, raw) {
		return nil, nil, &NotFoundError{}
	}
	d, err := h.devices.Resolve(req.ctx, raw)
	if err != nil {
		return nil, nil, err
	}
	if !p.ReachesResource(req.scope.orgID, req.scope.projectID, d.ID) {
		return nil, nil, &NotFoundError{}
	}
	return &d.ID, nil, nil
}

func (h *Handler) listChats(w http.ResponseWriter, r *http.Request) {
	req, ok := h.begin(w, r)
	if !ok {
		return
	}
	p, err := h.authorizeScope(req, authn.ScopeChatsRead)
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
	items, next, err := h.chats.List(req.ctx, ChatListOpts{DeviceID: one, DeviceIDs: many, Kind: q.Get("kind"), Search: q.Get("q"), Cursor: q.Get("cursor"), Limit: limit})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := listEnvelope[chatView]{Data: make([]chatView, 0, len(items)), NextCursor: next}
	for _, c := range items {
		out.Data = append(out.Data, h.chatViewOf(req, c))
	}
	h.writeJSON(w, r, out)
}

// SECURITY: the same order as beginDevice: shape, then the key's scope, then the project-scoped read, then the reach check on the owning device; every refusal is the masked 404.
func (h *Handler) chatFor(w http.ResponseWriter, r *http.Request, scopeName string, write bool) (request, ChatRef, bool) {
	begin := h.begin
	if write {
		begin = h.beginWrite
	}
	req, ok := begin(w, r)
	if !ok {
		return request{}, ChatRef{}, false
	}
	raw := r.PathValue("chat")
	if !publicid.Valid(chat.PublicIDPrefix, raw) {
		h.fail(w, r, &NotFoundError{})
		return request{}, ChatRef{}, false
	}
	p, err := h.authorizeScope(req, scopeName)
	if err != nil {
		h.fail(w, r, err)
		return request{}, ChatRef{}, false
	}
	c, err := h.chats.Resolve(req.ctx, raw)
	if err != nil {
		h.fail(w, r, err)
		return request{}, ChatRef{}, false
	}
	// SECURITY: reach is checked on the OWNING device id (S2), never the chat's own id, so a device-bound key sees only its devices' chats.
	if !p.ReachesResource(req.scope.orgID, req.scope.projectID, c.DeviceID) {
		h.fail(w, r, &NotFoundError{})
		return request{}, ChatRef{}, false
	}
	return req, c, true
}

func (h *Handler) getChat(w http.ResponseWriter, r *http.Request) {
	req, c, ok := h.chatFor(w, r, authn.ScopeChatsRead, false)
	if !ok {
		return
	}
	h.writeJSON(w, r, h.chatViewOf(req, c))
}

func (h *Handler) readChat(w http.ResponseWriter, r *http.Request) {
	req, c, ok := h.chatFor(w, r, authn.ScopeChatsWrite, true)
	if !ok {
		return
	}
	if err := h.messages.MarkRead(req.ctx, c.ID); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) getGroup(w http.ResponseWriter, r *http.Request) {
	req, c, ok := h.chatFor(w, r, authn.ScopeChatsRead, false)
	if !ok {
		return
	}
	g, err := h.chats.GroupInfo(req.ctx, c.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, groupView(g))
}

func (h *Handler) leaveGroup(w http.ResponseWriter, r *http.Request) {
	req, c, ok := h.chatFor(w, r, authn.ScopeChatsWrite, true)
	if !ok {
		return
	}
	left, err := h.chats.LeaveGroup(req.ctx, c.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, h.chatViewOf(req, left))
}

// SECURITY: beginDevice (shape, scope, project-scoped Resolve, reach) runs before the body is read, so a foreign device is a masked 404 that never reaches the engine.
func (h *Handler) joinGroup(w http.ResponseWriter, r *http.Request) {
	req, dev, ok := h.beginDevice(w, r, authn.ScopeChatsWrite)
	if !ok {
		return
	}
	_, body, err := decodeJSON[struct {
		InviteLink string `json:"invite_link"`
	}](r)
	if err != nil || body.InviteLink == "" {
		h.fail(w, r, &BadRequestError{})
		return
	}
	c, err := h.chats.JoinGroup(req.ctx, dev.ID, body.InviteLink)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out, err := json.Marshal(h.chatViewOf(req, c))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeBytes(w, http.StatusCreated, out)
}
