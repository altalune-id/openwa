package postgres

import "github.com/go-jet/jet/v2/postgres"

// AlertOptouts is the jet binding for the alert_optouts table.
type AlertOptouts struct {
	postgres.Table

	ProjectID postgres.ColumnString
	OrgID     postgres.ColumnString
	Email     postgres.ColumnString
	CreatedAt postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
}

// NewAlertOptouts builds the alert_optouts binding.
func NewAlertOptouts(schema, tablePrefix string) *AlertOptouts {
	if schema == "" {
		schema = "public"
	}
	var (
		projectID = postgres.StringColumn("project_id")
		orgID     = postgres.StringColumn("org_id")
		email     = postgres.StringColumn("email")
		createdAt = postgres.TimestampzColumn("created_at")
		all       = postgres.ColumnList{projectID, orgID, email, createdAt}
	)
	return &AlertOptouts{
		Table:      postgres.NewTable(schema, tablePrefix+"alert_optouts", "alert_optouts", all...),
		ProjectID:  projectID,
		OrgID:      orgID,
		Email:      email,
		CreatedAt:  createdAt,
		AllColumns: all,
	}
}
