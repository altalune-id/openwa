package postgres

import "github.com/go-jet/jet/v2/postgres"

// WhatsappLeases is the jet binding for the whatsapp_leases table.
type WhatsappLeases struct {
	postgres.Table

	DeviceID  postgres.ColumnString
	OrgID     postgres.ColumnString
	ProjectID postgres.ColumnString
	JID       postgres.ColumnString
	Owner     postgres.ColumnString
	ExpiresAt postgres.ColumnTimestampz
	UpdatedAt postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
}

// NewWhatsappLeases builds the whatsapp_leases binding.
func NewWhatsappLeases(schema, tablePrefix string) *WhatsappLeases {
	if schema == "" {
		schema = "public"
	}
	var (
		deviceID  = postgres.StringColumn("device_id")
		orgID     = postgres.StringColumn("org_id")
		projectID = postgres.StringColumn("project_id")
		jid       = postgres.StringColumn("jid")
		owner     = postgres.StringColumn("owner")
		expiresAt = postgres.TimestampzColumn("expires_at")
		updatedAt = postgres.TimestampzColumn("updated_at")
		all       = postgres.ColumnList{deviceID, orgID, projectID, jid, owner, expiresAt, updatedAt}
	)
	return &WhatsappLeases{
		Table:      postgres.NewTable(schema, tablePrefix+"whatsapp_leases", "whatsapp_leases", all...),
		DeviceID:   deviceID,
		OrgID:      orgID,
		ProjectID:  projectID,
		JID:        jid,
		Owner:      owner,
		ExpiresAt:  expiresAt,
		UpdatedAt:  updatedAt,
		AllColumns: all,
	}
}
