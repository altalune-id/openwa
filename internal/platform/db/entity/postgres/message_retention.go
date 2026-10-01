package postgres

import "github.com/go-jet/jet/v2/postgres"

// MessageRetention is the jet binding for the message_retention table.
type MessageRetention struct {
	postgres.Table

	ProjectID postgres.ColumnString
	OrgID     postgres.ColumnString
	Days      postgres.ColumnInteger
	UpdatedAt postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
}

// NewMessageRetention builds the message_retention binding.
func NewMessageRetention(schema, tablePrefix string) *MessageRetention {
	if schema == "" {
		schema = "public"
	}
	var (
		projectID = postgres.StringColumn("project_id")
		orgID     = postgres.StringColumn("org_id")
		days      = postgres.IntegerColumn("days")
		updatedAt = postgres.TimestampzColumn("updated_at")
		all       = postgres.ColumnList{projectID, orgID, days, updatedAt}
	)
	return &MessageRetention{
		Table:      postgres.NewTable(schema, tablePrefix+"message_retention", "message_retention", all...),
		ProjectID:  projectID,
		OrgID:      orgID,
		Days:       days,
		UpdatedAt:  updatedAt,
		AllColumns: all,
	}
}
