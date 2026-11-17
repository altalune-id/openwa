package web

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var fontURL = regexp.MustCompile(`url\("\./([a-z0-9-]+\.woff2)"\)`)

// TestVendorMatchesPinnedDigest fails on any embedded vendored byte scripts/ui-vendor.sh did not verify.
func TestVendorMatchesPinnedDigest(t *testing.T) {
	pinned := map[string]string{
		"static/easymde.min.css":                               "6eee36340432776d682e7372ec4a7eb29be4fdb3ffada32c0bfa5e31a5ef34a2",
		"static/easymde.min.js":                                "2c06bddfd0c89176db08ccf9d42e2beaa9b4f1a4ff8fb2ec0bf1bed25ce08e05",
		"static/htmx.min.js":                                   "e484d9171a9db30a39c8f16e3d709d4137f3211c659f8e6125816635033d593f",
		"static/hx-csp.min.js":                                 "279f659b9ec8658e3d47230cc545b0bb4e3efa4c2d780454d54db104a5257b7d",
		"static/plus-jakarta-sans-latin-wght-normal.woff2":     "153fc85b70298beeb1d61a5f723331649e7f23bb77302a66e61cb3e2fbdb5e79",
		"static/plus-jakarta-sans-latin-ext-wght-normal.woff2": "38e3b8fd8045048eb311d90170a4429ed2c8f405852dc3d91b5af8452758703f",
		"static/jetbrains-mono-latin-400-normal.woff2":         "14425ba9c695763c1547f48a206b7aa60350a33ae23de09f0407877f3fcd89eb",
		"static/jetbrains-mono-latin-500-normal.woff2":         "cb182feeed4d798ff6961d3c79f7026279448fca0676438aaecb21f3fc39553a",
	}
	for name, want := range pinned {
		b, err := staticFS.ReadFile(name)
		if err != nil {
			t.Errorf("read %s: %v — run `make ui-vendor`", name, err)
			continue
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("vendored %s sha256 = %s, want %s — re-run scripts/ui-vendor.sh and bump both pins together", name, got, want)
		}
	}
}

// TestCompiledCSSIsEmbedded fails when the committed Tailwind output is missing from the binary.
func TestCompiledCSSIsEmbedded(t *testing.T) {
	const minBytes = 5000
	b, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("read static/app.css: %v — run `make ui-vendor` and commit the result", err)
	}
	if len(b) < minBytes {
		t.Errorf("static/app.css is %d bytes, want >= %d — the Tailwind compile produced an empty sheet", len(b), minBytes)
	}
}

// TestBuildPluginsMatchPinnedDigest fails when a build-only daisyUI file drifts from the pin in scripts/ui-vendor.sh.
func TestBuildPluginsMatchPinnedDigest(t *testing.T) {
	pinned := map[string]string{
		"ui/daisyui.mjs":       "15b69906afa8334e0e04e7da072394999e9c9d21b23451576f25417a95dcdc14",
		"ui/daisyui-theme.mjs": "b643f5d69bb4db8ba9332c83a8db6a9e65ed49ca8bd8a4d74391b26027708feb",
	}
	for name, want := range pinned {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Errorf("read %s: %v — run `make ui-vendor`", name, err)
			continue
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("%s sha256 = %s, want %s — re-run scripts/ui-vendor.sh and bump both pins together", name, got, want)
		}
	}
}

// TestBuildOnlyFilesStayOutOfStatic fails when a build-only file lands in the embedded static tree.
func TestBuildOnlyFilesStayOutOfStatic(t *testing.T) {
	for _, name := range []string{"daisyui.mjs", "daisyui-theme.mjs", "app.tailwind.css", "basecoat.css"} {
		if _, err := staticFS.ReadFile("static/" + name); err == nil {
			t.Errorf("static/%s is embedded; build-only files live in internal/web/ui/", name)
		}
	}
	if _, err := os.Stat("tailwind.config.js"); err == nil {
		t.Error("internal/web/tailwind.config.js is back; Tailwind 4 reads ui/app.tailwind.css")
	}
}

// TestCompiledCSSIncludesGoOnlyClasses fails when the Tailwind sources stop scanning hand-written Go helpers.
func TestCompiledCSSIncludesGoOnlyClasses(t *testing.T) {
	const class = "status-" + "warning"
	templs, err := filepath.Glob("templates/*.templ")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range templs {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), class) {
			t.Fatalf("%s uses %s; pick a class that only a Go helper uses", f, class)
		}
	}
	helper, err := os.ReadFile("templates/ui.go")
	if err != nil {
		t.Fatalf("read templates/ui.go: %v", err)
	}
	if !strings.Contains(string(helper), class) {
		t.Fatalf("templates/ui.go no longer uses %s", class)
	}
	css, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !selectorDefined(string(css), "."+class) {
		t.Errorf("static/app.css lacks .%s — the Tailwind @source misses Go files; run `make ui-vendor`", class)
	}
}

// TestCompiledCSSHasDaisyUIComponents fails when the daisyUI plugin is not part of the Tailwind build.
func TestCompiledCSSHasDaisyUIComponents(t *testing.T) {
	css, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	if !selectorDefined(string(css), ".btn") {
		t.Error("static/app.css lacks .btn — the daisyUI plugin did not run")
	}
}

// TestCompiledCSSUsesVendoredFonts fails when a font-face points at a file the binary does not embed or the stacks drop the brand fonts.
func TestCompiledCSSUsesVendoredFonts(t *testing.T) {
	css, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	sheet := string(css)
	if !strings.Contains(sheet, `--font-sans: "Plus Jakarta Sans"`) || !strings.Contains(sheet, `--font-mono: "JetBrains Mono"`) {
		t.Error("app.css font tokens must lead with Plus Jakarta Sans and JetBrains Mono")
	}
	urls := fontURL.FindAllStringSubmatch(sheet, -1)
	if len(urls) < 4 {
		t.Fatalf("app.css has %d font-face urls, want 4", len(urls))
	}
	for _, m := range urls {
		if _, err := staticFS.ReadFile("static/" + m[1]); err != nil {
			t.Errorf("font-face url %s is not embedded: %v", m[1], err)
		}
	}
}
