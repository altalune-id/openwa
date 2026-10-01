package chat_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/testutil/tenantseed"
)

type pgFixture struct {
	seed   tenantseed.DB
	store  chat.Store
	tc     tenant.Context
	device uuid.UUID
}

func newPgFixture(t *testing.T) pgFixture {
	t.Helper()
	seed := tenantseed.Open(t)
	tc := seed.Tenant(t)
	return pgFixture{seed: seed, store: chat.NewStore(seed.Cfg, seed.Pool, seed.PC), tc: tc, device: seed.Device(t, tc)}
}

func (f pgFixture) ctx(t *testing.T) context.Context { return tenant.Into(t.Context(), f.tc) }

func (f pgFixture) newChat(t *testing.T, jid, lid string) *chat.Chat {
	t.Helper()
	c, err := chat.New(f.tc.OrgID, f.tc.ProjectID, f.device, fakes.ChatPublicID(), jid, lid, chat.KindDM)
	require.NoError(t, err)
	return c
}

func TestPostgres_Chat_SaveRoundTripsAndDerivesActivity(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.ctx(t)
	c := f.newChat(t, "628111@s.whatsapp.net", "77@lid")
	c.Rename("Budi")
	require.NoError(t, f.store.Save(ctx, c, 0))

	got, err := f.store.ByID(ctx, c.ID)
	require.NoError(t, err)
	require.Equal(t, "Budi", got.Name)
	require.WithinDuration(t, c.CreatedAt, got.ActivityAt, time.Millisecond, "activity_at falls back to created_at")

	at := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	got.Touch(at, "hi", true)
	require.NoError(t, f.store.Save(ctx, got, got.Version))
	again, err := f.store.ByID(ctx, c.ID)
	require.NoError(t, err)
	require.True(t, at.Equal(again.ActivityAt), "activity_at follows last_message_at")
	require.Equal(t, 1, again.UnreadCount)
	require.Equal(t, 2, again.Version)
}

func TestPostgres_Chat_ByJIDMatchesEitherColumn(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.ctx(t)
	c := f.newChat(t, "628111@s.whatsapp.net", "77@lid")
	require.NoError(t, f.store.Save(ctx, c, 0))

	for _, key := range []string{"628111@s.whatsapp.net", "77@lid"} {
		got, err := f.store.ByJID(ctx, f.device, key)
		require.NoError(t, err, key)
		require.Equal(t, c.ID, got.ID)
	}
	_, err := f.store.ByJID(ctx, f.device, "")
	require.True(t, chat.IsNotFoundError(err), "an empty key must never match the empty lid of another chat")

	byPub, err := f.store.ByPublicID(ctx, c.PublicID)
	require.NoError(t, err)
	require.Equal(t, c.ID, byPub.ID)
	ids, err := f.store.PublicIDs(ctx, []uuid.UUID{c.ID, uuid.New()})
	require.NoError(t, err)
	require.Equal(t, map[uuid.UUID]string{c.ID: c.PublicID}, ids, "one query; unknown ids are absent")
}

func TestPostgres_Chat_EnsureForJIDLinksAPhoneChatWithoutAbortingOnATakenLID(t *testing.T) {
	f := newPgFixture(t)
	svc := chat.NewService(f.store, slog.New(slog.NewTextHandler(io.Discard, nil)), passthrough(),
		tenant.NewUnitOfWork(f.seed.PC), &fakes.Groups{})
	phone, err := svc.EnsureForJID(f.ctx(t), f.device, "628111@s.whatsapp.net", "", chat.KindDM, "")
	require.NoError(t, err)
	_, err = svc.EnsureForJID(f.ctx(t), f.device, "77@lid", "77@lid", chat.KindDM, "")
	require.NoError(t, err)
	both, err := svc.EnsureForJID(f.ctx(t), f.device, "628111@s.whatsapp.net", "77@lid", chat.KindDM, "")
	require.NoError(t, err, "no 23505 from chats_device_lid_key")
	require.Equal(t, phone.ID, both.ID)
}

func TestPostgres_Chat_InsertIsInsertOrIgnore(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.ctx(t)
	a := f.newChat(t, "628111@s.whatsapp.net", "")
	ok, err := f.store.Insert(ctx, a)
	require.NoError(t, err)
	require.True(t, ok)

	dup := f.newChat(t, "628111@s.whatsapp.net", "")
	ok, err = f.store.Insert(ctx, dup)
	require.NoError(t, err)
	require.False(t, ok, "same device and jid")

	b := f.newChat(t, "628222@s.whatsapp.net", "")
	ok, err = f.store.Insert(ctx, b)
	require.NoError(t, err)
	require.True(t, ok, "two chats with an empty lid coexist under the partial index")

	c := f.newChat(t, "628333@s.whatsapp.net", "55@lid")
	d := f.newChat(t, "628444@s.whatsapp.net", "55@lid")
	ok, err = f.store.Insert(ctx, c)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = f.store.Insert(ctx, d)
	require.NoError(t, err)
	require.False(t, ok, "a LID belongs to one chat per device")
}

