package contact_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/contact"
	"altalune.id/openwa/internal/platform/keyset"
)

var errBoom = errors.New("boom")

type failingStore struct{}

func (failingStore) Upsert(context.Context, *contact.Contact) error { return errBoom }
func (failingStore) ByJID(context.Context, uuid.UUID, string) (*contact.Contact, error) {
	return nil, errBoom
}
func (failingStore) List(context.Context, contact.ListOpts) ([]*contact.Contact, string, error) {
	return nil, "", errBoom
}

func newFailingSvc(t *testing.T) (*contact.Service, context.Context) {
	t.Helper()
	_, _, ctx, _ := newSvc(t)
	unexpected := func(_ context.Context, msg string, cause error, _ ...any) *apperror.AppError {
		return apperror.New("GEN900", msg, codes.Internal).WithCause(cause)
	}
	return contact.NewService(failingStore{}, slog.New(slog.NewTextHandler(io.Discard, nil)), unexpected), ctx
}

func TestNotFoundError_MessageAndAppError(t *testing.T) {
	err := &contact.NotFoundError{ID: "628111@s.whatsapp.net"}
	require.Equal(t, "contact: not found: 628111@s.whatsapp.net", err.Error())
	ae := err.ToAppError()
	require.Equal(t, apperror.CodeContactNotFound, ae.Code())
	require.Equal(t, "CNT001", ae.Code())
	require.Equal(t, codes.NotFound, ae.GRPCCode())
	require.Equal(t, "Contact not found", ae.Message())
	require.Len(t, ae.Details(), 1)
	require.True(t, contact.IsNotFoundError(err))
	require.False(t, contact.IsNotFoundError(errBoom))
}

func TestServiceList_PagesFiltersAndIsolates(t *testing.T) {
	svc, store, ctx, tc := newSvc(t)
	devA, devB := uuid.New(), uuid.New()
	base := time.Now().UTC().Truncate(time.Second)
	seed := func(org, project, device uuid.UUID, jid, name string, age time.Duration) {
		c := contact.New(org, project, device, jid)
		c.Name, c.UpdatedAt = name, base.Add(-age)
		store.Seed(c)
	}
	seed(tc.OrgID, tc.ProjectID, devA, "1@s.whatsapp.net", "Budi", 0)
	seed(tc.OrgID, tc.ProjectID, devA, "2@s.whatsapp.net", "Bunga", time.Minute)
	seed(tc.OrgID, tc.ProjectID, devB, "3@s.whatsapp.net", "Citra", 2*time.Minute)
	seed(tc.OrgID, uuid.New(), devA, "4@s.whatsapp.net", "SiblingProject", 0)
	seed(uuid.New(), uuid.New(), devA, "5@s.whatsapp.net", "OtherOrg", 0)

	page, next, err := svc.List(ctx, contact.ListOpts{Limit: 2})
	require.NoError(t, err)
	require.Equal(t, []string{"1@s.whatsapp.net", "2@s.whatsapp.net"}, jids(page))
	require.NotEmpty(t, next)

	page, next, err = svc.List(ctx, contact.ListOpts{Limit: 2, Cursor: next})
	require.NoError(t, err)
	require.Equal(t, []string{"3@s.whatsapp.net"}, jids(page))
	require.Empty(t, next)

	page, _, err = svc.List(ctx, contact.ListOpts{DeviceIDs: []uuid.UUID{devB}})
	require.NoError(t, err)
	require.Equal(t, []string{"3@s.whatsapp.net"}, jids(page))

	page, _, err = svc.List(ctx, contact.ListOpts{DeviceID: &devA, Search: " bu "})
	require.NoError(t, err)
	require.Equal(t, []string{"1@s.whatsapp.net", "2@s.whatsapp.net"}, jids(page))
}

func TestServiceList_Errors(t *testing.T) {
	svc, _, _, _ := newSvc(t)
	_, _, err := svc.List(t.Context(), contact.ListOpts{})
	require.Error(t, err, "no tenant in ctx")

	svc, _, ctx, _ := newSvc(t)
	_, _, err = svc.List(ctx, contact.ListOpts{Cursor: "not-a-cursor"})
	require.True(t, keyset.IsInvalidCursorError(err), "a bad cursor passes through typed, got %v", err)

	failing, fctx := newFailingSvc(t)
	_, _, err = failing.List(fctx, contact.ListOpts{})
	ae, ok := errors.AsType[*apperror.AppError](err)
	require.True(t, ok)
	require.Equal(t, "GEN900", ae.Code())
	require.ErrorIs(t, err, errBoom)
}

