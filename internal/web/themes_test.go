package web

import (
	"math"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestThemes_EveryBlockDefinesEveryToken(t *testing.T) {
	daisyThemeTokens := []string{
		"--color-base-100", "--color-base-200", "--color-base-300", "--color-base-content",
		"--color-primary", "--color-primary-content", "--color-secondary", "--color-secondary-content",
		"--color-accent", "--color-accent-content", "--color-neutral", "--color-neutral-content",
		"--color-info", "--color-info-content", "--color-success", "--color-success-content",
		"--color-warning", "--color-warning-content", "--color-error", "--color-error-content",
		"--radius-selector", "--radius-field", "--radius-box",
		"--size-selector", "--size-field", "--border", "--depth", "--noise",
	}
	themes := Themes()
	if len(themes) == 0 {
		t.Fatal("no themes parsed from the theme source")
	}
	for _, th := range themes {
		for _, tok := range daisyThemeTokens {
			if th.Tokens[tok] == "" {
				t.Errorf("%s: missing %s", th.Name, tok)
			}
		}
	}
}

func TestThemes_OnePairPerFamily(t *testing.T) {
	modes := map[string][]string{}
	for _, th := range Themes() {
		if th.Name != th.Family+"-"+th.Mode {
			t.Errorf("%s: name is not <family>-<mode>", th.Name)
		}
		modes[th.Family] = append(modes[th.Family], th.Mode)
	}
	for family, got := range modes {
		slices.Sort(got)
		if !slices.Equal(got, []string{ModeDark, ModeLight}) {
			t.Errorf("family %s has modes %v, want exactly one light and one dark", family, got)
		}
	}
	var inOrder []string
	for _, th := range Themes() {
		if !slices.Contains(inOrder, th.Family) {
			inOrder = append(inOrder, th.Family)
		}
	}
	if !slices.Equal(Families(), inOrder) {
		t.Errorf("Families() = %v, want the families of Themes() in source order %v", Families(), inOrder)
	}
}

func TestThemes_OneDefaultLightAndOnePrefersDarkPerFamily(t *testing.T) {
	for _, f := range Families() {
		var defaults, dark []string
		for _, th := range Themes() {
			if th.Family != f {
				continue
			}
			if th.Default {
				defaults = append(defaults, th.Name)
			}
			if th.PrefersDark {
				dark = append(dark, th.Name)
			}
		}
		if !slices.Equal(defaults, []string{f + "-light"}) {
			t.Errorf("family %s: default blocks %v, want exactly %s-light", f, defaults, f)
		}
		if !slices.Equal(dark, []string{f + "-dark"}) {
			t.Errorf("family %s: prefersdark blocks %v, want exactly %s-dark", f, dark, f)
		}
	}
}

func TestThemes_OpenwaIsTheOnlyFamilyOnTheBareRoot(t *testing.T) {
	if got := Families(); !slices.Equal(got, []string{"openwa"}) {
		t.Fatalf("Families() = %v, want [openwa]", got)
	}
	if got := DefaultFamily(); got != "openwa" {
		t.Fatalf("DefaultFamily() = %q, want openwa", got)
	}
	for _, th := range Themes() {
		if th.Root != ":root" {
			t.Errorf("%s: root %q, want :root", th.Name, th.Root)
		}
		if th.ThemeController {
			t.Errorf("%s: themecontroller must be false", th.Name)
		}
	}
}

func TestCompiledCSS_RootScopedSystemSelectors(t *testing.T) {
	b, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	for _, want := range []string{
		":where(:root)",
		":root:not([data-theme])",
		`[data-theme="openwa-dark"]`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css lacks %s", want)
		}
	}
	if strings.Contains(css, "input.theme-controller") {
		t.Error("app.css still emits theme-controller selectors")
	}
}

func TestThemes_RegistryEqualsCompiledCSS(t *testing.T) {
	css, err := os.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, m := range regexp.MustCompile(`\[data-theme="([a-z0-9-]+)"\]`).FindAllStringSubmatch(string(css), -1) {
		if !slices.Contains(want, m[1]) {
			want = append(want, m[1])
		}
	}
	got := names(Themes())
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("registry %v, compiled app.css themes %v", got, want)
	}
}

