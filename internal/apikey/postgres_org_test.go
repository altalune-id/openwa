package apikey_test

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/apikey"
	"altalune.id/openwa/internal/platform/authn"
	"altalune.id/openwa/internal/platform/tenant"
)

func TestPostgres_OrgKeyRoundTrip(t *testing.T) {
	f := newPgFixture(t)
	second := seedPgProject(t, f.sqlDB, f.prefix, f.tc)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: f.tc.OrgID, UserID: f.tc.UserID})

	k, plaintext, err := apikey.Scheme{}.MintOrg(f.tc.OrgID, "ci", []string{authn.ScopePostsRead}, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{f.tc.ProjectID}}, nil, time.Now().UTC())
	require.NoError(t, err)
	k.CreatedBy = f.tc.UserID
	require.NoError(t, f.store.Save(ctx, k))

	got, err := f.store.ByID(ctx, k.ID)
	require.NoError(t, err)
	assert.Equal(t, apikey.KindOrg, got.Kind)
	assert.Equal(t, uuid.Nil, got.ProjectID)
	assert.Equal(t, []uuid.UUID{f.tc.ProjectID}, got.ProjectIDs)
	assert.Equal(t, k.SecretHint, got.SecretHint)
	assert.Equal(t, f.tc.UserID, got.CreatedBy)

	require.NoError(t, got.GrantProjects([]uuid.UUID{second}))
	require.NoError(t, f.store.Save(ctx, got))
	resolved, err := f.store.BySecretHash(t.Context(), sha(plaintext))
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{f.tc.ProjectID, second}, resolved.ProjectIDs, "the definer function must carry the grant")

	require.NoError(t, resolved.GrantAllProjects())
	require.NoError(t, f.store.Save(ctx, resolved))
	all, err := f.store.ByID(ctx, k.ID)
	require.NoError(t, err)
	assert.True(t, all.AllProjects)
	assert.Empty(t, all.ProjectIDs)

	orgKeys, err := f.store.ListOrg(ctx)
	require.NoError(t, err)
	require.Len(t, orgKeys, 1)
	projectKeys, err := f.store.List(ctx, f.tc.ProjectID)
	require.NoError(t, err)
	assert.Empty(t, projectKeys)
}

// SECURITY: a superuser fixture bypasses RLS, so the composite foreign key is the only guard this exercises.
func TestPostgres_GrantCannotNameAnotherOrgsProject(t *testing.T) {
	f := newPgFixture(t)
	foreign := seedPgTenant(t, f.sqlDB, f.prefix)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: f.tc.OrgID, UserID: f.tc.UserID})

	k, _, err := apikey.Scheme{}.MintOrg(f.tc.OrgID, "ci", nil, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{foreign.ProjectID}}, nil, time.Now().UTC())
	require.NoError(t, err)
	require.Error(t, f.store.Save(ctx, k), "a grant row naming another org's project must be refused")

	var n int
	require.NoError(t, f.sqlDB.QueryRowContext(t.Context(), "SELECT count(*) FROM "+f.prefix+"api_key_projects").Scan(&n))
	assert.Zero(t, n)
}

// TestPostgres_OrgKeyGrantsAreInvisibleAcrossOrgs is the RLS regression detector for api_key_projects under a NOBYPASSRLS role.
func TestPostgres_OrgKeyGrantsAreInvisibleAcrossOrgs(t *testing.T) {
	store, migDB, appConn, prefix := newPgRLSFixture(t)
	a := seedRLSTenant(t, migDB, prefix)
	b := seedRLSTenant(t, migDB, prefix)
	aCtx := tenant.Into(t.Context(), tenant.Context{OrgID: a.OrgID, UserID: a.UserID})
	bCtx := tenant.Into(t.Context(), tenant.Context{OrgID: b.OrgID, UserID: b.UserID})

	k, plaintext, err := apikey.Scheme{}.MintOrg(a.OrgID, "A only", []string{authn.ScopePostsRead}, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{a.ProjectID}}, nil, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, store.Save(aCtx, k))

	listed, err := store.ListOrg(bCtx)
	require.NoError(t, err)
	assert.Empty(t, listed, "org B must not list org A's org keys")

	tx, err := tenant.NewPgConn(appConn).BeginTenanted(t.Context(), tenant.Context{OrgID: b.OrgID})
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	var rows int
	require.NoError(t, tx.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+prefix+"api_key_projects WHERE key_id = $1", k.ID).Scan(&rows))
	assert.Zero(t, rows, "org A's grant rows must be invisible under org B's tenant scope")

	resolved, err := store.BySecretHash(t.Context(), sha(plaintext))
	require.NoError(t, err, "the pre-tenant lookup must still resolve the key and its grant")
	assert.Equal(t, []uuid.UUID{a.ProjectID}, resolved.ProjectIDs)
}

