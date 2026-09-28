package controlplane_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	apikeyv1 "altalune.id/template/gen/go/apikey/v1"
	apikeyv1connect "altalune.id/template/gen/go/apikey/v1/apikeyv1connect"
	"altalune.id/template/internal/apikey"
	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/controlplane"
	"altalune.id/template/internal/platform"
	"altalune.id/template/internal/platform/authn"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/platform/tokens"
	"altalune.id/template/internal/project"
	"altalune.id/template/internal/testutil/fakes"
)

type apikeyFixture struct {
	t       *testing.T
	baseURL string
	store   *fakes.APIKey
	projs   *fakes.Project
	project *project.Project
	orgID   uuid.UUID
}

func newAPIKeyFixture(t *testing.T) *apikeyFixture {
	t.Helper()
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := apperror.NewReporter(log, false)

	orgID := uuid.New()
	principal := session.Principal{UserID: uuid.New(), Email: "a@b", ActiveOrgID: orgID}

	projs := fakes.NewProject()
	keyStore := fakes.NewAPIKey()

	projectSvc := project.NewService(projs, log, reporter.Unexpected)
	keySvc := apikey.NewService(keyStore, apikey.Scheme{}, log, reporter.Unexpected)

	kernel := &platform.Kernel{
		Log:      log,
		Reporter: reporter,
		Verifier: stubVerifier{principal: principal},
	}

	srv := controlplane.New(
		nil, // cfg — OpenAPI off by default in these tests
		kernel,
		nil, nil,
		nil, projectSvc, nil, nil,
		nil,
		nil, nil, nil,
	)
	srv.Authn = authn.Chain{apikey.NewAuthenticator(keyStore, nil, apikey.Scheme{}), tokens.NewAuthenticator(kernel.Verifier)}
	srv.APIKeys = keySvc
	srv.KeyPrefix = apikey.DefaultPrefix

	ts := httptest.NewServer(srv.Handler(""))
	t.Cleanup(ts.Close)

	proj, err := project.New(orgID, "p1", "Project 1")
	require.NoError(t, err)
	require.NoError(t, projs.Save(ctx, proj))

	return &apikeyFixture{t: t, baseURL: ts.URL, store: keyStore, projs: projs, project: proj, orgID: orgID}
}

func (f *apikeyFixture) client() apikeyv1connect.APIKeyServiceClient {
	return apikeyv1connect.NewAPIKeyServiceClient(http.DefaultClient, f.baseURL+"/api")
}

func TestAPIKey_Create_MintsAndReturnsPlaintextOnce(t *testing.T) {
	f := newAPIKeyFixture(t)

	req := connect.NewRequest(&apikeyv1.CreateRequest{
		ProjectId: f.project.ID.String(),
		Name:      "ci-deploy",
		Scopes:    []string{authn.ScopeAPIKeysRead},
	})
	withBearer(req.Header())

	resp, err := f.client().Create(t.Context(), req)
	require.NoError(t, err)

	require.NotEmpty(t, resp.Msg.GetPlaintext())
	require.True(t, strings.HasPrefix(resp.Msg.GetPlaintext(), apikey.DefaultPrefix))
	require.Equal(t, "ci-deploy", resp.Msg.GetKey().GetName())
	require.Equal(t, f.project.ID.String(), resp.Msg.GetKey().GetProjectId())
	require.Equal(t, []string{authn.ScopeAPIKeysRead}, resp.Msg.GetKey().GetScopes())
	require.NotEmpty(t, resp.Msg.GetKey().GetId())
}

// SECURITY: the plaintext secret comes back from Create only, so List must never carry it.
func TestAPIKey_List_NeverCarriesPlaintext(t *testing.T) {
	f := newAPIKeyFixture(t)

	createReq := connect.NewRequest(&apikeyv1.CreateRequest{
		ProjectId: f.project.ID.String(),
		Name:      "ci-deploy",
		Scopes:    []string{authn.ScopeAPIKeysRead},
	})
	withBearer(createReq.Header())
	created, err := f.client().Create(t.Context(), createReq)
	require.NoError(t, err)
	plaintext := created.Msg.GetPlaintext()
	require.NotEmpty(t, plaintext)

	listReq := connect.NewRequest(&apikeyv1.ListRequest{ProjectId: f.project.ID.String()})
	withBearer(listReq.Header())
	listed, err := f.client().List(t.Context(), listReq)
	require.NoError(t, err)
	require.Len(t, listed.Msg.GetKeys(), 1)
	require.Equal(t, created.Msg.GetKey().GetId(), listed.Msg.GetKeys()[0].GetId())

	raw := rawJSONList(t, f.baseURL, f.project.ID.String())
	require.NotContains(t, raw, plaintext, "the raw List response body must never contain the plaintext secret")
}

