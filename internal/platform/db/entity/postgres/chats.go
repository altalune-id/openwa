package postgres

import "github.com/go-jet/jet/v2/postgres"

// Chats is the jet binding for the chats table.
type Chats struct {
	postgres.Table

	ID                 postgres.ColumnString
	PublicID           postgres.ColumnString
	OrgID              postgres.ColumnString
	ProjectID          postgres.ColumnString
	DeviceID           postgres.ColumnString
	JID                postgres.ColumnString
	LID                postgres.ColumnString
	Kind               postgres.ColumnString
	Name               postgres.ColumnString
	LastMessageAt      postgres.ColumnTimestampz
	LastMessagePreview postgres.ColumnString
	UnreadCount        postgres.ColumnInteger
	Archived           postgres.ColumnBool
	ActivityAt         postgres.ColumnTimestampz
	Version            postgres.ColumnInteger
	CreatedAt          postgres.ColumnTimestampz
	UpdatedAt          postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
	// MutableColumns omits the generated activity_at, which an INSERT must never name.
	MutableColumns postgres.ColumnList
}

// NewChats builds the chats binding.
func NewChats(schema, tablePrefix string) *Chats {
	if schema == "" {
		schema = "public"
	}
	var (
		id                 = postgres.StringColumn("id")
		publicID           = postgres.StringColumn("public_id")
		orgID              = postgres.StringColumn("org_id")
		projectID          = postgres.StringColumn("project_id")
		deviceID           = postgres.StringColumn("device_id")
		jid                = postgres.StringColumn("jid")
		lid                = postgres.StringColumn("lid")
		kind               = postgres.StringColumn("kind")
		name               = postgres.StringColumn("name")
		lastMessageAt      = postgres.TimestampzColumn("last_message_at")
		lastMessagePreview = postgres.StringColumn("last_message_preview")
		unreadCount        = postgres.IntegerColumn("unread_count")
		archived           = postgres.BoolColumn("archived")
		activityAt         = postgres.TimestampzColumn("activity_at")
		version            = postgres.IntegerColumn("version")
		createdAt          = postgres.TimestampzColumn("created_at")
		updatedAt          = postgres.TimestampzColumn("updated_at")
		mutable            = postgres.ColumnList{id, publicID, orgID, projectID, deviceID, jid, lid, kind, name, lastMessageAt, lastMessagePreview, unreadCount, archived, version, createdAt, updatedAt}
		all                = postgres.ColumnList{id, publicID, orgID, projectID, deviceID, jid, lid, kind, name, lastMessageAt, lastMessagePreview, unreadCount, archived, activityAt, version, createdAt, updatedAt}
	)
	return &Chats{
		Table:              postgres.NewTable(schema, tablePrefix+"chats", "chats", all...),
		ID:                 id,
		PublicID:           publicID,
		OrgID:              orgID,
		ProjectID:          projectID,
		DeviceID:           deviceID,
		JID:                jid,
		LID:                lid,
		Kind:               kind,
		Name:               name,
		LastMessageAt:      lastMessageAt,
		LastMessagePreview: lastMessagePreview,
		UnreadCount:        unreadCount,
		Archived:           archived,
		ActivityAt:         activityAt,
		Version:            version,
		CreatedAt:          createdAt,
		UpdatedAt:          updatedAt,
		AllColumns:         all,
		MutableColumns:     mutable,
	}
}
