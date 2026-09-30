package dataplane_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/blog"
	"altalune.id/openwa/internal/dataplane"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/authn"
	"altalune.id/openwa/internal/platform/publicid"
	"altalune.id/openwa/internal/platform/session"
)

const testKey = "key_test"

var errFakeNotFound = errors.New("fake: not found")

type fakeOrgs map[string]dataplane.OrgRef

func (f fakeOrgs) BySlug(_ context.Context, slug string) (dataplane.OrgRef, error) {
	o, ok := f[slug]
	if !ok {
		return dataplane.OrgRef{}, errFakeNotFound
	}
	return o, nil
}

type projectKey struct {
	orgID uuid.UUID
	slug  string
}

type fakeProjects map[projectKey]dataplane.ProjectRef

func (f fakeProjects) BySlug(_ context.Context, orgID uuid.UUID, slug string) (dataplane.ProjectRef, error) {
	p, ok := f[projectKey{orgID: orgID, slug: slug}]
	if !ok {
		return dataplane.ProjectRef{}, errFakeNotFound
	}
	return p, nil
}

type fakePosts struct {
	mu   sync.Mutex
	rows map[string]dataplane.PostRef

	// NOTE: runs outside the lock, and is set before the fake is shared.
	beforeCreate func()
	createErr    error
	bySlugErr    error
	listErr      error
}

func newFakePosts(seed ...dataplane.PostRef) *fakePosts {
	f := &fakePosts{rows: map[string]dataplane.PostRef{}}
	for _, p := range seed {
		f.rows[p.Slug] = p
	}
	return f
}

// NOTE: a missing post is a *blog.NotFoundError, the same type both real stores return.
func (f *fakePosts) BySlug(_ context.Context, _ uuid.UUID, slug string) (dataplane.PostRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bySlugErr != nil {
		return dataplane.PostRef{}, f.bySlugErr
	}
	p, ok := f.rows[slug]
	if !ok {
		return dataplane.PostRef{}, &blog.NotFoundError{ID: slug}
	}
	return p, nil
}

func (f *fakePosts) List(_ context.Context, _, _ uuid.UUID, opts dataplane.ListOpts) ([]dataplane.PostRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]dataplane.PostRef, 0, len(f.rows))
	for _, p := range f.rows {
		if opts.PublishedOnly && !p.Published {
			continue
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b dataplane.PostRef) int { return strings.Compare(a.Slug, b.Slug) })
	return out, nil
}

func (f *fakePosts) Create(_ context.Context, categoryID uuid.UUID, title, slug, body string) (dataplane.PostRef, error) {
	if f.beforeCreate != nil {
		f.beforeCreate()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		err := f.createErr
		f.createErr = nil
		return dataplane.PostRef{}, err
	}
	if _, clash := f.rows[slug]; clash {
		return dataplane.PostRef{}, &blog.AlreadyExistsError{Slug: slug}
	}
	p := dataplane.PostRef{
		ID:         uuid.New(),
		CategoryID: categoryID,
		Title:      title,
		Slug:       slug,
		Body:       body,
		Version:    1,
	}
	f.rows[slug] = p
	return p, nil
}

func (f *fakePosts) Update(_ context.Context, id uuid.UUID, title, slug, body string, categoryID uuid.UUID, ifVersion int) (dataplane.PostRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	current, key, ok := f.find(id)
	if !ok {
		return dataplane.PostRef{}, &blog.NotFoundError{ID: id.String()}
	}
	if ifVersion != 0 && current.Version != ifVersion {
		return dataplane.PostRef{}, &blog.StaleVersionError{Want: ifVersion, Got: current.Version}
	}
	current.Title, current.Slug, current.Body, current.CategoryID = title, slug, body, categoryID
	current.Version++
	delete(f.rows, key)
	f.rows[current.Slug] = current
	return current, nil
}

func (f *fakePosts) Publish(ctx context.Context, id uuid.UUID, ifVersion int) (dataplane.PostRef, error) {
	return f.setPublished(ctx, id, ifVersion, true)
}

func (f *fakePosts) Unpublish(ctx context.Context, id uuid.UUID, ifVersion int) (dataplane.PostRef, error) {
	return f.setPublished(ctx, id, ifVersion, false)
}

func (f *fakePosts) setPublished(_ context.Context, id uuid.UUID, ifVersion int, published bool) (dataplane.PostRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	current, key, ok := f.find(id)
	if !ok {
		return dataplane.PostRef{}, &blog.NotFoundError{ID: id.String()}
	}
	if ifVersion != 0 && current.Version != ifVersion {
		return dataplane.PostRef{}, &blog.StaleVersionError{Want: ifVersion, Got: current.Version}
	}
	current.Published = published
	current.Version++
	f.rows[key] = current
	return current, nil
}

