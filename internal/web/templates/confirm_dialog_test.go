package templates

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfirmDialog_RendersNoTriggerAndOpensFromAnyElement(t *testing.T) {
	html := render(t, ConfirmDialog(projectShell(), ConfirmProps{ID: "rule-delete-r1", Title: "Delete?", Action: "/x/delete", Kind: ButtonDanger}))
	require.Contains(t, html, `<dialog id="rule-delete-r1"`)
	require.NotContains(t, html, "data-dialog-open", "the dialog alone carries no trigger")
	require.Equal(t, 1, strings.Count(html, `type="submit"`))

	attrs := ConfirmOpener("rule-delete-r1")
	require.Equal(t, "rule-delete-r1", attrs["data-dialog-open"])
	require.Equal(t, "dialog", attrs["aria-haspopup"])
	require.Equal(t, "rule-delete-r1", attrs["aria-controls"])
}
