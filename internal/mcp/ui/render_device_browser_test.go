package ui

import (
	"strings"
	"testing"
)

func TestRenderDeviceList(t *testing.T) {
	rep := renderInBrowser(t, "device_list", "openwa-device-list", fixtureJSON(t, "device_list.json"))
	if !rep.Scoped {
		t.Error("the view rendered outside a shadow root")
	}
	for _, want := range []string{"Sales", "Support", "+628123456789", "Needs attention"} {
		if !strings.Contains(rep.Text, want) {
			t.Errorf("rendered list is missing %q:\n%s", want, rep.Text)
		}
	}
	if rep.Buttons != 1 {
		t.Errorf("rendered %d Pair buttons, want 1", rep.Buttons)
	}
	if rep.Images != 0 {
		t.Errorf("the list rendered %d images; it never carries a QR", rep.Images)
	}
}

func TestRenderDevicePairShowsTheQRAndRefresh(t *testing.T) {
	rep := renderInBrowser(t, "device_pair", "openwa-device-pair", fixtureJSON(t, "device_pair.json"))
	if rep.Images != 1 || !strings.HasPrefix(rep.ImgSrc, "data:image/png;base64,iVBOR") {
		t.Errorf("QR image missing or wrong: %d images, src %q", rep.Images, rep.ImgSrc)
	}
	if rep.Buttons != 1 {
		t.Errorf("rendered %d buttons, want the one Refresh", rep.Buttons)
	}
}

func TestRenderDevicePairDropsAHostilePNG(t *testing.T) {
	rep := renderInBrowser(t, "device_pair", "openwa-device-pair", `{"deviceId":"d","link":{"outcome":"pending","png":"x\" onerror=\"alert(1)"}}`)
	if rep.Images != 0 {
		t.Errorf("a hostile png rendered an image: %q", rep.ImgSrc)
	}
}