func TestAPIKey_Create_UnknownScope_ReturnsInvalidArgument(t *testing.T) {
	f := newAPIKeyFixture(t)

	req := connect.NewRequest(&apikeyv1.CreateRequest{
		ProjectId: f.project.ID.String(),
		Name:      "bad-scope",
		Scopes:    []string{"not:a:real:scope"},
	})
	withBearer(req.Header())

	_, err := f.client().Create(t.Context(), req)
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connectCode(err))
}

func TestAPIKey_Create_EmptyName_ReturnsInvalidArgument(t *testing.T) {
	f := newAPIKeyFixture(t)

	req := connect.NewRequest(&apikeyv1.CreateRequest{
		ProjectId: f.project.ID.String(),
		Name:      "   ",
		Scopes:    []string{authn.ScopeAPIKeysRead},
	})
	withBearer(req.Header())

	_, err := f.client().Create(t.Context(), req)
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connectCode(err))
}

func TestAPIKey_Revoke_MarksKeyRevoked(t *testing.T) {
	f := newAPIKeyFixture(t)

	createReq := connect.NewRequest(&apikeyv1.CreateRequest{
		ProjectId: f.project.ID.String(),
		Name:      "temp",
		Scopes:    []string{authn.ScopeAPIKeysWrite},
	})
	withBearer(createReq.Header())
	created, err := f.client().Create(t.Context(), createReq)
	require.NoError(t, err)

	revokeReq := connect.NewRequest(&apikeyv1.RevokeRequest{
		ProjectId: f.project.ID.String(),
		Id:        created.Msg.GetKey().GetId(),
	})
	withBearer(revokeReq.Header())
	_, err = f.client().Revoke(t.Context(), revokeReq)
	require.NoError(t, err)

	listReq := connect.NewRequest(&apikeyv1.ListRequest{ProjectId: f.project.ID.String()})
	withBearer(listReq.Header())
	listed, err := f.client().List(t.Context(), listReq)
	require.NoError(t, err)
	require.Len(t, listed.Msg.GetKeys(), 1)
	require.NotNil(t, listed.Msg.GetKeys()[0].GetRevokedAt())
}

// TestAPIKey_Revoke_KeyFromAnotherProjectInSameOrg_ReturnsNotFound proves apikey.Service.Revoke's ownership check reaches this RPC surface.
func TestAPIKey_Revoke_KeyFromAnotherProjectInSameOrg_ReturnsNotFound(t *testing.T) {
	f := newAPIKeyFixture(t)
	ctx := context.Background()

	proj2, err := project.New(f.orgID, "p2", "Project 2")
	require.NoError(t, err)
	require.NoError(t, f.projs.Save(ctx, proj2))

	createReq := connect.NewRequest(&apikeyv1.CreateRequest{
		ProjectId: f.project.ID.String(),
		Name:      "p1-key",
		Scopes:    []string{authn.ScopeAPIKeysWrite},
	})
	withBearer(createReq.Header())
	created, err := f.client().Create(t.Context(), createReq)
	require.NoError(t, err)

	revokeReq := connect.NewRequest(&apikeyv1.RevokeRequest{
		ProjectId: proj2.ID.String(),
		Id:        created.Msg.GetKey().GetId(),
	})
	withBearer(revokeReq.Header())
	_, err = f.client().Revoke(t.Context(), revokeReq)
	require.Error(t, err)
	require.Equal(t, connect.CodeNotFound, connectCode(err))
}

// TestAPIKey_ScopeEnforcement proves ScopeTable's entries gate these procedures for a real API-key credential.
func TestAPIKey_ScopeEnforcement(t *testing.T) {
	f := newAPIKeyFixture(t)
	now := time.Now().UTC()

	readKey, readPlain, err := apikey.Scheme{}.Mint(f.orgID, f.project.ID, "reader", []string{authn.ScopeAPIKeysRead}, nil, nil, now)
	require.NoError(t, err)
	f.store.Seed(readKey)

	listReq := connect.NewRequest(&apikeyv1.ListRequest{ProjectId: f.project.ID.String()})
	listReq.Header().Set("Authorization", "Bearer "+readPlain)
	_, err = f.client().List(t.Context(), listReq)
	require.NoError(t, err, "a read-scoped key must be admitted to List")

	createReq := connect.NewRequest(&apikeyv1.CreateRequest{
		ProjectId: f.project.ID.String(),
		Name:      "should-not-be-created",
		Scopes:    []string{authn.ScopeAPIKeysRead},
	})
	createReq.Header().Set("Authorization", "Bearer "+readPlain)
	_, err = f.client().Create(t.Context(), createReq)
	require.Error(t, err, "a read-scoped key must be denied Create")
	require.Equal(t, connect.CodePermissionDenied, connectCode(err))
}

func rawJSONList(t *testing.T, baseURL, projectID string) string {
	t.Helper()
	body := strings.NewReader(`{"projectId":"` + projectID + `"}`)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+"/api/apikey.v1.APIKeyService/List", body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	withBearer(req.Header)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	return string(raw)
}
