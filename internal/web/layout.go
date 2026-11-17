package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"altalune.id/openwa/internal/i18n"
	"altalune.id/openwa/internal/platform/capabilities"
	"altalune.id/openwa/internal/platform/session"

	"github.com/a-h/templ"
)

// IsHTMXRequest reports whether r was issued by htmx rather than a browser navigation.
func IsHTMXRequest(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// HeaderPushURL is the htmx response header that pushes a URL onto the browser history after the swap.
const HeaderPushURL = "HX-Push-Url"

// HeaderRedirect is the htmx response header that makes the browser load a URL as a full page.
const HeaderRedirect = "HX-Redirect"

// HeaderRetarget is the htmx response header that replaces the requesting element's swap target.
const HeaderRetarget = "HX-Retarget"

// HeaderReswap is the htmx response header that replaces the requesting element's swap style.
const HeaderReswap = "HX-Reswap"

// NavScope selects which sidebars the shell renders.
type NavScope string

const (
	// NavScopeNone hides both sidebars (login, onboarding, error).
	NavScopeNone NavScope = ""
	// NavScopeOrg shows sidebar A only.
	NavScopeOrg NavScope = "org"
	// NavScopeProject shows sidebar A and sidebar B.
	NavScopeProject NavScope = "project"
	// NavScopeSettings shows sidebar A and the personal settings sidebar in sidebar B's slot.
	NavScopeSettings NavScope = "settings"
)

// ActiveNav tells the layout which sidebar item to mark selected.
type ActiveNav struct {
	Scope       NavScope
	OrgKey      string
	ProjectKey  string
	Parent      string
	SettingsKey string
}

// ActiveOrg is what the org switcher pill and menu heading show.
type ActiveOrg struct {
	ID     string
	Slug   string
	Name   string
	Role   string
	System bool
}

// ActiveProject is what the project switcher pill and sidebar heading show.
type ActiveProject struct {
	ID   string
	Slug string
	Name string
}

// LayoutData is the shape every page-level templ component receives.
type LayoutData struct {
	Title     string
	BasePath  string
	BaseURL   string
	Version   string
	BrandName string
	Caps      capabilities.Capabilities
	Principal *session.Principal
	Flash     *FlashMessage
	Content   templ.Component
	// Dialogs renders in the body-level dialog host, outside every swap target.
	Dialogs       templ.Component
	ActiveOrg     *ActiveOrg
	OtherOrgs     []ActiveOrg
	ActiveProject *ActiveProject
	OtherProjects []ActiveProject
	ActiveNav     ActiveNav
	Crumbs        []Crumb
	Locale        i18n.Locale
	Dir           string
	Translator    *i18n.Translator
	CurrentPath   string
	SupportedLocs []i18n.Locale
	// Theme is the resolved choice the picker shows checked.
	Theme ThemePref
	// ThemeAttr is the data-theme rendered on <html>; empty for System.
	ThemeAttr string
	// RequestID is echoed on every user-visible error so a report can be matched to a log line.
	RequestID string
	// SECURITY: every <script> the layout renders must carry this, or the browser refuses to run it.
	Nonce string
	// SECURITY: gates the hx-csp nonce gate, which strips every htmx attribute when it cannot read the nonce back.
	CSPEnforced bool
}

// LocaleOption is one row in the locale-selector dropdown.
type LocaleOption struct {
	Code   string
	Native string
	Active bool
}

// Tr returns the translation for key, with args as key/value pairs.
func (d LayoutData) Tr(key string, args ...any) string {
	if d.Translator == nil {
		return key
	}
	return d.Translator.T(key, args...)
}

//i18n:use dashboard.projects_count

// TrN returns the pluralized translation with Count auto-injected, with extra args as key/value pairs.
func (d LayoutData) TrN(key string, n int, args ...any) string {
	if d.Translator == nil {
		return key
	}
	return d.Translator.Tn(key, n, args...)
}

// LocaleLabel returns the native name for the active locale.
func (d LayoutData) LocaleLabel() string {
	if d.Locale == "" {
		return i18n.EnUS.NativeName()
	}
	return d.Locale.NativeName()
}

// LocaleOptions returns every supported locale ordered for the dropdown.
func (d LayoutData) LocaleOptions() []LocaleOption {
	out := make([]LocaleOption, 0, len(d.SupportedLocs))
	for _, l := range d.SupportedLocs {
		out = append(out, LocaleOption{
			Code:   string(l),
			Native: l.NativeName(),
			Active: l == d.Locale,
		})
	}
	return out
}

// FlashKind is the tone of a flash or toast.
type FlashKind string

const (
	FlashOK   FlashKind = "ok"
	FlashWarn FlashKind = "warn"
	FlashErr  FlashKind = "err"
	FlashInfo FlashKind = "info"
)

// FlashMessage is the translated notice the layout renders as a toast.
type FlashMessage struct {
	Kind    FlashKind
	Message string
}

// Crumb is one breadcrumb segment; an empty Href renders as the current page.
type Crumb struct {
	Label string
	Href  string
}

// Static returns a basePath-aware, cache-busted URL for a vendored asset (e.g. static/htmx.min.js).
func (d LayoutData) Static(sub string) string {
	return Path(d.BasePath, "static/"+sub) + "?v=" + staticFingerprint()
}

// NOTE: hashes the embedded assets, not the build version, so an edited asset busts the immutable static cache.
var staticFingerprint = sync.OnceValue(func() string { return computeStaticFingerprint(StaticFS()) }) //nolint:gochecknoglobals // memoized once, not runtime state.

func computeStaticFingerprint(fsys fs.FS) string {
	sum := sha256.New()
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rErr := fs.ReadFile(fsys, path)
		if rErr != nil {
			return rErr
		}
		_, _ = sum.Write([]byte(path))
		_, _ = sum.Write(b)
		return nil
	})
	if err != nil {
		return "dev"
	}
	return hex.EncodeToString(sum.Sum(nil))[:12]
}

