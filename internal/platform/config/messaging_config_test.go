package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func validMessagingCfg() *Config {
	c := Defaults()
	c.HTTP.BaseURL = "https://wa.example.com"
	c.HTTP.StateSecret = "0123456789abcdef0123456789abcdef"
	c.Security.EncryptionKey = "0000000000000000000000000000000000000000000000000000000000000000"
	c.DB.DSN = "postgres://u:p@localhost/db"
	return c
}

func TestDefaults_MessagingKeys(t *testing.T) {
	c := Defaults()
	require.Equal(t, 30, c.Retention.MessageDays)
	require.Equal(t, "wa", c.Media.Store)
	require.Equal(t, time.Second, c.WhatsApp.SendSpacingMin)
	require.Equal(t, 3*time.Second, c.WhatsApp.SendSpacingMax)
	require.Equal(t, int64(33554432), c.WhatsApp.MediaMaxBytes)
	require.True(t, c.WhatsApp.TypingBeforeText)
}

func TestValidate_SendSpacingMaxNotBelowMin(t *testing.T) {
	c := validMessagingCfg()
	c.WhatsApp.SendSpacingMin, c.WhatsApp.SendSpacingMax = 5*time.Second, time.Second
	require.ErrorContains(t, c.Validate(), "whatsapp.sendSpacingMax")
}

func TestValidate_RetentionAndMediaStoreBounds(t *testing.T) {
	c := validMessagingCfg()
	c.Retention.MessageDays = 0
	require.Error(t, c.Validate())
	c = validMessagingCfg()
	c.Media.Store = "s3"
	require.Error(t, c.Validate(), "only the wa store exists in v1")
}

func TestValidate_DataPlaneNeedsBaseURL(t *testing.T) {
	c := validMessagingCfg()
	require.NoError(t, c.Validate())
	c.HTTP.BaseURL = ""
	err := c.Validate()
	require.True(t, IsDataPlaneBaseURLRequiredError(err), "got %v", err)
	require.ErrorContains(t, err, "OPENWA_HTTP_BASE_URL")
	c.DataPlane.Enabled = false
	require.NoError(t, c.Validate())
}

func TestEnvKeys_MessagingAwareness(t *testing.T) {
	want := map[string]string{
		"retention.messageDays":     "",
		"media.store":               "bootstrap",
		"whatsapp.sendSpacingMin":   "",
		"whatsapp.sendSpacingMax":   "",
		"whatsapp.mediaMaxBytes":    "",
		"whatsapp.typingBeforeText": "",
	}
	seen := map[string]bool{}
	for _, k := range WalkEnvKeys(EnvPrefix) {
		marker, ok := want[k.YAML]
		if !ok {
			continue
		}
		seen[k.YAML] = true
		if marker != "" {
			require.Contains(t, k.Awareness, marker, k.YAML)
		}
		require.NotContains(t, k.Awareness, "secret", k.YAML)
	}
	require.Len(t, seen, len(want), "every messaging key is reachable from the environment")
}
