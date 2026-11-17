package web

//i18n:use tips.*

// Tips returns the registry of every tip the server may render or mark as seen.
func Tips() []Tip {
	return []Tip{
		{ID: "connect-number", Flow: "home", TextKey: "tips.connect_number", Target: "connect"},
	}
}
