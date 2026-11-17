package templates

const brandIcon = "openwa-mark-16.svg"

type navItem struct {
	Key      string
	Path     string
	Icon     string
	LabelKey string
	Org      bool
}

type navGroup struct {
	LabelKey  string
	Collapsed bool
	Items     []navItem
}

//i18n:use nav.home
//i18n:use nav.group_messages
//i18n:use nav.numbers
//i18n:use nav.inbox
//i18n:use nav.contacts
//i18n:use nav.group_developers
//i18n:use nav.webhooks
//i18n:use nav.apikeys
//i18n:use nav.usage
//i18n:use nav.organization
func projectNav() []navGroup {
	return []navGroup{
		{Items: []navItem{{Key: "overview", Path: "/overview", Icon: "house", LabelKey: "nav.home"}}},
		{LabelKey: "nav.group_messages", Items: []navItem{
			{Key: "devices", Path: "/devices", Icon: "smartphone", LabelKey: "nav.numbers"},
			{Key: "inbox", Path: "/inbox", Icon: "inbox", LabelKey: "nav.inbox"},
			{Key: "contacts", Path: "/contacts", Icon: "book-user", LabelKey: "nav.contacts"},
		}},
		{LabelKey: "nav.group_developers", Collapsed: true, Items: []navItem{
			{Key: "webhooks", Path: "/webhooks", Icon: "webhook", LabelKey: "nav.webhooks"},
			{Key: "apikeys", Path: "/apikeys", Icon: "key-round", LabelKey: "nav.apikeys"},
		}},
	}
}

func projectFooterNav() []navItem {
	return []navItem{
		{Key: "usage", Path: "/usage", Icon: "chart-column", LabelKey: "nav.usage"},
		{Key: "organization", Icon: "building-2", LabelKey: "nav.organization", Org: true},
		{Key: "settings", Path: "/settings", Icon: "settings", LabelKey: "nav.settings"},
	}
}
