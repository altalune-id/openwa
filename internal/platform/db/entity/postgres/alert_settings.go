package postgres

import "github.com/go-jet/jet/v2/postgres"

// AlertSettings is the jet binding for the alert_settings table.
type AlertSettings struct {
	postgres.Table

	ProjectID         postgres.ColumnString
	OrgID             postgres.ColumnString
	DisconnectEnabled postgres.ColumnBool
	Recipients        postgres.ColumnString
	UpdatedAt         postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
}

// NewAlertSettings builds the alert_settings binding.
func NewAlertSettings(schema, tablePrefix string) *AlertSettings {
	if schema == "" {
		schema = "public"
	}
	var (
		projectID         = postgres.StringColumn("project_id")
		orgID             = postgres.StringColumn("org_id")
		disconnectEnabled = postgres.BoolColumn("disconnect_enabled")
		recipients        = postgres.StringColumn("recipients")
		updatedAt         = postgres.TimestampzColumn("updated_at")
		all               = postgres.ColumnList{projectID, orgID, disconnectEnabled, recipients, updatedAt}
	)
	return &AlertSettings{
		Table:             postgres.NewTable(schema, tablePrefix+"alert_settings", "alert_settings", all...),
		ProjectID:         projectID,
		OrgID:             orgID,
		DisconnectEnabled: disconnectEnabled,
		Recipients:        recipients,
		UpdatedAt:         updatedAt,
		AllColumns:        all,
	}
}
