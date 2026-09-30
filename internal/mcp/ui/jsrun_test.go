package ui

import (
	"strings"
	"testing"

	"github.com/dop251/goja"
)

//nolint:gochecknoglobals // a classification table has to be package level.
var (
	litParts = map[string]bool{
		"src/lit.js":                true,
		"src/styles.js":             true,
		"src/views/project_list.js": true,
		"src/views/device_list.js":  true,
		"src/views/device_pair.js":  true,
		"src/app.js":                true,
	}
	domParts = map[string]bool{
		"src/bridge.js": true,
		"src/boot.js":   true,
	}
)

// TestEveryScriptPartIsClassified: an unclassified part would silently drop out of the goja harness.
func TestEveryScriptPartIsClassified(t *testing.T) {
	for _, p := range scriptParts {
		if litParts[p] && domParts[p] {
			t.Errorf("%s is classified twice", p)
		}
	}
	for p := range litParts {
		if !contains(scriptParts, p) {
			t.Errorf("litParts names %s, which is not a script part", p)
		}
	}
	for p := range domParts {
		if !contains(scriptParts, p) {
			t.Errorf("domParts names %s, which is not a script part", p)
		}
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func newJSVM(t *testing.T) *goja.Runtime {
	t.Helper()
	vm := goja.New()
	for _, p := range scriptParts {
		if litParts[p] || domParts[p] {
			continue
		}
		if _, err := vm.RunString(mustRead(t, p)); err != nil {
			t.Fatalf("eval %s: %v", p, err)
		}
	}
	if _, err := vm.RunString(`registerView("project_list", projectListModel, null);`); err != nil {
		t.Fatalf("register project_list: %v", err)
	}
	if _, err := vm.RunString(`registerView("device_list", deviceListModel, null); registerView("device_pair", devicePairModel, null); registerView("device_get", deviceGetModel, null);`); err != nil {
		t.Fatalf("register device views: %v", err)
	}
	return vm
}

func jsString(t *testing.T, vm *goja.Runtime, expr string) string {
	t.Helper()
	v, err := vm.RunString(expr)
	if err != nil {
		t.Fatalf("eval %q: %v", expr, err)
	}
	return v.String()
}

func jsQuote(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", `\'`) + "'"
}

func TestFormatNumGroupsWithoutToLocaleString(t *testing.T) {
	vm := newJSVM(t)
	for expr, want := range map[string]string{
		`num(42)`:        "42",
		`num(1234567)`:   "1,234,567",
		`num(-1234)`:     "-1,234",
		`num(undefined)`: "0",
	} {
		if got := jsString(t, vm, expr); got != want {
			t.Errorf("%s = %q, want %q", expr, got, want)
		}
	}
}

func TestFormatDayToleratesMissingTimestamps(t *testing.T) {
	vm := newJSVM(t)
	for expr, want := range map[string]string{
		`day("2026-09-01T08:30:00Z")`: "2026-09-01",
		`day("2026-09-01")`:           "2026-09-01",
		`day(undefined)`:              "—",
		`day("")`:                     "—",
	} {
		if got := jsString(t, vm, expr); got != want {
			t.Errorf("%s = %q, want %q", expr, got, want)
		}
	}
}

func TestProjectListModelRendersProjectsWithoutActions(t *testing.T) {
	vm := newJSVM(t)
	model, actions := modelFixture(t, vm, "project_list", "project_list.json")
	body := jsString(t, vm, `JSON.stringify(renderTool("project_list", `+fixtureJSON(t, "project_list.json")+`).model)`)
	for _, want := range []string{"Smoke", "smoke-proj", "Alpha", "0193f2a0-0000-7000-8000-0000000000aa"} {
		if !strings.Contains(body, want) {
			t.Errorf("project_list model is missing %q:\n%s", want, body)
		}
	}
	if model["empty"] != false {
		t.Errorf("model.empty = %v, want false", model["empty"])
	}
	if len(actions) != 0 {
		t.Errorf("project_list declared %d actions, want 0: %v", len(actions), actions)
	}
}

func TestProjectListModelCountsKPIs(t *testing.T) {
	vm := newJSVM(t)
	got := jsString(t, vm, `renderTool("project_list", `+fixtureJSON(t, "project_list.json")+`).model.kpis.map(function (k) { return k.label + "=" + k.value; }).join(",")`)
	if got != "Projects=2" {
		t.Errorf("kpis = %q", got)
	}
}

func TestProjectListModelRendersAnEmptyState(t *testing.T) {
	vm := newJSVM(t)
	model, actions := modelFixture(t, vm, "project_list", "project_list_empty.json")
	if model["empty"] != true {
		t.Errorf("empty fixture produced empty = %v, want true", model["empty"])
	}
	if len(actions) != 0 {
		t.Errorf("empty project_list declared %d actions, want 0", len(actions))
	}
}

func TestUnknownToolIsReportedMissing(t *testing.T) {
	vm := newJSVM(t)
	if got := jsString(t, vm, `String(renderTool("todo_list", {}).missing)`); got != "true" {
		t.Errorf("an unregistered tool must report missing, got %q", got)
	}
	if got := jsString(t, vm, `String(renderTool("todo_list", {}).template)`); got != "null" {
		t.Errorf("an unregistered tool must carry no template, got %q", got)
	}
}
