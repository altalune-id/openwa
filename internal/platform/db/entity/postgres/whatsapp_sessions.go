package postgres

import "github.com/go-jet/jet/v2/postgres"

// WhatsappSessions is the jet binding for the whatsapp_sessions table.
type WhatsappSessions struct {
	postgres.Table

	DeviceID        postgres.ColumnString
	OrgID           postgres.ColumnString
	ProjectID       postgres.ColumnString
	Engine          postgres.ColumnString
	JID             postgres.ColumnString
	LID             postgres.ColumnString
	Phone           postgres.ColumnString
	PushName        postgres.ColumnString
	Platform        postgres.ColumnString
	State           postgres.ColumnString
	Reason          postgres.ColumnString
	LastConnectedAt postgres.ColumnTimestampz
	LastSeenAt      postgres.ColumnTimestampz
	LastError       postgres.ColumnString
	Version         postgres.ColumnInteger
	UpdatedAt       postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
}

// NewWhatsappSessions builds the whatsapp_sessions binding.
func NewWhatsappSessions(schema, tablePrefix string) *WhatsappSessions {
	if schema == "" {
		schema = "public"
	}
	var (
		deviceID        = postgres.StringColumn("device_id")
		orgID           = postgres.StringColumn("org_id")
		projectID       = postgres.StringColumn("project_id")
		engine          = postgres.StringColumn("engine")
		jid             = postgres.StringColumn("jid")
		lid             = postgres.StringColumn("lid")
		phone           = postgres.StringColumn("phone")
		pushName        = postgres.StringColumn("push_name")
		platform        = postgres.StringColumn("platform")
		state           = postgres.StringColumn("state")
		reason          = postgres.StringColumn("reason")
		lastConnectedAt = postgres.TimestampzColumn("last_connected_at")
		lastSeenAt      = postgres.TimestampzColumn("last_seen_at")
		lastError       = postgres.StringColumn("last_error")
		version         = postgres.IntegerColumn("version")
		updatedAt       = postgres.TimestampzColumn("updated_at")
		all             = postgres.ColumnList{deviceID, orgID, projectID, engine, jid, lid, phone, pushName, platform, state, reason, lastConnectedAt, lastSeenAt, lastError, version, updatedAt}
	)
	return &WhatsappSessions{
		Table:           postgres.NewTable(schema, tablePrefix+"whatsapp_sessions", "whatsapp_sessions", all...),
		DeviceID:        deviceID,
		OrgID:           orgID,
		ProjectID:       projectID,
		Engine:          engine,
		JID:             jid,
		LID:             lid,
		Phone:           phone,
		PushName:        pushName,
		Platform:        platform,
		State:           state,
		Reason:          reason,
		LastConnectedAt: lastConnectedAt,
		LastSeenAt:      lastSeenAt,
		LastError:       lastError,
		Version:         version,
		UpdatedAt:       updatedAt,
		AllColumns:      all,
	}
}
