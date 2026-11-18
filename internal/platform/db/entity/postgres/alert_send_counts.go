package postgres

import "github.com/go-jet/jet/v2/postgres"

// AlertSendCounts is the jet binding for the alert_send_counts table.
type AlertSendCounts struct {
	postgres.Table

	ProjectID   postgres.ColumnString
	OrgID       postgres.ColumnString
	Kind        postgres.ColumnString
	WindowStart postgres.ColumnTimestampz
	Sent        postgres.ColumnInteger

	AllColumns postgres.ColumnList
}

// NewAlertSendCounts builds the alert_send_counts binding.
func NewAlertSendCounts(schema, tablePrefix string) *AlertSendCounts {
	if schema == "" {
		schema = "public"
	}
	var (
		projectID   = postgres.StringColumn("project_id")
		orgID       = postgres.StringColumn("org_id")
		kind        = postgres.StringColumn("kind")
		windowStart = postgres.TimestampzColumn("window_start")
		sent        = postgres.IntegerColumn("sent")
		all         = postgres.ColumnList{projectID, orgID, kind, windowStart, sent}
	)
	return &AlertSendCounts{
		Table:       postgres.NewTable(schema, tablePrefix+"alert_send_counts", "alert_send_counts", all...),
		ProjectID:   projectID,
		OrgID:       orgID,
		Kind:        kind,
		WindowStart: windowStart,
		Sent:        sent,
		AllColumns:  all,
	}
}
