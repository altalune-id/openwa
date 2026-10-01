package device_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
)

func TestRules_Match(t *testing.T) {
	t.Parallel()
	open := device.Rules{GroupMode: device.GroupOpen, IgnoreFromMe: true}
	mention := device.Rules{GroupMode: device.GroupMention, IgnoreFromMe: true}
	ignore := device.Rules{GroupMode: device.GroupIgnore, IgnoreFromMe: true}
	senders := device.Rules{GroupMode: device.GroupOpen, AllowedSenders: []string{"628111"}, IgnoreFromMe: true}
	groups := device.Rules{GroupMode: device.GroupOpen, AllowedGroups: []string{"1@g.us"}, IgnoreFromMe: true}
	prefix := device.Rules{GroupMode: device.GroupOpen, TriggerPrefix: "!Bot", IgnoreFromMe: true}
	fromMeOK := device.Rules{GroupMode: device.GroupOpen, IgnoreFromMe: false}

	cases := []struct {
		name    string
		rules   device.Rules
		in      device.MatchInput
		want    bool
		reasons []string
	}{
		{"dm plain", open, device.MatchInput{Body: "hi", SenderPhone: "628111"}, true, []string{"dm"}},
		{"dm from me ignored", open, device.MatchInput{FromMe: true, Body: "hi"}, false, nil},
		{"dm from me allowed", fromMeOK, device.MatchInput{FromMe: true, Body: "hi"}, true, []string{"dm"}},
		{"dm sender allowed", senders, device.MatchInput{SenderPhone: "628111", Body: "hi"}, true, []string{"sender_allowed", "dm"}},
		{"dm sender not allowed", senders, device.MatchInput{SenderPhone: "628222", Body: "hi"}, false, nil},
		{"dm unknown phone never matches allow-list", senders, device.MatchInput{SenderPhone: "", Body: "hi"}, false, nil},
		{"dm prefix matches case-insensitively after trim", prefix, device.MatchInput{Body: "  !bot hello"}, true, []string{"prefix", "dm"}},
		{"dm prefix missing", prefix, device.MatchInput{Body: "hello !bot"}, false, nil},
		{"group ignore", ignore, device.MatchInput{IsGroup: true, ChatJID: "1@g.us", Body: "x", MentionedMe: true}, false, nil},
		{"group open", open, device.MatchInput{IsGroup: true, ChatJID: "1@g.us", Body: "x"}, true, []string{"group_open"}},
		{"group open from me ignored", open, device.MatchInput{IsGroup: true, FromMe: true, ChatJID: "1@g.us", Body: "x"}, false, nil},
		{"group mention via mention", mention, device.MatchInput{IsGroup: true, ChatJID: "1@g.us", Body: "x", MentionedMe: true}, true, []string{"group_mention"}},
		{"group mention via reply", mention, device.MatchInput{IsGroup: true, ChatJID: "1@g.us", Body: "x", RepliedToMe: true}, true, []string{"group_reply"}},
		{"group mention prefers mention reason", mention, device.MatchInput{IsGroup: true, ChatJID: "1@g.us", Body: "x", MentionedMe: true, RepliedToMe: true}, true, []string{"group_mention"}},
		{"group mention neither", mention, device.MatchInput{IsGroup: true, ChatJID: "1@g.us", Body: "x"}, false, nil},
		{"group allowed", groups, device.MatchInput{IsGroup: true, ChatJID: "1@g.us", Body: "x"}, true, []string{"group_open"}},
		{"group not allowed", groups, device.MatchInput{IsGroup: true, ChatJID: "2@g.us", Body: "x"}, false, nil},
		{"group sender allowed then open", senders, device.MatchInput{IsGroup: true, ChatJID: "1@g.us", SenderPhone: "628111", Body: "x"}, true, []string{"sender_allowed", "group_open"}},
		{"group sender denied before group checks", senders, device.MatchInput{IsGroup: true, ChatJID: "1@g.us", SenderPhone: "628222", Body: "x", MentionedMe: true}, false, nil},
		{"group prefix", prefix, device.MatchInput{IsGroup: true, ChatJID: "1@g.us", Body: "!BOT go"}, true, []string{"group_open", "prefix"}},
		{"group prefix missing", prefix, device.MatchInput{IsGroup: true, ChatJID: "1@g.us", Body: "go"}, false, nil},
		{"group ignore short-circuits before allowed groups", device.Rules{GroupMode: device.GroupIgnore, AllowedGroups: []string{"1@g.us"}}, device.MatchInput{IsGroup: true, ChatJID: "1@g.us", Body: "x"}, false, nil},
		{"empty body dm still matches without prefix", open, device.MatchInput{}, true, []string{"dm"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, reasons := tc.rules.Match(tc.in)
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.reasons, reasons)
		})
	}
}
