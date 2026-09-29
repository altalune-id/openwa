package controlplane_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	apikeyv1 "altalune.id/template/gen/go/apikey/v1"
	blogv1 "altalune.id/template/gen/go/blog/v1"
	projectv1 "altalune.id/template/gen/go/project/v1"
	todov1 "altalune.id/template/gen/go/todo/v1"
	"altalune.id/template/internal/blog"
	"altalune.id/template/internal/platform/authn"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/project"
	"altalune.id/template/internal/todo"
)

type reachFixture struct {
	h            *harness
	orgID        uuid.UUID
	own, sibling *project.Project
	ownPost      *blog.Post
	siblingPost  *blog.Post
	siblingTodo  *todo.Todo
}

// SECURITY: the fakes filter by org only, so the handler's reach check is the sole guard these tests exercise.
func newReachFixture(t *testing.T) *reachFixture {
	t.Helper()
	ctx := context.Background()
	orgID := uuid.New()
	h := newHarness(t, session.Principal{UserID: uuid.New(), Email: "a@b", ActiveOrgID: orgID})

	own, err := project.New(orgID, "own", "Own")
	require.NoError(t, err)
	require.NoError(t, h.projs.Save(ctx, own))
	sibling, err := project.New(orgID, "sibling", "Sibling")
	require.NoError(t, err)
	require.NoError(t, h.projs.Save(ctx, sibling))

	ownPost, err := blog.New(orgID, own.ID, uuid.New(), "Own", "own-post", "body")
	require.NoError(t, err)
	h.posts.Seed(ownPost)
	siblingPost, err := blog.New(orgID, sibling.ID, uuid.New(), "Sibling", "sibling-post", "body")
	require.NoError(t, err)
	h.posts.Seed(siblingPost)

	siblingTodo, err := todo.New(orgID, sibling.ID, "sibling todo")
	require.NoError(t, err)
	require.NoError(t, h.todos.Save(ctx, siblingTodo))

	return &reachFixture{h: h, orgID: orgID, own: own, sibling: sibling, ownPost: ownPost, siblingPost: siblingPost, siblingTodo: siblingTodo}
}

func (f *reachFixture) key(scopes []string, resourceIDs ...uuid.UUID) string {
	return f.h.mintKey(f.orgID, f.own.ID, scopes, resourceIDs)
}

func allScopes() []string {
	return []string{authn.ScopePostsRead, authn.ScopePostsWrite, authn.ScopePostsAdmin, authn.ScopeAPIKeysRead, authn.ScopeAPIKeysWrite}
}

// TestKeyNeverReachesASiblingProject pins a key to the project it was minted in. SECURITY: every row names a verb that took a sibling project's id; revert the reach check and each one fails.
func TestKeyNeverReachesASiblingProject(t *testing.T) {
	f := newReachFixture(t)
	key := f.key(allScopes())
	sibling := f.sibling.ID.String()

	tests := []struct {
		name string
		call func(context.Context) error
		want connect.Code
	}{
		{"ListPosts", func(ctx context.Context) error {
			req := connect.NewRequest(&blogv1.ListPostsRequest{ProjectId: sibling})
			withKey(key)(req.Header())
			_, err := f.h.blogClient().ListPosts(ctx, req)
			return err
		}, connect.CodePermissionDenied},
		{"CreatePost", func(ctx context.Context) error {
			req := connect.NewRequest(&blogv1.CreatePostRequest{ProjectId: sibling, CategoryId: uuid.NewString(), Title: "x"})
			withKey(key)(req.Header())
			_, err := f.h.blogClient().CreatePost(ctx, req)
			return err
		}, connect.CodePermissionDenied},
		{"GetPost", func(ctx context.Context) error {
			req := connect.NewRequest(&blogv1.GetPostRequest{PostId: f.siblingPost.ID.String()})
			withKey(key)(req.Header())
			_, err := f.h.blogClient().GetPost(ctx, req)
			return err
		}, connect.CodeNotFound},
		{"PublishPost", func(ctx context.Context) error {
			req := connect.NewRequest(&blogv1.PublishPostRequest{PostId: f.siblingPost.ID.String()})
			withKey(key)(req.Header())
			_, err := f.h.blogClient().PublishPost(ctx, req)
			return err
		}, connect.CodeNotFound},
		{"DeletePost", func(ctx context.Context) error {
			req := connect.NewRequest(&blogv1.DeletePostRequest{PostId: f.siblingPost.ID.String()})
			withKey(key)(req.Header())
			_, err := f.h.blogClient().DeletePost(ctx, req)
			return err
		}, connect.CodeNotFound},
		{"TodoList", func(ctx context.Context) error {
			req := connect.NewRequest(&todov1.ListRequest{ProjectId: sibling})
			withKey(key)(req.Header())
			_, err := f.h.authClient().List(ctx, req)
			return err
		}, connect.CodePermissionDenied},
		{"TodoCreate", func(ctx context.Context) error {
			req := connect.NewRequest(&todov1.CreateRequest{ProjectId: sibling, Title: "x"})
			withKey(key)(req.Header())
			_, err := f.h.authClient().Create(ctx, req)
			return err
		}, connect.CodePermissionDenied},
		{"TodoToggle", func(ctx context.Context) error {
			req := connect.NewRequest(&todov1.ToggleRequest{TodoId: f.siblingTodo.ID.String()})
			withKey(key)(req.Header())
			_, err := f.h.authClient().Toggle(ctx, req)
			return err
		}, connect.CodePermissionDenied},
		{"APIKeyCreate", func(ctx context.Context) error {
			req := connect.NewRequest(&apikeyv1.CreateRequest{
				ExpiresAt: timestamppb.New(time.Now().Add(24 * time.Hour)), ProjectId: sibling, Name: "escalate", Scopes: []string{authn.ScopePostsRead}})
			withKey(key)(req.Header())
			_, err := f.h.apikeyClient().Create(ctx, req)
			return err
		}, connect.CodePermissionDenied},
		{"APIKeyList", func(ctx context.Context) error {
			req := connect.NewRequest(&apikeyv1.ListRequest{ProjectId: sibling})
			withKey(key)(req.Header())
			_, err := f.h.apikeyClient().List(ctx, req)
			return err
		}, connect.CodePermissionDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(t.Context())
			require.Error(t, err, "a key minted for one project reached its sibling")
			require.Equal(t, tt.want, connectCode(err), "err=%v", err)
		})
	}

	stored, err := f.h.posts.ByID(context.Background(), f.siblingPost.ID)
	require.NoError(t, err, "the sibling post must survive")
	require.Equal(t, blog.StatusDraft, stored.Status, "the sibling post must be left untouched")
	td, err := f.h.todos.ByID(context.Background(), f.siblingTodo.ID)
	require.NoError(t, err)
	require.False(t, td.Done, "the sibling todo must be left untouched")
}