func TestPostgres_PersonalTokenRoundTrip(t *testing.T) {
	f := newPgFixture(t)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: f.tc.OrgID, UserID: f.tc.UserID})

	k, plaintext, err := apikey.Scheme{}.MintPersonal(f.tc.OrgID, f.tc.UserID, "laptop", []string{authn.ScopePostsRead}, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{f.tc.ProjectID}}, nil, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, k))

	mine, err := f.store.ListPersonal(ctx, f.tc.UserID)
	require.NoError(t, err)
	require.Len(t, mine, 1)
	assert.Equal(t, []uuid.UUID{f.tc.ProjectID}, mine[0].ProjectIDs)
	none, err := f.store.ListPersonal(ctx, uuid.New())
	require.NoError(t, err)
	assert.Empty(t, none)

	resolved, err := f.store.BySecretHash(t.Context(), sha(plaintext))
	require.NoError(t, err)
	assert.Equal(t, apikey.KindPersonal, resolved.Kind)
	assert.Equal(t, f.tc.UserID, resolved.CreatedBy)
}

func sha(plaintext string) [32]byte { return sha256.Sum256([]byte(plaintext)) }

// SECURITY: a superuser fixture bypasses RLS, so the explicit org_id predicate is the only thing keeping one org's tokens out of another org's list and revoke.
func TestPostgres_PersonalTokensStayInTheirOrg(t *testing.T) {
	f := newPgFixture(t)
	a := f.tc
	b := seedPgTenant(t, f.sqlDB, f.prefix)
	_, err := f.sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+f.prefix+"memberships (id, org_id, user_id, role, created_at) VALUES ($1, $2, $3, 'member', $4)",
		uuid.New(), b.OrgID, a.UserID, time.Now().UTC())
	require.NoError(t, err)
	inA := tenant.Into(t.Context(), tenant.Context{OrgID: a.OrgID, UserID: a.UserID})
	inB := tenant.Into(t.Context(), tenant.Context{OrgID: b.OrgID, UserID: a.UserID})

	ka, _, err := apikey.Scheme{}.MintPersonal(a.OrgID, a.UserID, "a", nil, apikey.ProjectGrant{All: true}, nil, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, f.store.Save(inA, ka))
	kb, _, err := apikey.Scheme{}.MintPersonal(b.OrgID, a.UserID, "b", nil, apikey.ProjectGrant{All: true}, nil, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, f.store.Save(inB, kb))

	listed, err := f.store.ListPersonal(inA, a.UserID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, ka.ID, listed[0].ID, "org A's list must not include the same owner's token in org B")

	require.NoError(t, f.store.RevokePersonal(inA, a.UserID, time.Now().UTC()))
	gotA, err := f.store.ByID(inA, ka.ID)
	require.NoError(t, err)
	assert.NotNil(t, gotA.RevokedAt)
	gotB, err := f.store.ByID(inB, kb.ID)
	require.NoError(t, err)
	assert.Nil(t, gotB.RevokedAt, "leaving org A must not revoke the token in org B")
}

// SECURITY: the api_keys_kind_shape CHECK is the only thing refusing a project key without a project, so the raw insert bypasses the store.
func TestPostgres_KindShapeIsEnforced(t *testing.T) {
	f := newPgFixture(t)
	for name, q := range map[string]string{
		"project key without a project": "INSERT INTO " + f.prefix + "api_keys (id, org_id, project_id, kind, all_projects, name, secret_hash, created_at) VALUES ($1, $2, NULL, 'project', false, 'bad', $3, now())",
		"org key naming a project":      "INSERT INTO " + f.prefix + "api_keys (id, org_id, project_id, kind, all_projects, name, secret_hash, created_at) VALUES ($1, $2, '" + f.tc.ProjectID.String() + "', 'org', false, 'bad', $3, now())",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.sqlDB.ExecContext(t.Context(), q, uuid.New(), f.tc.OrgID, []byte(uuid.NewString()))
			require.Error(t, err, "the kind check must refuse this shape")
			assert.Contains(t, err.Error(), "api_keys_kind_shape")
		})
	}
}
