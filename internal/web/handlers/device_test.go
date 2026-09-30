package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/web/handlers"
	"altalune.id/openwa/internal/whatsapp"
)

const devicesBase = "/orgs/acme/projects/alpha/devices"

type deviceFixture struct {
	*handlerFixture
	Mux      *http.ServeMux
	Devices  *device.Service
	Store    *fakes.Device
	Sessions *fakes.DeviceSessions
	uid      uuid.UUID
	org      uuid.UUID
	project  uuid.UUID
}

func newDeviceFixture(t *testing.T) *deviceFixture {
	t.Helper()
	f := newFixture(t)
	store := fakes.NewDevice()
	sessions := fakes.NewDeviceSessions()
	devices := device.NewService(store, discardLogger(), passthroughUnexpected(), fakes.UnitOfWork, sessions)

	uid := uuid.New()
	o := f.seedOrg(t, "acme", uid)
	proj, err := f.Projects.Create(setTenant(context.Background(), o.ID, uid), o.ID, "alpha", "Alpha")
	require.NoError(t, err)

	mux := http.NewServeMux()
	handlers.NewDeviceHandler(f.Deps, f.Projects, devices).Register(mux)
	return &deviceFixture{handlerFixture: f, Mux: mux, Devices: devices, Store: store, Sessions: sessions, uid: uid, org: o.ID, project: proj.ID}
}

func (x *deviceFixture) do(t *testing.T, method, target, body string, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	p := session.Principal{UserID: x.uid, ActiveOrgID: x.org, ActiveProjectID: x.project}
	req := x.authedRequest(t, method, target, body, p)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	x.Mux.ServeHTTP(rec, req)
	return rec
}

func (x *deviceFixture) seed(t *testing.T, name string) *device.Device {
	t.Helper()
	d, err := x.Devices.Create(setTenantProject(context.Background(), x.org, x.project, x.uid), name)
	require.NoError(t, err)
	return d
}

func TestDevices_ListShowsTheEmptyStateThenCards(t *testing.T) {
	x := newDeviceFixture(t)
	rec := x.do(t, http.MethodGet, devicesBase, "", false)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "devices.empty_title")

	d := x.seed(t, "Sales")
	x.Sessions.Statuses[d.ID] = device.SessionStatus{State: device.SessionConnected, Phone: "628111", PushName: "Ops"}
	down := x.seed(t, "Support")
	x.Sessions.Statuses[down.ID] = device.SessionStatus{State: device.SessionDisconnected, Reason: "network"}
	rec = x.do(t, http.MethodGet, devicesBase, "", false)
	body := rec.Body.String()
	require.Contains(t, body, `id="device-`+d.PublicID+`"`)
	require.Contains(t, body, "devices.state.connected")
	require.Contains(t, body, "+628111")
	require.Equal(t, "2", statValue(t, body, "devices.stat_total"))
	require.Equal(t, "1", statValue(t, body, "devices.stat_connected"))
	require.Equal(t, "1", statValue(t, body, "devices.stat_attention"), "the disconnected device needs attention")
}

func statValue(t *testing.T, body, label string) string {
	t.Helper()
	m := regexp.MustCompile(regexp.QuoteMeta(label) + `</p>\s*<p[^>]*>([^<]*)</p>`).FindStringSubmatch(body)
	require.Len(t, m, 2, "no stat card labelled %s", label)
	return m[1]
}

func TestDevices_CreateRedirectsToTheDetailWithAFlash(t *testing.T) {
	x := newDeviceFixture(t)
	rec := x.do(t, http.MethodPost, devicesBase, "name=Sales", false)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, 1, x.Store.Len())
	require.True(t, strings.HasPrefix(rec.Header().Get("Location"), devicesBase+"/"))
	require.Contains(t, rec.Header().Get("Set-Cookie"), "openwa_flash=")
}

func TestDevices_CreateWithABadOrTakenNameIs422OnTheField(t *testing.T) {
	x := newDeviceFixture(t)
	rec := x.do(t, http.MethodPost, devicesBase, "name=%20", false)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, rec.Body.String(), `id="name-error"`)
	require.Contains(t, rec.Body.String(), "devices.error.invalid_name")

	x.seed(t, "Sales")
	rec = x.do(t, http.MethodPost, devicesBase, "name=sales", false)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "names are unique per project, case-insensitively")
	require.Contains(t, rec.Body.String(), "devices.error.name_taken")
}