func TestKeyStillReachesItsOwnProject(t *testing.T) {
	f := newReachFixture(t)
	key := f.key(allScopes())

	listReq := connect.NewRequest(&blogv1.ListPostsRequest{ProjectId: f.own.ID.String()})
	withKey(key)(listReq.Header())
	_, err := f.h.blogClient().ListPosts(t.Context(), listReq)
	require.NoError(t, err)

	activeReq := connect.NewRequest(&blogv1.ListPostsRequest{})
	withKey(key)(activeReq.Header())
	_, err = f.h.blogClient().ListPosts(t.Context(), activeReq)
	require.NoError(t, err, "an omitted projectId must resolve to the key's own project")

	getReq := connect.NewRequest(&blogv1.GetPostRequest{PostId: f.ownPost.ID.String()})
	withKey(key)(getReq.Header())
	_, err = f.h.blogClient().GetPost(t.Context(), getReq)
	require.NoError(t, err)
}

// SECURITY: project_list hands an agent the ids every other tool takes, so a key must see only the projects it reaches.
func TestKeyProjectListShowsOnlyReachableProjects(t *testing.T) {
	f := newReachFixture(t)
	key := f.key(allScopes())

	req := connect.NewRequest(&projectv1.ListProjectsRequest{})
	withKey(key)(req.Header())
	resp, err := f.h.projectClient().ListProjects(t.Context(), req)
	require.NoError(t, err)

	ids := make([]string, 0, len(resp.Msg.GetProjects()))
	for _, p := range resp.Msg.GetProjects() {
		ids = append(ids, p.GetId())
	}
	require.Equal(t, []string{f.own.ID.String()}, ids)
}

func TestPersonStillReachesEverySiblingProject(t *testing.T) {
	f := newReachFixture(t)

	listReq := connect.NewRequest(&blogv1.ListPostsRequest{ProjectId: f.sibling.ID.String()})
	withBearer(listReq.Header())
	_, err := f.h.blogClient().ListPosts(t.Context(), listReq)
	require.NoError(t, err, "org membership reaches every project in the org")

	getReq := connect.NewRequest(&blogv1.GetPostRequest{PostId: f.siblingPost.ID.String()})
	withBearer(getReq.Header())
	_, err = f.h.blogClient().GetPost(t.Context(), getReq)
	require.NoError(t, err)

	projReq := connect.NewRequest(&projectv1.ListProjectsRequest{})
	withBearer(projReq.Header())
	resp, err := f.h.projectClient().ListProjects(t.Context(), projReq)
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetProjects(), 2)
}

// SECURITY: a key restricted to named resources reaches only those, and never a project-wide verb.
func TestResourceBoundKeyReachesOnlyItsResources(t *testing.T) {
	f := newReachFixture(t)
	other, err := blog.New(f.orgID, f.own.ID, uuid.New(), "Other", "other-post", "body")
	require.NoError(t, err)
	f.h.posts.Seed(other)
	key := f.key(allScopes(), f.ownPost.ID)

	getOwn := connect.NewRequest(&blogv1.GetPostRequest{PostId: f.ownPost.ID.String()})
	withKey(key)(getOwn.Header())
	_, err = f.h.blogClient().GetPost(t.Context(), getOwn)
	require.NoError(t, err, "the named post must stay reachable")

	getOther := connect.NewRequest(&blogv1.GetPostRequest{PostId: other.ID.String()})
	withKey(key)(getOther.Header())
	_, err = f.h.blogClient().GetPost(t.Context(), getOther)
	require.Equal(t, connect.CodeNotFound, connectCode(err), "err=%v", err)

	list := connect.NewRequest(&blogv1.ListPostsRequest{ProjectId: f.own.ID.String()})
	withKey(key)(list.Header())
	_, err = f.h.blogClient().ListPosts(t.Context(), list)
	require.Equal(t, connect.CodePermissionDenied, connectCode(err), "a project-wide list would expose every post; err=%v", err)
}
