package chat_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/platform/publicid"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
)

type svcFixture struct {
	store  *fakes.Chat
	groups *fakes.Groups
	svc    *chat.Service
	tc     tenant.Context
	device uuid.UUID
}

func passthrough() apperror.UnexpectedFunc {
	return func(_ context.Context, msg string, cause error, _ ...any) *apperror.AppError {
		return apperror.New("GEN900", msg, codes.Internal).WithCause(cause)
	}
}

func newSvc(t *testing.T) *svcFixture {
	t.Helper()
	store := fakes.NewChat()
	groups := &fakes.Groups{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &svcFixture{
		store: store, groups: groups,
		svc:    chat.NewService(store, log, passthrough(), fakes.UnitOfWork, groups),
		tc:     tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()},
		device: uuid.New(),
	}
}

func (f *svcFixture) ctx(t *testing.T) context.Context { return tenant.Into(t.Context(), f.tc) }

func TestEnsureForJID_CreatesOnce(t *testing.T) {
	f := newSvc(t)
	a, err := f.svc.EnsureForJID(f.ctx(t), f.device, "628111@s.whatsapp.net", "", chat.KindDM, "Budi")
	require.NoError(t, err)
	b, err := f.svc.EnsureForJID(f.ctx(t), f.device, "628111@s.whatsapp.net", "", chat.KindDM, "")
	require.NoError(t, err)
	require.Equal(t, a.ID, b.ID)
	require.Equal(t, "Budi", b.Name)
	require.Equal(t, f.tc.ProjectID, a.ProjectID)
}

func TestEnsureForJID_LIDReplyLandsInThePhoneChat(t *testing.T) {
	f := newSvc(t)
	sent, err := f.svc.EnsureForJID(f.ctx(t), f.device, "628111@s.whatsapp.net", "", chat.KindDM, "")
	require.NoError(t, err)
	reply, err := f.svc.EnsureForJID(f.ctx(t), f.device, "628111@s.whatsapp.net", "77@lid", chat.KindDM, "")
	require.NoError(t, err)
	require.Equal(t, sent.ID, reply.ID)
	require.Equal(t, "77@lid", reply.LID)

	lidOnly, err := f.svc.EnsureForJID(f.ctx(t), f.device, "77@lid", "77@lid", chat.KindDM, "")
	require.NoError(t, err)
	require.Equal(t, sent.ID, lidOnly.ID, "a LID-addressed message finds the chat through its alias")
}

func TestEnsureForJID_APhoneAndALIDChatNeverCollide(t *testing.T) {
	f := newSvc(t)
	phone, err := f.svc.EnsureForJID(f.ctx(t), f.device, "628111@s.whatsapp.net", "", chat.KindDM, "")
	require.NoError(t, err)
	lidOnly, err := f.svc.EnsureForJID(f.ctx(t), f.device, "77@lid", "", chat.KindDM, "")
	require.NoError(t, err)
	require.NotEqual(t, phone.ID, lidOnly.ID)
	both, err := f.svc.EnsureForJID(f.ctx(t), f.device, "628111@s.whatsapp.net", "77@lid", chat.KindDM, "")
	require.NoError(t, err, "the second LID holder is not written, so the unique LID index never fires")
	require.Equal(t, phone.ID, both.ID)
	require.Empty(t, both.LID)
}

func TestResolve_ByPublicIDAndRefusesMalformedOrForeignIDs(t *testing.T) {
	f := newSvc(t)
	c, err := f.svc.EnsureForJID(f.ctx(t), f.device, "628111@s.whatsapp.net", "", chat.KindDM, "")
	require.NoError(t, err)
	require.True(t, publicid.Valid(chat.PublicIDPrefix, c.PublicID), "EnsureForJID mints a cht_ id, got %q", c.PublicID)
	got, err := f.svc.Resolve(f.ctx(t), c.PublicID)
	require.NoError(t, err)
	require.Equal(t, c.ID, got.ID)
	_, err = f.svc.Resolve(f.ctx(t), c.ID.String())
	require.True(t, chat.IsNotFoundError(err), "a UUID is not a public id")
	other, err := chat.New(f.tc.OrgID, uuid.New(), f.device, fakes.ChatPublicID(), "628999@s.whatsapp.net", "", chat.KindDM)
	require.NoError(t, err)
	f.store.Seed(other)
	_, err = f.svc.Resolve(f.ctx(t), other.PublicID)
	require.True(t, chat.IsNotFoundError(err), "a sibling project's chat is not found")
	ids, err := f.svc.PublicIDs(f.ctx(t), []uuid.UUID{c.ID, other.ID})
	require.NoError(t, err)
	require.Equal(t, c.PublicID, ids[c.ID])
}

