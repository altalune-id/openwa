package web

import (
	"regexp"
	"strings"
	"testing"
)

func TestCompiledCSS_FollowsTheWritingDirection(t *testing.T) {
	b, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if hit := regexp.MustCompile(`text-align:\s*(left|right)`).FindString(src); hit != "" {
		t.Errorf("app.css uses %q; use start or end", hit)
	}
	if strings.Contains(src, "translateX(") && !strings.Contains(src, ":dir(rtl)") && !strings.Contains(src, `[dir="rtl"]`) {
		t.Error("app.css nudges along x with no RTL mirror")
	}
}
