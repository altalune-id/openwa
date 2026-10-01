package fakes_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
)

func TestMessageFakeMirrorsTheRealStoreContract(t *testing.T) {
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()}
	ctx := tenant.Into(t.Context(), tc)
	store := fakes.NewMessage()
	dev := uuid.New()
	m, err := message.NewOutbound(tc.OrgID, tc.ProjectID, dev, uuid.New(), fakes.MessagePublicID(), message.SendInput{Text: "x"}, 1)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, m, 0))
	require.True(t, message.IsVersionMismatchError(store.Save(ctx, m, 5)))

	claimed, err := store.ClaimNext(ctx, dev, time.Hour)
	require.NoError(t, err)
	require.Equal(t, m.ID, claimed.ID)
	again, err := store.ClaimNext(ctx, dev, time.Hour)
	require.NoError(t, err)
	require.Nil(t, again, "a fresh sending row is not reclaimed")

	in := message.NewInbound(message.Ref{DeviceID: dev, OrgID: tc.OrgID, ProjectID: tc.ProjectID}, uuid.New(), fakes.MessagePublicID(), message.InboundInput{WAID: "A", Type: "text"})
	ok, err := store.InsertInbound(ctx, in)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = store.InsertInbound(ctx, in)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestMessageFakeKeepsRawOnAnyUpdateUntilReleased(t *testing.T) {
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()}
	ctx := tenant.Into(t.Context(), tc)
	store := fakes.NewMessage()
	dev := uuid.New()
	m, err := message.NewOutbound(tc.OrgID, tc.ProjectID, dev, uuid.New(), fakes.MessagePublicID(),
		message.SendInput{Media: &message.MediaInput{Bytes: []byte("img"), Mime: "image/png"}}, 1<<20)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, m, 0))

	for _, ifVersion := range []int{0, 1} {
		loaded, err := store.ByID(ctx, m.ID)
		require.NoError(t, err)
		require.Nil(t, loaded.Raw, "reads omit raw")
		loaded.MarkSending(time.Now())
		loaded.Release(time.Now())
		require.NoError(t, store.Save(ctx, loaded, map[int]int{0: 0, 1: loaded.Version}[ifVersion]))
		quoted, err := store.QuotedByWAID(ctx, dev, m.WAMessageID)
		require.NoError(t, err)
		require.Equal(t, []byte("img"), quoted.Raw, "an update that did not release the bytes keeps them, ifVersion %d", ifVersion)
	}

	loaded, err := store.ByID(ctx, m.ID)
	require.NoError(t, err)
	loaded.MarkSending(time.Now())
	require.True(t, loaded.Receipt(message.ReceiptDelivered, time.Now(), time.Now()))
	require.NoError(t, store.Save(ctx, loaded, loaded.Version))
	quoted, err := store.QuotedByWAID(ctx, dev, m.WAMessageID)
	require.NoError(t, err)
	require.Nil(t, quoted.Raw)
}

func TestMessageFakeClaimNextReturnsAnExhaustedRowFailed(t *testing.T) {
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()}
	ctx := tenant.Into(t.Context(), tc)
	store := fakes.NewMessage()
	dev := uuid.New()
	m, err := message.NewOutbound(tc.OrgID, tc.ProjectID, dev, uuid.New(), fakes.MessagePublicID(), message.SendInput{Text: "x"}, 1)
	require.NoError(t, err)
	m.Status, m.Attempts = message.StatusSending, message.MaxAttempts
	m.UpdatedAt = time.Now().Add(-time.Hour)
	store.Seed(m)
	got, err := store.ClaimNext(ctx, dev, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, message.StatusFailed, got.Status)
	stored, err := store.ByID(ctx, m.ID)
	require.NoError(t, err)
	require.Equal(t, message.StatusFailed, stored.Status)
}
