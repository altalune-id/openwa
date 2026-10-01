package templates

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/web"
)

func renderShell(t *testing.T, d web.LayoutData) string {
	t.Helper()
	if d.Content == nil {
		d.Content = templ.Raw("<p>body</p>")
	}
	var buf bytes.Buffer
	require.NoError(t, Layout(d).Render(context.Background(), &buf))
	return buf.String()
}

func projectShell() web.LayoutData {
	return web.LayoutData{
		Title:         "Overview",
		Nonce:         "n0nce",
		Version:       "0.1.0-test",
		BrandName:     "OpenWA",
		Principal:     &session.Principal{Email: "a@b.co"},
		ActiveOrg:     &web.ActiveOrg{ID: "o", Slug: "acme", Name: "Acme"},
		ActiveProject: &web.ActiveProject{ID: "p", Slug: "alpha", Name: "Alpha"},
		ActiveNav:     web.ActiveNav{Scope: web.NavScopeProject, ProjectKey: "overview"},
		Themes:        web.Themes(),
		ColorModes:    web.ColorModes(),
	}
}

func TestLayout_ThemeScriptsUseDefaultThemeKey(t *testing.T) {
	html := renderShell(t, projectShell())
	require.NotContains(t, html, "'slate'")
	require.Contains(t, html, `data-default-theme="`+web.DefaultThemeKey+`"`)
	require.Regexp(t, regexp.MustCompile(`data-theme-label>Tide<`), html)
}

func TestLayout_RendersToastRegionWithFlash(t *testing.T) {
	d := projectShell()
	d.Flash = &web.FlashMessage{Kind: web.FlashOK, Message: "Saved"}
	html := renderShell(t, d)
	require.Contains(t, html, `id="hx-notice" aria-live="polite"`)
	require.Contains(t, html, ">Saved<")
	require.NotRegexp(t, colourLiteral, html)
}

func TestLayout_ShipsConfirmScriptOnce(t *testing.T) {
	html := renderShell(t, projectShell())
	require.Equal(t, 1, strings.Count(html, "window.openwaConfirmBound = true"))
	require.Contains(t, html, "<noscript><style>dialog[data-confirm]", "the no-JS dialog fallback lives once in the layout head, not per dialog")
}

func TestLayout_ShellStructure(t *testing.T) {
	d := projectShell()
	d.ActiveNav.Parent = "webhooks"
	d.ActiveNav.ProjectKey = ""
	d.Crumbs = []web.Crumb{{Label: "Acme", Href: "/orgs/acme"}, {Label: "Alpha", Href: "/orgs/acme/projects/alpha/overview"}}
	html := renderShell(t, d)

	require.Contains(t, html, "<title>Overview · Alpha · OpenWA</title>")
	require.Regexp(t, `<body[^>]*>\s*<a href="#main" class="sr-only focus:not-sr-only`, html)
	require.Contains(t, html, `<main id="main" tabindex="-1"`)
	require.Contains(t, html, `<button type="button" id="drawer-toggle" aria-controls="drawer" aria-expanded="false"`)
	require.Contains(t, html, `<aside id="drawer" aria-label="common.menu" class="hidden fixed`, "the drawer is closed by the hidden class; a hidden attribute loses to .flex")
	require.NotContains(t, html, `id="drawer" hidden`)
	require.Contains(t, html, `data-drawer-close`)
	require.Contains(t, html, `openwa-mark.svg`)
	require.Contains(t, html, `>OpenWA<`)
	require.Contains(t, html, "nav.group_project")
	require.Contains(t, html, "nav.group_developer")
	require.Contains(t, html, `aria-label="nav.breadcrumb"`)
	require.Contains(t, html, "OpenWA · 0.1.0-test")
	require.Equal(t, 1, strings.Count(html, "0.1.0-test"), "the version shows once, in the user menu")
	require.Equal(t, 2, strings.Count(html, "bg-primary px-3 py-2 text-sm font-medium text-primary-foreground"), "webhooks lights up once per menu copy (desktop + drawer) and nothing else does")
	require.NotContains(t, html, "text-muted-foreground/60")
	require.NotContains(t, html, "text-muted-foreground/50")
	require.NotContains(t, html, `for="alt-drawer"`)
}

func TestLayout_OrgProjectsItemDoesNotLightUpOnProjectPages(t *testing.T) {
	html := renderShell(t, projectShell())
	require.Regexp(t, `<a href="/orgs/acme/projects" class="flex items-center gap-2 min-h-\[40px\] rounded-md px-3 py-2 text-sm text-muted-foreground`, html)
}

