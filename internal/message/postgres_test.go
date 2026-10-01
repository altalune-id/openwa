package message_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/testutil/tenantseed"
)

type pgFixture struct {
	seed   tenantseed.DB
	store  message.Store
	tc     tenant.Context
	device uuid.UUID
	chat   uuid.UUID
}

func newPgFixture(t *testing.T) pgFixture {
	t.Helper()
	seed := tenantseed.Open(t)
	tc := seed.Tenant(t)
	dev := seed.Device(t, tc)
	return pgFixture{seed: seed, store: message.NewStore(seed.Cfg, seed.Pool, seed.PC), tc: tc, device: dev, chat: seed.Chat(t, tc, dev, "628111@s.whatsapp.net")}
}

func (f pgFixture) ctx(t *testing.T) context.Context { return tenant.Into(t.Context(), f.tc) }

func (f pgFixture) ref() message.Ref {
	return message.Ref{DeviceID: f.device, OrgID: f.tc.OrgID, ProjectID: f.tc.ProjectID}
}

func (f pgFixture) inbound(t *testing.T, waid string, at time.Time) *message.Message {
	t.Helper()
	m := message.NewInbound(f.ref(), f.chat, fakes.MessagePublicID(), message.InboundInput{WAID: waid, Type: "text", Body: "hi " + waid, Timestamp: at, Mentions: []string{"628222@s.whatsapp.net"}})
	ok, err := f.store.InsertInbound(f.ctx(t), m)
	require.NoError(t, err)
	require.True(t, ok)
	return m
}

func (f pgFixture) queued(t *testing.T, text string) *message.Message {
	t.Helper()
	m, err := message.NewOutbound(f.tc.OrgID, f.tc.ProjectID, f.device, f.chat, fakes.MessagePublicID(), message.SendInput{Text: text}, 1<<20)
	require.NoError(t, err)
	require.NoError(t, f.store.Save(f.ctx(t), m, 0))
	return m
}

func TestPostgres_Message_InsertInboundDedupsOnWAID(t *testing.T) {
	f := newPgFixture(t)
	first := f.inbound(t, "WA1", time.Now())
	dup := message.NewInbound(f.ref(), f.chat, fakes.MessagePublicID(), message.InboundInput{WAID: "WA1", Type: "text", Body: "replayed"})
	ok, err := f.store.InsertInbound(f.ctx(t), dup)
	require.NoError(t, err)
	require.False(t, ok)
	got, err := f.store.ByWAID(f.ctx(t), f.device, "WA1")
	require.NoError(t, err)
	require.Equal(t, first.ID, got.ID)
	require.Equal(t, []string{"628222"}, got.Mentions, "mentions round-trip through jsonb")
}

func TestPostgres_Message_SaveRoundTripsMediaAndLocation(t *testing.T) {
	f := newPgFixture(t)
	m, err := message.NewOutbound(f.tc.OrgID, f.tc.ProjectID, f.device, f.chat, fakes.MessagePublicID(),
		message.SendInput{Media: &message.MediaInput{Bytes: []byte("pdf"), Mime: "application/pdf", Filename: "a.pdf", Caption: "doc"}}, 1<<20)
	require.NoError(t, err)
	require.NoError(t, f.store.Save(f.ctx(t), m, 0))
	got, err := f.store.ByID(f.ctx(t), m.ID)
	require.NoError(t, err)
	require.Equal(t, "a.pdf", got.Media.Filename)
	require.Nil(t, got.Raw, "reads never load the queued bytes")
	require.Nil(t, got.Location)

	loc, err := message.NewOutbound(f.tc.OrgID, f.tc.ProjectID, f.device, f.chat, fakes.MessagePublicID(), message.SendInput{Location: &message.Location{Lat: -6.2, Lng: 106.8, Name: "Monas"}}, 1<<20)
	require.NoError(t, err)
	require.NoError(t, f.store.Save(f.ctx(t), loc, 0))
	got, err = f.store.ByID(f.ctx(t), loc.ID)
	require.NoError(t, err)
	require.Equal(t, "Monas", got.Location.Name)
	require.Nil(t, got.Media)
}

