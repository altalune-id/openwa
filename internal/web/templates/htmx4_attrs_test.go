package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplatesUseTheHtmx4DisableAttribute(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.templ")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // repo template paths
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "hx-disabled-elt") {
			t.Errorf("%s uses hx-disabled-elt, the htmx 2 name; htmx 4 reads hx-disable", f)
		}
	}
}
