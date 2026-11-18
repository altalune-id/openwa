package handlers

import (
	"slices"

	"altalune.id/openwa/internal/device"
)

const (
	moveUp   = "up"
	moveDown = "down"
)

type ruleGroup struct {
	At   int
	N    int
	ID   string
	IDs  []string
	Form ruleForm
	OK   bool
}

func ruleGroups(items []device.Rule) []ruleGroup {
	var out []ruleGroup
	for i := 0; i < len(items); {
		f, n, ok := formAt(items, i)
		g := ruleGroup{At: i, N: n, ID: items[i].ID, Form: f, OK: ok}
		for _, r := range items[i : i+n] {
			g.IDs = append(g.IDs, r.ID)
		}
		out = append(out, g)
		i += n
	}
	return out
}

func groupIndex(groups []ruleGroup, id string) int {
	return slices.IndexFunc(groups, func(g ruleGroup) bool { return g.ID == id })
}

func moveGroup(items []device.Rule, id, dir string) ([]device.Rule, bool) {
	groups := ruleGroups(items)
	i := groupIndex(groups, id)
	j := i - 1
	if dir == moveDown {
		j = i + 1
	}
	if i < 0 || j < 0 || j >= len(groups) {
		return nil, false
	}
	a, b := groups[min(i, j)], groups[max(i, j)]
	return slices.Concat(items[:a.At], items[b.At:b.At+b.N], items[a.At:a.At+a.N], items[b.At+b.N:]), true
}

func toggleGroup(items []device.Rule, id string) ([]device.Rule, bool) {
	groups := ruleGroups(items)
	i := groupIndex(groups, id)
	if i < 0 {
		return nil, false
	}
	out := slices.Clone(items)
	g := groups[i]
	for k := g.At; k < g.At+g.N; k++ {
		out[k].Enabled = !items[g.At].Enabled
	}
	return out, true
}

func deleteGroup(items []device.Rule, id string) ([]device.Rule, bool) {
	groups := ruleGroups(items)
	i := groupIndex(groups, id)
	if i < 0 {
		return nil, false
	}
	g := groups[i]
	out := make([]device.Rule, 0, len(items)-g.N)
	return append(append(out, items[:g.At]...), items[g.At+g.N:]...), true
}

func putGroup(items []device.Rule, id string, rules []device.Rule) []device.Rule {
	groups := ruleGroups(items)
	i := groupIndex(groups, id)
	if i < 0 {
		return slices.Concat(items, rules)
	}
	g := groups[i]
	return slices.Concat(items[:g.At], rules, items[g.At+g.N:])
}
