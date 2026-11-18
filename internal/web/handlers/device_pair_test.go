package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
)

func (x *deviceFixture) only(t *testing.T) device.DeviceView {
	t.Helper()
	views, err := x.Devices.List(setTenantProject(context.Background(), x.org, x.project, x.uid))
	require.NoError(t, err)
	require.Len(t, views, 1)
	return views[0]
}

func TestDevices_CreateWithoutANameCreatesTheDefaultAndStartsLinking(t *testing.T) {
	x := newDeviceFixture(t)
	rec := x.do(t, http.MethodPost, devicesBase, "", false)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())

	dv := x.only(t)
	require.Equal(t, device.DefaultName, dv.Device.Name)
	require.Equal(t, []uuid.UUID{dv.Device.ID}, x.Sessions.Linked, "the same request starts linking")
	require.Equal(t, devicesBase+"/new?device="+dv.Device.PublicID, rec.Header().Get("Location"))
}

func TestDevices_ALinkFailureKeepsTheNumberAndOffersRetryAndDelete(t *testing.T) {
	x := newDeviceFixture(t)
	x.Sessions.LinkErr = errors.New("whatsapp did not answer")
	rec := x.do(t, http.MethodPost, devicesBase, "", false)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	dv := x.only(t)

	x.Sessions.State = device.LinkState{Outcome: device.LinkFailed}
	page := x.do(t, http.MethodGet, rec.Header().Get("Location"), "", false)
	require.Equal(t, http.StatusOK, page.Code)
	body := mainOf(t, page.Body.String())
	require.Contains(t, body, "devices.connect.failed_title")
	require.Contains(t, body, `hx-post="`+devicesBase+"/"+dv.Device.PublicID+`/links"`, "Try again starts a new attempt for the same number")
	require.Contains(t, body, `action="`+devicesBase+"/"+dv.Device.PublicID+`/delete"`, "the number can be deleted from the error")

	x.Sessions.LinkErr = nil
	rec = x.do(t, http.MethodPost, devicesBase, "", false)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	again := x.only(t)
	require.Equal(t, dv.Device.ID, again.Device.ID, "a retry reuses the never-paired number instead of piling up WhatsApp 2")
	require.Equal(t, []uuid.UUID{dv.Device.ID, dv.Device.ID}, x.Sessions.Linked)
}

func TestDevices_CreateWithANameKeepsTheNamedPath(t *testing.T) {
	x := newDeviceFixture(t)
	rec := x.do(t, http.MethodPost, devicesBase, "name=Sales", false)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, "Sales", x.only(t).Device.Name)
	require.Empty(t, x.Sessions.Linked, "the named path does not start linking")
}

func TestDevices_NewPageShowsTheFirstRunStepsAndThePairing(t *testing.T) {
	x := newDeviceFixture(t)
	d := x.seed(t, device.DefaultName)
	x.Sessions.State = device.LinkState{Outcome: device.LinkPending, Method: device.LinkQR, PNG: []byte{0x89, 'P', 'N', 'G'}, ExpiresAt: time.Now().Add(20 * time.Second)}

	rec := x.do(t, http.MethodGet, devicesBase+"/new?device="+d.PublicID, "", false)
	require.Equal(t, http.StatusOK, rec.Code)
	body := mainOf(t, rec.Body.String())
	require.Contains(t, body, `class="steps`)
	require.Contains(t, body, `aria-current="step"`)
	require.Contains(t, body, `id="device-pairing"`)
	require.Contains(t, body, `hx-get="`+devicesBase+"/"+d.PublicID+`/links/current?flow=new"`)
	require.Contains(t, body, `hx-swap="outerMorph"`)
	require.Contains(t, body, "radial-progress")
	require.Contains(t, body, "data-countdown-until=")
	require.Contains(t, body, `value="qr" checked`, "the server picks the QR tab; the tabs script moves phones to the code tab")
}

func TestDevices_NewPageWithoutANumberOffersToStart(t *testing.T) {
	x := newDeviceFixture(t)
	rec := x.do(t, http.MethodGet, devicesBase+"/new", "", false)
	require.Equal(t, http.StatusOK, rec.Code)
	body := mainOf(t, rec.Body.String())
	require.Contains(t, body, `action="`+devicesBase+`"`)
	require.Zero(t, x.Store.Len(), "a GET creates nothing")
}

