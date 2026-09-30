package web_test

import (
	"testing"

	"altalune.id/openwa/internal/i18n"
	"altalune.id/openwa/internal/web"
)

func TestLayoutData_LocaleLabel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		loc  i18n.Locale
		want string
	}{
		{i18n.EnUS, "English"},
		{i18n.IdID, "Bahasa Indonesia"},
		{i18n.MsMY, "Bahasa Melayu"},
		{i18n.JaJP, "日本語"},
		{i18n.ArSA, "العربية"},
		{"", "English"},
	}
	for _, tc := range tests {
		t.Run(string(tc.loc), func(t *testing.T) {
			d := web.LayoutData{Locale: tc.loc}
			if got := d.LocaleLabel(); got != tc.want {
				t.Errorf("LocaleLabel=%q want %q", got, tc.want)
			}
		})
	}
}

func TestLayoutData_LocaleOptions(t *testing.T) {
	t.Parallel()
	d := web.LayoutData{
		Locale:        i18n.IdID,
		SupportedLocs: []i18n.Locale{i18n.EnUS, i18n.IdID, i18n.MsMY, i18n.JaJP, i18n.ArSA},
	}
	opts := d.LocaleOptions()
	if len(opts) != 5 {
		t.Fatalf("options=%d want 5", len(opts))
	}
	var activeCount int
	for _, o := range opts {
		if o.Active {
			activeCount++
			if o.Code != "id-ID" {
				t.Errorf("active locale=%q want id-ID", o.Code)
			}
		}
		if o.Native == "" {
			t.Errorf("native name empty for %q", o.Code)
		}
	}
	if activeCount != 1 {
		t.Errorf("active count=%d want 1", activeCount)
	}
}

func TestLocale_NativeName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		loc  i18n.Locale
		want string
	}{
		{i18n.EnUS, "English"},
		{i18n.IdID, "Bahasa Indonesia"},
		{i18n.MsMY, "Bahasa Melayu"},
		{i18n.JaJP, "日本語"},
		{i18n.ArSA, "العربية"},
		{"de-DE", "de-DE"},
	}
	for _, tc := range tests {
		t.Run(string(tc.loc), func(t *testing.T) {
			if got := tc.loc.NativeName(); got != tc.want {
				t.Errorf("NativeName(%q)=%q want %q", tc.loc, got, tc.want)
			}
		})
	}
}

func TestLayoutData_TrAndTrN(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	d := web.LayoutData{Locale: i18n.IdID, Translator: b.For(i18n.IdID)}
	if got := d.Tr("nav.overview"); got != "Ringkasan" {
		t.Errorf("Tr=%q", got)
	}
	if got := d.TrN("dashboard.projects_count", 3); got != "3 proyek" {
		t.Errorf("TrN=%q", got)
	}
}

func TestLayoutData_TrNilTranslator(t *testing.T) {
	t.Parallel()
	d := web.LayoutData{}
	if got := d.Tr("k"); got != "k" {
		t.Errorf("Tr nil=%q", got)
	}
	if got := d.TrN("k", 2); got != "k" {
		t.Errorf("TrN nil=%q", got)
	}
}

func TestLayoutData_DocumentTitle(t *testing.T) {
	t.Parallel()
	base := web.LayoutData{Title: "API keys", BrandName: "OpenWA"}
	if got := base.DocumentTitle(); got != "API keys · OpenWA" {
		t.Errorf("chromeless=%q", got)
	}
	proj := base
	proj.ActiveNav = web.ActiveNav{Scope: web.NavScopeProject}
	proj.ActiveProject = &web.ActiveProject{Name: "Alpha"}
	if got := proj.DocumentTitle(); got != "API keys · Alpha · OpenWA" {
		t.Errorf("project=%q", got)
	}
	org := base
	org.ActiveNav = web.ActiveNav{Scope: web.NavScopeOrg}
	org.ActiveOrg = &web.ActiveOrg{Name: "Acme"}
	if got := org.DocumentTitle(); got != "API keys · Acme · OpenWA" {
		t.Errorf("org=%q", got)
	}
	settings := base
	settings.ActiveNav = web.ActiveNav{Scope: web.NavScopeSettings}
	settings.ActiveOrg = &web.ActiveOrg{Name: "Acme"}
	if got := settings.DocumentTitle(); got != "API keys · OpenWA" {
		t.Errorf("settings=%q", got)
	}
	if got := (web.LayoutData{BrandName: "OpenWA"}).DocumentTitle(); got != "OpenWA" {
		t.Errorf("empty=%q", got)
	}
}

func TestLayoutData_StaticCarriesAssetVersion(t *testing.T) {
	t.Parallel()
	d := web.LayoutData{BasePath: "/app", AssetVersion: "1.2.3"}
	if got := d.Static("app.css"); got != "/app/static/app.css?v=1.2.3" {
		t.Errorf("Static=%q", got)
	}
	if got := (web.LayoutData{}).Static("app.css"); got != "/static/app.css" {
		t.Errorf("unversioned=%q", got)
	}
}
