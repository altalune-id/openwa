package device_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
)

func legacyTokens(reasons []string) []string {
	newTokens := []string{"chat", "sender_excluded", "text", "mention", "reply"}
	return slices.DeleteFunc(slices.Clone(reasons), func(r string) bool { return slices.Contains(newTokens, r) })
}

func TestRules_LegacyEquivalence(t *testing.T) {
	t.Parallel()
	open := device.Legacy{GroupMode: device.GroupOpen, IgnoreFromMe: true}
	mention := device.Legacy{GroupMode: device.GroupMention, IgnoreFromMe: true}
	ignore := device.Legacy{GroupMode: device.GroupIgnore, IgnoreFromMe: true}
	senders := device.Legacy{GroupMode: device.GroupOpen, AllowedSenders: []string{"628111"}, IgnoreFromMe: true}
	groups := device.Legacy{GroupMode: device.GroupOpen, AllowedGroups: []string{"1@g.us"}, IgnoreFromMe: true}
	prefix := device.Legacy{GroupMode: device.GroupOpen, TriggerPrefix: "!Bot", IgnoreFromMe: true}
	fromMeOK := device.Legacy{GroupMode: device.GroupOpen, IgnoreFromMe: false}

	cases := []struct {
		name    string
		rules   device.Legacy
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
		{"group ignore short-circuits before allowed groups", device.Legacy{GroupMode: device.GroupIgnore, AllowedGroups: []string{"1@g.us"}}, device.MatchInput{IsGroup: true, ChatJID: "1@g.us", Body: "x"}, false, nil},
		{"empty body dm still matches without prefix", open, device.MatchInput{}, true, []string{"dm"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := device.FromLegacy(tc.rules).Match(tc.in)
			require.Equal(t, tc.want, got.Outcome == device.OutcomeMatched)
			if !tc.want {
				require.Equal(t, device.OutcomeNone, got.Outcome)
				require.Nil(t, got.Reasons)
				return
			}
			require.Equal(t, tc.reasons, legacyTokens(got.Reasons))
			wantID := "legacy-dm"
			if tc.in.IsGroup {
				wantID = "legacy-group"
			}
			require.Equal(t, wantID, got.RuleID)
		})
	}
}

func TestRules_LegacyAllowedGroupsAddOnlyTheChatToken(t *testing.T) {
	t.Parallel()
	got := device.FromLegacy(device.Legacy{GroupMode: device.GroupMention, AllowedSenders: []string{"628111222333"}, AllowedGroups: []string{"1@g.us"}, TriggerPrefix: "!b"}).
		Match(device.MatchInput{IsGroup: true, ChatJID: "1@g.us", SenderPhone: "628111222333", Body: "!b x", MentionedMe: true})
	require.Equal(t, []string{"sender_allowed", "chat", "group_mention", "prefix"}, got.Reasons)
}

func cond(f device.Field, op device.Op, values ...string) device.Condition {
	return device.Condition{Field: f, Op: op, Values: values}
}

func rule(id string, conds ...device.Condition) device.Rule {
	return device.Rule{ID: id, Name: id, Enabled: true, Action: device.ActionMatch, Conditions: conds}
}

