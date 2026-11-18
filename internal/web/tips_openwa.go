package web

//i18n:use tips.*

// Tips returns the registry of every tip the server may render or mark as seen.
func Tips() []Tip {
	return []Tip{
		{ID: "connect-number", Flow: "home", TextKey: "tips.connect_number", Target: "connect"},
		{ID: "rules-where", Flow: "rules", TextKey: "tips.rules_where", Target: "rules-where"},
		{ID: "rules-groups", Flow: "rules", TextKey: "tips.rules_groups", Target: "rules-groups"},
		{ID: "rules-test", Flow: "rules", TextKey: "tips.rules_test", Target: "rules-test"},
		{ID: "fix-it", Flow: "number", TextKey: "tips.fix_it", Target: "fix"},
	}
}
