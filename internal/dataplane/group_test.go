package dataplane_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/dataplane"
	"altalune.id/openwa/internal/testutil/fakes"
)

func TestGroupRoutes(t *testing.T) {
	e := newEnv(true)
	g := dataplane.ChatRef{ID: uuid.New(), PublicID: fakes.ChatPublicID(), DeviceID: e.deviceA, JID: "111@g.us", Kind: "group", Name: "Tim"}
	dm := dataplane.ChatRef{ID: uuid.New(), PublicID: fakes.ChatPublicID(), DeviceID: e.deviceA, JID: "628111@s.whatsapp.net", Kind: "dm"}
	e.chats.rows[g.ID], e.chats.rows[dm.ID] = g, dm
	h := e.handler()

	rec := do(t, h, http.MethodGet, project+"/chats/"+g.PublicID+"/group", "", nil, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"participants":3`)
	rec = do(t, h, http.MethodGet, project+"/chats/"+dm.PublicID+"/group", "", nil, nil)
	require.Equal(t, http.StatusConflict, rec.Code)

	rec = do(t, h, http.MethodPost, project+"/chats/"+g.PublicID+"/leave", "", nil, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"archived":true`)

	rec = do(t, h, http.MethodPost, project+"/devices/"+e.deviceAPub+"/groups", "application/json", []byte(`{"invite_link":"https://chat.whatsapp.com/AbC"}`), nil)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Contains(t, rec.Body.String(), `"jid":"999@g.us"`)

	rec = do(t, h, http.MethodPost, project+"/devices/"+e.deviceAPub+"/groups", "application/json", []byte(`{}`), nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, "a join with no invite link is refused")

	e.authz.resources = []uuid.UUID{e.deviceB}
	rec = do(t, e.handler(), http.MethodPost, project+"/devices/"+e.deviceAPub+"/groups", "application/json", []byte(`{"invite_link":"x"}`), nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, http.StatusNotFound, do(t, e.handler(), http.MethodGet, project+"/chats/"+g.PublicID+"/group", "", nil, nil).Code, "a key bound to another device cannot read this group")
	require.Equal(t, http.StatusNotFound, do(t, e.handler(), http.MethodPost, project+"/chats/"+g.PublicID+"/leave", "", nil, nil).Code)

	e.authz.resources = nil
	before := len(e.chats.rows)
	for _, foreign := range []string{e.scoped.sibling.PublicID, uuid.NewString()} {
		rec = do(t, e.handler(), http.MethodPost, project+"/devices/"+foreign+"/groups", "application/json", []byte(`{"invite_link":"https://chat.whatsapp.com/AbC"}`), nil)
		require.Equal(t, http.StatusNotFound, rec.Code, "SECURITY: a project-wide key cannot join through another project's device, nor through a UUID")
	}
	require.Equal(t, 1, e.scoped.siblingHits)
	require.Len(t, e.chats.rows, before)
}