func TestPostgres_Chat_ListPagesByActivityWithEqualTimestamps(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.ctx(t)
	at := time.Now().UTC().Truncate(time.Microsecond)
	want := make([]uuid.UUID, 0, 5)
	for i := range 5 {
		c := f.newChat(t, "62811"+string(rune('0'+i))+"@s.whatsapp.net", "")
		c.Touch(at, "same second", false)
		require.NoError(t, f.store.Save(ctx, c, 0))
		want = append(want, c.ID)
	}
	var got []uuid.UUID
	cursor := ""
	for {
		page, next, err := f.store.List(ctx, chat.ListOpts{Limit: 2, Cursor: cursor}.WithDefaults())
		require.NoError(t, err)
		for _, c := range page {
			got = append(got, c.ID)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	require.Len(t, got, 5, "every row exactly once across pages")
	for i := 1; i < len(got); i++ {
		require.Greater(t, got[i-1].String(), got[i].String(), "id DESC breaks the timestamp tie")
	}
	require.ElementsMatch(t, want, got)
}

func TestPostgres_Chat_ListFilters(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.ctx(t)
	budi := f.newChat(t, "628111@s.whatsapp.net", "")
	budi.Rename("Budi Santoso")
	require.NoError(t, f.store.Save(ctx, budi, 0))
	other := f.seed.Device(t, f.tc)
	g, err := chat.New(f.tc.OrgID, f.tc.ProjectID, other, fakes.ChatPublicID(), "1203@g.us", "", chat.KindGroup)
	require.NoError(t, err)
	g.Rename("50% off_club")
	require.NoError(t, f.store.Save(ctx, g, 0))

	page, _, err := f.store.List(ctx, chat.ListOpts{Search: "bud"}.WithDefaults())
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, budi.ID, page[0].ID)

	page, _, err = f.store.List(ctx, chat.ListOpts{Search: "50%"}.WithDefaults())
	require.NoError(t, err)
	require.Len(t, page, 1, "a percent sign in search is a literal")

	group := chat.KindGroup
	page, _, err = f.store.List(ctx, chat.ListOpts{Kind: &group}.WithDefaults())
	require.NoError(t, err)
	require.Len(t, page, 1)

	page, _, err = f.store.List(ctx, chat.ListOpts{DeviceIDs: []uuid.UUID{f.device}}.WithDefaults())
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, budi.ID, page[0].ID)

	_, _, err = f.store.List(ctx, chat.ListOpts{Cursor: "garbage"}.WithDefaults())
	require.Error(t, err)
}

func TestPostgres_Chat_VersionGuardAndDelete(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.ctx(t)
	c := f.newChat(t, "628111@s.whatsapp.net", "")
	require.NoError(t, f.store.Save(ctx, c, 0))
	require.NoError(t, f.store.Save(ctx, c, 1))
	err := f.store.Save(ctx, c, 1)
	require.True(t, chat.IsVersionMismatchError(err), "got %v", err)
	require.NoError(t, f.store.Delete(ctx, c.ID))
	require.True(t, chat.IsNotFoundError(f.store.Delete(ctx, c.ID)))
}

func TestPostgres_Chat_SaveMapsAPublicIDCollision(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.ctx(t)
	a := f.newChat(t, "628111@s.whatsapp.net", "")
	require.NoError(t, f.store.Save(ctx, a, 0))
	b := f.newChat(t, "628222@s.whatsapp.net", "")
	b.PublicID = a.PublicID
	require.True(t, chat.IsPublicIDTakenError(f.store.Save(ctx, b, 0)))
}

func TestPostgres_Chat_ConcurrentEnsureForJIDCreatesOneChat(t *testing.T) {
	f := newPgFixture(t)
	svc := chat.NewService(f.store, slog.New(slog.NewTextHandler(io.Discard, nil)), passthrough(),
		tenant.NewUnitOfWork(f.seed.PC), &fakes.Groups{})
	const workers = 8
	ids := make([]uuid.UUID, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			c, err := svc.EnsureForJID(f.ctx(t), f.device, "628555@s.whatsapp.net", "", chat.KindDM, "")
			errs[i] = err
			if c != nil {
				ids[i] = c.ID
			}
		})
	}
	wg.Wait()
	for i := range workers {
		require.NoError(t, errs[i], "no raw 23505 from the unique jid key")
		require.Equal(t, ids[0], ids[i])
	}
	require.Equal(t, 1, f.count(t, "jid = '628555@s.whatsapp.net'"))
}
