package boot_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

//nolint:gochecknoglobals // a golden-update flag has to be package level.
var updateMCPGolden = flag.Bool("update-mcp-golden", false, "rewrite the MCP messaging tool descriptor goldens")

func TestMCP_MessagingToolDescriptorsAreFrozen(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true, appsUI: true})
	rec := f.call(t, f.msgKey, listToolsBody())
	require.Equal(t, 200, rec.Code)
	tools := toolsByName(t, rec.Body.Bytes())
	for _, name := range []string{"message_send", "message_list", "chat_list", "contact_list", "group_list", "group_join"} {
		t.Run(name, func(t *testing.T) {
			got, err := json.MarshalIndent(tools[name], "", "  ")
			require.NoError(t, err)
			got = append(got, '\n')
			path := filepath.Join("testdata", "mcp_tools", name+".golden.json")
			if *updateMCPGolden {
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
				require.NoError(t, os.WriteFile(path, got, 0o600))
				return
			}
			want, err := os.ReadFile(path) //nolint:gosec // fixed test path
			require.NoError(t, err)
			require.Equal(t, string(want), string(got), "tool %s changed on the wire; rerun with -update-mcp-golden once the change is intended", name)
		})
	}
}
