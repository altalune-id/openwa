package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefaults_MailAndNotificationKeys(t *testing.T) {
	c := Defaults()
	require.Equal(t, 10*time.Minute, c.Mail.IdlePoll)
	d := c.Notifications.Disconnect
	require.True(t, d.Enabled)
	require.Equal(t, 10*time.Minute, d.Grace)
	require.Equal(t, 6*time.Hour, d.Cooldown)
	require.Equal(t, 50, d.DailyOrgCap)
	require.NoError(t, validMessagingCfg().Validate())
}

func TestValidate_DisconnectNotifyBounds(t *testing.T) {
	for name, mutate := range map[string]func(*DisconnectNotifyConfig){
		"no grace":             func(d *DisconnectNotifyConfig) { d.Grace = 0 },
		"cooldown below grace": func(d *DisconnectNotifyConfig) { d.Grace, d.Cooldown = time.Hour, time.Minute },
		"no daily cap":         func(d *DisconnectNotifyConfig) { d.DailyOrgCap = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			c := validMessagingCfg()
			mutate(&c.Notifications.Disconnect)
			require.Error(t, c.Validate())
		})
	}
}

func TestValidate_MailIdlePollNotNegative(t *testing.T) {
	c := validMessagingCfg()
	c.Mail.IdlePoll = -time.Second
	require.Error(t, c.Validate())
}
