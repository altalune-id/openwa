package ui

import (
	"strings"
	"testing"
)

func TestDeviceListModel_KPIsRowsAndOnePairAction(t *testing.T) {
	vm := newJSVM(t)
	fx := fixtureJSON(t, "device_list.json")
	kpis := jsString(t, vm, `renderTool("device_list", `+fx+`).model.kpis.map(function (k) { return k.label + "=" + k.value; }).join(",")`)
	if kpis != "Devices=2,Connected=1,Needs attention=0" {
		t.Fatalf("kpis = %q", kpis)
	}
	rows := jsString(t, vm, `JSON.stringify(renderTool("device_list", `+fx+`).model.rows)`)
	for _, want := range []string{`"name":"Sales"`, `"phone":"+628123456789"`, `"seen":"2026-09-28"`, `"state":"unlinked"`} {
		if !strings.Contains(rows, want) {
			t.Errorf("rows missing %s: %s", want, rows)
		}
	}
	actions := jsString(t, vm, `Object.keys(renderTool("device_list", `+fx+`).actions).join(",")`)
	if actions != "pair:dev_SupportDevice001" {
		t.Errorf("only the unpaired device offers Pair, got %q", actions)
	}
}

func TestDevicePairModel_OnlyBase64ReachesTheImage(t *testing.T) {
	vm := newJSVM(t)
	qr := jsString(t, vm, `renderTool("device_pair", `+fixtureJSON(t, "device_pair.json")+`).model.qr`)
	if !strings.HasPrefix(qr, "data:image/png;base64,iVBOR") {
		t.Fatalf("qr = %q", qr)
	}
	hostile := jsString(t, vm, `renderTool("device_pair", {"deviceId":"d","link":{"outcome":"pending","png":"x\" onerror=\"alert(1)"}}).model.qr`)
	if hostile != "" {
		t.Errorf("a non-base64 png reached the data URL: %q", hostile)
	}
	refresh := jsString(t, vm, `JSON.stringify(renderTool("device_pair", `+fixtureJSON(t, "device_pair.json")+`).actions)`)
	if !strings.Contains(refresh, `"tool":"device_get"`) {
		t.Errorf("Refresh must call device_get: %s", refresh)
	}
}

func TestDeviceGetModel_ReadsTheNestedLink(t *testing.T) {
	vm := newJSVM(t)
	got := jsString(t, vm, `JSON.stringify(renderTool("device_get", {"device":{"id":"d1","state":"connected","phone":"628111"}}).model)`)
	for _, want := range []string{`"state":"connected"`, `"phone":"+628111"`, `"outcome":"none"`} {
		if !strings.Contains(got, want) {
			t.Errorf("device_get model missing %s: %s", want, got)
		}
	}
}
