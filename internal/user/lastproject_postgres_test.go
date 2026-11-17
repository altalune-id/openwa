package user_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/testutil/pgtest"
	"altalune.id/openwa/internal/user"
	"altalune.id/openwa/schema"
)

func TestPostgres_User_LastProject(t *testing.T) {
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)
	cfg := config.Defaults()
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true
	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))
	store := user.NewStore(db.DBConfig{Schema: h.Schema, TablePrefix: cfg.DB.TablePrefix}, db.Pool{W: sqlDB, R: sqlDB})
	ctx := t.Context()
	tbl := func(name string) string { return h.Schema + "." + cfg.DB.TablePrefix + name }

	me, err := user.New("last-me@example.com", "Me", user.SourceLocal)
	require.NoError(t, err)
	other, err := user.New("last-other@example.com", "Other", user.SourceLocal)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, me))
	require.NoError(t, store.Save(ctx, other))
	project := seedProject(t, sqlDB, tbl, me.ID)

	fresh, err := store.ByID(ctx, me.ID)
	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, fresh.LastProjectID)

	require.NoError(t, store.SetLastProject(ctx, me.ID, project))
	require.NoError(t, store.Save(ctx, me))
	got, err := store.ByID(ctx, me.ID)
	require.NoError(t, err)
	assert.Equal(t, project, got.LastProjectID, "Save leaves last_project_id alone")
	byEmail, err := store.ByEmail(ctx, me.Email)
	require.NoError(t, err)
	assert.Equal(t, project, byEmail.LastProjectID)
	untouched, err := store.ByID(ctx, other.ID)
	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, untouched.LastProjectID)

	assert.True(t, user.IsNotFoundError(store.SetLastProject(ctx, uuid.New(), project)))
	require.Error(t, store.SetLastProject(ctx, me.ID, uuid.New()), "a project that does not exist is refused by the foreign key")

	_, err = sqlDB.ExecContext(ctx, "DELETE FROM "+tbl("projects")+" WHERE id = $1", project)
	require.NoError(t, err)
	gone, err := store.ByID(ctx, me.ID)
	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, gone.LastProjectID, "deleting the project clears the pointer")

	var indexed bool
	require.NoError(t, sqlDB.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = $1 AND indexname = $2)", h.Schema, cfg.DB.TablePrefix+"users_last_project_idx").Scan(&indexed))
	assert.True(t, indexed, "a project delete finds its pointing users through an index")
}

func seedProject(t *testing.T, sqlDB *sql.DB, tbl func(string) string, owner uuid.UUID) uuid.UUID {
	t.Helper()
	now := time.Now().UTC()
	orgID, projectID := uuid.New(), uuid.New()
	_, err := sqlDB.ExecContext(t.Context(), "INSERT INTO "+tbl("orgs")+" (id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, 'Acme', $3, $4, $4)", orgID, "acme-"+orgID.String()[:8], owner, now)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(t.Context(), "INSERT INTO "+tbl("projects")+" (id, org_id, slug, name, created_by, created_at, updated_at) VALUES ($1, $2, 'alpha', 'Alpha', $3, $4, $4)", projectID, orgID, owner, now)
	require.NoError(t, err)
	return projectID
}
