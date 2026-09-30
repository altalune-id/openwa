package whatsapp_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/whatsapp"
)

func TestParseJID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want whatsapp.JID
		bad  bool
	}{
		{"628111@s.whatsapp.net", whatsapp.JID{User: "628111", Server: "s.whatsapp.net"}, false},
		{"628111:12@s.whatsapp.net", whatsapp.JID{User: "628111", Device: 12, Server: "s.whatsapp.net"}, false},
		{"628111.2:12@s.whatsapp.net", whatsapp.JID{User: "628111", Agent: 2, Device: 12, Server: "s.whatsapp.net"}, false},
		{"120363@g.us", whatsapp.JID{User: "120363", Server: "g.us"}, false},
		{"", whatsapp.JID{}, true},
		{"noserver", whatsapp.JID{}, true},
		{"x:abc@s.whatsapp.net", whatsapp.JID{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := whatsapp.ParseJID(tc.in)
			if tc.bad {
				require.True(t, whatsapp.IsInvalidJIDError(err), "got %v", err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.in, got.String())
		})
	}
}

func TestJIDHelpers(t *testing.T) {
	t.Parallel()
	require.Equal(t, "628111@s.whatsapp.net", whatsapp.NonAD("628111.2:12@s.whatsapp.net"))
	require.Equal(t, "garbage", whatsapp.NonAD("garbage"))
	require.True(t, whatsapp.IsGroup("1@g.us"))
	require.False(t, whatsapp.IsGroup("1@s.whatsapp.net"))
	require.True(t, whatsapp.IsLID("1@lid"))
	require.Equal(t, "628111", whatsapp.PhoneFromJID("628111:4@s.whatsapp.net"))
	require.Equal(t, "", whatsapp.PhoneFromJID("628111@lid"))
	require.Equal(t, "", whatsapp.PhoneFromJID(""))
}

func TestNormalizePhone(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
		bad  bool
	}{
		{"+62 812-3456", "628123456", false},
		{"62 (812) 3456.789", "628123456789", false},
		{"0812 3456 789", "", true},
		{"+1", "", true},
		{"abc", "", true},
		{"", "", true},
		{"+12345678901234567", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := whatsapp.NormalizePhone(tc.in)
			if tc.bad {
				require.True(t, whatsapp.IsInvalidPhoneError(err), "got %v", err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestResolvePhone(t *testing.T) {
	t.Parallel()
	lookup := func(lid string) string {
		if lid == "77@lid" {
			return "628222@s.whatsapp.net"
		}
		return ""
	}
	require.Equal(t, "628111", whatsapp.ResolvePhone("628111:1@s.whatsapp.net", "", lookup))
	require.Equal(t, "628333", whatsapp.ResolvePhone("55@lid", "628333@s.whatsapp.net", lookup))
	require.Equal(t, "628222", whatsapp.ResolvePhone("77:2@lid", "", lookup))
	require.Equal(t, "", whatsapp.ResolvePhone("55@lid", "", lookup))
	require.Equal(t, "", whatsapp.ResolvePhone("55@lid", "", nil))
	require.Equal(t, "", whatsapp.ResolvePhone("55@lid", "99@lid", lookup), "an alt that is itself a LID is never a phone")
}