func TestDevices_DetailPollsOnlyWhileAnAttemptIsPending(t *testing.T) {
	x := newDeviceFixture(t)
	d := x.seed(t, "Sales")
	rec := x.do(t, http.MethodGet, devicesBase+"/"+d.PublicID, "", false)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, `hx-get="`+devicesBase+"/"+d.PublicID+`/links/current"`)
	require.Contains(t, body, `hx-trigger="every 5s"`)
	require.Contains(t, body, "hx-nonce=")

	x.Sessions.State = device.LinkState{Outcome: device.LinkNone}
	x.Sessions.Statuses[d.ID] = device.SessionStatus{State: device.SessionConnected, Phone: "628111"}
	body = x.do(t, http.MethodGet, devicesBase+"/"+d.PublicID, "", false).Body.String()
	require.NotContains(t, body, `hx-trigger="every 5s"`)
	require.Contains(t, body, "data-paired")
}

func TestDevices_PostLinkReturnsTheQRFragment(t *testing.T) {
	x := newDeviceFixture(t)
	d := x.seed(t, "Sales")
	rec := x.do(t, http.MethodPost, devicesBase+"/"+d.PublicID+"/links", "", true)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, `id="device-pairing"`)
	require.Contains(t, body, `src="data:image/png;base64,`)
	require.NotContains(t, body, "<html", "a fragment, not a page")
	require.Equal(t, []uuid.UUID{d.ID}, x.Sessions.Linked)

	rec = x.do(t, http.MethodPost, devicesBase+"/"+d.PublicID+"/links", "", false)
	require.Equal(t, http.StatusSeeOther, rec.Code, "without htmx the form redirects back to the detail page")
}

func TestDevices_PostLinkRefusalRendersTheReason(t *testing.T) {
	x := newDeviceFixture(t)
	d := x.seed(t, "Sales")
	x.Sessions.LinkErr = &whatsapp.AlreadyLinkedError{ID: d.ID.String()}
	rec := x.do(t, http.MethodPost, devicesBase+"/"+d.PublicID+"/links", "", true)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "devices.error.already_linked")
}

func TestDevices_PhoneLinkShowsTheCodeOrTheFieldError(t *testing.T) {
	x := newDeviceFixture(t)
	d := x.seed(t, "Sales")
	rec := x.do(t, http.MethodPost, devicesBase+"/"+d.PublicID+"/phone-links", "phone=%2B62+812+3456", true)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "ABCD-EFGH")
	require.Equal(t, []string{"+62 812 3456"}, x.Sessions.Phones)

	x.Sessions.LinkErr = &whatsapp.InvalidPhoneError{Reason: "national number"}
	rec = x.do(t, http.MethodPost, devicesBase+"/"+d.PublicID+"/phone-links", "phone=0812", true)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, rec.Body.String(), `id="phone-error"`)
}

func TestDevices_DetailConfirmsLogoutAndDelete(t *testing.T) {
	x := newDeviceFixture(t)
	d := x.seed(t, "Sales")
	x.Sessions.State = device.LinkState{Outcome: device.LinkNone}
	body := x.do(t, http.MethodGet, devicesBase+"/"+d.PublicID, "", false).Body.String()
	require.Equal(t, 2, strings.Count(body, "<dialog"))
	require.Equal(t, 2, strings.Count(body, `data-confirm-open="`), "each dialog has a labelled trigger button")
	require.Contains(t, body, `action="`+devicesBase+"/"+d.PublicID+`/unlink"`)
	require.Contains(t, body, `action="`+devicesBase+"/"+d.PublicID+`/delete"`)
	require.Equal(t, 2, strings.Count(body, `hx-indicator="#device-pair-busy"`), "both pair forms show the spinner during the up-to-15s POST")
	require.Contains(t, body, `id="device-pair-busy"`)
}

func TestDevices_UnlinkAndDelete(t *testing.T) {
	x := newDeviceFixture(t)
	d := x.seed(t, "Sales")
	rec := x.do(t, http.MethodPost, devicesBase+"/"+d.PublicID+"/unlink", "", false)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, devicesBase+"/"+d.PublicID, rec.Header().Get("Location"))
	require.Equal(t, []uuid.UUID{d.ID}, x.Sessions.Unlinked)

	rec = x.do(t, http.MethodPost, devicesBase+"/"+d.PublicID+"/delete", "", false)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, devicesBase, rec.Header().Get("Location"))
	require.Equal(t, []uuid.UUID{d.ID}, x.Sessions.Forgot)
	require.Zero(t, x.Store.Len())
}

