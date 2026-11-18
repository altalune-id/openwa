package handlers

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
)

const tokoJID = "120363001@g.us"

func builderForms() map[string]ruleForm {
	sari := []string{"phone:6281111222333"}
	words := []textRow{{Op: device.OpContains, Values: []string{"order", "pesan"}}}
	base := ruleForm{ID: "orders", Name: "Orders", Action: device.ActionMatch, Enabled: true, Who: whoEveryone}
	with := func(mut func(*ruleForm)) ruleForm {
		f := base
		mut(&f)
		return f
	}
	return map[string]ruleForm{
		"dm only":                   with(func(f *ruleForm) { f.DM, f.Groups = true, groupsNone }),
		"dm only from sari":         with(func(f *ruleForm) { f.DM, f.Groups, f.Who, f.Senders = true, groupsNone, whoOnly, sari }),
		"all groups":                with(func(f *ruleForm) { f.Groups = groupsAll }),
		"all groups when mentioned": with(func(f *ruleForm) { f.Groups, f.Mention = groupsAll, true }),
		"toko and sari":             with(func(f *ruleForm) { f.Groups, f.Chats, f.Who, f.Senders = groupsThese, []string{tokoJID}, whoOnly, sari }),
		"toko except sari, words": with(func(f *ruleForm) {
			f.Groups, f.Chats, f.Who, f.Senders, f.Texts = groupsThese, []string{tokoJID}, whoExcept, sari, words
		}),
		"dm and toko pair":       with(func(f *ruleForm) { f.DM, f.Groups, f.Chats, f.Texts = true, groupsThese, []string{tokoJID}, words }),
		"dm and all groups pair": with(func(f *ruleForm) { f.DM, f.Groups, f.Mention, f.Action = true, groupsAll, true, device.ActionIgnore }),
		"paused":                 with(func(f *ruleForm) { f.Groups, f.Enabled = groupsAll, false }),
	}
}

func TestRuleForm_RoundTripsEveryBuilderShape(t *testing.T) {
	t.Parallel()
	for name, f := range builderForms() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rules, ferr := f.rules()
			require.Nil(t, ferr)
			back, n, ok := formAt(rules, 0)
			require.True(t, ok)
			require.Equal(t, len(rules), n)
			require.Equal(t, f, back)
		})
	}
}

func TestRuleForm_DMWithGroupsSavesAnAdjacentPair(t *testing.T) {
	t.Parallel()
	rules, ferr := builderForms()["dm and toko pair"].rules()
	require.Nil(t, ferr)
	require.Len(t, rules, 2)
	require.Equal(t, "orders", rules[0].ID)
	require.Equal(t, "orders-dm", rules[1].ID)
	require.Equal(t, device.Condition{Field: device.FieldChatKind, Op: device.OpIs, Values: []string{"group"}}, rules[0].Conditions[0])
	require.Equal(t, device.Condition{Field: device.FieldChatKind, Op: device.OpIs, Values: []string{"dm"}}, rules[1].Conditions[0])
	require.Equal(t, rules[0].Conditions[2:], rules[1].Conditions[1:], "both halves share From and Text")
}

func TestRuleForm_LegacyRulesStayLegacyAfterASave(t *testing.T) {
	t.Parallel()
	legacy := device.FromLegacy(device.Legacy{GroupMode: device.GroupMention, AllowedSenders: []string{"6281111222333"}, AllowedGroups: []string{tokoJID}, TriggerPrefix: "!"})
	for i := range legacy.Items {
		f, n, ok := formAt(legacy.Items, i)
		require.True(t, ok, legacy.Items[i].ID)
		require.Equal(t, 1, n)
		rules, ferr := f.rules()
		require.Nil(t, ferr)
		require.Equal(t, legacy.Items[i:i+1], rules, "a builder save keeps the legacy shape, so REST and RPC legacy reads stay expressible")
	}
}

