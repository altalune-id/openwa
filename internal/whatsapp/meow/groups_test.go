package meow

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow/types"
)

func TestToGroupInfo(t *testing.T) {
	g := &types.GroupInfo{
		JID:              types.NewJID("120363", types.GroupServer),
		GroupName:        types.GroupName{Name: "Tim Sales"},
		GroupTopic:       types.GroupTopic{Topic: "harian"},
		GroupAnnounce:    types.GroupAnnounce{IsAnnounce: true},
		GroupLocked:      types.GroupLocked{IsLocked: true},
		ParticipantCount: 12,
	}
	got := toGroupInfo(g)
	require.Equal(t, "120363@g.us", got.JID)
	require.Equal(t, "Tim Sales", got.Name)
	require.Equal(t, "harian", got.Topic)
	require.Equal(t, 12, got.Participants)
	require.True(t, got.Announce)
	require.True(t, got.Locked)
}

func TestInviteCode(t *testing.T) {
	for in, want := range map[string]string{
		"https://chat.whatsapp.com/AbCdEf123": "AbCdEf123",
		"chat.whatsapp.com/AbCdEf123":         "AbCdEf123",
		"AbCdEf123":                           "AbCdEf123",
	} {
		got, err := inviteCode(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got)
	}
	for _, bad := range []string{"", "https://evil.example/AbC", "https://chat.whatsapp.com/", "a b"} {
		_, err := inviteCode(bad)
		require.Error(t, err, bad)
	}
}