// DocumentTitle composes the <title> as page, then org or project, then brand.
func (d LayoutData) DocumentTitle() string {
	parts := make([]string, 0, 3)
	if d.Title != "" {
		parts = append(parts, d.Title)
	}
	switch d.ActiveNav.Scope {
	case NavScopeProject:
		if d.ActiveProject != nil {
			parts = append(parts, d.ActiveProject.Name)
		}
	case NavScopeOrg:
		if d.ActiveOrg != nil {
			parts = append(parts, d.ActiveOrg.Name)
		}
	}
	if d.BrandName != "" {
		parts = append(parts, d.BrandName)
	}
	return strings.Join(parts, " · ")
}

// ProjectMenuActive reports whether key is the selected project nav item, directly or as the parent of a nested page.
func (d LayoutData) ProjectMenuActive(key string) bool {
	return d.ActiveNav.ProjectKey == key || d.ActiveNav.Parent == key
}

// Href joins BasePath with a subpath.
func (d LayoutData) Href(sub string) string { return Path(d.BasePath, sub) }

// OrgPath returns sub under the active org.
func (d LayoutData) OrgPath(sub string) string {
	if d.ActiveOrg == nil {
		return d.Href("/orgs")
	}
	return d.Href("/orgs/" + d.ActiveOrg.Slug + sub)
}

// ProjectPath returns sub under one of the active org's projects.
func (d LayoutData) ProjectPath(projectSlug, sub string) string {
	return d.OrgPath("/projects/" + projectSlug + sub)
}

// OIDCButtonLabel returns cfg.OIDC.ButtonLabel if set, else fallback.
func (d LayoutData) OIDCButtonLabel(fallback string) string {
	if s := strings.TrimSpace(d.Caps.OIDCButtonLabel); s != "" {
		return s
	}
	return fallback
}

// OIDCButtonLogoURL returns cfg.OIDC.ButtonLogoURL if set, else the vendored altalune-logo.png under BasePath.
func (d LayoutData) OIDCButtonLogoURL() string {
	if s := strings.TrimSpace(d.Caps.OIDCButtonLogoURL); s != "" {
		return s
	}
	return d.Static("altalune-logo.png")
}

// WithDialogs returns a copy of d with Dialogs set.
func WithDialogs(d LayoutData, c templ.Component) LayoutData {
	d.Dialogs = c
	return d
}

// WithContent returns a copy of d with Content set.
func WithContent(d LayoutData, c templ.Component) LayoutData {
	d.Content = c
	return d
}