func TestDevices_NewPageForAnUnknownNumberIsNotFound(t *testing.T) {
	x := newDeviceFixture(t)
	rec := x.do(t, http.MethodGet, devicesBase+"/new?device=dev_V1StGXR8Z5jdHi6B", "", false)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDevices_PairingPollKeepsTheChosenTab(t *testing.T) {
	x := newDeviceFixture(t)
	d := x.seed(t, device.DefaultName)
	x.Sessions.State = device.LinkState{Outcome: device.LinkPending, Method: device.LinkQR, PNG: []byte{0x89, 'P', 'N', 'G'}}

	body := x.do(t, http.MethodGet, devicesBase+"/"+d.PublicID+"/links/current?flow=new&pair_tab=code&phone=0812", "", true).Body.String()
	require.Contains(t, body, `value="code" checked`)
	require.NotContains(t, body, `value="qr" checked`)
	require.Contains(t, body, `value="0812"`, "the typed number survives the poll")
}

func TestDevices_ALinkedNumberContinuesToTheNameStep(t *testing.T) {
	x := newDeviceFixture(t)
	d := x.seed(t, device.DefaultName)
	x.Sessions.State = device.LinkState{Outcome: device.LinkConnected}
	x.Sessions.Statuses[d.ID] = device.SessionStatus{State: device.SessionConnected, Phone: "6281234567890", PushName: "Toko Sari"}

	body := mainOf(t, x.do(t, http.MethodGet, devicesBase+"/new?device="+d.PublicID, "", false).Body.String())
	next := devicesBase + "/new?device=" + d.PublicID + "&amp;step=name"
	require.Contains(t, body, `href="`+next+`"`)

	rec := x.do(t, http.MethodGet, devicesBase+"/new?device="+d.PublicID+"&step=name", "", false)
	require.Equal(t, http.StatusOK, rec.Code)
	body = mainOf(t, rec.Body.String())
	require.Contains(t, body, `action="`+devicesBase+"/"+d.PublicID+`/rename"`)
	require.Contains(t, body, `name="next" value="rules"`)
	require.Contains(t, body, `value="Toko Sari"`, "the name is prefilled from the WhatsApp profile")
	require.Contains(t, body, `href="`+devicesBase+"/"+d.PublicID+`/rules"`, "Skip is a plain link to the rules")
}

// SECURITY: next is a fixed step name, never a URL, so rename cannot become an open redirect.
func TestDevices_RenameFollowsOnlyTheRulesStep(t *testing.T) {
	cases := map[string]string{
		"rules":                "/rules",
		"https://evil.example": "",
		"//evil":               "",
		"/rules":               "",
		"":                     "",
	}
	for next, suffix := range cases {
		t.Run(next, func(t *testing.T) {
			x := newDeviceFixture(t)
			d := x.seed(t, device.DefaultName)
			form := "version=1&name=Toko+Sari&next=" + strings.ReplaceAll(next, "/", "%2F")
			rec := x.do(t, http.MethodPost, devicesBase+"/"+d.PublicID+"/rename", form, false)
			require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
			require.Equal(t, devicesBase+"/"+d.PublicID+suffix, rec.Header().Get("Location"))
		})
	}
}

func TestDevices_PhoneHintPromisesLocalFormatsOnlyWithADefaultCountryCode(t *testing.T) {
	for cc, want := range map[string]string{"62": "devices.connect.phone_help<", "": "devices.connect.phone_help_intl<"} {
		t.Run("cc="+cc, func(t *testing.T) {
			x := newDeviceFixture(t)
			x.Cfg.WhatsApp.DefaultCountryCode = cc
			d := x.seed(t, device.DefaultName)
			x.Sessions.State = device.LinkState{Outcome: device.LinkNone}
			body := x.do(t, http.MethodGet, devicesBase+"/"+d.PublicID+"/links/current?pair_tab=code", "", true).Body.String()
			require.Contains(t, body, want)
		})
	}
}