func TestDevices_RulesAndRename(t *testing.T) {
	x := newDeviceFixture(t)
	d := x.seed(t, "Sales")
	path := devicesBase + "/" + d.PublicID

	rec := x.do(t, http.MethodPost, path+"/rules", "version=1&group_mode=open&allowed_senders=%2B62+812-3456-789%0A628111111111&trigger_prefix=%21bot&ignore_from_me=1", false)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	got, err := x.Devices.Get(setTenantProject(context.Background(), x.org, x.project, x.uid), d.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"628123456789", "628111111111"}, got.Device.Rules.AllowedSenders)
	require.True(t, got.Device.Rules.IgnoreFromMe)

	rec = x.do(t, http.MethodPost, path+"/rules", "version=2&group_mode=open&allowed_groups=nope", false)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, rec.Body.String(), `id="allowed_groups-error"`)

	rec = x.do(t, http.MethodPost, path+"/rules", "version=1&group_mode=open", false)
	require.Equal(t, http.StatusConflict, rec.Code, "a stale version is refused")

	rec = x.do(t, http.MethodPost, path+"/rename", "version=2&name=Support", false)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	rec = x.do(t, http.MethodPost, path+"/rename", "version=3&name=%20", false)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, rec.Body.String(), `id="rename-name-error"`)
}

func TestDevices_ADeviceOfASiblingProjectIsNotFound(t *testing.T) {
	x := newDeviceFixture(t)
	other, err := x.Projects.Create(setTenant(context.Background(), x.org, x.uid), x.org, "beta", "Beta")
	require.NoError(t, err)
	d, err := x.Devices.Create(setTenantProject(context.Background(), x.org, other.ID, x.uid), "Elsewhere")
	require.NoError(t, err)
	rec := x.do(t, http.MethodGet, devicesBase+"/"+d.PublicID, "", false)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDevices_TheURLTakesThePublicIDNeverTheUUID(t *testing.T) {
	x := newDeviceFixture(t)
	d := x.seed(t, "Sales")
	require.Equal(t, http.StatusOK, x.do(t, http.MethodGet, devicesBase+"/"+d.PublicID, "", false).Code)
	require.Equal(t, http.StatusNotFound, x.do(t, http.MethodGet, devicesBase+"/"+d.ID.String(), "", false).Code,
		"the internal UUID never addresses a device")
	require.Equal(t, http.StatusNotFound, x.do(t, http.MethodGet, devicesBase+"/dev_short", "", false).Code)

	body := x.do(t, http.MethodGet, devicesBase+"/"+d.PublicID, "", false).Body.String()
	require.Contains(t, body, `data-copy="`+d.PublicID+`"`, "the detail page offers the public id on a copy button")
	require.NotContains(t, body, d.ID.String(), "the internal UUID never reaches the page")
}

func TestDevices_AMissingOrGarbledVersionIsRefusedNotWrittenUnconditionally(t *testing.T) {
	x := newDeviceFixture(t)
	d := x.seed(t, "Sales")
	path := devicesBase + "/" + d.PublicID
	for _, v := range []string{"", "&version=", "&version=abc", "&version=0", "&version=-1"} {
		rec := x.do(t, http.MethodPost, path+"/rename", "name=Changed"+v, false)
		require.Equal(t, http.StatusConflict, rec.Code, "rename with version %q", v)
		rec = x.do(t, http.MethodPost, path+"/rules", "group_mode=open&trigger_prefix=%21x"+v, false)
		require.Equal(t, http.StatusConflict, rec.Code, "rules with version %q", v)
	}
	got, err := x.Devices.Get(setTenantProject(context.Background(), x.org, x.project, x.uid), d.ID)
	require.NoError(t, err)
	require.Equal(t, "Sales", got.Device.Name)
	require.Empty(t, got.Device.Rules.TriggerPrefix)
	require.Equal(t, d.Version, got.Device.Version)
}

func TestDevices_PendingPairingShowsAVisibleWaitingLine(t *testing.T) {
	x := newDeviceFixture(t)
	d := x.seed(t, "Sales")
	x.Sessions.State = device.LinkState{Outcome: device.LinkPending}
	body := x.do(t, http.MethodGet, devicesBase+"/"+d.PublicID+"/links/current", "", true).Body.String()
	require.Contains(t, body, "data-pairing-wait")
	require.NotContains(t, body, "htmx-indicator")
}