func TestRuleForm_BrokenPairsReadAsSeparateRules(t *testing.T) {
	t.Parallel()
	pair, _ := builderForms()["dm and toko pair"].rules()
	breakers := map[string]func([]device.Rule){
		"different text":    func(r []device.Rule) { r[1].Conditions[1].Values = []string{"other"} },
		"different action":  func(r []device.Rule) { r[1].Action = device.ActionIgnore },
		"different enabled": func(r []device.Rule) { r[1].Enabled = false },
		"different id":      func(r []device.Rule) { r[1].ID = "other-dm" },
		"extra dm condition": func(r []device.Rule) {
			r[1].Conditions = append(r[1].Conditions, device.Condition{Field: device.FieldMention})
		},
	}
	for name, breakIt := range breakers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rules := cloneRules(pair)
			breakIt(rules)
			_, n, _ := formAt(rules, 0)
			require.Equal(t, 1, n, "a broken pair is not grouped")
		})
	}
}

func TestRuleForm_APIOnlyShapesAreCustom(t *testing.T) {
	t.Parallel()
	kind := func(v ...string) device.Condition {
		return device.Condition{Field: device.FieldChatKind, Op: device.OpIs, Values: v}
	}
	shapes := map[string][]device.Condition{
		"no chat kind":            {{Field: device.FieldText, Op: device.OpContains, Values: []string{"x"}}},
		"chat_kind is_not":        {{Field: device.FieldChatKind, Op: device.OpIsNot, Values: []string{"group"}}},
		"two chat kinds":          {kind("group"), kind("group")},
		"chat is_not":             {kind("group"), {Field: device.FieldChat, Op: device.OpIsNot, Values: []string{tokoJID}}},
		"chat in a dm rule":       {kind("dm"), {Field: device.FieldChat, Op: device.OpIs, Values: []string{tokoJID}}},
		"mention in a dm rule":    {kind("dm"), {Field: device.FieldMention}},
		"mention with dm and grp": {kind("dm", "group"), {Field: device.FieldMention}},
		"two sender rows":         {kind("dm"), {Field: device.FieldSender, Op: device.OpIs, Values: []string{"phone:6281111222333"}}, {Field: device.FieldSender, Op: device.OpIs, Values: []string{"phone:6289999888777"}}},
	}
	for name, conds := range shapes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, n, ok := formAt([]device.Rule{{ID: "a", Enabled: true, Action: device.ActionMatch, Conditions: conds}}, 0)
			require.False(t, ok)
			require.Equal(t, 1, n)
		})
	}
}

func TestRuleForm_DMAndAllGroupsFromRESTReadsAsThePair(t *testing.T) {
	t.Parallel()
	f, _, ok := formAt([]device.Rule{{ID: "a", Enabled: true, Action: device.ActionMatch, Conditions: []device.Condition{{Field: device.FieldChatKind, Op: device.OpIs, Values: []string{"dm", "group"}}}}}, 0)
	require.True(t, ok)
	require.True(t, f.DM)
	require.Equal(t, groupsAll, f.Groups)
}

func TestRuleForm_InvalidBlocks(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		form  ruleForm
		block string
	}{
		"nowhere":          {ruleForm{Groups: groupsNone, Who: whoEveryone}, blockWhere},
		"these, no groups": {ruleForm{Groups: groupsThese, Who: whoEveryone}, blockWhere},
		"only, nobody":     {ruleForm{Groups: groupsAll, Who: whoOnly}, blockWho},
		"except, nobody":   {ruleForm{Groups: groupsAll, Who: whoExcept}, blockWho},
		"empty text row":   {ruleForm{Groups: groupsAll, Who: whoEveryone, Texts: []textRow{{Op: device.OpContains, Values: []string{"a"}}, {Op: device.OpEquals}}}, "t1"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tc.form.ID, tc.form.Action = "a", device.ActionMatch
			_, ferr := tc.form.rules()
			require.NotNil(t, ferr)
			require.Equal(t, tc.block, ferr.Block)
		})
	}
}

func cloneRules(in []device.Rule) []device.Rule {
	out := make([]device.Rule, len(in))
	for i, r := range in {
		r.Conditions = slices.Clone(r.Conditions)
		for j := range r.Conditions {
			r.Conditions[j].Values = slices.Clone(r.Conditions[j].Values)
		}
		out[i] = r
	}
	return out
}