func TestThemeSource_NotServed(t *testing.T) {
	for _, name := range []string{"themes.css", "ui/themes.css", "app.tailwind.css"} {
		if _, err := StaticFS().Open(name); err == nil {
			t.Errorf("%s is reachable under /static", name)
		}
	}
}

func TestParseThemePref_Allowlist(t *testing.T) {
	def := ThemePref{Family: DefaultFamily(), Mode: ModeSystem}
	for _, tc := range []struct {
		family, mode string
		want         ThemePref
	}{
		{DefaultFamily(), ModeLight, ThemePref{DefaultFamily(), ModeLight}},
		{DefaultFamily(), ModeDark, ThemePref{DefaultFamily(), ModeDark}},
		{DefaultFamily(), ModeSystem, def},
		{"", "", def},
		{"tide", ModeDark, ThemePref{DefaultFamily(), ModeDark}},
		{"forest", ModeLight, ThemePref{DefaultFamily(), ModeLight}},
		{"Openwa", ModeDark, ThemePref{DefaultFamily(), ModeDark}},
		{DefaultFamily(), "auto", def},
		{DefaultFamily(), "DARK", def},
		{`openwa"><script>`, `dark"><x`, def},
		{"glacier", "midnight", def},
		{"midnight", "midnight", def},
	} {
		if got := ParseThemePref(tc.family, tc.mode); got != tc.want {
			t.Errorf("ParseThemePref(%q, %q) = %+v, want %+v", tc.family, tc.mode, got, tc.want)
		}
	}
}

func TestThemePref_AttrAndCookie(t *testing.T) {
	f := DefaultFamily()
	for _, tc := range []struct {
		pref ThemePref
		attr string
	}{
		{ThemePref{f, ModeLight}, f + "-light"},
		{ThemePref{f, ModeDark}, f + "-dark"},
		{ThemePref{f, ModeSystem}, ""},
	} {
		if got := tc.pref.Attr(); got != tc.attr {
			t.Errorf("%+v.Attr() = %q, want %q", tc.pref, got, tc.attr)
		}
		if got := ParseThemeCookie(tc.pref.CookieValue()); got != tc.pref {
			t.Errorf("cookie round trip of %+v = %+v", tc.pref, got)
		}
	}
	def := ThemePref{f, ModeSystem}
	for _, raw := range []string{"", "slate", "tide", "dark", "auto", "openwa", "midnight-dark-x", f + "-sepia", f + "-system-", "%00"} {
		if got := ParseThemeCookie(raw); got != def {
			t.Errorf("ParseThemeCookie(%q) = %+v, want default", raw, got)
		}
	}
	if got := ParseThemeCookie("midnight-dark"); got != (ThemePref{f, ModeDark}) {
		t.Errorf("an unknown family keeps a valid mode: got %+v", got)
	}
}

func TestThemePref_Metas(t *testing.T) {
	for _, f := range Families() {
		assertMetas(t, f)
	}
}

func assertMetas(t *testing.T, f string) {
	t.Helper()
	light, dark := themeByName(t, f+"-light"), themeByName(t, f+"-dark")
	lightHex, err := oklchHex(light.Tokens["--color-base-100"])
	if err != nil {
		t.Fatal(err)
	}
	darkHex, err := oklchHex(dark.Tokens["--color-base-100"])
	if err != nil {
		t.Fatal(err)
	}
	if got := (ThemePref{f, ModeLight}).Metas(); !slices.Equal(got, []ThemeMeta{{Color: lightHex}}) {
		t.Errorf("light metas = %+v", got)
	}
	if got := (ThemePref{f, ModeDark}).Metas(); !slices.Equal(got, []ThemeMeta{{Color: darkHex}}) {
		t.Errorf("dark metas = %+v", got)
	}
	want := []ThemeMeta{
		{Media: "(prefers-color-scheme: light)", Color: lightHex},
		{Media: "(prefers-color-scheme: dark)", Color: darkHex},
	}
	if got := (ThemePref{f, ModeSystem}).Metas(); !slices.Equal(got, want) {
		t.Errorf("system metas = %+v, want %+v", got, want)
	}
	if !regexp.MustCompile(`^#[0-9a-f]{6}$`).MatchString(lightHex) {
		t.Errorf("light hex %q", lightHex)
	}
}

