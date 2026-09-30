package boot_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/mcp/ui"
	rootmcp "altalune.id/openwa/mcp"
)

func readResourceBody(uri string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "resources/read",
		"params":  map[string]any{"uri": uri},
	}
}

// TestMCP_AppsUIServesTheAdvertisedResource is the end-to-end guard for the backlog item: the ui:// link project_list advertises must resolve to a document over the real mount.
func TestMCP_AppsUIServesTheAdvertisedResource(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true, appsUI: true})

	rec := f.call(t, f.readKey, readResourceBody(ui.ResourceURI))
	require.Equal(t, 200, rec.Code)

	var out struct {
		Result struct {
			Contents []struct {
				URI      string `json:"uri"`
				MIMEType string `json:"mimeType"`
				Text     string `json:"text"`
			} `json:"contents"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	require.Nil(t, out.Error, rec.Body.String())
	require.Len(t, out.Result.Contents, 1)

	got := out.Result.Contents[0]
	require.Equal(t, ui.ResourceURI, got.URI)
	require.Equal(t, rootmcp.MIMEApp, got.MIMEType)
	require.Equal(t, ui.Document(), got.Text)
	require.True(t, strings.Contains(got.Text, `id="root"`), "the served document is not the assembled bundle")
	for _, tag := range []string{"openwa-device-list", "openwa-device-pair"} {
		require.Contains(t, got.Text, tag, "the served bundle lacks the %s view", tag)
	}
}

func TestMCP_AppsUIOffPublishesNoResource(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true, appsUI: false})

	rec := f.call(t, f.readKey, readResourceBody(ui.ResourceURI))
	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Body.String(), "error", "reading an unpublished resource must fail, not serve a blank frame")
}

func TestMCP_AppsUIBindsProjectList(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true, appsUI: true})

	rec := f.call(t, f.readKey, listToolsBody())
	require.Equal(t, 200, rec.Code, rec.Body.String())

	var out struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
				Meta struct {
					UI struct {
						ResourceURI string `json:"resourceUri"`
					} `json:"ui"`
				} `json:"_meta"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())

	uris := map[string]string{}
	for _, tool := range out.Result.Tools {
		uris[tool.Name] = tool.Meta.UI.ResourceURI
	}
	require.Equal(t, ui.ResourceURI, uris["project_list"], "project_list must carry _meta.ui.resourceUri; body=%s", rec.Body.String())
}
