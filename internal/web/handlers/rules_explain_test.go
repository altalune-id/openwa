package handlers

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
)

func TestExplainResult_NamesTheDecidingRuleOrTheFailingBlock(t *testing.T) {
	t.Parallel()
	kind := func(v ...string) device.Condition {
		return device.Condition{Field: device.FieldChatKind, Op: device.OpIs, Values: v}
	}
	text := func(op device.Op, v ...string) device.Condition {
		return device.Condition{Field: device.FieldText, Op: op, Values: v}
	}
	draft := func(conds ...device.Condition) device.Rule {
		return device.Rule{ID: "d", Name: "Draft", Enabled: true, Action: device.ActionMatch, Conditions: conds}
	}
	group := device.MatchInput{IsGroup: true, ChatJID: "1@g.us", Body: "order one", SenderPhone: "6281111222333"}
	dm := device.MatchInput{Body: "order one", SenderPhone: "6281111222333"}
	cases := []struct {
		name  string
		items []device.Rule
		ids   []string
		in    device.MatchInput
		key   string
		block string
		ok    bool
	}{
		{"draft decides", []device.Rule{draft(kind("group"))}, []string{"d"}, group, "rules.test.sent", "", true},
		{"draft ignores", []device.Rule{{ID: "d", Enabled: true, Action: device.ActionIgnore, Conditions: []device.Condition{kind("group")}}}, []string{"d"}, group, "rules.test.ignored", "", true},
		{"paused", []device.Rule{{ID: "d", Enabled: false, Action: device.ActionMatch, Conditions: []device.Condition{kind("group")}}}, []string{"d"}, group, "rules.test.paused", "", false},
		{"earlier rule sends", []device.Rule{{ID: "e", Name: "Earlier", Enabled: true, Action: device.ActionMatch, Conditions: []device.Condition{kind("group")}}, draft(kind("group"))}, []string{"d"}, group, "rules.test.shadowed_sent", "", false},
		{"earlier rule ignores", []device.Rule{{ID: "e", Enabled: true, Action: device.ActionIgnore, Conditions: []device.Condition{kind("group")}}, draft(kind("group"))}, []string{"d"}, group, "rules.test.shadowed_ignored", "", false},
		{"dm not admitted", []device.Rule{draft(text(device.OpContains, "order"))}, []string{"d"}, dm, "rules.test.dm_off", blockWhere, false},
		{"group rule in a dm", []device.Rule{draft(kind("group"))}, []string{"d"}, dm, "rules.test.dm_off", blockWhere, false},
		{"dm rule in a group", []device.Rule{draft(kind("dm"))}, []string{"d"}, group, "rules.test.no_groups", blockWhere, false},
		{"other group", []device.Rule{draft(kind("group"), device.Condition{Field: device.FieldChat, Op: device.OpIs, Values: []string{"2@g.us"}})}, []string{"d"}, group, "rules.test.not_group", blockWhere, false},
		{"mention needed", []device.Rule{draft(kind("group"), device.Condition{Field: device.FieldMention})}, []string{"d"}, group, "rules.test.mention_only", blockWhere, false},
		{"not in only", []device.Rule{draft(kind("group"), device.Condition{Field: device.FieldSender, Op: device.OpIs, Values: []string{"phone:6289999888777"}})}, []string{"d"}, group, "rules.test.not_only", blockWho, false},
		{"excluded", []device.Rule{draft(kind("group"), device.Condition{Field: device.FieldSender, Op: device.OpIsNot, Values: []string{"phone:6281111222333"}})}, []string{"d"}, group, "rules.test.excluded", blockWho, false},
		{"first text row fails", []device.Rule{draft(kind("group"), text(device.OpStartsWith, "promo"), text(device.OpContains, "order"))}, []string{"d"}, group, "rules.test.text_needs", "t0", false},
		{"second text row fails", []device.Rule{draft(kind("group"), text(device.OpContains, "order"), text(device.OpEquals, "pesan"))}, []string{"d"}, group, "rules.test.text_needs", "t1", false},
		{"pair: dm half answers a dm", []device.Rule{draft(kind("group")), {ID: "d-dm", Enabled: true, Action: device.ActionMatch, Conditions: []device.Condition{kind("dm"), text(device.OpEquals, "x")}}}, []string{"d", "d-dm"}, dm, "rules.test.text_needs", "t0", false},
	}
	tr := func(key string, _ ...any) string { return key }
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := device.Rules{Items: tc.items}.Explain(tc.in)
			got := explainResult(tr, e, tc.items, tc.ids, tc.in, "Rina", ruleLabels{})
			require.Equal(t, tc.key, got.Message)
			require.Equal(t, tc.block, got.Block)
			require.Equal(t, tc.ok, got.OK)
		})
	}
}
