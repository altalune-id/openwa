package boot

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/config"
)

func TestAllowInsecureWebhooks(t *testing.T) {
	tests := []struct {
		name string
		mode config.Mode
		flag bool
		want bool
	}{
		{"selfhosted flag on", config.ModeSelfhosted, true, true},
		{"selfhosted flag off", config.ModeSelfhosted, false, false},
		{"cloud flag on is ignored", config.ModeCloud, true, false},
		{"cloud flag off", config.ModeCloud, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Mode: tt.mode, Webhook: config.WebhookConfig{AllowInsecure: tt.flag}}
			require.Equal(t, tt.want, allowInsecureWebhooks(cfg))
		})
	}
}

func TestWebhookHTTPClient_PrivateHosts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	t.Cleanup(srv.Close)

	denied, err := webhookHTTPClient(false).Get(srv.URL)
	if denied != nil {
		_ = denied.Body.Close()
	}
	require.Error(t, err, "default client must refuse loopback")

	resp, err := webhookHTTPClient(true).Get(srv.URL)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}
