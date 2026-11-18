package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"altalune.id/openwa/internal/org"
)

func TestDevices_MemberSettingsShowTheManagersOnlyLineNotTheDangerCopy(t *testing.T) {
	x := newDeviceFixture(t)
	d := x.seed(t, "Sales")
	member := addMember(t, x.handlerFixture, x.org, x.uid, org.RoleMember)
	rec := httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, x.authedRequest(t, http.MethodGet, devicesBase+"/"+d.PublicID+"?tab=settings", "", member))
	body := mainOf(t, rec.Body.String())
	assert.Contains(t, body, managersOnlyKey)
	assert.NotContains(t, body, "devices.unlink_row")
	assert.NotContains(t, body, "devices.delete_row")

	mgr := mainOf(t, x.do(t, http.MethodGet, devicesBase+"/"+d.PublicID+"?tab=settings", "", false).Body.String())
	assert.Contains(t, mgr, "devices.unlink_row")
	assert.Contains(t, mgr, "devices.delete_row")
}