func TestRules_Match(t *testing.T) {
	t.Parallel()
	dm := cond(device.FieldChatKind, device.OpIs, "dm")
	group := cond(device.FieldChatKind, device.OpIs, "group")
	cases := []struct {
		name    string
		rules   device.Rules
		in      device.MatchInput
		outcome device.Outcome
		ruleID  string
		reasons []string
	}{
		{"no items sends nothing", device.Rules{Items: []device.Rule{}}, device.MatchInput{Body: "hi"}, device.OutcomeNone, "", nil},
		{"a rule without chat_kind never takes a dm", device.Rules{Items: []device.Rule{rule("a", cond(device.FieldText, device.OpContains, "hi"))}}, device.MatchInput{Body: "hi"}, device.OutcomeNone, "", nil},
		{"a rule without chat_kind takes a group", device.Rules{Items: []device.Rule{rule("a", cond(device.FieldText, device.OpContains, "HI"))}}, device.MatchInput{IsGroup: true, ChatJID: "1@g.us", Body: "oh hi"}, device.OutcomeMatched, "a", []string{"group_open", "text"}},
		{"chat_kind is_not group admits a dm", device.Rules{Items: []device.Rule{rule("a", cond(device.FieldChatKind, device.OpIsNot, "group"))}}, device.MatchInput{Body: "x"}, device.OutcomeMatched, "a", []string{"dm"}},
		{"first holding rule decides", device.Rules{Items: []device.Rule{rule("a", group, cond(device.FieldMention, "")), rule("b", group)}}, device.MatchInput{IsGroup: true, ChatJID: "1@g.us"}, device.OutcomeMatched, "b", []string{"group_open"}},
		{"disabled rule skipped", device.Rules{Items: []device.Rule{{ID: "a", Enabled: false, Action: device.ActionMatch, Conditions: []device.Condition{dm}}, rule("b", dm)}}, device.MatchInput{}, device.OutcomeMatched, "b", []string{"dm"}},
		{"ignore action stops evaluation", device.Rules{Items: []device.Rule{{ID: "a", Name: "spam", Enabled: true, Action: device.ActionIgnore, Conditions: []device.Condition{dm, cond(device.FieldText, device.OpContains, "promo")}}, rule("b", dm)}}, device.MatchInput{Body: "PROMO"}, device.OutcomeIgnored, "a", []string{"text", "dm"}},
		{"or across values", device.Rules{Items: []device.Rule{rule("a", dm, cond(device.FieldText, device.OpEquals, "yes", "ok"))}}, device.MatchInput{Body: " OK "}, device.OutcomeMatched, "a", []string{"text", "dm"}},
		{"and across conditions", device.Rules{Items: []device.Rule{rule("a", dm, cond(device.FieldText, device.OpEquals, "ok"), cond(device.FieldSender, device.OpIs, "phone:628111222333"))}}, device.MatchInput{Body: "ok", SenderPhone: "628999"}, device.OutcomeNone, "", nil},
		{"sender is_not excludes", device.Rules{Items: []device.Rule{rule("a", dm, cond(device.FieldSender, device.OpIsNot, "phone:628111222333"))}}, device.MatchInput{SenderPhone: "628111222333"}, device.OutcomeNone, "", nil},
		{"sender is_not passes others", device.Rules{Items: []device.Rule{rule("a", dm, cond(device.FieldSender, device.OpIsNot, "phone:628111222333"))}}, device.MatchInput{SenderPhone: "628444555666"}, device.OutcomeMatched, "a", []string{"sender_excluded", "dm"}},
		{"hidden-phone member matches by lid", device.Rules{Items: []device.Rule{rule("a", group, cond(device.FieldSender, device.OpIs, "12345678901234@lid"))}}, device.MatchInput{IsGroup: true, ChatJID: "1@g.us", SenderLID: "12345678901234@lid"}, device.OutcomeMatched, "a", []string{"sender_allowed", "group_open"}},
		{"phone value never matches a lid user part", device.Rules{Items: []device.Rule{rule("a", group, cond(device.FieldSender, device.OpIs, "phone:12345678901234"))}}, device.MatchInput{IsGroup: true, ChatJID: "1@g.us", SenderLID: "12345678901234@lid"}, device.OutcomeNone, "", nil},
		{"jid value matches sender jid by full string", device.Rules{Items: []device.Rule{rule("a", dm, cond(device.FieldSender, device.OpIs, "628111222333@s.whatsapp.net"))}}, device.MatchInput{SenderJID: "628111222333@s.whatsapp.net"}, device.OutcomeMatched, "a", []string{"sender_allowed", "dm"}},
		{"jid value never matches a phone", device.Rules{Items: []device.Rule{rule("a", dm, cond(device.FieldSender, device.OpIs, "628111222333@s.whatsapp.net"))}}, device.MatchInput{SenderPhone: "628111222333"}, device.OutcomeNone, "", nil},
		{"chat is_not", device.Rules{Items: []device.Rule{rule("a", group, cond(device.FieldChat, device.OpIsNot, "1@g.us"))}}, device.MatchInput{IsGroup: true, ChatJID: "1@g.us"}, device.OutcomeNone, "", nil},
		{"mention in a dm uses dm tokens", device.Rules{Items: []device.Rule{rule("a", dm, cond(device.FieldMention, ""))}}, device.MatchInput{RepliedToMe: true}, device.OutcomeMatched, "a", []string{"reply", "dm"}},
		{"text starts_with is prefix", device.Rules{Items: []device.Rule{rule("a", dm, cond(device.FieldText, device.OpStartsWith, "!b"))}}, device.MatchInput{Body: " !B go"}, device.OutcomeMatched, "a", []string{"prefix", "dm"}},
		{"from me ignored before items", device.Rules{IgnoreFromMe: true, Items: []device.Rule{rule("a", dm)}}, device.MatchInput{FromMe: true}, device.OutcomeNone, "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.rules.Match(tc.in)
			require.Equal(t, tc.outcome, got.Outcome)
			require.Equal(t, tc.ruleID, got.RuleID)
			require.Equal(t, tc.reasons, got.Reasons)
		})
	}
}

func newDevice(t *testing.T) *device.Device {
	t.Helper()
	d, err := device.New(uuid.New(), uuid.New(), testPublicID(t), "x")
	require.NoError(t, err)
	return d
}

