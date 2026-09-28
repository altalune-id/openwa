package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// APIKeys is the jet binding for the api_keys table.
type APIKeys struct {
	sqlite.Table

	ID          sqlite.ColumnString
	OrgID       sqlite.ColumnString
	ProjectID   sqlite.ColumnString
	Name        sqlite.ColumnString
	SecretHash  sqlite.ColumnBlob
	Scopes      sqlite.ColumnString
	ResourceIDs sqlite.ColumnString
	CreatedAt   sqlite.ColumnString
	ExpiresAt   sqlite.ColumnString
	RevokedAt   sqlite.ColumnString
	LastUsedAt  sqlite.ColumnString

	AllColumns sqlite.ColumnList
}

// NewAPIKeys builds the api_keys binding.
func NewAPIKeys(tablePrefix string) *APIKeys {
	var (
		id          = sqlite.StringColumn("id")
		orgID       = sqlite.StringColumn("org_id")
		projectID   = sqlite.StringColumn("project_id")
		name        = sqlite.StringColumn("name")
		secretHash  = sqlite.BlobColumn("secret_hash")
		scopes      = sqlite.StringColumn("scopes")
		resourceIDs = sqlite.StringColumn("resource_ids")
		createdAt   = sqlite.StringColumn("created_at")
		expiresAt   = sqlite.StringColumn("expires_at")
		revokedAt   = sqlite.StringColumn("revoked_at")
		lastUsedAt  = sqlite.StringColumn("last_used_at")
		all         = sqlite.ColumnList{id, orgID, projectID, name, secretHash, scopes, resourceIDs, createdAt, expiresAt, revokedAt, lastUsedAt}
	)
	return &APIKeys{
		Table:       sqlite.NewTable("", tablePrefix+"api_keys", "api_keys", all...),
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
