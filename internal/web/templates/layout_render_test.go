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
		Theme:         web.ParseThemePref("", ""),
	}
}

func TestLayout_SystemModeRendersNoDataThemeAndBothMetas(t *testing.T) {
	html := renderShell(t, projectShell())
	require.NotContains(t, html, "data-theme=")
	require.Equal(t, 2, strings.Count(html, `<meta name="theme-color" media="(prefers-color-scheme:`))
	require.NotContains(t, html, "localStorage")
}

func TestLayout_ChosenModeRendersDataTheme(t *testing.T) {
	d := projectShell()
	d.Theme = web.ParseThemePref("openwa", web.ModeDark)
	d.ThemeAttr = d.Theme.Attr()
	html := renderShell(t, d)
	require.Contains(t, html, `data-theme="openwa-dark"`)
	require.Equal(t, 1, strings.Count(html, `<meta name="theme-color"`))
}

func TestLayout_RendersToastHostWithFlash(t *testing.T) {
	d := projectShell()
	d.Flash = &web.FlashMessage{Kind: web.FlashOK, Message: "Saved"}
	html := renderShell(t, d)
	require.Contains(t, html, `id="hx-notice" aria-live="polite" class="toast`)
	require.Contains(t, html, "alert-success")
	require.Contains(t, html, ">Saved<")
	require.NotRegexp(t, colourLiteral, html)
}

func TestLayout_LoadsOwnScriptsOnce(t *testing.T) {
	html := renderShell(t, projectShell())
	for _, js := range []string{"dialog.js", "dropdown.js", "tips.js", "copy.js", "localtime.js", "pairing.js"} {
		require.Equal(t, 1, strings.Count(html, `src="/static/`+js), js)
	}
	require.NotContains(t, html, "basecoat")
}

func TestLayout_ShellStructure(t *testing.T) {
	d := projectShell()
	d.ActiveNav.Parent = "webhooks"
	d.ActiveNav.ProjectKey = ""
	d.Crumbs = []web.Crumb{{Label: "Acme", Href: "/orgs/acme"}, {Label: "Alpha", Href: "/orgs/acme/projects/alpha/overview"}}
	html := renderShell(t, d)

	require.Contains(t, html, "<title>Overview · Alpha · OpenWA</title>")
	require.Regexp(t, `<body[^>]*>\s*<a href="#main" class="btn btn-sm sr-only focus:not-sr-only`, html)
	require.Contains(t, html, `<div class="drawer lg:drawer-open">`)
	require.Contains(t, html, `<input id="alt-drawer" type="checkbox" class="drawer-toggle"`)
	require.Contains(t, html, `<main id="main" tabindex="-1"`)
	require.Contains(t, html, `class="drawer-overlay"`)
	require.Contains(t, html, `data-drawer-toggle`)
	require.Contains(t, html, `/static/openwa-mark-16.svg`)
	require.Contains(t, html, `>OpenWA<`)
	require.Contains(t, html, `aria-label="nav.breadcrumb"`)
	require.Contains(t, html, "OpenWA 0.1.0-test")
	require.Equal(t, 1, strings.Count(html, "0.1.0-test"), "the version shows once, in the user menu")
	require.Len(t, regexp.MustCompile(`<a href="[^"]*" class="menu-active"`).FindAllString(html, -1), 1, "webhooks lights up once and nothing else does")
	require.Contains(t, html, `<a href="/orgs/acme/projects/alpha/webhooks" class="menu-active" aria-current="page">`)
	require.Regexp(t, `<details open>\s*<summary>`, html, "Developers opens when one of its pages is active")
	require.NotContains(t, html, "style=")
}

func TestLayout_ProjectNavGroups(t *testing.T) {
	html := renderShell(t, projectShell())
	order := []string{
		`href="/orgs/acme/projects/alpha/overview"`,
		"nav.group_messages",
		`href="/orgs/acme/projects/alpha/devices"`,
		`href="/orgs/acme/projects/alpha/inbox"`,
		`href="/orgs/acme/projects/alpha/contacts"`,
		"nav.group_developers",
		`href="/orgs/acme/projects/alpha/webhooks"`,
		`href="/orgs/acme/projects/alpha/apikeys"`,
		`href="/orgs/acme/projects/alpha/usage"`,
		`href="/orgs/acme"`,
		`href="/orgs/acme/projects/alpha/settings"`,
	}
	at := 0
	for _, want := range order {
		i := strings.Index(html[at:], want)
		require.GreaterOrEqual(t, i, 0, "%s missing or out of order", want)
		at += i
	}
	require.Regexp(t, `<details>\s*<summary>`, html, "Developers starts collapsed")
}