func TestEnsureForJID_LostInsertRaceReturnsTheWinner(t *testing.T) {
	f := newSvc(t)
	winner, err := chat.New(f.tc.OrgID, f.tc.ProjectID, f.device, fakes.ChatPublicID(), "628111@s.whatsapp.net", "", chat.KindDM)
	require.NoError(t, err)
	f.store.InsertFn = func(_ context.Context, _ *chat.Chat) (bool, error) {
		f.store.Seed(winner)
		return false, nil
	}
	got, err := f.svc.EnsureForJID(f.ctx(t), f.device, "628111@s.whatsapp.net", "", chat.KindDM, "")
	require.NoError(t, err)
	require.Equal(t, winner.ID, got.ID)
}

func TestGet_SiblingProjectIsNotFound(t *testing.T) {
	f := newSvc(t)
	c, err := chat.New(f.tc.OrgID, uuid.New(), f.device, fakes.ChatPublicID(), "628111@s.whatsapp.net", "", chat.KindDM)
	require.NoError(t, err)
	f.store.Seed(c)
	_, err = f.svc.Get(f.ctx(t), c.ID)
	require.True(t, chat.IsNotFoundError(err), "the store filters by org only; the service must check the project")
}

func TestTouch_RetriesAVersionRace(t *testing.T) {
	f := newSvc(t)
	c, err := f.svc.EnsureForJID(f.ctx(t), f.device, "628111@s.whatsapp.net", "", chat.KindDM, "")
	require.NoError(t, err)
	calls := 0
	f.store.SaveFn = func(ctx context.Context, cc *chat.Chat, ifVersion int) error {
		calls++
		if calls == 1 {
			return &chat.VersionMismatchError{Want: ifVersion, Got: ifVersion + 1}
		}
		f.store.SaveFn = nil
		return f.store.Save(ctx, cc, 0)
	}
	require.NoError(t, f.svc.Touch(f.ctx(t), c.ID, time.Now(), "hi", true))
	got, err := f.svc.Get(f.ctx(t), c.ID)
	require.NoError(t, err)
	require.Equal(t, 1, got.UnreadCount)
	require.Equal(t, 2, calls)
}

func TestTouch_JoinsAnAmbientUnitOfWork(t *testing.T) {
	f := newSvc(t)
	c, err := f.svc.EnsureForJID(f.ctx(t), f.device, "628111@s.whatsapp.net", "", chat.KindDM, "")
	require.NoError(t, err)
	err = fakes.UnitOfWork(f.ctx(t), func(ctx context.Context) error {
		return f.svc.Touch(ctx, c.ID, time.Now(), "hi", true)
	})
	require.NoError(t, err, "a caller's transaction is joined, never nested")
}

func TestMarkReadArchiveRepair(t *testing.T) {
	f := newSvc(t)
	c, err := f.svc.EnsureForJID(f.ctx(t), f.device, "628111@s.whatsapp.net", "", chat.KindDM, "")
	require.NoError(t, err)
	require.NoError(t, f.svc.Touch(f.ctx(t), c.ID, time.Now(), "hi", true))
	require.NoError(t, f.svc.MarkRead(f.ctx(t), c.ID))
	require.NoError(t, f.svc.Archive(f.ctx(t), c.ID))
	at := time.Now().Add(-time.Hour).UTC()
	require.NoError(t, f.svc.Repair(f.ctx(t), c.ID, &at, "older", 3))
	got, err := f.svc.Get(f.ctx(t), c.ID)
	require.NoError(t, err)
	require.True(t, got.Archived)
	require.Equal(t, 3, got.UnreadCount)
	require.Equal(t, "older", got.LastMessagePreview)
}

func TestList_PassesInvalidCursorThrough(t *testing.T) {
	f := newSvc(t)
	_, _, err := f.svc.List(f.ctx(t), chat.ListOpts{Cursor: "nope"})
	require.Error(t, err)
	_, ok := apperror.AsAppError(err)
	require.True(t, ok, "an invalid cursor is a validation error, never unexpected")
}

func TestEnsureForJID_DeviceOfAnotherProjectIsNotFound(t *testing.T) {
	f := newSvc(t)
	foreign := uuid.New()
	f.store.SeedDevice(foreign, f.tc.OrgID, uuid.New())
	_, err := f.svc.EnsureForJID(f.ctx(t), foreign, "628111@s.whatsapp.net", "", chat.KindDM, "")
	require.True(t, chat.IsNotFoundError(err), "got %v", err)
	require.Zero(t, f.store.Len())
}

func TestEnsureForJID_ExhaustedInsertRaceIsAnError(t *testing.T) {
	f := newSvc(t)
	f.store.InsertFn = func(context.Context, *chat.Chat) (bool, error) { return false, nil }
	_, err := f.svc.EnsureForJID(f.ctx(t), f.device, "628111@s.whatsapp.net", "", chat.KindDM, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "insert race")
}
