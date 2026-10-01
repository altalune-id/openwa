package web

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	cssBlockRe = regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`)
	cssVarRe   = regexp.MustCompile(`--([a-z0-9-]+)\s*:\s*([^;]+);`)
	themeSelRe = regexp.MustCompile(`\[data-theme="([a-z]+)"\]`)
)

type themeBlock struct {
	key  string
	dark bool
	vars map[string]string
}

func loadThemeBlocks(t *testing.T) []themeBlock {
	t.Helper()
	raw, err := staticFS.ReadFile("static/themes.css")
	require.NoError(t, err)
	var out []themeBlock
	for _, m := range cssBlockRe.FindAllStringSubmatch(string(raw), -1) {
		sel := m[1]
		keys := themeSelRe.FindStringSubmatch(sel)
		if keys == nil || strings.Contains(sel, "button[") {
			continue
		}
		vars := map[string]string{}
		for _, v := range cssVarRe.FindAllStringSubmatch(m[2], -1) {
			vars[v[1]] = strings.TrimSpace(v[2])
		}
		if len(vars) == 0 {
			continue
		}
		out = append(out, themeBlock{key: keys[1], dark: strings.Contains(sel, ".dark"), vars: vars})
	}
	return out
}

func requiredTokens() []string {
	return []string{
		"background", "foreground", "card", "card-foreground", "popover", "popover-foreground",
		"primary", "primary-foreground", "secondary", "secondary-foreground", "muted", "muted-foreground",
		"accent", "accent-foreground", "destructive", "destructive-foreground",
		"success", "success-foreground", "warning", "warning-foreground", "info", "info-foreground",
		"border", "input", "ring", "chart-1", "chart-2", "chart-3", "chart-4", "chart-5",
	}
}

func TestThemes_EveryBlockDefinesTheFullTokenSet(t *testing.T) {
	blocks := loadThemeBlocks(t)
	require.NotEmpty(t, blocks)
	seen := map[string]int{}
	for _, b := range blocks {
		seen[b.key]++
		for _, tok := range requiredTokens() {
			require.Contains(t, b.vars, tok, "theme %s dark=%v lacks --%s", b.key, b.dark, tok)
		}
	}
	for _, th := range Themes() {
		require.Equal(t, 2, seen[th.Key], "theme %s needs one light and one dark block", th.Key)
	}
}

func TestThemes_TideIsTheDefaultAndRootBindsToIt(t *testing.T) {
	require.Equal(t, "tide", DefaultThemeKey)
	require.Equal(t, "tide", Themes()[0].Key)
	raw, err := staticFS.ReadFile("static/themes.css")
	require.NoError(t, err)
	require.Regexp(t, `:root,\s*\[data-theme="tide"\]\s*\{`, string(raw))
	require.NotRegexp(t, `:root,\s*\[data-theme="slate"\]`, string(raw))
}

func contrastPairs() [][2]string {
	return [][2]string{
		{"background", "foreground"},
		{"card", "card-foreground"},
		{"popover", "popover-foreground"},
		{"primary", "primary-foreground"},
		{"secondary", "secondary-foreground"},
		{"muted", "muted-foreground"},
		{"accent", "accent-foreground"},
		{"destructive", "destructive-foreground"},
		{"success", "success-foreground"},
		{"warning", "warning-foreground"},
		{"info", "info-foreground"},
		{"card", "muted-foreground"},
		{"card", "destructive"},
		{"card", "success"},
		{"card", "warning"},
		{"card", "info"},
	}
}

func TestThemes_ContrastIsAtLeast4_5(t *testing.T) {
	for _, b := range loadThemeBlocks(t) {
		for _, p := range contrastPairs() {
			bg, fg := b.vars[p[0]], b.vars[p[1]]
			require.NotEmpty(t, bg, "%s dark=%v lacks --%s", b.key, b.dark, p[0])
			require.NotEmpty(t, fg, "%s dark=%v lacks --%s", b.key, b.dark, p[1])
			r := contrastRatio(bg, fg)
			require.GreaterOrEqual(t, r, 4.5, "%s dark=%v --%s on --%s = %.2f", b.key, b.dark, p[1], p[0], r)
		}
	}
}

func parseHSL(s string) (float64, float64, float64) {
	f := strings.Fields(strings.ReplaceAll(s, "%", ""))
	h, _ := strconv.ParseFloat(f[0], 64)
	sat, _ := strconv.ParseFloat(f[1], 64)
	l, _ := strconv.ParseFloat(f[2], 64)
	return h, sat / 100, l / 100
}

func hslToRGB(h, s, l float64) (float64, float64, float64) {
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return r + m, g + m, b + m
}

func linear(c float64) float64 {
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func luminance(hsl string) float64 {
	r, g, b := hslToRGB(parseHSL(hsl))
	return 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b)
}

func contrastRatio(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}
