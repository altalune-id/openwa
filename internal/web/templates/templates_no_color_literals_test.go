package templates

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTemplates_NoTailwindStatusColourLiterals(t *testing.T) {
	files, err := filepath.Glob("*.templ")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		require.Empty(t, colourLiteral.FindAllString(string(raw), -1), "%s uses a Tailwind palette colour; use a theme token (bg-success, text-destructive, …)", f)
	}
}

func TestColourLiteral_MatchesUtilitiesOnly(t *testing.T) {
	require.Regexp(t, colourLiteral, `class="hover:bg-red-50 text-emerald-800"`)
	require.NotRegexp(t, colourLiteral, `toolbar: ['unordered-list', 'ordered-list']`)
	require.NotRegexp(t, colourLiteral, `class="bg-destructive/10 text-success"`)
}

func TestTemplates_NoLowContrastMutedOpacities(t *testing.T) {
	files, err := filepath.Glob("*.templ")
	require.NoError(t, err)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, b := range []string{"text-muted-foreground/60", "text-muted-foreground/50"} {
			require.False(t, strings.Contains(string(raw), b), "%s uses %s", f, b)
		}
	}
}

var (
	darkVariant    = regexp.MustCompile(`(^|[\s"'\x60:])dark:`)
	arbitraryColor = regexp.MustCompile(`-\[(#|rgba?\(|hsla?\(|oklch\(|oklab\(|color-mix\(|color:)|(^|[\s"'\x60:!])\[color:|\b(bg|text|border(-[trblxyse])?|ring|outline|fill|stroke|from|via|to|decoration|accent|caret|divide|shadow|placeholder)-\[(var\(|[a-z]+\])`)
	physicalSide   = regexp.MustCompile(`(^|[\s"'\x60:!])-?((scroll-)?(ml|mr|pl|pr)-[\w.\[]|(left|right)-[\w.\[]|(text|float|clear)-(left|right)\b|border-[lr](-[\w\[]|\b)|rounded-([lr]|tl|tr|bl|br)(-[\w\[]|\b))`)
	inlineStyle    = regexp.MustCompile(`<[a-zA-Z][^<>]*\sstyle=[^<>]*>`)
	styleAttr      = regexp.MustCompile(`"style"\s*:`)
	radialProgress = regexp.MustCompile(`class="[^"]*\bradial-progress\b`)
)

// TestTemplatesUseThemeAwareLogicalClasses keeps every template and Go class helper theme-safe and RTL-safe.
func TestTemplatesUseThemeAwareLogicalClasses(t *testing.T) {
	root := repoRoot(t)
	files := classSourceFiles(t, root)
	if len(files) < 40 {
		t.Fatalf("scanned only %d class sources; the walk is broken", len(files))
	}
	for _, f := range files {
		src := readSource(t, root, f)
		for line := range strings.SplitSeq(src, "\n") {
			if darkVariant.MatchString(line) {
				t.Errorf("%s: dark: variant (use daisyUI semantic colors): %s", f, oneLine(line))
			}
			if arbitraryColor.MatchString(line) {
				t.Errorf("%s: arbitrary color value: %s", f, oneLine(line))
			}
			if physicalSide.MatchString(line) {
				t.Errorf("%s: physical side utility (use ms-/me-/ps-/pe-/start-/end-, text-start/end, border-s/e, rounded-s/e): %s", f, oneLine(line))
			}
			if styleAttr.MatchString(line) {
				t.Errorf("%s: inline style in attributes: %s", f, oneLine(line))
			}
		}
		for _, tag := range inlineStyle.FindAllString(src, -1) {
			if !radialProgress.MatchString(tag) {
				t.Errorf("%s: inline style: %s", f, oneLine(tag))
			}
		}
	}
}

func classSourceFiles(t *testing.T, root string) []string {
	t.Helper()
	files := templFiles(t, root)
	for _, dir := range []string{"internal/web/templates", "internal/web"} {
		gos, err := filepath.Glob(filepath.Join(root, dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range gos {
			if strings.HasSuffix(g, "_test.go") || strings.HasSuffix(g, "_templ.go") {
				continue
			}
			rel, _ := filepath.Rel(root, g)
			files = append(files, rel)
		}
	}
	return files
}

func readSource(t *testing.T, root, f string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, f))
	if err != nil {
		t.Fatal(err)
	}
	return comment.ReplaceAllString(string(raw), "")
}
