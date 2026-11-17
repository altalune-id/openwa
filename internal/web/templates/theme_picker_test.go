package templates

import (
	"regexp"
	"strings"
	"testing"

	"altalune.id/openwa/internal/web"
)

func TestThemePicker_PlainFormWithCheckedModeAndOneFamily(t *testing.T) {
	d := web.LayoutData{Theme: web.ThemePref{Family: web.DefaultFamily(), Mode: web.ModeDark}, CurrentPath: "/back"}
	out := render(t, ThemePicker(d))
	for _, want := range []string{
		`<form method="post" action="/theme"`,
		`name="redirect" value="/back"`,
		`<input type="hidden" name="family" value="openwa">`,
		`type="submit"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("picker lacks %s:\n%s", want, out)
		}
	}
	radios := regexp.MustCompile(`<input type="radio" name="mode" value="([a-z]+)"[^>]*>`).FindAllStringSubmatch(out, -1)
	if len(radios) != 3 {
		t.Fatalf("want 3 mode radios, got %d", len(radios))
	}
	for _, r := range radios {
		if strings.Contains(r[0], " checked") != (r[1] == web.ModeDark) {
			t.Errorf("radio %s checked mismatch", r[1])
		}
	}
	if strings.Contains(out, `name="family" value="tide"`) || strings.Contains(out, `type="radio" name="family"`) {
		t.Error("a single family renders no family choice")
	}
}
