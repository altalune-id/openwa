package dataplane_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/dataplane"
	"altalune.id/openwa/internal/platform/authn"
	"altalune.id/openwa/internal/whatsapp"
)

const devBase = "/api/v1/orgs/acme/projects/main/devices"

func jsonHeader(kv ...string) http.Header {
	h := http.Header{"Content-Type": {"application/json"}}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

func (e *env) deviceNamed(name string) dataplane.DeviceRef {
	for _, d := range e.devices.rows {
		if d.Name == name {
			return d
		}
	}
	return dataplane.DeviceRef{}
}

func TestDevices_ListAndDeviceBoundKeys(t *testing.T) {
	e := newEnv(true)
	rec := send(t, e.handler(), http.MethodGet, devBase, "", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		Devices []map[string]any `json:"devices"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out.Devices, 2)
	require.Equal(t, "Sales", out.Devices[0]["name"])
	require.Equal(t, "connected", out.Devices[0]["state"])
	require.Equal(t, "628111", out.Devices[0]["phone"])

	e.authz.resources = []uuid.UUID{e.deviceNamed("Support").ID}
	rec = send(t, e.handler(), http.MethodGet, devBase, "", nil)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out.Devices, 1, "a device-bound key lists only its devices")
	require.Equal(t, "Support", out.Devices[0]["name"])
}

// TestDevices_OrgKeyAgreesWithItsGrant mirrors apikey's TestDataPlane_OrgKeyAgreesWithItsGrant: a key whose grant reaches the project lists and gets its devices; a key granted only another project reads nothing here. The handler asks Principal.Reaches*, so an org key or personal token that reaches the project is admitted — the removed AuthorizeResources/Allows model rejected them.
func TestDevices_OrgKeyAgreesWithItsGrant(t *testing.T) {
	e := newEnv(true)
	sales := e.deviceNamed("Sales")

	require.Equal(t, http.StatusOK, send(t, e.handler(), http.MethodGet, devBase, "", nil).Code, "granted the project: list")
	require.Equal(t, http.StatusOK, send(t, e.handler(), http.MethodGet, devBase+"/"+sales.PublicID, "", nil).Code, "granted the project: get")

	e.authz.projectIDs = []uuid.UUID{e.altProjectID}
	require.Equal(t, http.StatusNotFound, send(t, e.handler(), http.MethodGet, devBase, "", nil).Code, "granted another project: list reads nothing")
	require.Equal(t, http.StatusNotFound, send(t, e.handler(), http.MethodGet, devBase+"/"+sales.PublicID, "", nil).Code, "granted another project: get reads nothing")
}

func TestDevices_EveryRouteNeedsACredential(t *testing.T) {
	e := newEnv(true)
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, devBase},
		{http.MethodGet, devBase + "/dev_SalesDevice00001"},
		{http.MethodGet, devBase + "/dev_SalesDevice00001/links/current"},
	} {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		rec := httptest.NewRecorder()
		e.handler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code, rt.path)
	}
}

func TestDevices_GetHonoursETagAndResourceNarrowing(t *testing.T) {
	e := newEnv(true)
	sales := e.deviceNamed("Sales")
	rec := send(t, e.handler(), http.MethodGet, devBase+"/"+sales.PublicID, "", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, `W/"3"`, rec.Header().Get("ETag"))
	rec = send(t, e.handler(), http.MethodGet, devBase+"/"+sales.PublicID, "", http.Header{"If-None-Match": {`W/"3"`}})
	require.Equal(t, http.StatusNotModified, rec.Code)

	e.authz.resources = []uuid.UUID{e.deviceNamed("Support").ID}
	denied := send(t, e.handler(), http.MethodGet, devBase+"/"+sales.PublicID, "", nil)
	missing := send(t, e.handler(), http.MethodGet, devBase+"/dev_NoSuchDevice0001", "", nil)
	require.Equal(t, http.StatusNotFound, denied.Code)
	require.Equal(t, missing.Body.String(), denied.Body.String(), "a denial reads exactly like a missing device")

	before := e.devices.resolves
	for _, bad := range []string{sales.ID.String(), "not-an-id", "dev_short", "cht_SalesDevice00001"} {
		rec := send(t, e.handler(), http.MethodGet, devBase+"/"+bad, "", nil)
		require.Equal(t, http.StatusNotFound, rec.Code, bad)
		require.Equal(t, missing.Body.String(), rec.Body.String(), "a malformed id reads like a missing device: %s", bad)
	}
	require.Equal(t, before, e.devices.resolves, "a malformed public id never reaches the service")
}

func TestDevices_BodiesCarryThePublicIDNeverTheUUID(t *testing.T) {
	e := newEnv(true)
	sales := e.deviceNamed("Sales")
	for _, path := range []string{devBase, devBase + "/" + sales.PublicID} {
		rec := send(t, e.handler(), http.MethodGet, path, "", nil)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), `"id":"`+sales.PublicID+`"`, path)
		require.NotContains(t, rec.Body.String(), sales.ID.String(), path)
	}
}

func TestDevices_CreateHonoursIdempotencyKey(t *testing.T) {
	e := newEnv(true)
	shared := e.handler()
	a := send(t, shared, http.MethodPost, devBase, `{"name":"Desk"}`, jsonHeader("Idempotency-Key", "k-2"))
	b := send(t, shared, http.MethodPost, devBase, `{"name":"Desk"}`, jsonHeader("Idempotency-Key", "k-2"))
	require.Equal(t, http.StatusCreated, a.Code, a.Body.String())
	require.Equal(t, http.StatusCreated, b.Code)
	require.Equal(t, a.Body.String(), b.Body.String(), "a replay answers the first response")
	require.Equal(t, 1, e.devices.creates, "a replay never reaches the service")
	c := send(t, shared, http.MethodPost, devBase, `{"name":"Other"}`, jsonHeader("Idempotency-Key", "k-2"))
	require.Equal(t, http.StatusConflict, c.Code, "the same key with a different body is refused")

	e.authz.resources = []uuid.UUID{uuid.New()}
	require.Equal(t, http.StatusNotFound, send(t, e.handler(), http.MethodPost, devBase, `{"name":"X"}`, jsonHeader()).Code,
		"a device-bound key cannot create devices")
}

func TestDevices_PatchIsConditional(t *testing.T) {
	e := newEnv(true)
	sales := e.deviceNamed("Sales")
	path := devBase + "/" + sales.PublicID
	require.Equal(t, http.StatusPreconditionRequired, send(t, e.handler(), http.MethodPatch, path, `{"name":"X"}`, jsonHeader()).Code)
	require.Equal(t, http.StatusPreconditionFailed, send(t, e.handler(), http.MethodPatch, path, `{"name":"X"}`, jsonHeader("If-Match", `W/"2"`)).Code)
	rec := send(t, e.handler(), http.MethodPatch, path, `{"name":"Sales EU","rules":{"group_mode":"open","allowed_senders":[],"allowed_groups":[],"trigger_prefix":"","ignore_from_me":true}}`, jsonHeader("If-Match", `W/"3"`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, `W/"4"`, rec.Header().Get("ETag"))
	require.Contains(t, rec.Body.String(), `"group_mode":"open"`)
}

func TestDevices_DeleteAndUnlink(t *testing.T) {
	e := newEnv(true)
	sales := e.deviceNamed("Sales")
	require.Equal(t, http.StatusNoContent, send(t, e.handler(), http.MethodPost, devBase+"/"+sales.PublicID+"/unlink", "", nil).Code)
	require.Equal(t, []uuid.UUID{sales.ID}, e.devices.unlinked)
	require.Equal(t, http.StatusNoContent, send(t, e.handler(), http.MethodDelete, devBase+"/"+sales.PublicID, "", nil).Code)
	require.Equal(t, []uuid.UUID{sales.ID}, e.devices.deleted)
}

func TestDevices_LinksAreResources(t *testing.T) {
	e := newEnv(true)
	sales := e.deviceNamed("Sales")
	path := devBase + "/" + sales.PublicID + "/links"

	rec := send(t, e.handler(), http.MethodPost, path, `{"method":"qr"}`, jsonHeader())
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var link map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &link))
	require.Equal(t, "lnk_V1StGXR8Z5jdHi6B", link["id"], "the link resource id is the attempt's lnk_ public id")
	require.Equal(t, "pending", link["status"])
	require.Equal(t, "qr", link["method"])
	require.NotEmpty(t, link["qr_png_base64"])
	require.Equal(t, path+"/current", link["url"])

	rec = send(t, e.handler(), http.MethodPost, path, `{"method":"phone","phone":"+62 812 3456"}`, jsonHeader())
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Contains(t, rec.Body.String(), `"code":"ABCD-EFGH"`)
	require.Equal(t, []string{"+62 812 3456"}, e.devices.phones)

	require.Equal(t, http.StatusBadRequest, send(t, e.handler(), http.MethodPost, path, `{"method":"fax"}`, jsonHeader()).Code)
	require.Equal(t, http.StatusBadRequest, send(t, e.handler(), http.MethodPost, path, `{"method":"phone"}`, jsonHeader()).Code)

	for _, tc := range []struct {
		err  error
		code int
	}{
		{&whatsapp.InvalidPhoneError{Reason: "national"}, http.StatusBadRequest},
		{&whatsapp.AlreadyLinkedError{ID: "x"}, http.StatusConflict},
		{&whatsapp.NotOwnedError{ID: "x"}, http.StatusServiceUnavailable},
		{&whatsapp.UnsupportedError{Feature: "passkey"}, http.StatusNotImplemented},
	} {
		e.devices.linkErr = tc.err
		require.Equal(t, tc.code, send(t, e.handler(), http.MethodPost, path, `{"method":"qr"}`, jsonHeader()).Code, "%T", tc.err)
	}
	e.devices.linkErr = nil

	e.devices.link.Outcome = "timeout"
	rec = send(t, e.handler(), http.MethodGet, path+"/current", "", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"status":"expired"`)

	e.devices.link.Outcome = "none"
	require.Equal(t, http.StatusNotFound, send(t, e.handler(), http.MethodGet, path+"/current", "", nil).Code)
}

func TestDevices_PatchRulesMergesOmittedFields(t *testing.T) {
	e := newEnv(true)
	sales := e.deviceNamed("Sales")
	sales.Rules = dataplane.RulesRef{GroupMode: "mention", AllowedSenders: []string{"628111"}, AllowedGroups: []string{"g1@g.us"}, TriggerPrefix: "!", IgnoreFromMe: true}
	e.devices.rows[sales.ID] = sales
	path := devBase + "/" + sales.PublicID

	rec := send(t, e.handler(), http.MethodPatch, path, `{"rules":{"group_mode":"open"}}`, jsonHeader("If-Match", `W/"3"`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := e.devices.rows[sales.ID].Rules
	require.Equal(t, dataplane.RulesRef{GroupMode: "open", AllowedSenders: []string{"628111"}, AllowedGroups: []string{"g1@g.us"}, TriggerPrefix: "!", IgnoreFromMe: true}, got,
		"a partial rules body changes only the fields it names")

	rec = send(t, e.handler(), http.MethodPatch, path, `{"rules":{"allowed_senders":[],"ignore_from_me":false}}`, jsonHeader("If-Match", `W/"4"`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got = e.devices.rows[sales.ID].Rules
	require.Equal(t, "open", got.GroupMode)
	require.Empty(t, got.AllowedSenders, "an explicit empty list clears the list")
	require.False(t, got.IgnoreFromMe, "an explicit false is applied")
	require.Equal(t, []string{"g1@g.us"}, got.AllowedGroups)
	require.Equal(t, "!", got.TriggerPrefix)
}

func TestDevices_UnauthorizedKeyNeverResolvesTheDevice(t *testing.T) {
	for name, mutate := range map[string]func(e *env){
		"no devices scope":   func(e *env) { e.authz.scopes = []string{authn.ScopePostsRead} },
		"another project":    func(e *env) { e.authz.projectIDs = []uuid.UUID{e.altProjectID} },
		"write scope absent": func(e *env) { e.authz.scopes = []string{authn.ScopeDevicesRead} },
	} {
		e := newEnv(true)
		mutate(e)
		sales := e.deviceNamed("Sales")
		method := http.MethodGet
		if name == "write scope absent" {
			method = http.MethodDelete
		}
		rec := send(t, e.handler(), method, devBase+"/"+sales.PublicID, "", nil)
		require.Equal(t, http.StatusNotFound, rec.Code, name)
		require.Zero(t, e.devices.resolves, "%s: an unauthorized key must be refused before any device lookup", name)
		require.Empty(t, e.devices.deleted, name)
	}
}

func TestDevices_ReadOnlyKeyCannotMutate(t *testing.T) {
	e := newEnv(true)
	e.authz.scopes = []string{authn.ScopeDevicesRead}
	sales := e.deviceNamed("Sales")
	path := devBase + "/" + sales.PublicID

	require.Equal(t, http.StatusOK, send(t, e.handler(), http.MethodGet, path, "", nil).Code, "devices:read still reads")
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPatch, path, `{"name":"X"}`},
		{http.MethodDelete, path, ""},
		{http.MethodPost, path + "/unlink", ""},
		{http.MethodPost, path + "/links", `{"method":"phone","phone":"1"}`},
		{http.MethodPost, devBase, `{"name":"New"}`},
	} {
		rec := send(t, e.handler(), tc.method, tc.path, tc.body, jsonHeader("If-Match", `W/"3"`))
		require.Equal(t, http.StatusNotFound, rec.Code, "%s %s", tc.method, tc.path)
	}
	require.Empty(t, e.devices.deleted)
	require.Empty(t, e.devices.unlinked)
	require.Empty(t, e.devices.phones)
	require.Zero(t, e.devices.creates)
	require.Equal(t, "Sales", e.devices.rows[sales.ID].Name)
}

func TestDevices_BoundKeyCannotMutateAnotherDevice(t *testing.T) {
	e := newEnv(true)
	sales, support := e.deviceNamed("Sales"), e.deviceNamed("Support")
	e.authz.resources = []uuid.UUID{support.ID}
	path := devBase + "/" + sales.PublicID

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPatch, path, `{"name":"X"}`},
		{http.MethodDelete, path, ""},
		{http.MethodPost, path + "/unlink", ""},
		{http.MethodPost, path + "/links", `{"method":"phone","phone":"1"}`},
		{http.MethodGet, path + "/links/current", ""},
	} {
		rec := send(t, e.handler(), tc.method, tc.path, tc.body, jsonHeader("If-Match", `W/"3"`))
		require.Equal(t, http.StatusNotFound, rec.Code, "%s %s", tc.method, tc.path)
	}
	require.Empty(t, e.devices.deleted)
	require.Empty(t, e.devices.unlinked)
	require.Empty(t, e.devices.phones)
	require.Equal(t, "Sales", e.devices.rows[sales.ID].Name)

	require.Equal(t, http.StatusNoContent, send(t, e.handler(), http.MethodPost, devBase+"/"+support.PublicID+"/unlink", "", nil).Code, "its own device still works")
}