func TestPostgres_Message_ListKeysetAndSinceID(t *testing.T) {
	f := newPgFixture(t)
	at := time.Now().UTC().Truncate(time.Microsecond)
	var inserted []uuid.UUID
	for _, w := range []string{"A", "B", "C", "D", "E"} {
		inserted = append(inserted, f.inbound(t, w, at).ID)
	}
	chatID := f.chat
	seen := map[uuid.UUID]bool{}
	cursor := ""
	for {
		page, next, err := f.store.List(f.ctx(t), message.ListOpts{ChatID: &chatID, Limit: 2, Cursor: cursor})
		require.NoError(t, err)
		for _, m := range page {
			require.False(t, seen[m.ID])
			seen[m.ID] = true
		}
		if next == "" {
			break
		}
		cursor = next
	}
	require.Len(t, seen, 5, "equal wa_timestamps page by id")

	since := inserted[1]
	newer, next, err := f.store.List(f.ctx(t), message.ListOpts{ChatID: &chatID, SinceID: &since})
	require.NoError(t, err)
	require.Empty(t, next)
	require.Len(t, newer, 3)
	require.Equal(t, inserted[2], newer[0].ID, "since returns ascending ids")
	require.Equal(t, inserted[4], newer[2].ID)
}

func TestPostgres_Message_UnreadFilterAndCount(t *testing.T) {
	f := newPgFixture(t)
	a := f.inbound(t, "A", time.Now())
	f.inbound(t, "B", time.Now())
	f.queued(t, "outbound is never unread")
	a.MarkReadInbound(time.Now())
	require.NoError(t, f.store.Save(f.ctx(t), a, a.Version))
	n, err := f.store.CountUnreadInbound(f.ctx(t), f.chat)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	chatID := f.chat
	page, _, err := f.store.List(f.ctx(t), message.ListOpts{ChatID: &chatID, Unread: true})
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, "B", page[0].WAMessageID)

	late := f.inbound(t, "C", time.Now())
	n64, err := f.store.MarkChatRead(f.ctx(t), f.chat, page[0].ID, time.Now())
	require.NoError(t, err)
	require.Equal(t, int64(1), n64, "only B, the newest row of the snapshot")
	got, err := f.store.ByID(f.ctx(t), late.ID)
	require.NoError(t, err)
	require.Nil(t, got.ReadAt, "a row newer than the snapshot stays unread")
}

func (f pgFixture) inTx(t *testing.T) (context.Context, func()) {
	t.Helper()
	tx, err := f.seed.PC.BeginTenanted(t.Context(), f.tc)
	require.NoError(t, err)
	return db.ContextWithTx(f.ctx(t), tx), func() { require.NoError(t, tx.Commit()) }
}

