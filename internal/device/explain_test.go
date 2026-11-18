package device_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
)

func TestRules_ExplainAgreesWithMatch(t *testing.T) {
	t.Parallel()
	dm := cond(device.FieldChatKind, device.OpIs, "dm")
	group := cond(device.FieldChatKind, device.OpIs, "group")
	sets := []device.Rules{
		{Items: []device.Rule{}},
		{Items: []device.Rule{rule("a", cond(device.FieldText, device.OpContains, "hi"))}},
		{Items: []device.Rule{rule("a", group, cond(device.FieldMention, "")), rule("b", group)}},
		{Items: []device.Rule{{ID: "a", Enabled: false, Action: device.ActionMatch, Conditions: []device.Condition{dm}}, rule("b", dm)}},
		{Items: []device.Rule{{ID: "a", Enabled: true, Action: device.ActionIgnore, Conditions: []device.Condition{dm, cond(device.FieldText, device.OpContains, "promo")}}, rule("b", dm)}},
		{IgnoreFromMe: true, Items: []device.Rule{rule("a", dm)}},
	}
	inputs := []device.MatchInput{
		{Body: "hi"},
		{Body: "promo"},
		{IsGroup: true, ChatJID: "1@g.us", Body: "hi"},
		{IsGroup: true, ChatJID: "1@g.us", MentionedMe: true},
		{FromMe: true},
	}
	for _, rs := range sets {
		for _, in := range inputs {
			require.Equal(t, rs.Match(in), rs.Explain(in).Result)
		}
	}
}

func TestRules_ExplainNamesTheFirstFailingCondition(t *testing.T) {
	t.Parallel()
	toko := "120363001@g.us"
	sari := device.Condition{Field: device.FieldSender, Op: device.OpIs, Values: []string{"phone:6281111222333"}}
	draft := rule("toko", cond(device.FieldChatKind, device.OpIs, "group"), cond(device.FieldChat, device.OpIs, toko), sari)
	rs := device.Rules{Items: []device.Rule{draft}}
	cases := []struct {
		name    string
		in      device.MatchInput
		outcome device.Outcome
		failed  int
		dm      bool
	}{
		{"wife in the group is sent", device.MatchInput{IsGroup: true, ChatJID: toko, SenderPhone: "6281111222333"}, device.OutcomeMatched, -1, false},
		{"courier in the group fails the sender row", device.MatchInput{IsGroup: true, ChatJID: toko, SenderPhone: "6289999888777"}, device.OutcomeNone, 2, false},
		{"wife in a dm fails the chat kind", device.MatchInput{SenderPhone: "6281111222333"}, device.OutcomeNone, 0, false},
		{"another group fails the chat row", device.MatchInput{IsGroup: true, ChatJID: "999@g.us", SenderPhone: "6281111222333"}, device.OutcomeNone, 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := rs.Explain(tc.in)
			require.Equal(t, tc.outcome, got.Result.Outcome)
			require.Len(t, got.Checks, 1)
			require.Equal(t, "toko", got.Checks[0].RuleID)
			require.Equal(t, tc.failed, got.Checks[0].Failed)
			require.Equal(t, tc.failed < 0, got.Checks[0].Held)
			require.Equal(t, tc.dm, got.Checks[0].DMNotAdmitted)
		})
	}
}

func TestRules_ExplainMarksADMRuleWithoutChatKind(t *testing.T) {
	t.Parallel()
	got := device.Rules{Items: []device.Rule{rule("a", cond(device.FieldText, device.OpContains, "hi"))}}.Explain(device.MatchInput{Body: "hi"})
	require.Equal(t, device.OutcomeNone, got.Result.Outcome)
	require.Equal(t, -1, got.Checks[0].Failed)
	require.True(t, got.Checks[0].DMNotAdmitted)
	require.False(t, got.Checks[0].Held)
}

func TestRules_ExplainChecksEveryRuleAndSkipsDisabled(t *testing.T) {
	t.Parallel()
	dm := cond(device.FieldChatKind, device.OpIs, "dm")
	rs := device.Rules{Items: []device.Rule{
		{ID: "off", Enabled: false, Action: device.ActionMatch, Conditions: []device.Condition{dm}},
		{ID: "spam", Enabled: true, Action: device.ActionIgnore, Conditions: []device.Condition{dm}},
		rule("after", dm),
	}}
	got := rs.Explain(device.MatchInput{Body: "x"})
	require.Equal(t, device.OutcomeIgnored, got.Result.Outcome)
	require.Equal(t, "spam", got.Result.RuleID)
	require.Len(t, got.Checks, 3)
	require.False(t, got.Checks[0].Enabled)
	require.False(t, got.Checks[0].Held, "a disabled rule never holds")
	require.True(t, got.Checks[1].Held)
	require.True(t, got.Checks[2].Held, "a rule after the decider is still checked, so a draft shadowed by an earlier rule shows it would have held")
}

func TestValidRuleID(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"a", "orders-dm", "legacy-dm", "legacy-group", "A_1-b", strings.Repeat("a", 64)} {
		require.True(t, device.ValidRuleID(ok), ok)
	}
	for _, bad := range []string{"", "../delete", "a b", "a/b", "legacy-x", strings.Repeat("a", 65), "é"} {
		require.False(t, device.ValidRuleID(bad), bad)
	}
}