func TestLayout_SwitcherRailAndPhoneSheet(t *testing.T) {
	d := projectShell()
	d.OtherProjects = []web.ActiveProject{{Slug: "beta", Name: "Beta"}}
	d.OtherOrgs = []web.ActiveOrg{{Slug: "sys", Name: "System", System: true}}
	html := renderShell(t, d)
	require.Contains(t, html, `data-org-switcher="rail"`)
	require.Contains(t, html, `<details class="dropdown w-full">`)
	require.Contains(t, html, `data-dialog-open="switcher"`)
	require.Regexp(t, `<div id="dialog-host" hx-morph-skip hx-nonce="n0nce">\s*<dialog id="switcher" class="modal modal-bottom sm:modal-middle"`, html)
	require.Contains(t, html, `href="/orgs/acme/projects/beta/overview"`)
	require.Contains(t, html, `href="/orgs/sys/switch"`)
	require.Contains(t, html, "common.system")
}

func TestLayout_DialogsRenderInTheHostOutsideMain(t *testing.T) {
	d := web.WithDialogs(projectShell(), templ.Raw(`<dialog id="x-dlg"></dialog>`))
	html := renderShell(t, d)
	host := strings.Index(html, `id="dialog-host"`)
	main := strings.Index(html, `</main>`)
	require.Greater(t, host, main)
	require.Greater(t, strings.Index(html, `id="x-dlg"`), host)
}

func TestLayout_NoLinkToTheRetiredProjectsPage(t *testing.T) {
	d := projectShell()
	d.ActiveNav = web.ActiveNav{Scope: web.NavScopeOrg}
	d.ActiveProject = nil
	d.ActiveOrg.CanManage = true
	d.OtherOrgs = []web.ActiveOrg{{Slug: "beta", Name: "Beta"}}
	for name, html := range map[string]string{
		"shell":       renderShell(t, d),
		"orgs":        render(t, OrgsPage(d, OrgsView{Orgs: []OrgSummary{{Slug: "acme", Name: "Acme"}}})),
		"project_new": render(t, ProjectNewPage(d, ProjectNewView{OrgSlug: "acme"})),
	} {
		require.NotRegexp(t, `href="/orgs/[a-z]+/projects"`, html, name)
	}
}

func TestLayout_OrgScopeShowsAPIKeysLink(t *testing.T) {
	d := projectShell()
	d.ActiveProject = nil
	d.ActiveNav = web.ActiveNav{Scope: web.NavScopeOrg, OrgKey: "apikeys"}
	require.Contains(t, renderShell(t, d), `href="/orgs/acme/apikeys"`)
}

func TestLayout_SettingsScopeShowsPersonalTokens(t *testing.T) {
	d := projectShell()
	d.ActiveProject = nil
	d.ActiveNav = web.ActiveNav{Scope: web.NavScopeSettings, SettingsKey: "tokens"}
	html := renderShell(t, d)
	require.Contains(t, html, "nav.personal_tokens")
	require.Contains(t, html, `href="/settings/tokens"`)
}

func TestLayout_UserMenuLinksToSettings(t *testing.T) {
	require.Contains(t, renderShell(t, projectShell()), `href="/settings/tokens"`)
}

func TestLayout_SignedOutShowsThemePickerAndNoDrawer(t *testing.T) {
	d := web.LayoutData{Nonce: "n0nce", BrandName: "OpenWA", Theme: web.ParseThemePref("", "")}
	html := renderShell(t, d)
	require.NotContains(t, html, "drawer")
	require.Contains(t, html, `action="/theme"`)
	require.Contains(t, html, `name="mode" value="system" class="radio radio-sm radio-primary" checked`)
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
	require.Regexp(t, `<body[^>]*>\s*<a href="#main" class="btn btn-sm sr-only focus:not-sr-only`, html)
	require.Contains(t, html, `<main id="main" tabindex="-1"`)
	require.Contains(t, html, "<header")
	require.Regexp(t, `<nav aria-label="[^"]+"`, html)
	require.Contains(t, html, `aria-label="nav.open_menu"`)
	require.Contains(t, html, `id="hx-error" class="mb-4 empty:hidden empty:mb-0" aria-live="polite"`)
	require.Contains(t, html, `id="hx-notice" aria-live="polite"`)
	require.Regexp(t, `<dialog id="[^"]+" class="modal[^"]*" aria-labelledby="[^"]+-title"`, html)
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

func TestSwitcherTrigger_OrgPageNamesTheOrgOnceAndPromptsForAProject(t *testing.T) {
	d := web.LayoutData{ActiveOrg: &web.ActiveOrg{Slug: "toko", Name: "Toko Sari"}}
	for name, c := range map[string]templ.Component{"rail": switcherRailTrigger(d), "phone": switcherPhoneTrigger(d)} {
		var buf bytes.Buffer
		require.NoError(t, c.Render(context.Background(), &buf))
		text := stripTags(buf.String())
		require.Equal(t, 1, strings.Count(text, "Toko Sari"), "%s trigger: org name once: %q", name, text)
		require.Contains(t, buf.String(), `text-base-content/70">`+d.Tr("common.select_project")+`</span>`, "%s trigger: muted prompt", name)
	}
	require.Equal(t, d.Tr("switcher.label", "Org", "Toko Sari", "Project", d.Tr("common.select_project")), switcherLabel(d))
}
