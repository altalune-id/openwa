package templates

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/web"
)

func render(t *testing.T, c templ.Component) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, c.Render(context.Background(), &buf))
	return buf.String()
}

func renderWith(t *testing.T, c, child templ.Component) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, c.Render(templ.WithChildren(context.Background(), child), &buf))
	return buf.String()
}

func uiData() web.LayoutData {
	return web.LayoutData{Nonce: "n0nce", BasePath: ""}
}

var colourLiteral = regexp.MustCompile(`\b(bg|text|border|ring|from|to|via|fill|stroke|divide|outline|decoration|placeholder|accent|caret)-(red|green|amber|emerald|yellow|rose|orange)-[0-9]`)

func TestButton_KindsAndIconLabel(t *testing.T) {
	html := render(t, Button(ButtonProps{Kind: ButtonPrimary, Label: "Save"}))
	assert.Contains(t, html, `<button type="button"`)
	assert.Contains(t, html, "bg-primary")
	assert.Contains(t, html, ">Save<")

	html = render(t, Button(ButtonProps{Kind: ButtonSecondary, Label: "Open", Href: "/x"}))
	assert.Contains(t, html, `<a href="/x"`)

	html = render(t, Button(ButtonProps{Kind: ButtonDanger, Type: "submit", Label: "Delete"}))
	assert.Contains(t, html, `type="submit"`)
	assert.Contains(t, html, "bg-destructive")
	assert.NotRegexp(t, colourLiteral, html)

	html = render(t, Button(ButtonProps{Kind: ButtonIcon, Icon: "trash-2", Attrs: templ.Attributes{"aria-label": "Remove"}}))
	assert.Contains(t, html, `aria-label="Remove"`)
	assert.Contains(t, html, "<svg")

	var buf bytes.Buffer
	err := Button(ButtonProps{Kind: ButtonIcon, Icon: "trash-2"}).Render(context.Background(), &buf)
	require.Error(t, err)
	assert.True(t, IsIconButtonUnlabelledError(err))
}

func TestField_WiresDescribedByAndInvalid(t *testing.T) {
	d := uiData()
	p := FieldProps{ID: "f", Label: "Name", Help: "help", Error: "bad"}
	attrs := InputAttrs(p)
	assert.Equal(t, "f", attrs["id"])
	assert.Equal(t, "f-help f-error", attrs["aria-describedby"])
	assert.Equal(t, "true", attrs["aria-invalid"])

	html := renderWith(t, Field(d, p), templ.Raw(`<input/>`))
	assert.Contains(t, html, `<label for="f"`)
	assert.Contains(t, html, `id="f-help"`)
	assert.Contains(t, html, `<p role="alert" id="f-error" class="mt-1 flex items-center gap-1.5 text-xs text-foreground">`)
	assert.Contains(t, html, "bg-destructive", "the status colour sits on the marker, the text stays foreground")

	attrs = InputAttrs(FieldProps{ID: "g"})
	_, hasDescribed := attrs["aria-describedby"]
	assert.False(t, hasDescribed)
	_, hasInvalid := attrs["aria-invalid"]
	assert.False(t, hasInvalid)
}

func TestBadgeAndStatusDot_UseThemeTokensOnly(t *testing.T) {
	for _, tone := range []Tone{ToneNeutral, TonePrimary, ToneSuccess, ToneWarning, ToneDanger, ToneInfo} {
		html := render(t, Badge(tone, "x"))
		assert.NotRegexp(t, colourLiteral, html)
		dot := render(t, StatusDot(tone, "connected"))
		assert.Contains(t, dot, `aria-label="connected"`)
		assert.NotRegexp(t, colourLiteral, dot)
	}
	assert.Contains(t, render(t, Badge(ToneSuccess, "ok")), "text-success")
	assert.Contains(t, render(t, StatusDot(ToneDanger, "down")), "bg-destructive")
}