func (f *fakePosts) Delete(_ context.Context, id uuid.UUID, ifVersion int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	current, key, ok := f.find(id)
	if !ok {
		return &blog.NotFoundError{ID: id.String()}
	}
	if ifVersion != 0 && current.Version != ifVersion {
		return &blog.StaleVersionError{Want: ifVersion, Got: current.Version}
	}
	delete(f.rows, key)
	return nil
}

func (f *fakePosts) find(id uuid.UUID) (dataplane.PostRef, string, bool) {
	for key, p := range f.rows {
		if p.ID == id {
			return p, key, true
		}
	}
	return dataplane.PostRef{}, "", false
}

func (f *fakePosts) len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

type fakeAuthz struct {
	key        string
	scopes     []string
	orgID      uuid.UUID
	projectIDs []uuid.UUID
	resources  []uuid.UUID
}

func (f *fakeAuthz) Authenticate(_ context.Context, raw string) (session.Principal, error) {
	if raw != f.key {
		return session.Principal{}, &authn.UnauthorizedError{}
	}
	return session.Principal{KeyID: uuid.New(), ActiveOrgID: f.orgID, ActiveProjectID: f.projectIDs[0]}, nil
}

func (f *fakeAuthz) Authorize(ctx context.Context, raw, scope string, orgID, projectID, resourceID uuid.UUID) (session.Principal, error) {
	p, err := f.tenant(ctx, raw, scope, orgID, projectID)
	if err != nil {
		return session.Principal{}, err
	}
	if len(f.resources) > 0 && !slices.Contains(f.resources, resourceID) {
		return session.Principal{}, &authn.InsufficientScopeError{Scope: scope}
	}
	return p, nil
}

func (f *fakeAuthz) AuthorizeProject(ctx context.Context, raw, scope string, orgID, projectID uuid.UUID) (session.Principal, error) {
	p, err := f.tenant(ctx, raw, scope, orgID, projectID)
	if err != nil {
		return session.Principal{}, err
	}
	if len(f.resources) > 0 {
		return session.Principal{}, &authn.InsufficientScopeError{Scope: scope}
	}
	return p, nil
}

func (f *fakeAuthz) AuthorizeScope(ctx context.Context, raw, scope string, orgID, projectID uuid.UUID) (session.Principal, error) {
	if _, err := f.tenant(ctx, raw, scope, orgID, projectID); err != nil {
		return session.Principal{}, err
	}
	return session.Principal{
		Source:      session.SourceAPIKey,
		ActiveOrgID: orgID,
		ProjectIDs:  []uuid.UUID{projectID},
		ResourceIDs: slices.Clone(f.resources),
		Scopes:      slices.Clone(f.scopes),
	}, nil
}

func (f *fakeAuthz) tenant(ctx context.Context, raw, scope string, orgID, projectID uuid.UUID) (session.Principal, error) {
	p, err := f.Authenticate(ctx, raw)
	if err != nil {
		return session.Principal{}, err
	}
	if orgID != f.orgID || !slices.Contains(f.projectIDs, projectID) {
		return session.Principal{}, &authn.UnauthorizedError{}
	}
	if !slices.Contains(f.scopes, scope) {
		return session.Principal{}, &authn.InsufficientScopeError{Scope: scope}
	}
	return p, nil
}

type fakeDevices struct {
	mu       sync.Mutex
	rows     map[uuid.UUID]dataplane.DeviceRef
	link     dataplane.LinkRef
	linkErr  error
	creates  int
	resolves int
	unlinked []uuid.UUID
	deleted  []uuid.UUID
	phones   []string
}

func newFakeDevices(seed ...dataplane.DeviceRef) *fakeDevices {
	f := &fakeDevices{rows: map[uuid.UUID]dataplane.DeviceRef{}, link: dataplane.LinkRef{
		ID: "lnk_V1StGXR8Z5jdHi6B", Method: "qr", Outcome: "pending", QR: "2@x", PNG: []byte{0x89, 'P', 'N', 'G'},
		StartedAt: time.Unix(1790000000, 0).UTC(), ExpiresAt: time.Unix(1790000060, 0).UTC(),
	}}
	for _, d := range seed {
		f.rows[d.ID] = d
	}
	return f
}

func (f *fakeDevices) List(context.Context) ([]dataplane.DeviceRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]dataplane.DeviceRef, 0, len(f.rows))
	for _, d := range f.rows {
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b dataplane.DeviceRef) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (f *fakeDevices) Resolve(_ context.Context, publicID string) (dataplane.DeviceRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolves++
	for _, d := range f.rows {
		if d.PublicID == publicID {
			return d, nil
		}
	}
	return dataplane.DeviceRef{}, &device.NotFoundError{ID: publicID}
}

