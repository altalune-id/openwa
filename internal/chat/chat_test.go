package chat_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/testutil/fakes"
)

func newDM(t *testing.T) *chat.Chat {
	t.Helper()
	c, err := chat.New(uuid.New(), uuid.New(), uuid.New(), fakes.ChatPublicID(), "628111@s.whatsapp.net", "", chat.KindDM)
	require.NoError(t, err)
	return c
}

func TestNew_Validates(t *testing.T) {
	cases := []struct {
		name, jid, lid string
		kind           chat.Kind
		ok             bool
	}{
		{"pn dm", "628111@s.whatsapp.net", "", chat.KindDM, true},
		{"lid dm", "123456@lid", "123456@lid", chat.KindDM, true},
		{"pn with lid alias", "628111@s.whatsapp.net", "99@lid", chat.KindDM, true},
		{"group", "1203@g.us", "", chat.KindGroup, true},
		{"empty", "", "", chat.KindDM, false},
		{"no server", "628111", "", chat.KindDM, false},
		{"lid not a lid", "628111@s.whatsapp.net", "628111@s.whatsapp.net", chat.KindDM, false},
		{"group needs g.us", "628111@s.whatsapp.net", "", chat.KindGroup, false},
		{"unknown kind", "628111@s.whatsapp.net", "", chat.Kind("channel"), false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c, err := chat.New(uuid.New(), uuid.New(), uuid.New(), fakes.ChatPublicID(), tt.jid, tt.lid, tt.kind)
			if !tt.ok {
				require.True(t, chat.IsInvalidJIDError(err), "got %v", err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, c.Version)
			require.Equal(t, 7, int(c.ID.Version()))
			require.Equal(t, c.CreatedAt, c.ActivityAt)
		})
	}
}

func TestTouch_AdvancesOnlyForwardAndCountsInbound(t *testing.T) {
	c := newDM(t)
	t1 := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	c.Touch(t1, "hello", true)
	require.Equal(t, t1, *c.LastMessageAt)
	require.Equal(t, t1, c.ActivityAt)
	require.Equal(t, "hello", c.LastMessagePreview)
	require.Equal(t, 1, c.UnreadCount)

	c.Touch(t1.Add(-time.Hour), "older replay", true)
	require.Equal(t, "hello", c.LastMessagePreview, "an older message must not replace the preview")
	require.Equal(t, 2, c.UnreadCount, "an older inbound message is still unread")

	c.Touch(t1.Add(time.Minute), strings.Repeat("é", 200), false)
	require.Equal(t, 2, c.UnreadCount, "an outbound touch never counts as unread")
	require.Equal(t, chat.MaxPreviewRunes, len([]rune(c.LastMessagePreview)))
}

func TestTouch_InboundUnarchives(t *testing.T) {
	c := newDM(t)
	c.Archive()
	c.Touch(time.Now(), "hi", true)
	require.False(t, c.Archived)
}

func TestMarkRead_ClearsUnread(t *testing.T) {
	c := newDM(t)
	c.Touch(time.Now(), "a", true)
	c.MarkRead()
	require.Zero(t, c.UnreadCount)
}

func TestIdentify_FillsMissingIdentifiers(t *testing.T) {
	c, err := chat.New(uuid.New(), uuid.New(), uuid.New(), fakes.ChatPublicID(), "77@lid", "77@lid", chat.KindDM)
	require.NoError(t, err)
	require.True(t, c.Identify("628111@s.whatsapp.net", "77@lid"), "a phone JID replaces a LID-keyed JID")
	require.Equal(t, "628111@s.whatsapp.net", c.JID)
	require.Equal(t, "77@lid", c.LID)
	require.False(t, c.Identify("628111@s.whatsapp.net", "77@lid"), "nothing new to learn")

	pn := newDM(t)
	require.True(t, pn.Identify("628111@s.whatsapp.net", "88@lid"))
	require.Equal(t, "88@lid", pn.LID)
	require.False(t, pn.Identify("99@lid", ""), "a LID never replaces a phone JID")
	require.Equal(t, "628111@s.whatsapp.net", pn.JID)
}

func TestRepair_ReplacesDerivedFields(t *testing.T) {
	c := newDM(t)
	c.Touch(time.Now(), "gone", true)
	c.Repair(nil, "", 0)
	require.Nil(t, c.LastMessageAt)
	require.Empty(t, c.LastMessagePreview)
	require.Zero(t, c.UnreadCount)
	require.Equal(t, c.CreatedAt, c.ActivityAt)
}

func TestListOpts_WithDefaults(t *testing.T) {
	require.Equal(t, chat.DefaultListLimit, chat.ListOpts{}.WithDefaults().Limit)
	require.Equal(t, chat.MaxListLimit, chat.ListOpts{Limit: 5000}.WithDefaults().Limit)
	require.Equal(t, "bud", chat.ListOpts{Search: "  bud "}.WithDefaults().Search)
}