func TestSetRules_Validation(t *testing.T) {
	t.Parallel()
	dm := cond(device.FieldChatKind, device.OpIs, "dm")
	many := func(n int, f func(int) device.Rule) []device.Rule {
		out := make([]device.Rule, n)
		for i := range out {
			out[i] = f(i)
		}
		return out
	}
	values := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = strings.Repeat("v", i+1)
		}
		return out
	}
	cases := []struct {
		name  string
		items []device.Rule
	}{
		{"51 rules", many(51, func(int) device.Rule { return rule("", dm) })},
		{"21 conditions", []device.Rule{rule("a", slices.Repeat([]device.Condition{dm}, 21)...)}},
		{"101 values", []device.Rule{rule("a", device.Condition{Field: device.FieldText, Op: device.OpContains, Values: values(101)})}},
		{"text over 256 runes", []device.Rule{rule("a", cond(device.FieldText, device.OpContains, strings.Repeat("é", 257)))}},
		{"unknown field", []device.Rule{rule("a", cond("color", device.OpIs, "x"))}},
		{"op not allowed for field", []device.Rule{rule("a", cond(device.FieldChatKind, device.OpContains, "dm"))}},
		{"bad chat_kind value", []device.Rule{rule("a", cond(device.FieldChatKind, device.OpIs, "channel"))}},
		{"chat not a group jid", []device.Rule{rule("a", cond(device.FieldChat, device.OpIs, "628@s.whatsapp.net"))}},
		{"bare sender user part", []device.Rule{rule("a", cond(device.FieldSender, device.OpIs, "628111222333"))}},
		{"short phone sender", []device.Rule{rule("a", cond(device.FieldSender, device.OpIs, "phone:123"))}},
		{"national phone sender", []device.Rule{rule("a", cond(device.FieldSender, device.OpIs, "phone:08123456789"))}},
		{"mention with values", []device.Rule{rule("a", cond(device.FieldMention, device.OpIs, "x"))}},
		{"condition without values", []device.Rule{rule("a", cond(device.FieldText, device.OpContains, " "))}},
		{"unknown action", []device.Rule{{ID: "a", Enabled: true, Action: "forward", Conditions: []device.Condition{dm}}}},
		{"duplicate ids", []device.Rule{rule("a", dm), rule("a", dm)}},
		{"id with a slash", []device.Rule{rule("a/b", dm)}},
		{"id over 64", []device.Rule{rule(strings.Repeat("a", 65), dm)}},
		{"reserved legacy prefix", []device.Rule{rule("legacy-x", dm)}},
		{"name over 64 runes", []device.Rule{{ID: "a", Name: strings.Repeat("é", 65), Enabled: true, Action: device.ActionMatch}}},
		{"encoded over 64 KiB", bigRules(50)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := newDevice(t)
			before := d.Rules
			err := d.SetRules(device.Rules{Items: tc.items})
			require.True(t, device.IsInvalidRulesError(err), "got %v", err)
			require.Equal(t, before, d.Rules)
		})
	}
}

func TestSetRules_NormalizesAndMintsIDs(t *testing.T) {
	t.Parallel()
	d := newDevice(t)
	require.NoError(t, d.SetRules(device.Rules{Items: []device.Rule{
		{Name: "  Sales  ", Enabled: true, Conditions: []device.Condition{cond(device.FieldText, device.OpContains, " a ", "", "a", "b")}},
		{ID: "legacy-dm", Enabled: true, Action: device.ActionMatch, Conditions: []device.Condition{cond(device.FieldChatKind, device.OpIs, "dm")}},
	}}))
	got := d.Rules.Items
	require.NotEmpty(t, got[0].ID)
	require.Equal(t, "Sales", got[0].Name)
	require.Equal(t, device.ActionMatch, got[0].Action)
	require.Equal(t, []string{"a", "b"}, got[0].Conditions[0].Values)
	require.Equal(t, "legacy-dm", got[1].ID)
}

func bigRules(n int) []device.Rule {
	out := make([]device.Rule, 0, n)
	for range n {
		vals := make([]string, 0, 6)
		for _, c := range "abcdef" {
			vals = append(vals, strings.Repeat(string(c), 250))
		}
		out = append(out, rule("", device.Condition{Field: device.FieldText, Op: device.OpContains, Values: vals}))
	}
	return out
}

func TestSetRules_EncodedCapBoundary(t *testing.T) {
	t.Parallel()
	d := newDevice(t)
	require.True(t, device.IsInvalidRulesError(d.SetRules(device.Rules{Items: bigRules(50)})))
	require.NoError(t, d.SetRules(device.Rules{Items: bigRules(30)}))
}