func (f *fakeDevices) Get(_ context.Context, id uuid.UUID) (dataplane.DeviceRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.rows[id]
	if !ok {
		return dataplane.DeviceRef{}, &device.NotFoundError{ID: id.String()}
	}
	return d, nil
}

func (f *fakeDevices) Create(_ context.Context, name string) (dataplane.DeviceRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates++
	for _, d := range f.rows {
		if strings.EqualFold(d.Name, name) {
			return dataplane.DeviceRef{}, &device.NameTakenError{Name: name}
		}
	}
	pub, _ := publicid.New(device.PublicIDPrefix)
	d := dataplane.DeviceRef{ID: uuid.New(), PublicID: pub, Name: name, Version: 1, State: "unlinked", Rules: dataplane.RulesRef{GroupMode: "mention", IgnoreFromMe: true}}
	f.rows[d.ID] = d
	return d, nil
}

func (f *fakeDevices) Update(_ context.Context, id uuid.UUID, name *string, rules *dataplane.RulesRef, ifVersion int) (dataplane.DeviceRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.rows[id]
	if !ok {
		return dataplane.DeviceRef{}, &device.NotFoundError{ID: id.String()}
	}
	if ifVersion != 0 && ifVersion != d.Version {
		return dataplane.DeviceRef{}, &device.StaleVersionError{Want: ifVersion, Got: d.Version}
	}
	if name != nil {
		d.Name = *name
	}
	if rules != nil {
		d.Rules = *rules
	}
	d.Version++
	f.rows[id] = d
	return d, nil
}

func (f *fakeDevices) Delete(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, id)
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeDevices) StartLink(context.Context, uuid.UUID) (dataplane.LinkRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.link, f.linkErr
}

func (f *fakeDevices) LinkWithPhone(_ context.Context, _ uuid.UUID, phone string) (dataplane.LinkRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.phones = append(f.phones, phone)
	l := f.link
	l.Method, l.PairingCode = "phone", "ABCD-EFGH"
	return l, f.linkErr
}

func (f *fakeDevices) LinkState(context.Context, uuid.UUID) (dataplane.LinkRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.link, nil
}

func (f *fakeDevices) Unlink(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unlinked = append(f.unlinked, id)
	return f.linkErr
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type env struct {
	log          *slog.Logger
	orgID        uuid.UUID
	projectID    uuid.UUID
	altProjectID uuid.UUID
	categoryID   uuid.UUID
	posts        *fakePosts
	devices      *fakeDevices
	authz        *fakeAuthz
	caps         dataplane.Capabilities
}

func newEnv(published bool) *env {
	orgID, projectID, categoryID := uuid.New(), uuid.New(), uuid.New()
	altProjectID := uuid.New()
	return &env{
		orgID:        orgID,
		projectID:    projectID,
		altProjectID: altProjectID,
		categoryID:   categoryID,
		posts: newFakePosts(dataplane.PostRef{
			ID:         uuid.New(),
			CategoryID: categoryID,
			Title:      "Hello",
			Slug:       "hello",
			Body:       "body",
			Published:  published,
			Version:    3,
		}),
		devices: newFakeDevices(
			dataplane.DeviceRef{ID: uuid.New(), PublicID: "dev_SalesDevice00001", Name: "Sales", Version: 3, State: "connected", Phone: "628111"},
			dataplane.DeviceRef{ID: uuid.New(), PublicID: "dev_SupportDevice001", Name: "Support", Version: 1, State: "unlinked"},
		),
		authz: &fakeAuthz{
			key:        testKey,
			scopes:     []string{authn.ScopePostsRead, authn.ScopePostsWrite, authn.ScopePostsAdmin, authn.ScopeDevicesRead, authn.ScopeDevicesWrite},
			orgID:      orgID,
			projectIDs: []uuid.UUID{projectID, altProjectID},
		},
	}
}

func (e *env) logger() *slog.Logger {
	if e.log != nil {
		return e.log
	}
	return discardLogger()
}

func (e *env) handler() http.Handler {
	return dataplane.NewHandler(dataplane.HandlerParams{
		BasePath: "/api/v1",
		Orgs:     fakeOrgs{"acme": {ID: e.orgID}},
		Projects: fakeProjects{
			{orgID: e.orgID, slug: "main"}:  {ID: e.projectID},
			{orgID: e.orgID, slug: "other"}: {ID: e.altProjectID},
		},
		Posts:   e.posts,
		Devices: e.devices,
		Authz:   e.authz,
		Caps:    e.caps,
		Log:     e.logger(),
	})
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	return newEnv(true).handler()
}
