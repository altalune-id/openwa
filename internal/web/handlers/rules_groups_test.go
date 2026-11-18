package handlers

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
)

func pairedItems(t *testing.T) []device.Rule {
	t.Helper()
	pair, ferr := builderForms()["dm and toko pair"].rules()
	require.Nil(t, ferr)
	solo, ferr := builderForms()["all groups"].rules()
	require.Nil(t, ferr)
	solo[0].ID = "solo"
	custom := device.Rule{ID: "api", Enabled: true, Action: device.ActionMatch, Conditions: []device.Condition{{Field: device.FieldChatKind, Op: device.OpIsNot, Values: []string{"group"}}}}
	return append(append([]device.Rule{custom}, pair...), solo...)
}

func TestRuleGroups_GroupsAPairAsOne(t *testing.T) {
	t.Parallel()
	groups := ruleGroups(pairedItems(t))
	require.Len(t, groups, 3)
	require.False(t, groups[0].OK, "an API-only shape is a custom row")
	require.Equal(t, 2, groups[1].N)
	require.Equal(t, "orders", groups[1].ID)
	require.Equal(t, []string{"orders", "orders-dm"}, groups[1].IDs)
}

func TestRuleGroups_MoveTogglesAndDeletesActOnBothHalves(t *testing.T) {
	t.Parallel()
	items := pairedItems(t)

	moved, ok := moveGroup(items, "orders", moveUp)
	require.True(t, ok)
	require.Equal(t, []string{"orders", "orders-dm", "api", "solo"}, idsOf(moved))
	moved, ok = moveGroup(items, "orders", moveDown)
	require.True(t, ok)
	require.Equal(t, []string{"api", "solo", "orders", "orders-dm"}, idsOf(moved))
	_, ok = moveGroup(items, "api", moveUp)
	require.False(t, ok, "the first rule cannot move up")
	_, ok = moveGroup(items, "solo", moveDown)
	require.False(t, ok, "the last rule cannot move down")

	toggled, ok := toggleGroup(items, "orders")
	require.True(t, ok)
	require.False(t, toggled[1].Enabled)
	require.False(t, toggled[2].Enabled)
	require.True(t, items[1].Enabled, "the input is not changed")

	deleted, ok := deleteGroup(items, "orders")
	require.True(t, ok)
	require.Equal(t, []string{"api", "solo"}, idsOf(deleted))

	_, ok = deleteGroup(items, "missing")
	require.False(t, ok)
}

func TestRuleGroups_PutReplacesInPlaceOrAppends(t *testing.T) {
	t.Parallel()
	items := pairedItems(t)
	single, ferr := builderForms()["toko and sari"].rules()
	require.Nil(t, ferr)

	got := putGroup(items, "orders", single)
	require.Equal(t, []string{"api", "orders", "solo"}, idsOf(got), "turning DMs off replaces the pair with one rule in place")

	single[0].ID = "fresh"
	got = putGroup(items, "fresh", single)
	require.Equal(t, []string{"api", "orders", "orders-dm", "solo", "fresh"}, idsOf(got))
}

func TestRuleGroups_AllDeletedLeavesNoItems(t *testing.T) {
	t.Parallel()
	items := pairedItems(t)
	for _, id := range []string{"api", "orders", "solo"} {
		var ok bool
		items, ok = deleteGroup(items, id)
		require.True(t, ok)
	}
	require.NotNil(t, items, "an empty, non-nil list is what saves the v2 marker with no items")
	require.Empty(t, items)
	require.Equal(t, device.OutcomeNone, device.Rules{Items: items}.Match(device.MatchInput{Body: "hi"}).Outcome)
}
