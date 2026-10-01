package ui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// NOTE: lit-html needs a real parsed DOM, so the render layer is browser-tested; set MCP_UI_BROWSER to a Chrome or Chromium binary or these skip.

//nolint:gochecknoglobals // a candidate path table has to be package level.
var browserCandidates = []string{
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"/Applications/Chromium.app/Contents/MacOS/Chromium",
	"/usr/bin/google-chrome",
	"/usr/bin/chromium",
	"/usr/bin/chromium-browser",
}

func findBrowser(t *testing.T) string {
	t.Helper()
	if bin := os.Getenv("MCP_UI_BROWSER"); bin != "" {
		return bin
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser"} {
		if bin, err := exec.LookPath(name); err == nil {
			return bin
		}
	}
	for _, bin := range browserCandidates {
		if _, err := os.Stat(bin); err == nil {
			return bin
		}
	}
	t.Skip("no Chrome or Chromium found; set MCP_UI_BROWSER to run the render-layer test")
	return ""
}

const driverTemplate = `
const app = document.createElement("openwa-app");
document.getElementById("root").appendChild(app);
app.view = renderTool(%TOOL%, %FIXTURE%);
app.status = "view";
await app.updateComplete;
const inner = app.renderRoot.querySelector(%TAG%);
await inner.updateComplete;
const badge = inner.renderRoot.querySelector(".app-badge");
const img = inner.renderRoot.querySelector("img");
const submit = %SUBMIT%;
let actions = 0;
document.addEventListener("openwa-action", () => { actions++; }, true);
if (submit) {
  const form = inner.renderRoot.querySelector("form");
  form.querySelector("[name=text]").value = submit.text;
  form.requestSubmit();
  await inner.updateComplete;
}
const payload = {
  outer: app.renderRoot.innerHTML,
  inner: inner.renderRoot.innerHTML,
  text: inner.renderRoot.textContent,
  images: inner.renderRoot.querySelectorAll("img").length,
  imgSrc: img ? img.getAttribute("src") : "",
  scripts: inner.renderRoot.querySelectorAll("script").length,
  buttons: inner.renderRoot.querySelectorAll("button").length,
  badgeStyle: badge ? badge.getAttribute("style") : "",
  badgeBackground: badge ? getComputedStyle(badge).backgroundColor : "",
  badgePosition: badge ? getComputedStyle(badge).position : "",
  scoped: !!inner.shadowRoot,
  actions: actions,
};
document.getElementById("out").textContent = btoa(unescape(encodeURIComponent(JSON.stringify(payload))));
`

type renderReport struct {
	Outer   string `json:"outer"`
	Inner   string `json:"inner"`
	Text    string `json:"text"`
	Images  int    `json:"images"`
	ImgSrc  string `json:"imgSrc"`
	Scripts int    `json:"scripts"`
	Buttons int    `json:"buttons"`
	Scoped  bool   `json:"scoped"`
	Actions int    `json:"actions"`
}

//nolint:gochecknoglobals // compiled once; a package-level regexp is the idiom.
var dumpedOut = regexp.MustCompile(`(?s)<pre id="out">(.*?)</pre>`)

func renderInBrowser(t *testing.T, tool, tag, fixture string) renderReport {
	t.Helper()
	return runInBrowser(t, tool, tag, fixture, "null")
}

func submitInBrowser(t *testing.T, tool, tag, fixture, text string) renderReport {
	t.Helper()
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		t.Fatalf("marshal submit: %v", err)
	}
	return runInBrowser(t, tool, tag, fixture, string(body))
}

func runInBrowser(t *testing.T, tool, tag, fixture, submit string) renderReport {
	t.Helper()
	bin := findBrowser(t)

	var doc strings.Builder
	doc.WriteString("<!doctype html><html><head><style>")
	doc.WriteString(read("app.css"))
	doc.WriteString("</style></head><body><div id=\"root\"></div><pre id=\"out\"></pre>\n")
	doc.WriteString("<script type=\"module\">")
	doc.WriteString(exposeExports(mustRead(t, litPart), "__lit"))
	doc.WriteString("</script>\n<script type=\"module\">\n")
	for _, p := range scriptParts {
		if domParts[p] {
			continue
		}
		doc.WriteString(mustRead(t, p))
		doc.WriteString("\n")
	}
	doc.WriteString(strings.NewReplacer(
		"%TOOL%", strconv.Quote(tool),
		"%TAG%", strconv.Quote(tag),
		"%SUBMIT%", submit,
		// NOTE: the fixture is embedded in a <script> block, so "</" must not close it early.
		"%FIXTURE%", strings.ReplaceAll(fixture, "</", `<\/`),
	).Replace(driverTemplate))
	doc.WriteString("</script></body></html>")

	dir := t.TempDir()
	page := filepath.Join(dir, "render.html")
	if err := os.WriteFile(page, []byte(doc.String()), 0o600); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin,
		"--headless", "--disable-gpu", "--no-sandbox",
		"--virtual-time-budget=5000", "--dump-dom", "file://"+page)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run %s: %v", bin, err)
	}
	m := dumpedOut.FindSubmatch(out)
	if m == nil {
		t.Fatalf("the harness produced no #out payload:\n%s", out)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(m[1])))
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var rep renderReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return rep
}

func TestRenderLayerScopesTheViewInAShadowRoot(t *testing.T) {
	rep := renderInBrowser(t, "project_list", "openwa-project-list", fixtureJSON(t, "project_list.json"))
	if !rep.Scoped {
		t.Error("the view rendered outside a shadow root; CSS scoping is the reason it is a LitElement")
	}
	for _, want := range []string{"Smoke", "smoke-proj", "Alpha", "0193f2a0-0000-7000-8000-0000000000aa"} {
		if !strings.Contains(rep.Text, want) {
			t.Errorf("rendered view is missing %q:\n%s", want, rep.Text)
		}
	}
	if rep.Buttons != 0 {
		t.Errorf("rendered %d buttons, want 0 — project_list is read-only", rep.Buttons)
	}
	if !strings.Contains(rep.Outer, "openwa-project-list") {
		t.Errorf("the shell did not mount the view element:\n%s", rep.Outer)
	}
}

// TestRenderLayerEscapesAToolResultInTextPosition is the browser half of the escaping guard: Lit makes a text node, it never parses markup.
func TestRenderLayerEscapesAToolResultInTextPosition(t *testing.T) {
	const hostile = `{"projects":[{"id":"a","name":"<img src=x onerror=alert(1)><script>alert(2)</` + `script>"}]}`
	rep := renderInBrowser(t, "project_list", "openwa-project-list", hostile)
	if rep.Images != 0 || rep.Scripts != 0 {
		t.Errorf("a tool result was parsed as markup: %d img, %d script:\n%s", rep.Images, rep.Scripts, rep.Inner)
	}
	if !strings.Contains(rep.Inner, "&lt;img src=x onerror=alert(1)&gt;") {
		t.Errorf("the hostile name did not render as escaped text:\n%s", rep.Inner)
	}
	if !strings.Contains(rep.Text, "<img src=x onerror=alert(1)>") {
		t.Errorf("the hostile name is not visible as text:\n%s", rep.Text)
	}
}