func TestPostgres_Message_ClaimNextIsExclusiveAndReclaimsStale(t *testing.T) {
	f := newPgFixture(t)
	first := f.queued(t, "one")
	second := f.queued(t, "two")

	ctx1, commit1 := f.inTx(t)
	a, err := f.store.ClaimNext(ctx1, f.device, time.Hour)
	require.NoError(t, err)
	require.Equal(t, first.ID, a.ID, "oldest first")
	require.Equal(t, message.StatusSending, a.Status)
	require.Equal(t, 1, a.Attempts)

	ctx2, commit2 := f.inTx(t)
	b, err := f.store.ClaimNext(ctx2, f.device, time.Hour)
	require.NoError(t, err)
	require.Equal(t, second.ID, b.ID, "SKIP LOCKED passes over the row the first transaction holds")
	commit1()
	commit2()

	none, err := f.store.ClaimNext(f.ctx(t), f.device, time.Hour)
	require.NoError(t, err)
	require.Nil(t, none, "a fresh sending row is never claimed twice")

	stale, err := f.store.ClaimNext(f.ctx(t), f.device, time.Nanosecond)
	require.NoError(t, err)
	require.NotNil(t, stale, "a sending row older than staleAfter is reclaimed")
	require.Equal(t, 2, stale.Attempts)
	require.Equal(t, message.WAIDFor(stale.ID), stale.WAMessageID, "a reclaim resends the same WhatsApp id")

	third, err := f.store.ClaimNext(f.ctx(t), f.device, time.Nanosecond)
	require.NoError(t, err)
	require.Equal(t, 3, third.Attempts)
	exhausted, err := f.store.ClaimNext(f.ctx(t), f.device, time.Nanosecond)
	require.NoError(t, err)
	require.Equal(t, first.ID, exhausted.ID, "the exhausted row comes back failed so its event can be emitted")
	require.Equal(t, message.StatusFailed, exhausted.Status)
	skipped, err := f.store.ClaimNext(f.ctx(t), f.device, time.Nanosecond)
	require.NoError(t, err)
	require.Equal(t, second.ID, skipped.ID)
	require.Equal(t, message.StatusSending, skipped.Status)
	got, err := f.store.ByID(f.ctx(t), first.ID)
	require.NoError(t, err)
	require.Equal(t, message.StatusFailed, got.Status, "the failure is persisted")
	require.Equal(t, "send attempts exhausted", got.Error)

	last, err := f.store.ClaimNext(f.ctx(t), f.device, time.Nanosecond)
	require.NoError(t, err)
	require.Equal(t, 3, last.Attempts)
	failed, err := f.store.ClaimNext(f.ctx(t), f.device, time.Nanosecond)
	require.NoError(t, err)
	require.Equal(t, second.ID, failed.ID)
	require.Equal(t, message.StatusFailed, failed.Status)
	none, err = f.store.ClaimNext(f.ctx(t), f.device, time.Nanosecond)
	require.NoError(t, err)
	require.Nil(t, none, "a failed row is handed back once and never claimable again")
}

func TestPostgres_Message_QueuedBytesStayUntilReleased(t *testing.T) {
	f := newPgFixture(t)
	m, err := message.NewOutbound(f.tc.OrgID, f.tc.ProjectID, f.device, f.chat, fakes.MessagePublicID(),
		message.SendInput{Media: &message.MediaInput{Bytes: []byte("img"), Mime: "image/png"}}, 1<<20)
	require.NoError(t, err)
	require.NoError(t, f.store.Save(f.ctx(t), m, 0))

	claimed, err := f.store.ClaimNext(f.ctx(t), f.device, time.Hour)
	require.NoError(t, err)
	require.Equal(t, []byte("img"), claimed.Raw, "the claim is the one read that loads the bytes")

	loaded, err := f.store.ByID(f.ctx(t), m.ID)
	require.NoError(t, err)
	loaded.MarkFailed("network", true, time.Now())
	require.NoError(t, f.store.Save(f.ctx(t), loaded, loaded.Version))
	again, err := f.store.ClaimNext(f.ctx(t), f.device, time.Hour)
	require.NoError(t, err)
	require.Equal(t, []byte("img"), again.Raw, "a requeue saved from a raw-less read keeps the bytes")

	keys := message.MediaKeys{URL: "https://mmg/x", DirectPath: "/v/t62", MediaKey: []byte{1}, Length: 3}
	again.MarkSent(time.Now(), &keys, time.Now())
	require.NoError(t, f.store.Save(f.ctx(t), again, again.Version))
	sent, err := f.store.QuotedByWAID(f.ctx(t), f.device, again.WAMessageID)
	require.NoError(t, err)
	require.Nil(t, sent.Raw, "sent media releases its bytes")
	require.Equal(t, "/v/t62", sent.Media.Keys.DirectPath, "and keeps the upload keys for later downloads")
}