func TestOklchHex_KnownValues(t *testing.T) {
	for in, want := range map[string]string{
		"oklch(100% 0 0)":           "#ffffff",
		"oklch(0% 0 0)":             "#000000",
		"oklch(62.8% 0.2577 29.23)": "#ff0000",
	} {
		got, err := oklchHex(in)
		if err != nil || got != want {
			t.Errorf("oklchHex(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := oklchHex("red"); err == nil {
		t.Error("oklchHex accepted a non-oklch value")
	}
}

var overrideAlpha = regexp.MustCompile(`var\(--color-base-content\)\s+(\d+)%`)

func renderedAlpha(t *testing.T, css, selectorStart string, daisyDefault float64) float64 {
	t.Helper()
	i := strings.Index(css, selectorStart)
	if i < 0 {
		return daisyDefault
	}
	body := css[i:]
	body = body[strings.Index(body, "{"):strings.Index(body, "}")]
	m := overrideAlpha.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("override %q sets no base-content mix", selectorStart)
	}
	if strings.Contains(body, "opacity") && !strings.Contains(body, "opacity: 1;") {
		t.Fatalf("override %q changes opacity; the contrast check assumes 1", selectorStart)
	}
	p, _ := strconv.Atoi(m[1])
	return float64(p) / 100
}

func TestThemes_ContrastAA(t *testing.T) {
	src, err := os.ReadFile("ui/app.tailwind.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(src)
	muted := []struct {
		part  string
		alpha float64
	}{
		{"inactive tab", renderedAlpha(t, css, "@utility tab {", 0.5)},
		{"fieldset-legend", renderedAlpha(t, css, "@utility fieldset-legend {", 1)},
		{"menu-title", renderedAlpha(t, css, "@utility menu-title {", 0.4)},
		{"input placeholder", renderedAlpha(t, css, "@utility input {", 0.5)},
		{"textarea placeholder", renderedAlpha(t, css, "@utility textarea {", 0.5)},
	}
	for _, th := range Themes() {
		c := func(tok string) [3]float64 {
			rgb, err := oklchRGB(th.Tokens["--color-"+tok])
			if err != nil {
				t.Fatalf("%s: %s: %v", th.Name, tok, err)
			}
			return rgb
		}
		check := func(label string, fg, bg [3]float64, alpha float64) {
			if r := contrastRatio(fg, bg, alpha); r < 4.5 {
				t.Errorf("%s: %s %.2f:1, want >= 4.5:1", th.Name, label, r)
			}
		}
		for _, bg := range []string{"base-100", "base-200", "base-300"} {
			check("base-content on "+bg, c("base-content"), c(bg), 1)
			for _, m := range muted {
				check(m.part+" on "+bg, c("base-content"), c(bg), m.alpha)
			}
		}
		for _, k := range []string{"primary", "secondary", "accent", "neutral", "info", "success", "warning", "error"} {
			check(k+"-content on "+k, c(k+"-content"), c(k), 1)
		}
	}
}

func TestContrastRatio_Bounds(t *testing.T) {
	if r := contrastRatio([3]float64{0, 0, 0}, [3]float64{1, 1, 1}, 1); math.Abs(r-21) > 0.01 {
		t.Fatalf("black on white = %.3f, want 21", r)
	}
}

func contrastRatio(fg, bg [3]float64, alpha float64) float64 {
	var mixed [3]float64
	for i := range mixed {
		mixed[i] = alpha*fg[i] + (1-alpha)*bg[i]
	}
	x, y := relLuminance(mixed), relLuminance(bg)
	return (max(x, y) + 0.05) / (min(x, y) + 0.05)
}

func relLuminance(rgb [3]float64) float64 {
	var lin [3]float64
	for i, c := range rgb {
		if c <= 0.04045 {
			lin[i] = c / 12.92
			continue
		}
		lin[i] = math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*lin[0] + 0.7152*lin[1] + 0.0722*lin[2]
}

func names(ts []Theme) []string {
	out := make([]string, 0, len(ts))
	for _, th := range ts {
		out = append(out, th.Name)
	}
	return out
}

func themeByName(t *testing.T, name string) Theme {
	t.Helper()
	for _, th := range Themes() {
		if th.Name == name {
			return th
		}
	}
	t.Fatalf("no theme %s", name)
	return Theme{}
}

func TestParseThemes_RootAndControllerOptions(t *testing.T) {
	src := `@plugin "./daisyui-theme.mjs" {
  name: "ink-light";
  default: true;
  themecontroller: true;
  --color-base-100: oklch(99% 0 0);
}

@plugin "./daisyui-theme.mjs" {
  name: "ink-dark";
  themecontroller: false;
  --color-base-100: oklch(20% 0 0);
}
`
	ts, err := parseThemes(src)
	if err != nil {
		t.Fatal(err)
	}
	if ts[0].Root != ":root" || !ts[0].ThemeController || !ts[0].Default {
		t.Errorf("ink-light parsed as %+v", ts[0])
	}
	if ts[1].ThemeController || ts[1].Default {
		t.Errorf("ink-dark parsed as %+v", ts[1])
	}
	if got := defaultFamily(ts); got != "ink" {
		t.Errorf("defaultFamily = %q, want ink", got)
	}
	for name, bad := range map[string]string{
		"not <family>-<mode>": "@plugin \"./daisyui-theme.mjs\" {\n  name: \"nomode\";\n}\n",
		"scoped root":         "@plugin \"./daisyui-theme.mjs\" {\n  name: \"ink-light\";\n  root: \":root[data-family='ink']\";\n  default: true;\n}\n",
		"no default":          "@plugin \"./daisyui-theme.mjs\" {\n  name: \"ink-light\";\n}\n",
		"two families":        src + "@plugin \"./daisyui-theme.mjs\" {\n  name: \"moss-light\";\n  default: true;\n}\n",
	} {
		if _, err := parseThemes(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

var layerToken = regexp.MustCompile(`@layer\s+([\w.-]+)\s*\{|[{}]`)

func layerPathAt(css string, at int) []string {
	var stack []string
	for _, m := range layerToken.FindAllStringSubmatchIndex(css[:at], -1) {
		switch {
		case m[2] >= 0:
			stack = append(stack, css[m[2]:m[3]])
		case css[m[0]] == '{':
			stack = append(stack, "")
		default:
			stack = stack[:len(stack)-1]
		}
	}
	var out []string
	for _, s := range stack {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func TestCompiledCSS_ContrastOverridesBeatDaisyUIAndYieldToUtilities(t *testing.T) {
	b, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	daisy := strings.Index(css, "@layer daisyui")
	if daisy < 0 {
		t.Fatal("no daisyUI layer in app.css")
	}
	if got := layerPathAt(css, daisy); !slices.Equal(got, []string{"utilities"}) {
		t.Fatalf("daisyUI components sit in %v, want inside utilities", got)
	}
	utility := strings.Index(css, ".text-base-content {")
	if utility < 0 {
		t.Fatal("app.css lacks .text-base-content")
	}
	for _, sel := range []string{".tab:not(.tab-active", ".menu-title {", ".input::placeholder", ".textarea::placeholder"} {
		i := -1
		for at := 0; ; {
			n := strings.Index(css[at:], sel)
			if n < 0 {
				break
			}
			at += n
			if slices.Equal(layerPathAt(css, at), []string{"utilities"}) && strings.Contains(css[at:at+strings.Index(css[at:], "}")], "70%") {
				i = at
				break
			}
			at++
		}
		if i < 0 {
			t.Errorf("no unlayered %q override with the 70%% mix directly in @layer utilities, so daisyUI's own sublayer wins", sel)
			continue
		}
		if i > utility {
			t.Errorf("%q override comes after the text utilities, so it beats an explicit text-* class", sel)
		}
	}
	for _, m := range regexp.MustCompile(`(?m)^(\S[^{\n]*)\{`).FindAllStringSubmatch(css, -1) {
		if strings.Contains(m[1], "placeholder") || strings.Contains(m[1], "menu-title") {
			t.Errorf("unlayered rule %q beats every utility", m[1])
		}
	}
}

func TestThemes_BrandPerModeAndContrast(t *testing.T) {
	want := map[string]string{ModeLight: "#a86f00", ModeDark: "#ffbb00"}
	for _, th := range Themes() {
		got := th.Tokens["--brand"]
		if got != want[th.Mode] {
			t.Errorf("%s: --brand = %q, want %q", th.Name, got, want[th.Mode])
			continue
		}
		bg, err := oklchRGB(th.Tokens["--color-base-100"])
		if err != nil {
			t.Fatalf("%s base-100: %v", th.Name, err)
		}
		r := contrastRatio(hexRGB(t, got), bg, 1)
		t.Logf("%s: brand %s on base-100 = %.2f:1", th.Name, got, r)
		if r < 3 {
			t.Errorf("%s: brand %s on base-100 = %.2f:1, want at least 3:1 for graphics", th.Name, got, r)
		}
	}
}

func hexRGB(t *testing.T, hex string) [3]float64 {
	t.Helper()
	var rgb [3]float64
	for i := range rgb {
		v, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		if err != nil {
			t.Fatalf("bad hex %q: %v", hex, err)
		}
		rgb[i] = float64(v) / 255
	}
	return rgb
}

const minPrimaryDistance = 0.07

func oklabDistance(t *testing.T, x, y string) float64 {
	t.Helper()
	lab := func(v string) [3]float64 {
		l, c, h, err := parseOklch(v)
		if err != nil {
			t.Fatal(err)
		}
		return [3]float64{l, c * math.Cos(h*math.Pi/180), c * math.Sin(h*math.Pi/180)}
	}
	a, b := lab(x), lab(y)
	return math.Sqrt((a[0]-b[0])*(a[0]-b[0]) + (a[1]-b[1])*(a[1]-b[1]) + (a[2]-b[2])*(a[2]-b[2]))
}

func TestThemes_PrimaryClearOfSemanticColors(t *testing.T) {
	for _, th := range Themes() {
		for _, k := range []string{"info", "success", "warning", "error", "accent"} {
			if d := oklabDistance(t, th.Tokens["--color-primary"], th.Tokens["--color-"+k]); d < minPrimaryDistance {
				t.Errorf("%s: primary is %.3f OKLab from %s, want >= %.2f", th.Name, d, k, minPrimaryDistance)
			}
		}
	}
}

func TestParseThemes_SkipsCommentedBlocks(t *testing.T) {
	src := `@plugin "./daisyui-theme.mjs" {
  name: "ink-light";
  default: true;
}

/* TODO: hidden.
@plugin "./daisyui-theme.mjs" {
  name: "moss-light";
  default: true;
}
*/
`
	ts, err := parseThemes(src)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(ts); !slices.Equal(got, []string{"ink-light"}) {
		t.Errorf("parsed %v, want only ink-light", got)
	}
}

func TestThemes_DarkNeutralStaysDark(t *testing.T) {
	for _, th := range Themes() {
		if th.Mode != ModeDark {
			continue
		}
		l, _, _, err := parseOklch(th.Tokens["--color-neutral"])
		if err != nil {
			t.Fatalf("%s neutral: %v", th.Name, err)
		}
		if l > 0.45 {
			t.Errorf("%s: neutral lightness %.2f; active menu rows and tips must stay dark on a dark theme", th.Name, l)
		}
	}
}

func TestThemes_CallersCannotMutateTheRegistry(t *testing.T) {
	ts := Themes()
	before := ts[0].Tokens["--color-primary"]
	ts[0].Name = "mutated"
	ts[0].Tokens["--color-primary"] = "oklch(0% 0 0)"
	ts = append(ts[:0], Theme{Name: "x-light"})
	_ = ts
	again := Themes()
	if again[0].Name == "mutated" || again[0].Name == "x-light" {
		t.Errorf("Themes()[0].Name = %q; a caller's write leaked into the registry", again[0].Name)
	}
	if got := again[0].Tokens["--color-primary"]; got != before {
		t.Errorf("Tokens[--color-primary] = %q, want %q; a caller's map write leaked into the registry", got, before)
	}
}
