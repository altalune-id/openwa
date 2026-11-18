package handlers_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
)

func TestDevices_NumberPageSaysTheStateInPlainWords(t *testing.T) {
	changed := time.Date(2026, 11, 18, 13, 40, 0, 0, time.UTC)
	cases := map[string]struct {
		st    device.SessionStatus
		link  device.LinkState
		wants []string
	}{
		"connected":    {device.SessionStatus{State: device.SessionConnected, Phone: "628111"}, device.LinkState{Outcome: device.LinkNone}, []string{"devices.status.connected_none", "devices.state.connected"}},
		"reconnecting": {device.SessionStatus{State: device.SessionDisconnected, Phone: "628111"}, device.LinkState{Outcome: device.LinkNone}, []string{"devices.status.reconnecting", "loading-dots"}},
		"signed out":   {device.SessionStatus{State: device.SessionLoggedOut, Phone: "628111", UpdatedAt: changed}, device.LinkState{Outcome: device.LinkNone}, []string{"devices.status.signed_out", `data-tour="fix"`, "devices.fix_trying"}},
		"waiting":      {device.SessionStatus{State: device.SessionLinking}, device.LinkState{Outcome: device.LinkPending, PNG: []byte{0x89, 'P', 'N', 'G'}}, []string{"devices.status.waiting", `id="device-pairing"`}},
		"relinking":    {device.SessionStatus{State: device.SessionLoggedOut, Phone: "628111"}, device.LinkState{Outcome: device.LinkPending, PNG: []byte{0x89, 'P', 'N', 'G'}}, []string{"devices.relink_title", `id="device-pairing"`}},
		"not linked":   {device.SessionStatus{State: device.SessionUnlinked}, device.LinkState{Outcome: device.LinkNone}, []string{"devices.status.unlinked", "devices.connect.show_qr"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			x := newDeviceFixture(t)
			d := x.seed(t, "Gudang")
			x.Sessions.Statuses[d.ID] = c.st
			x.Sessions.State = c.link
			body := mainOf(t, x.do(t, http.MethodGet, devicesBase+"/"+d.PublicID, "", false).Body.String())
			for _, w := range c.wants {
				require.Contains(t, body, w)
			}
			require.Equal(t, c.st.State == device.SessionLoggedOut && c.link.Outcome != device.LinkPending, strings.Contains(body, `data-tour="fix"`), "Fix it only for a signed-out number with no new link running")
		})
	}
}

func TestDevices_FixItStartsANewLinkWithTheRelinkNote(t *testing.T) {
	x := newDeviceFixture(t)
	d := x.seed(t, "Gudang")
	x.Sessions.Statuses[d.ID] = device.SessionStatus{State: device.SessionLoggedOut, Phone: "628111"}
	x.Sessions.State = device.LinkState{Outcome: device.LinkPending, Method: device.LinkQR, PNG: []byte{0x89, 'P', 'N', 'G'}}

	rec := x.do(t, http.MethodPost, devicesBase+"/"+d.PublicID+"/links", "", true)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "devices.relink_title")
	require.Contains(t, body, `src="data:image/png;base64,`)
	require.Equal(t, []string{d.ID.String()}, func() []string {
		out := []string{}
		for _, id := range x.Sessions.Linked {
			out = append(out, id.String())
		}
		return out
	}())
}

func TestDevices_TimelineComesFromTheStoredTimes(t *testing.T) {
	connected := time.Date(2026, 11, 18, 9, 12, 0, 0, time.UTC)
	signedOut := connected.Add(4 * time.Hour)
	x := newDeviceFixture(t)
	d := x.seed(t, "Gudang")

	order := func(body string, keys ...string) {
		t.Helper()
		last := -1
		for _, k := range keys {
			i := strings.Index(body, k)
			require.Greater(t, i, last, "%s comes after the previous entry", k)
			last = i
		}
	}

	x.Sessions.Statuses[d.ID] = device.SessionStatus{State: device.SessionConnected, Phone: "628111", LastConnectedAt: &connected, UpdatedAt: connected}
	body := mainOf(t, x.do(t, http.MethodGet, devicesBase+"/"+d.PublicID, "", false).Body.String())
	order(body, "devices.timeline.added", "devices.timeline.connected_as")
	require.NotContains(t, body, "devices.timeline.signed_out")

	x.Sessions.Statuses[d.ID] = device.SessionStatus{State: device.SessionLoggedOut, Phone: "628111", LastConnectedAt: &connected, UpdatedAt: signedOut}
	body = mainOf(t, x.do(t, http.MethodGet, devicesBase+"/"+d.PublicID, "", false).Body.String())
	order(body, "devices.timeline.added", "devices.timeline.connected<", "devices.timeline.signed_out")
	require.NotContains(t, body, "devices.timeline.connected_as", "after a sign-out the connected line names no phone")

	x.Sessions.Statuses[d.ID] = device.SessionStatus{State: device.SessionUnlinked}
	body = mainOf(t, x.do(t, http.MethodGet, devicesBase+"/"+d.PublicID, "", false).Body.String())
	require.Contains(t, body, "devices.timeline.added")
	require.NotContains(t, body, "devices.timeline.unlinked", "a never-linked number was not unlinked")
}
