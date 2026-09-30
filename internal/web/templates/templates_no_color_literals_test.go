package templates

import (
	"os"
	"path/filepath"
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
