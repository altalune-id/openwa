package postgres

import "github.com/go-jet/jet/v2/postgres"

// APIKeys is the jet binding for the api_keys table.
type APIKeys struct {
	postgres.Table

	ID          postgres.ColumnString
	OrgID       postgres.ColumnString
	ProjectID   postgres.ColumnString
	Name        postgres.ColumnString
	SecretHash  postgres.ColumnBytea
	Scopes      postgres.ColumnString
	ResourceIDs postgres.ColumnString
	CreatedAt   postgres.ColumnTimestampz
	ExpiresAt   postgres.ColumnTimestampz
	RevokedAt   postgres.ColumnTimestampz
	LastUsedAt  postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
}

// NewAPIKeys builds the api_keys binding.
func NewAPIKeys(schema, tablePrefix string) *APIKeys {
	if schema == "" {
		schema = "public"
	}
	var (
		id          = postgres.StringColumn("id")
		orgID       = postgres.StringColumn("org_id")
		projectID   = postgres.StringColumn("project_id")
		name        = postgres.StringColumn("name")
		secretHash  = postgres.ByteaColumn("secret_hash")
		scopes      = postgres.StringColumn("scopes")
		resourceIDs = postgres.StringColumn("resource_ids")
		createdAt   = postgres.TimestampzColumn("created_at")
		expiresAt   = postgres.TimestampzColumn("expires_at")
		revokedAt   = postgres.TimestampzColumn("revoked_at")
		lastUsedAt  = postgres.TimestampzColumn("last_used_at")
		all         = postgres.ColumnList{id, orgID, projectID, name, secretHash, scopes, resourceIDs, createdAt, expiresAt, revokedAt, lastUsedAt}
	)
	return &APIKeys{
		Table:       postgres.NewTable(schema, tablePrefix+"api_keys", "api_keys", all...),
		ID:          id,
		OrgID:       orgID,
		ProjectID:   projectID,
		Name:        name,
		SecretHash:  secretHash,
		Scopes:      scopes,
		ResourceIDs: resourceIDs,
		CreatedAt:   createdAt,
		ExpiresAt:   expiresAt,
		RevokedAt:   revokedAt,
		LastUsedAt:  lastUsedAt,
		AllColumns:  all,
	}
}