func TestListShellAndRow_AddNonceOnlyWithHTMXAttrs(t *testing.T) {
	d := uiData()
	plain := renderWith(t, ListShell(d, "list", nil), templ.Raw(""))
	assert.Contains(t, plain, `id="list"`)
	assert.NotContains(t, plain, "hx-nonce")

	polled := renderWith(t, ListShell(d, "inbox", templ.Attributes{"hx-get": "/x", "hx-trigger": "every 5s", "hx-swap": "innerMorph"}), templ.Raw(""))
	assert.Contains(t, polled, `hx-get="/x"`)
	assert.Contains(t, polled, `hx-nonce="n0nce"`)

	row := renderWith(t, ListRow(d, templ.Attributes{"hx-get": "/row"}), templ.Raw("cell"))
	assert.Contains(t, row, `<li`)
	assert.Contains(t, row, `hx-nonce="n0nce"`)
}

func TestPoll_DropsTriggerWhenDone(t *testing.T) {
	d := uiData()
	live := renderWith(t, Poll(d, PollProps{ID: "qr", URL: "/qr", Every: "5s"}), templ.Raw("img"))
	assert.Contains(t, live, `hx-get="/qr"`)
	assert.Contains(t, live, `hx-trigger="every 5s"`)
	assert.Contains(t, live, `hx-status:4xx="target:#hx-error swap:innerHTML"`)
	assert.Contains(t, live, `hx-status:5xx="swap:none"`)
	assert.Contains(t, live, `hx-nonce="n0nce"`)

	done := renderWith(t, Poll(d, PollProps{ID: "qr", URL: "/qr", Every: "5s", Done: true}), templ.Raw("img"))
	assert.Contains(t, done, `id="qr"`)
	assert.NotContains(t, done, "hx-get")
	assert.NotContains(t, done, "hx-trigger")
}

func TestToasts_RegionAndOOB(t *testing.T) {
	d := uiData()
	d.Flash = &web.FlashMessage{Kind: web.FlashOK, Message: "Saved"}
	region := render(t, ToastRegion(d))
	assert.Contains(t, region, `id="hx-notice"`)
	assert.Contains(t, region, `aria-live="polite"`)
	assert.Contains(t, region, ">Saved<")
	assert.Contains(t, region, `<script nonce="n0nce">`)
	assert.Contains(t, region, "MutationObserver")

	oob := render(t, OOBToast(d, web.FlashErr, "Nope"))
	assert.Contains(t, oob, `<template hx type="partial" hx-target="#hx-notice" hx-swap="beforeend" hx-nonce="n0nce">`)
	assert.Contains(t, oob, "text-destructive")
	assert.Contains(t, oob, ">Nope<")
}

func TestCopyButton_CarriesLabelAndNoncedScript(t *testing.T) {
	d := uiData()
	html := render(t, CopyButton(d, "secret", "Copy secret"))
	assert.Contains(t, html, `data-copy="secret"`)
	assert.Contains(t, html, `aria-label="Copy secret"`)
	script := render(t, copyScript("n0nce"))
	assert.Contains(t, script, `<script nonce="n0nce">`)
	assert.Contains(t, script, "window.openwaCopyBound")
}

func TestTabs_ARIAPattern(t *testing.T) {
	d := uiData()
	html := render(t, Tabs(d, TabsProps{ID: "webhook-verify", Label: "Languages", Tabs: []Tab{
		{Key: "go", Label: "Go", Panel: templ.Raw("<pre>go</pre>"), Attrs: templ.Attributes{"data-snippet": "go"}},
		{Key: "node", Label: "Node", Panel: templ.Raw("<pre>js</pre>")},
	}}))
	assert.Contains(t, html, `<nav role="tablist" aria-orientation="horizontal" aria-label="Languages" class="tab-bar">`)
	assert.Contains(t, html, `<button type="button" role="tab" id="webhook-verify-tab-go" aria-controls="webhook-verify-panel-go" aria-selected="true" tabindex="0">Go</button>`)
	assert.Contains(t, html, `<button type="button" role="tab" id="webhook-verify-tab-node" aria-controls="webhook-verify-panel-node" aria-selected="false" tabindex="-1">Node</button>`)
	assert.Contains(t, html, `<div role="tabpanel" id="webhook-verify-panel-go" aria-labelledby="webhook-verify-tab-go" data-snippet="go" class="px-3 py-2">`)
	assert.Contains(t, html, `<div role="tabpanel" id="webhook-verify-panel-node" aria-labelledby="webhook-verify-tab-node" hidden class="px-3 py-2">`)
	assert.Contains(t, html, "(function() {\n\tif (window.openwaTabsBound) return;")
}