func TestLegacy_RoundTripAndExpressibility(t *testing.T) {
	t.Parallel()
	for _, l := range []device.Legacy{
		{GroupMode: device.GroupMention, AllowedSenders: []string{}, AllowedGroups: []string{}, IgnoreFromMe: true},
		{GroupMode: device.GroupOpen, AllowedSenders: []string{"628111222333"}, AllowedGroups: []string{"1@g.us", "2@g.us"}, TriggerPrefix: "!b"},
		{GroupMode: device.GroupIgnore, AllowedSenders: []string{"628111222333"}, AllowedGroups: []string{}, TriggerPrefix: ""},
	} {
		got, ok := device.FromLegacy(l).Legacy()
		require.True(t, ok)
		require.Equal(t, l, got)
	}

	renamed := device.FromLegacy(device.Legacy{GroupMode: device.GroupOpen})
	renamed.Items[0].Name = "Customers"
	_, ok := renamed.Legacy()
	require.True(t, ok, "a name is not behavior")

	notExpressible := []device.Rules{
		{Items: []device.Rule{}},
		{Items: []device.Rule{rule("a", cond(device.FieldChatKind, device.OpIs, "dm"))}},
		func() device.Rules {
			r := device.FromLegacy(device.Legacy{GroupMode: device.GroupOpen})
			r.Items[1].Enabled = false
			return r
		}(),
		func() device.Rules {
			r := device.FromLegacy(device.Legacy{GroupMode: device.GroupOpen})
			r.Items[0], r.Items[1] = r.Items[1], r.Items[0]
			return r
		}(),
		func() device.Rules {
			r := device.FromLegacy(device.Legacy{GroupMode: device.GroupOpen, AllowedSenders: []string{"628111222333"}})
			r.Items[1].Conditions = slices.DeleteFunc(slices.Clone(r.Items[1].Conditions), func(c device.Condition) bool { return c.Field == device.FieldSender })
			return r
		}(),
	}
	for i, r := range notExpressible {
		_, ok := r.Legacy()
		require.False(t, ok, "case %d", i)
	}
}

func TestLegacyView_CustomHasNoLists(t *testing.T) {
	t.Parallel()
	got := device.Rules{IgnoreFromMe: true, Items: []device.Rule{}}.LegacyView()
	require.Equal(t, device.Legacy{GroupMode: device.GroupCustom, IgnoreFromMe: true}, got)
}

func TestFromLegacy_UnknownGroupModeHasNoGroupRule(t *testing.T) {
	t.Parallel()
	r := device.FromLegacy(device.Legacy{GroupMode: ""})
	require.Len(t, r.Items, 1)
	require.Equal(t, device.OutcomeNone, r.Match(device.MatchInput{IsGroup: true, ChatJID: "1@g.us"}).Outcome)
	require.Equal(t, device.OutcomeMatched, r.Match(device.MatchInput{}).Outcome)
}

func TestSetLegacyRules_ConflictsWithCustomRules(t *testing.T) {
	t.Parallel()
	d := newDevice(t)
	custom := device.Rules{Items: []device.Rule{rule("a", cond(device.FieldChatKind, device.OpIs, "dm"), cond(device.FieldText, device.OpContains, "order"))}}
	require.NoError(t, d.SetRules(custom))
	before := d.Rules
	err := d.SetLegacyRules(device.Legacy{GroupMode: device.GroupOpen, AllowedSenders: []string{"628111222333"}})
	require.True(t, device.IsRulesShapeConflictError(err), "got %v", err)
	require.Equal(t, before, d.Rules)
}

func TestSetLegacyRules_CustomGroupModeConflicts(t *testing.T) {
	t.Parallel()
	d := newDevice(t)
	err := d.SetLegacyRules(device.Legacy{GroupMode: device.GroupCustom})
	require.True(t, device.IsRulesShapeConflictError(err), "got %v", err)
}

func TestSetLegacyRules_KeepsStoredNames(t *testing.T) {
	t.Parallel()
	d := newDevice(t)
	r := device.FromLegacy(device.Legacy{GroupMode: device.GroupOpen})
	r.Items[0].Name = "Customers"
	require.NoError(t, d.SetRules(r))
	require.NoError(t, d.SetLegacyRules(device.Legacy{GroupMode: device.GroupMention, AllowedSenders: []string{"628111222333"}}))
	require.Equal(t, "Customers", d.Rules.Items[0].Name)
	l, ok := d.Rules.Legacy()
	require.True(t, ok)
	require.Equal(t, device.GroupMention, l.GroupMode)
}

func TestSetRules_EmptyItemsMeansNothingSent(t *testing.T) {
	t.Parallel()
	d := newDevice(t)
	require.NoError(t, d.SetRules(device.Rules{Items: []device.Rule{}}))
	require.NotNil(t, d.Rules.Items)
	require.Equal(t, device.OutcomeNone, d.Rules.Match(device.MatchInput{Body: "hi"}).Outcome)
}