func TestServiceGetAndUpsert_StoreFailureIsUnexpected(t *testing.T) {
	svc, ctx := newFailingSvc(t)
	_, err := svc.Get(ctx, uuid.New(), "628111@s.whatsapp.net")
	require.ErrorIs(t, err, errBoom)
	require.False(t, contact.IsNotFoundError(err))

	err = svc.UpsertFromEngine(ctx, contact.ContactInput{DeviceID: uuid.New(), JID: "628111@s.whatsapp.net"})
	require.ErrorIs(t, err, errBoom)
	require.False(t, contact.IsNotFoundError(err))
}

func TestService_RequiresTenant(t *testing.T) {
	svc, _, _, _ := newSvc(t)
	_, err := svc.Get(t.Context(), uuid.New(), "x@s.whatsapp.net")
	require.Error(t, err)
	err = svc.UpsertFromMessage(t.Context(), uuid.New(), contact.SenderInput{JID: "x@s.whatsapp.net"})
	require.Error(t, err)
}

func TestService_UpsertPropagatesNotFound(t *testing.T) {
	svc, store, ctx, _ := newSvc(t)
	foreign := contact.New(uuid.New(), uuid.New(), uuid.New(), "628111@s.whatsapp.net")
	store.Seed(foreign)
	err := svc.UpsertFromEngine(ctx, contact.ContactInput{DeviceID: foreign.DeviceID, JID: foreign.JID})
	require.True(t, contact.IsNotFoundError(err))
}

func TestPostgres_Contact_StoreRequiresTenantAndValidCursor(t *testing.T) {
	f := newPgFixture(t)
	_, err := f.store.ByJID(t.Context(), f.device, "x@s.whatsapp.net")
	require.Error(t, err)
	_, _, err = f.store.List(t.Context(), contact.ListOpts{})
	require.Error(t, err)
	require.Error(t, f.store.Upsert(t.Context(), contact.New(f.tc.OrgID, f.tc.ProjectID, f.device, "x@s.whatsapp.net")))

	_, _, err = f.store.List(f.ctx(t), contact.ListOpts{Cursor: "garbage"})
	require.True(t, keyset.IsInvalidCursorError(err), "got %v", err)
}

func TestPostgres_Contact_UpsertRefusesUnknownDeviceAndForeignAggregate(t *testing.T) {
	f := newPgFixture(t)
	ghost := contact.New(f.tc.OrgID, f.tc.ProjectID, uuid.New(), "628111@s.whatsapp.net")
	require.True(t, contact.IsNotFoundError(f.store.Upsert(f.ctx(t), ghost)), "a device that does not exist")

	foreign := contact.New(uuid.New(), f.tc.ProjectID, f.device, "628111@s.whatsapp.net")
	require.True(t, contact.IsNotFoundError(f.store.Upsert(f.ctx(t), foreign)), "an aggregate stamped with another org")
	wrongProject := contact.New(f.tc.OrgID, uuid.New(), f.device, "628111@s.whatsapp.net")
	require.True(t, contact.IsNotFoundError(f.store.Upsert(f.ctx(t), wrongProject)), "an aggregate stamped with another project")
}

func TestServiceGet_ReturnsStoredContact(t *testing.T) {
	svc, store, ctx, tc := newSvc(t)
	c := contact.New(tc.OrgID, tc.ProjectID, uuid.New(), "628111@s.whatsapp.net")
	c.Name = "Budi"
	store.Seed(c)
	got, err := svc.Get(ctx, c.DeviceID, c.JID)
	require.NoError(t, err)
	require.Equal(t, "Budi", got.DisplayName())
	_, err = svc.Get(ctx, uuid.New(), c.JID)
	require.True(t, contact.IsNotFoundError(err))
}

func jids(cs []*contact.Contact) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.JID)
	}
	return out
}