func TestEmptyStateAndPageHeader(t *testing.T) {
	d := uiData()
	html := render(t, EmptyState(d, EmptyStateProps{Icon: "folder", Title: "None", Body: "Add one", CTA: &ButtonProps{Kind: ButtonPrimary, Label: "New", Href: "/new"}}))
	assert.Contains(t, html, "border-dashed")
	assert.Contains(t, html, ">None<")
	assert.Contains(t, html, `<a href="/new"`)

	header := render(t, PageHeader(d, PageHeaderProps{Title: "Devices", Eyebrow: "Messaging", Subtitle: "Linked phones"}))
	assert.Contains(t, header, "<header")
	assert.Contains(t, header, `<h1 class="text-2xl font-semibold tracking-tight text-foreground">Devices</h1>`)
	assert.Contains(t, header, ">Messaging<")
	assert.Contains(t, header, "pb-4 mb-6")
}

func TestChipCardStatAvatarSpinnerDisclosure(t *testing.T) {
	assert.Contains(t, render(t, Chip(ChipProps{Name: "scopes", Value: "a", Label: "A", Checked: true})), `class="chip`)
	assert.Contains(t, render(t, Chip(ChipProps{Name: "scopes", Value: "a", Label: "A", Checked: true})), " checked")
	card := renderWith(t, Card(CardProps{Title: "T", Description: "D"}), templ.Raw("<p>c</p>"))
	assert.Contains(t, card, `class="surface p-5"`)
	assert.Contains(t, card, `<h2 class="text-base font-semibold text-foreground">T</h2>`)
	stat := render(t, StatCard("Devices", "3"))
	assert.Contains(t, stat, ">Devices<")
	assert.Contains(t, stat, ">3<")
	assert.Contains(t, render(t, Avatar("hirzi@example.com", "h-6 w-6 text-[11px]")), ">H<")
	assert.Contains(t, render(t, Avatar("", "h-6 w-6")), ">?<")
	spin := render(t, Spinner("Loading"))
	assert.Contains(t, spin, `role="status"`)
	assert.Contains(t, spin, "htmx-indicator")
	assert.Contains(t, spin, `<span class="sr-only">Loading</span>`)
	disc := renderWith(t, Disclosure("More"), templ.Raw("<p>x</p>"))
	assert.Contains(t, disc, "<details")
	assert.Contains(t, disc, "<summary")
}

func TestBreadcrumb_RendersCrumbsWithSeparators(t *testing.T) {
	d := uiData()
	d.Crumbs = []web.Crumb{{Label: "Acme", Href: "/orgs/acme"}, {Label: "Alpha"}}
	html := render(t, Breadcrumb(d))
	assert.Contains(t, html, `<nav aria-label="nav.breadcrumb"`)
	assert.Contains(t, html, `<a href="/orgs/acme"`)
	assert.Contains(t, html, `aria-current="page"`)
	assert.Contains(t, html, `aria-hidden="true"`)
	assert.Empty(t, render(t, Breadcrumb(uiData())))

	d.Crumbs = []web.Crumb{{Label: "Acme", Href: "/orgs/acme"}, {Label: "Alpha", Href: "/orgs/acme/projects/alpha/overview"}}
	html = render(t, Breadcrumb(d))
	assert.Contains(t, html, `<a href="/orgs/acme/projects/alpha/overview" aria-current="page"`, "the last crumb is the current page even when it links")
}

