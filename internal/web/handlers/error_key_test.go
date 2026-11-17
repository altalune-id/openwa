package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"altalune.id/openwa/internal/i18n"
	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/web/handlers"
)

var (
	errorPageKeyLiteral = regexp.MustCompile(`ErrorPageKey\([^"\n]*"([^"]+)"`)
	signInKeyConst      = regexp.MustCompile(`(?m)^\s*signIn\w+\s*=\s*"([^"]+)"`)
)

func TestErrorPageKeys_HaveATitleAndABody(t *testing.T) {
	b, err := os.ReadFile("../../i18n/locales/active.en-US.yaml")
	if err != nil {
		t.Fatal(err)
	}
	msgs := map[string]any{}
	if err := yaml.Unmarshal(b, &msgs); err != nil {
		t.Fatal(err)
	}
	sources, _ := filepath.Glob("*.go")
	keys := 0
	for _, f := range sources {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, re := range []*regexp.Regexp{errorPageKeyLiteral, signInKeyConst} {
			for _, m := range re.FindAllStringSubmatch(string(src), -1) {
				keys++
				for _, half := range []string{"_title", "_body"} {
					if s, _ := msgs[m[1]+half].(string); s == "" {
						t.Errorf("%s: ErrorPageKey key %s has no %s in en-US", f, m[1], m[1]+half)
					}
				}
			}
		}
	}
	if keys == 0 {
		t.Fatal("no ErrorPageKey call found; the scan matches nothing")
	}
}

func TestManagersOnly_RefusalIsTranslatedOnPageAndFragment(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner := uuid.New()
	o := f.seedOrg(t, "acme", owner)
	f.seedProject(t, o, owner, "alpha")
	member := addMember(t, f, o.ID, owner, org.RoleMember)
	tr := i18n.NewEmbeddedBundle(i18n.EnUS).For(i18n.EnUS)
	mux := http.NewServeMux()
	handlers.NewSettingsHandler(f.Deps, f.Projects, nil).Register(mux)

	for _, path := range []string{"/orgs/acme/projects/alpha/rename", "/orgs/acme/projects/alpha/settings/retention"} {
		for _, hx := range []bool{false, true} {
			r := f.authedRequest(t, http.MethodPost, path, "name=Hijacked&days=1", member)
			if hx {
				r.Header.Set("HX-Request", "true")
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, r.WithContext(i18n.TranslatorInto(r.Context(), tr)))
			require.Equal(t, http.StatusForbidden, rec.Code, "%s hx=%v", path, hx)
			body := rec.Body.String()
			assert.NotContains(t, body, "_title", "%s hx=%v shows a raw key", path, hx)
			assert.NotContains(t, body, "_body", "%s hx=%v shows a raw key", path, hx)
			assert.Contains(t, body, tr.T("error.managers_only_title"), "%s hx=%v", path, hx)
		}
	}
}
