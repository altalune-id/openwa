package postgres

import "github.com/go-jet/jet/v2/postgres"

// Contacts is the jet binding for the contacts table.
type Contacts struct {
	postgres.Table

	ID           postgres.ColumnString
	OrgID        postgres.ColumnString
	ProjectID    postgres.ColumnString
	DeviceID     postgres.ColumnString
	JID          postgres.ColumnString
	LID          postgres.ColumnString
	Phone        postgres.ColumnString
	Name         postgres.ColumnString
	PushName     postgres.ColumnString
	BusinessName postgres.ColumnString
	UpdatedAt    postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
}

// NewContacts builds the contacts binding.
func NewContacts(schema, tablePrefix string) *Contacts {
	if schema == "" {
		schema = "public"
	}
	var (
		id           = postgres.StringColumn("id")
		orgID        = postgres.StringColumn("org_id")
		projectID    = postgres.StringColumn("project_id")
		deviceID     = postgres.StringColumn("device_id")
		jid          = postgres.StringColumn("jid")
		lid          = postgres.StringColumn("lid")
		phone        = postgres.StringColumn("phone")
		name         = postgres.StringColumn("name")
		pushName     = postgres.StringColumn("push_name")
		businessName = postgres.StringColumn("business_name")
		updatedAt    = postgres.TimestampzColumn("updated_at")
		all          = postgres.ColumnList{id, orgID, projectID, deviceID, jid, lid, phone, name, pushName, businessName, updatedAt}
	)
	return &Contacts{
		Table:        postgres.NewTable(schema, tablePrefix+"contacts", "contacts", all...),
		ID:           id,
		OrgID:        orgID,
		ProjectID:    projectID,
		DeviceID:     deviceID,
		JID:          jid,
		LID:          lid,
		Phone:        phone,
		Name:         name,
		PushName:     pushName,
		BusinessName: businessName,
		UpdatedAt:    updatedAt,
		AllColumns:   all,
	}
}