func TestLayout_OrgScopeShowsAPIKeysLink(t *testing.T) {
	d := projectShell()
	d.ActiveProject = nil
	d.ActiveNav = web.ActiveNav{Scope: web.NavScopeOrg, OrgKey: "apikeys"}
	require.Contains(t, renderShell(t, d), `href="/orgs/acme/apikeys"`)
}

func TestLayout_SettingsScopeShowsPersonalTokensInSecondarySidebar(t *testing.T) {
	d := projectShell()
	d.ActiveProject = nil
	d.ActiveNav = web.ActiveNav{Scope: web.NavScopeSettings, SettingsKey: "tokens"}
	html := renderShell(t, d)
	require.Contains(t, html, `aria-label="nav.settings"`)
	require.Contains(t, html, `href="/settings/tokens"`)
}

func TestLayout_UserMenuLinksToSettings(t *testing.T) {
	require.Contains(t, renderShell(t, projectShell()), `href="/settings/tokens"`)
}

var (
	tagRe       = regexp.MustCompile(`<[a-zA-Z][^>]*>`)
	scriptTagRe = regexp.MustCompile(`<script\b[^>]*>`)
	buttonRe    = regexp.MustCompile(`(?s)<button\b([^>]*)>(.*?)</button>`)
	textRe      = regexp.MustCompile(`>[^<\s][^<]*<`)
)

func richShell(t *testing.T) string {
	t.Helper()
	d := projectShell()
	d.Crumbs = []web.Crumb{{Label: "Acme", Href: "/orgs/acme"}, {Label: "Alpha"}}
	d.Flash = &web.FlashMessage{Kind: web.FlashOK, Message: "Saved"}
	now := time.Now()
	page := APIKeysPage(d, APIKeysView{
		Base:      "/orgs/acme/projects/alpha/apikeys",
		CanManage: true,
		Items:     []APIKeyRow{{ID: "k1", Base: "/orgs/acme/projects/alpha/apikeys", Name: "ci", CreatedAt: now}},
		Scopes:    []APIKeyScopeOption{{Value: "projects:read", LabelKey: "apikey.scope.projects_read"}},
		Expiries:  []APIKeyExpiryOption{{Days: "30", LabelKey: "apikey.expiry.30"}},
	})
	return renderShell(t, web.WithContent(d, page))
}

func TestA11y_EveryScriptCarriesANonce(t *testing.T) {
	html := richShell(t)
	scripts := scriptTagRe.FindAllString(html, -1)
	require.NotEmpty(t, scripts)
	for _, s := range scripts {
		require.Contains(t, s, `nonce="n0nce"`, s)
	}
}

func TestA11y_EveryHTMXElementCarriesHxNonce(t *testing.T) {
	html := richShell(t)
	checked := 0
	for _, tag := range tagRe.FindAllString(html, -1) {
		if !strings.Contains(strings.ReplaceAll(tag, "hx-nonce", ""), " hx-") {
			continue
		}
		checked++
		require.Contains(t, tag, `hx-nonce="n0nce"`, tag)
	}
	require.Positive(t, checked, "the page under test must carry htmx elements")
}

func TestA11y_LandmarksSkipLinkAndDrawer(t *testing.T) {
	html := richShell(t)
	require.Regexp(t, `<body[^>]*>\s*<a href="#main" class="sr-only focus:not-sr-only`, html)
	require.Contains(t, html, `<main id="main" tabindex="-1"`)
	require.Contains(t, html, "<header")
	require.Regexp(t, `<nav aria-label="[^"]+"`, html)
	require.Regexp(t, `<aside aria-label="[^"]+"`, html)
	require.Contains(t, html, `id="drawer-toggle" aria-controls="drawer" aria-expanded="false"`)
	require.Contains(t, html, "e.key === 'Escape' && isOpen()")
	require.Contains(t, html, `id="hx-error" class="mb-4 empty:hidden empty:mb-0" aria-live="polite"`)
	require.Contains(t, html, `id="hx-notice" aria-live="polite"`)
	require.Regexp(t, `<dialog id="[^"]+" aria-labelledby="[^"]+-title"`, html)
	require.NotContains(t, html, "'slate'")
	require.Contains(t, html, `data-default-theme="tide"`)
}

func TestA11y_IconOnlyButtonsAreLabelled(t *testing.T) {
	html := richShell(t)
	for _, m := range buttonRe.FindAllStringSubmatch(html, -1) {
		attrs, inner := m[1], m[2]
		if textRe.MatchString(">" + stripTags(inner) + "<") {
			continue
		}
		require.Contains(t, attrs, "aria-label=", "icon-only button without a label: <button%s>", attrs)
	}
}

func stripTags(s string) string {
	return strings.TrimSpace(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(s, ""))
}