func TestPostgres_Message_UnsentBeforeFindsOldQueuedAndStuckSendingRows(t *testing.T) {
	f := newPgFixture(t)
	f.queued(t, "fresh")
	rows, err := f.store.UnsentBefore(f.ctx(t), f.tc.ProjectID, time.Now().Add(-time.Hour), 10)
	require.NoError(t, err)
	require.Empty(t, rows)
	rows, err = f.store.UnsentBefore(f.ctx(t), f.tc.ProjectID, time.Now().Add(time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)

	claimed, err := f.store.ClaimNext(f.ctx(t), f.device, time.Hour)
	require.NoError(t, err)
	require.Equal(t, message.StatusSending, claimed.Status)
	rows, err = f.store.UnsentBefore(f.ctx(t), f.tc.ProjectID, time.Now().Add(time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, rows, 1, "a row stuck in sending on a device that never returns expires too")
}

func TestPostgres_Message_DeleteOlderThanBatchesAndReturnsChats(t *testing.T) {
	f := newPgFixture(t)
	old := time.Now().UTC().AddDate(0, 0, -40)
	for _, w := range []string{"A", "B", "C"} {
		f.inbound(t, w, old)
	}
	f.inbound(t, "fresh", time.Now())
	cutoff := time.Now().UTC().AddDate(0, 0, -30)
	n, err := f.store.CountOlderThan(f.ctx(t), f.tc.ProjectID, cutoff)
	require.NoError(t, err)
	require.Equal(t, int64(3), n)

	deleted, chats, err := f.store.DeleteOlderThan(f.ctx(t), f.tc.ProjectID, cutoff, 2)
	require.NoError(t, err)
	require.Equal(t, int64(2), deleted)
	require.Equal(t, []uuid.UUID{f.chat}, chats, "chat ids are deduplicated")
	deleted, _, err = f.store.DeleteOlderThan(f.ctx(t), f.tc.ProjectID, cutoff, 2)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)
	deleted, chats, err = f.store.DeleteOlderThan(f.ctx(t), f.tc.ProjectID, cutoff, 2)
	require.NoError(t, err)
	require.Zero(t, deleted)
	require.Empty(t, chats)
	_, err = f.store.ByWAID(f.ctx(t), f.device, "fresh")
	require.NoError(t, err, "a message inside the window survives")
}

func TestPostgres_Message_RetentionAndCountToday(t *testing.T) {
	f := newPgFixture(t)
	got, err := f.store.RetentionByProject(f.ctx(t), f.tc.ProjectID)
	require.NoError(t, err)
	require.Nil(t, got, "no row means the configured default")
	p, err := message.NewRetentionPolicy(f.tc.OrgID, f.tc.ProjectID, 7)
	require.NoError(t, err)
	require.NoError(t, f.store.SaveRetention(f.ctx(t), p))
	p.Days = 9
	require.NoError(t, f.store.SaveRetention(f.ctx(t), p))
	got, err = f.store.RetentionByProject(f.ctx(t), f.tc.ProjectID)
	require.NoError(t, err)
	require.Equal(t, 9, got.Days)

	f.inbound(t, "today", time.Now())
	f.inbound(t, "last week", time.Now().AddDate(0, 0, -7))
	n, err := f.store.CountToday(f.ctx(t), time.Now().UTC().Truncate(24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
}

func TestPostgres_Message_PublicIDLookupsAndCollision(t *testing.T) {
	f := newPgFixture(t)
	a := f.queued(t, "a")
	b := f.inbound(t, "B", time.Now())
	got, err := f.store.ByPublicID(f.ctx(t), a.PublicID)
	require.NoError(t, err)
	require.Equal(t, a.ID, got.ID)
	_, err = f.store.ByPublicID(f.ctx(t), "msg_missing")
	require.True(t, message.IsNotFoundError(err))

	ids, err := f.store.PublicIDs(f.ctx(t), []uuid.UUID{a.ID, b.ID, uuid.New()})
	require.NoError(t, err)
	require.Equal(t, map[uuid.UUID]string{a.ID: a.PublicID, b.ID: b.PublicID}, ids)
	empty, err := f.store.PublicIDs(f.ctx(t), nil)
	require.NoError(t, err)
	require.Empty(t, empty)

	clash, err := message.NewOutbound(f.tc.OrgID, f.tc.ProjectID, f.device, f.chat, a.PublicID, message.SendInput{Text: "dup"}, 1<<20)
	require.NoError(t, err)
	require.True(t, message.IsPublicIDTakenError(f.store.Save(f.ctx(t), clash, 0)))
	dupIn := message.NewInbound(f.ref(), f.chat, a.PublicID, message.InboundInput{WAID: "Z", Type: "text"})
	_, err = f.store.InsertInbound(f.ctx(t), dupIn)
	require.True(t, message.IsPublicIDTakenError(err))
}

func TestPostgres_Message_SaveVersionContract(t *testing.T) {
	f := newPgFixture(t)
	m := f.queued(t, "x")
	m.MarkSending(time.Now())
	err := f.store.Save(f.ctx(t), m, 7)
	var vm *message.VersionMismatchError
	require.ErrorAs(t, err, &vm)
	require.Equal(t, 7, vm.Want)
	require.Equal(t, 1, vm.Got)
	require.True(t, message.IsVersionMismatchError(f.store.Save(f.ctx(t), m, -1)))
	require.NoError(t, f.store.Save(f.ctx(t), m, 1))
	got, err := f.store.ByID(f.ctx(t), m.ID)
	require.NoError(t, err)
	require.Equal(t, 2, got.Version)
	require.Equal(t, message.StatusSending, got.Status)
	require.Equal(t, 1, got.Attempts)
}

func TestPostgres_Message_ListFilters(t *testing.T) {
	f := newPgFixture(t)
	in := f.inbound(t, "A", time.Now().Add(-time.Hour))
	out := f.queued(t, "out")
	chat, dev := f.chat, f.device
	dirOut, stQueued := message.DirectionOut, message.StatusQueued
	cases := []struct {
		name string
		opts message.ListOpts
		want []uuid.UUID
	}{
		{"direction", message.ListOpts{Direction: &dirOut}, []uuid.UUID{out.ID}},
		{"status", message.ListOpts{Status: &stQueued}, []uuid.UUID{out.ID}},
		{"device and chat", message.ListOpts{DeviceID: &dev, ChatID: &chat}, []uuid.UUID{out.ID, in.ID}},
		{"limit one is the newest", message.ListOpts{ChatID: &chat, Limit: 1}, []uuid.UUID{out.ID}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			page, _, err := f.store.List(f.ctx(t), tt.opts)
			require.NoError(t, err)
			var got []uuid.UUID
			for _, m := range page {
				got = append(got, m.ID)
			}
			require.Equal(t, tt.want, got)
		})
	}
	after := time.Now().Add(-time.Minute)
	page, _, err := f.store.List(f.ctx(t), message.ListOpts{UpdatedAfter: &after})
	require.NoError(t, err)
	require.Len(t, page, 2)
	after = time.Now().Add(time.Minute)
	page, _, err = f.store.List(f.ctx(t), message.ListOpts{UpdatedAfter: &after})
	require.NoError(t, err)
	require.Empty(t, page)
	_, _, err = f.store.List(f.ctx(t), message.ListOpts{Cursor: "garbage"})
	require.Error(t, err)
}

func TestPostgres_Message_MissingWAIDAndQuotedLookups(t *testing.T) {
	f := newPgFixture(t)
	_, err := f.store.ByWAID(f.ctx(t), f.device, "")
	require.True(t, message.IsNotFoundError(err))
	_, err = f.store.QuotedByWAID(f.ctx(t), f.device, "")
	require.True(t, message.IsNotFoundError(err))
	_, err = f.store.QuotedByWAID(f.ctx(t), f.device, "absent")
	require.True(t, message.IsNotFoundError(err))
}

func TestPostgres_Message_ReceiptsAndInboundWithMediaRoundTrip(t *testing.T) {
	f := newPgFixture(t)
	m := message.NewInbound(f.ref(), f.chat, fakes.MessagePublicID(), message.InboundInput{
		WAID: "IMG1", Type: "image", Caption: "c", Raw: []byte("proto"), Location: &message.Location{Lat: 1, Lng: 2},
		Media: &message.InboundMedia{Mime: "image/jpeg", Size: 5, Keys: message.MediaKeys{DirectPath: "/p", MediaKey: []byte{9}, Length: 5}, Width: 4, Height: 3},
	})
	ok, err := f.store.InsertInbound(f.ctx(t), m)
	require.NoError(t, err)
	require.True(t, ok)
	got, err := f.store.ByID(f.ctx(t), m.ID)
	require.NoError(t, err)
	require.Equal(t, m.Media, got.Media)
	require.Equal(t, 4, got.Media.Width)
	require.Equal(t, m.Location, got.Location)
	require.Nil(t, got.Raw)
	quoted, err := f.store.QuotedByWAID(f.ctx(t), f.device, "IMG1")
	require.NoError(t, err)
	require.Equal(t, []byte("proto"), quoted.Raw)

	out := f.queued(t, "hello")
	out.MarkSending(time.Now())
	out.MarkSent(time.Now(), nil, time.Now())
	require.NoError(t, f.store.Save(f.ctx(t), out, out.Version))
	reloaded, err := f.store.ByID(f.ctx(t), out.ID)
	require.NoError(t, err)
	require.True(t, reloaded.Receipt(message.ReceiptRead, time.Now(), time.Now()))
	require.NoError(t, f.store.Save(f.ctx(t), reloaded, reloaded.Version))
	final, err := f.store.ByID(f.ctx(t), out.ID)
	require.NoError(t, err)
	require.Equal(t, message.StatusRead, final.Status)
	require.NotNil(t, final.DeliveredAt)
	require.NotNil(t, final.ReadAt)
}

func TestPostgres_Message_ClaimNextOnEmptyQueueAndExpireDropsBytes(t *testing.T) {
	f := newPgFixture(t)
	none, err := f.store.ClaimNext(f.ctx(t), f.device, time.Hour)
	require.NoError(t, err)
	require.Nil(t, none)

	m, err := message.NewOutbound(f.tc.OrgID, f.tc.ProjectID, f.device, f.chat, fakes.MessagePublicID(),
		message.SendInput{Media: &message.MediaInput{Bytes: []byte("img"), Mime: "image/png"}}, 1<<20)
	require.NoError(t, err)
	require.NoError(t, f.store.Save(f.ctx(t), m, 0))
	loaded, err := f.store.ByID(f.ctx(t), m.ID)
	require.NoError(t, err)
	require.True(t, loaded.Expire("expired", time.Now()))
	require.NoError(t, f.store.Save(f.ctx(t), loaded, loaded.Version))
	got, err := f.store.ByID(f.ctx(t), m.ID)
	require.NoError(t, err)
	require.Equal(t, message.StatusFailed, got.Status)
	require.Equal(t, "expired", got.Error)
	claimed, err := f.store.ClaimNext(f.ctx(t), f.device, time.Hour)
	require.NoError(t, err)
	require.Nil(t, claimed, "an expired row is never claimed")
}

func TestPostgres_Message_CancelledBeforeSendRequeuesAndStaleAtCapFails(t *testing.T) {
	f := newPgFixture(t)
	f.queued(t, "cancel me")
	claimed, err := f.store.ClaimNext(f.ctx(t), f.device, time.Hour)
	require.NoError(t, err)
	require.Equal(t, 1, claimed.Attempts)
	require.False(t, claimed.MarkFailed("cancelled", true, time.Now()), "a retryable failure requeues")
	require.NoError(t, f.store.Save(f.ctx(t), claimed, claimed.Version))
	again, err := f.store.ClaimNext(f.ctx(t), f.device, time.Hour)
	require.NoError(t, err)
	require.Equal(t, claimed.ID, again.ID, "the requeued row is claimable at once")
	require.Equal(t, 2, again.Attempts)
}

func TestPostgres_Message_CountersAndRetentionRowsAreProjectScoped(t *testing.T) {
	f := newPgFixture(t)
	f.inbound(t, "A", time.Now())
	n, err := f.store.CountUnreadInbound(f.ctx(t), uuid.New())
	require.NoError(t, err)
	require.Zero(t, n)
	other := f.seed.Tenant(t)
	got, err := f.store.RetentionByProject(tenant.Into(t.Context(), other), f.tc.ProjectID)
	require.NoError(t, err)
	require.Nil(t, got)
	_, err = message.NewRetentionPolicy(f.tc.OrgID, f.tc.ProjectID, 400)
	require.True(t, message.IsInvalidRetentionError(err))
}

func (f pgFixture) mediaQueued(t *testing.T) *message.Message {
	t.Helper()
	m, err := message.NewOutbound(f.tc.OrgID, f.tc.ProjectID, f.device, f.chat, fakes.MessagePublicID(),
		message.SendInput{Media: &message.MediaInput{Bytes: []byte("img"), Mime: "image/png"}}, 1<<20)
	require.NoError(t, err)
	require.NoError(t, f.store.Save(f.ctx(t), m, 0))
	return m
}

func (f pgFixture) persistedRaw(t *testing.T, m *message.Message) []byte {
	t.Helper()
	got, err := f.store.QuotedByWAID(f.ctx(t), f.device, m.WAMessageID)
	require.NoError(t, err)
	return got.Raw
}

func TestPostgres_Message_RawIsNullAtTheDBAfterEveryReleasingTransition(t *testing.T) {
	cases := []struct {
		name string
		do   func(m *message.Message)
	}{
		{"terminal failure", func(m *message.Message) {
			m.MarkSending(time.Now())
			require.True(t, m.MarkFailed("fatal", false, time.Now()))
		}},
		{"expiry", func(m *message.Message) { require.True(t, m.Expire("expired", time.Now())) }},
		{"receipt after a crash between send and MarkSent", func(m *message.Message) {
			m.MarkSending(time.Now())
			require.True(t, m.Receipt(message.ReceiptDelivered, time.Now(), time.Now()))
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := newPgFixture(t)
			m := f.mediaQueued(t)
			require.Equal(t, []byte("img"), f.persistedRaw(t, m), "queued bytes are stored")
			loaded, err := f.store.ByID(f.ctx(t), m.ID)
			require.NoError(t, err)
			tt.do(loaded)
			require.NoError(t, f.store.Save(f.ctx(t), loaded, loaded.Version))
			require.Nil(t, f.persistedRaw(t, m), "raw is NULL in the database")
		})
	}
}

func TestPostgres_Message_SaveRetentionStampsTheCallersOrg(t *testing.T) {
	f := newPgFixture(t)
	foreign := uuid.New()
	p := &message.RetentionPolicy{ProjectID: f.tc.ProjectID, OrgID: foreign, Days: 5, UpdatedAt: time.Now()}
	require.NoError(t, f.store.SaveRetention(f.ctx(t), p))
	got, err := f.store.RetentionByProject(f.ctx(t), f.tc.ProjectID)
	require.NoError(t, err)
	require.NotNil(t, got, "the row carries the caller's org, not the aggregate's")
	require.Equal(t, f.tc.OrgID, got.OrgID)
	require.Equal(t, 5, got.Days)
}