func TestTime_SetsDatetimeAndTitle(t *testing.T) {
	d := uiData()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	html := render(t, Time(d, at))
	assert.Contains(t, html, `<time datetime="2026-01-02T03:04:05Z" title="2 Jan 2026 03:04 UTC">`)
	assert.Contains(t, html, "time.on_date")
}

func TestConfirmDialog_StructureAndFormOrder(t *testing.T) {
	d := uiData()
	html := render(t, ConfirmDialog(d, ConfirmProps{
		ID:           "revoke-k1",
		Title:        "Revoke key?",
		Body:         "Clients using it stop working.",
		ConfirmLabel: "Revoke",
		Kind:         ButtonDanger,
		Action:       "/orgs/acme/projects/alpha/apikeys/k1/revoke",
		HxTarget:     "#apikey-list",
		HxSwap:       "outerHTML",
		Hidden:       []HiddenField{{Name: "a", Value: "1"}, {Name: "b", Value: "2"}},
		Trigger:      ButtonProps{Kind: ButtonIcon, Icon: "trash-2", Attrs: templ.Attributes{"aria-label": "Revoke: k1"}},
	}))
	assert.Contains(t, html, `data-confirm-open="revoke-k1"`)
	assert.Contains(t, html, `aria-haspopup="dialog"`)
	assert.Contains(t, html, `<dialog id="revoke-k1" aria-labelledby="revoke-k1-title" data-confirm`)
	assert.Contains(t, html, `<form method="post" action="/orgs/acme/projects/alpha/apikeys/k1/revoke"`)
	assert.Contains(t, html, `hx-post="/orgs/acme/projects/alpha/apikeys/k1/revoke"`)
	assert.Contains(t, html, `hx-target="#apikey-list"`)
	assert.Contains(t, html, `hx-swap="outerHTML"`)
	assert.Contains(t, html, `hx-status:4xx="target:#hx-error swap:innerHTML"`)
	assert.Contains(t, html, `hx-status:5xx="target:#hx-error swap:innerHTML"`)
	assert.Contains(t, html, `hx-disable="find button"`)
	assert.Contains(t, html, `hx-nonce="n0nce"`)
	a := strings.Index(html, `name="a" value="1"`)
	b := strings.Index(html, `name="b" value="2"`)
	require.Greater(t, a, 0)
	require.Greater(t, b, a, "hidden fields keep their order")
	assert.Contains(t, html, `id="revoke-k1-title"`)
	assert.Contains(t, html, "bg-destructive")
	assert.Contains(t, html, "<noscript><style>")

	plain := render(t, ConfirmDialog(d, ConfirmProps{ID: "rm", Title: "t", ConfirmLabel: "Remove", Kind: ButtonDanger, Action: "/x", Trigger: ButtonProps{Kind: ButtonSecondary, Label: "Remove"}}))
	assert.NotContains(t, plain, "hx-post", "a full-page confirm carries no htmx attributes")
	assert.NotContains(t, plain, "hx-nonce")

	script := render(t, confirmScript("n0nce"))
	assert.Contains(t, script, `<script nonce="n0nce">`)
	assert.Contains(t, script, "showModal()")
	assert.Contains(t, script, "htmx:finally:request", "the dialog must close on success, an error status and a network failure alike")
}

func TestConfirmDialog_FallsBackToCommonConfirm(t *testing.T) {
	html := render(t, ConfirmDialog(uiData(), ConfirmProps{ID: "x", Title: "t", Action: "/x", Trigger: ButtonProps{Label: "Go"}}))
	assert.Contains(t, html, ">common.confirm<")
	assert.Contains(t, render(t, CopyButton(uiData(), "v", "Copy")), `data-copied-label="common.copy_done"`)
}
