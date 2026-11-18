package device_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
)

func TestRules_IsDefault(t *testing.T) {
	require.True(t, device.DefaultRules().IsDefault())

	renamed := device.DefaultRules()
	renamed.Items[0].Name = "My DMs"
	require.True(t, renamed.IsDefault(), "a rename does not change behavior")

	paused := device.DefaultRules()
	paused.Items[0].Enabled = false
	require.False(t, paused.IsDefault())

	open := device.FromLegacy(device.Legacy{GroupMode: device.GroupOpen, IgnoreFromMe: true})
	require.False(t, open.IsDefault())

	require.False(t, device.Rules{}.IsDefault(), "all rules deleted is a choice, not the default")
}
