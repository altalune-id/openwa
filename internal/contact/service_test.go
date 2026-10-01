package contact_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/contact"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
)

func newSvc(t *testing.T) (*contact.Service, *fakes.Contact, context.Context, tenant.Context) {
	t.Helper()
	store := fakes.NewContact()
	unexpected := func(_ context.Context, msg string, cause error, _ ...any) *apperror.AppError {
		return apperror.New("GEN900", msg, codes.Internal).WithCause(cause)
	}
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()}
	return contact.NewService(store, slog.New(slog.NewTextHandler(io.Discard, nil)), unexpected), store, tenant.Into(t.Context(), tc), tc
}

func TestUpsertFromMessage_KeysByPhoneJIDWhenKnown(t *testing.T) {
	svc, store, ctx, _ := newSvc(t)
	device := uuid.New()
	require.NoError(t, svc.UpsertFromMessage(ctx, device, contact.SenderInput{JID: "77@lid", LID: "77@lid", Phone: "628111", PushName: "budi"}))
	got, err := store.ByJID(ctx, device, "628111@s.whatsapp.net")
	require.NoError(t, err)
	require.Equal(t, "77@lid", got.LID)
	require.Equal(t, "budi", got.PushName)
}

func TestUpsertFromMessage_HiddenPhoneKeysByLID(t *testing.T) {
	svc, store, ctx, _ := newSvc(t)
	device := uuid.New()
	require.NoError(t, svc.UpsertFromMessage(ctx, device, contact.SenderInput{JID: "77@lid", LID: "77@lid", PushName: "anon"}))
	got, err := store.ByJID(ctx, device, "77@lid")
	require.NoError(t, err)
	require.Empty(t, got.Phone, "a LID is never presented as a phone")
}

func TestUpsertFromEngine_MergesWithoutClobbering(t *testing.T) {
	svc, store, ctx, _ := newSvc(t)
	device := uuid.New()
	require.NoError(t, svc.UpsertFromEngine(ctx, contact.ContactInput{DeviceID: device, JID: "628111@s.whatsapp.net", Phone: "628111", Name: "Budi Santoso"}))
	require.NoError(t, svc.UpsertFromMessage(ctx, device, contact.SenderInput{JID: "628111@s.whatsapp.net", Phone: "628111", PushName: "budi"}))
	got, err := store.ByJID(ctx, device, "628111@s.whatsapp.net")
	require.NoError(t, err)
	require.Equal(t, "Budi Santoso", got.Name, "an empty field in a later upsert keeps the stored value")
	require.Equal(t, "budi", got.PushName)
}

func TestUpsert_SkipsAnEmptyKey(t *testing.T) {
	svc, _, ctx, _ := newSvc(t)
	require.NoError(t, svc.UpsertFromMessage(ctx, uuid.New(), contact.SenderInput{}))
}

func TestGet_SiblingProjectIsNotFound(t *testing.T) {
	svc, store, ctx, tc := newSvc(t)
	c := contact.New(tc.OrgID, uuid.New(), uuid.New(), "628111@s.whatsapp.net")
	store.Seed(c)
	_, err := svc.Get(ctx, c.DeviceID, c.JID)
	require.True(t, contact.IsNotFoundError(err))

	own := contact.New(tc.OrgID, tc.ProjectID, uuid.New(), "628222@s.whatsapp.net")
	store.Seed(own)
	got, err := svc.Get(ctx, own.DeviceID, own.JID)
	require.NoError(t, err)
	require.Equal(t, own.JID, got.JID)
}

func TestUpsert_SiblingProjectDeviceIsRefused(t *testing.T) {
	svc, store, ctx, tc := newSvc(t)
	foreign := contact.New(tc.OrgID, uuid.New(), uuid.New(), "628111@s.whatsapp.net")
	foreign.Name = "Victim"
	store.Seed(foreign)

	err := svc.UpsertFromEngine(ctx, contact.ContactInput{DeviceID: foreign.DeviceID, JID: foreign.JID, Name: "Hijacked"})
	require.True(t, contact.IsNotFoundError(err))
	err = svc.UpsertFromEngine(ctx, contact.ContactInput{DeviceID: foreign.DeviceID, JID: "628999@s.whatsapp.net", Name: "Planted"})
	require.True(t, contact.IsNotFoundError(err))

	other := tenant.Into(t.Context(), tenant.Context{OrgID: tc.OrgID, ProjectID: foreign.ProjectID})
	got, err := store.ByJID(other, foreign.DeviceID, foreign.JID)
	require.NoError(t, err)
	require.Equal(t, "Victim", got.Name)
	_, err = store.ByJID(other, foreign.DeviceID, "628999@s.whatsapp.net")
	require.True(t, contact.IsNotFoundError(err))
}
