package boot

import (
	"testing"

	"altalune.id/openwa/internal/platform/capabilities"
	"altalune.id/openwa/internal/platform/config"
)

func TestAlertOptions_MailReadyNeedsADeliveringDriverAndABaseURL(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mail    bool
		baseURL string
		want    bool
	}{
		{"driver and base URL", true, "https://wa.example.com", true},
		{"no base URL for List-Unsubscribe", true, "", false},
		{"console or no driver", false, "https://wa.example.com", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.HTTP.BaseURL = tc.baseURL
			if got := alertOptions(cfg, capabilities.Capabilities{MailEnabled: tc.mail}).MailEnabled; got != tc.want {
				t.Fatalf("MailEnabled = %v, want %v", got, tc.want)
			}
		})
	}
}
