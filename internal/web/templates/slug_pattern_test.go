package templates

import (
	"regexp"
	"strings"
	"testing"
)

var slugInput = regexp.MustCompile(`<input name="slug"[^>]*>`)

func TestSlugInputs_PatternIsValidUnderTheVFlag(t *testing.T) {
	for name, html := range map[string]string{
		"project_new": render(t, ProjectNewPage(uiData(), ProjectNewView{})),
		"org_new":     render(t, OrgNewPage(uiData(), OrgNewView{})),
	} {
		tag := slugInput.FindString(html)
		if tag == "" {
			t.Fatalf("%s: no slug input", name)
		}
		if !strings.Contains(tag, `pattern="[a-z0-9\-]+"`) {
			t.Errorf("%s: slug pattern must escape the hyphen (a bare - in a class is invalid under the v flag): %s", name, tag)
		}
		if !strings.Contains(tag, `dir="ltr"`) {
			t.Errorf("%s: slug input must be dir=ltr: %s", name, tag)
		}
	}
}
