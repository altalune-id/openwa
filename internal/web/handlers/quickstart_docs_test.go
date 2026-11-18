package handlers_test

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/web/handlers"
)

func quickstartFences(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "integration", "README.md"))
	require.NoError(t, err)
	out := map[string]string{}
	name, body := "", ""
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := sc.Text()
		if name == "" {
			if _, tag, ok := strings.Cut(line, " quickstart="); ok && strings.HasPrefix(line, "```") {
				name = strings.TrimSpace(tag)
			}
			continue
		}
		if line == "```" {
			out[name], name, body = body, "", ""
			continue
		}
		body += line + "\n"
	}
	return out
}

func TestQuickstart_SnippetsMatchTheIntegrationDocs(t *testing.T) {
	fences := quickstartFences(t)
	shell, _, _, err := handlers.QuickstartSnippets("$BASE", "$DEVICE")
	require.NoError(t, err)
	send, verify, payload, err := handlers.QuickstartSnippets("https://wa.example.com/api/v1/orgs/acme/projects/main", "dev_V1StGXR8Z5jdHi6B")
	require.NoError(t, err)

	want := map[string]string{
		"send-curl":     shell["curl"],
		"send-js":       send["js"],
		"send-python":   send["python"],
		"verify-js":     verify["node"],
		"verify-python": verify["python"],
	}
	for name, code := range want {
		require.NotEmpty(t, code, name)
		assert.Equal(t, fences[name], code, "docs/integration/README.md fence quickstart=%s drifted from the console", name)
	}

	golden, err := os.ReadFile(filepath.Join("..", "..", "platform", "events", "testdata", "message_matched_v1.golden.json"))
	require.NoError(t, err)
	assert.JSONEq(t, string(golden), payload)
}
