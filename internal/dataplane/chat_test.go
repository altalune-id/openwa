package dataplane_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/dataplane"
	"altalune.id/openwa/internal/testutil/fakes"
)

func TestListChats_DeviceBoundKeyIsFiltered(t *testing.T) {
	e := newEnv(true)
	a := dataplane.ChatRef{ID: uuid.New(), PublicID: fakes.ChatPublicID(), DeviceID: e.deviceA, JID: "a@s.whatsapp.net", Kind: "dm"}
	b := dataplane.ChatRef{ID: uuid.New(), PublicID: fakes.ChatPublicID(), DeviceID: e.deviceB, JID: "b@s.whatsapp.net", Kind: "dm"}
	e.chats.rows[a.ID], e.chats.rows[b.ID] = a, b

	rec := do(t, e.handler(), http.MethodGet, project+"/chats", "", nil, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, e.chats.last.DeviceIDs, "a project-wide key lists every device")

	e.authz.resources = []uuid.UUID{e.deviceB}
	rec = do(t, e.handler(), http.MethodGet, project+"/chats?kind=dm&q=bu", "", nil, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var env struct {
		Data       []map[string]any `json:"data"`
		NextCursor string           `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	require.Len(t, env.Data, 1)
	require.Equal(t, b.PublicID, env.Data[0]["id"])
	require.Equal(t, "next-page", env.NextCursor)
	require.Equal(t, "bu", e.chats.last.Search)

	rec = do(t, e.handler(), http.MethodGet, project+"/chats?device_id="+e.deviceAPub, "", nil, nil)
	require.Equal(t, http.StatusNotFound, rec.Code, "naming a device outside the key's binding is the masked 404")
}

func TestChatDoors_AuthorizeOnTheChatsDevice(t *testing.T) {
	e := newEnv(true)
	c := dataplane.ChatRef{ID: uuid.New(), PublicID: fakes.ChatPublicID(), DeviceID: e.deviceA, JID: "a@s.whatsapp.net", Kind: "dm"}
	e.chats.rows[c.ID] = c
	h := e.handler()
	require.Equal(t, http.StatusOK, do(t, h, http.MethodGet, project+"/chats/"+c.PublicID, "", nil, nil).Code)
	require.Equal(t, http.StatusOK, do(t, h, http.MethodGet, project+"/chats/"+c.PublicID+"/messages", "", nil, nil).Code)
	require.Equal(t, http.StatusNoContent, do(t, h, http.MethodPost, project+"/chats/"+c.PublicID+"/read", "", nil, nil).Code)

	e.authz.resources = []uuid.UUID{e.deviceB}
	h = e.handler()
	for _, path := range []string{"/chats/" + c.PublicID, "/chats/" + c.PublicID + "/messages"} {
		require.Equal(t, http.StatusNotFound, do(t, h, http.MethodGet, project+path, "", nil, nil).Code, path)
	}
	reads := len(e.messages.reads)
	require.Equal(t, http.StatusNotFound, do(t, h, http.MethodPost, project+"/chats/"+c.PublicID+"/read", "", nil, nil).Code)
	require.Len(t, e.messages.reads, reads, "SECURITY: a key bound to another device sends no read receipts")
}

func TestListContacts_Filtered(t *testing.T) {
	e := newEnv(true)
	e.authz.resources = []uuid.UUID{e.deviceA}
	rec := do(t, e.handler(), http.MethodGet, project+"/contacts?q=bud", "", nil, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []uuid.UUID{e.deviceA}, e.contacts.last.DeviceIDs)
	require.Contains(t, rec.Body.String(), `"phone":"628111"`)
}

// SECURITY: a device-bound key never lists without a device filter, which would be every project row.
func TestDeviceBoundKey_NeverListsWithoutADeviceFilter(t *testing.T) {
	e := newEnv(true)
	e.authz.resources = []uuid.UUID{e.deviceA, e.deviceB}
	h := e.handler()
	require.Equal(t, http.StatusOK, do(t, h, http.MethodGet, project+"/chats", "", nil, nil).Code)
	require.NotEmpty(t, e.chats.last.DeviceIDs)
	require.ElementsMatch(t, []uuid.UUID{e.deviceA, e.deviceB}, e.chats.last.DeviceIDs)
	require.Equal(t, http.StatusOK, do(t, h, http.MethodGet, project+"/contacts", "", nil, nil).Code)
	require.ElementsMatch(t, []uuid.UUID{e.deviceA, e.deviceB}, e.contacts.last.DeviceIDs)

	rec := do(t, h, http.MethodGet, project+"/contacts?device_id="+e.deviceAPub, "", nil, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, &e.deviceA, e.contacts.last.DeviceID)
	require.Empty(t, e.contacts.last.DeviceIDs)
}
