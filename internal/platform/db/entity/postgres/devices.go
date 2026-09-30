package postgres

import "github.com/go-jet/jet/v2/postgres"

// Devices is the jet binding for the devices table.
type Devices struct {
	postgres.Table

	ID        postgres.ColumnString
	PublicID  postgres.ColumnString
	OrgID     postgres.ColumnString
	ProjectID postgres.ColumnString
	Name      postgres.ColumnString
	Rules     postgres.ColumnString
	Version   postgres.ColumnInteger
	CreatedAt postgres.ColumnTimestampz
	UpdatedAt postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
}

// NewDevices builds the devices binding.
func NewDevices(schema, tablePrefix string) *Devices {
	if schema == "" {
		schema = "public"
	}
	var (
		id        = postgres.StringColumn("id")
		publicID  = postgres.StringColumn("public_id")
		orgID     = postgres.StringColumn("org_id")
		projectID = postgres.StringColumn("project_id")
		name      = postgres.StringColumn("name")
		rules     = postgres.StringColumn("rules")
		version   = postgres.IntegerColumn("version")
		createdAt = postgres.TimestampzColumn("created_at")
		updatedAt = postgres.TimestampzColumn("updated_at")
		all       = postgres.ColumnList{id, publicID, orgID, projectID, name, rules, version, createdAt, updatedAt}
	)
	return &Devices{
		Table:      postgres.NewTable(schema, tablePrefix+"devices", "devices", all...),
		ID:         id,
		PublicID:   publicID,
		OrgID:      orgID,
		ProjectID:  projectID,
		Name:       name,
		Rules:      rules,
		Version:    version,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
		AllColumns: all,
	}
}
