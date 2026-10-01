package meow

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/whatsapp"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newTestSession(t *testing.T) (*session, *fakeClient, *fakes.Sink) {
	t.Helper()
	fc := newFakeClient()
	sink := &fakes.Sink{}
	ref := whatsapp.SessionRef{DeviceID: uuid.New(), OrgID: uuid.New(), ProjectID: uuid.New(), JID: "628111:1@s.whatsapp.net"}
	s := newSession(ref, fc, sink, 8, discard())
	t.Cleanup(func() { _ = s.Close(t.Context()) })
	return s, fc, sink
}

func TestDispatch_ConnectionEvents(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		evt    any
		kind   string
		state  whatsapp.State
		reason string
	}{
		{"connected", &events.Connected{}, "state", whatsapp.StateConnected, ""},
		{"disconnected", &events.Disconnected{}, "state", whatsapp.StateDisconnected, whatsapp.ReasonNetwork},
		{"keepalive restored", &events.KeepAliveRestored{}, "state", whatsapp.StateConnected, ""},
		{"logged out", &events.LoggedOut{}, "state", whatsapp.StateLoggedOut, whatsapp.ReasonLoggedOutByPhone},
		{"stream replaced", &events.StreamReplaced{}, "state", whatsapp.StateDisconnected, whatsapp.ReasonStreamReplaced},
		{"temporary ban", &events.TemporaryBan{Code: events.TempBanSentToTooManyPeople}, "state", whatsapp.StateDisconnected, "temp_ban_101"},
		{"client outdated", &events.ClientOutdated{}, "state", whatsapp.StateDisconnected, whatsapp.ReasonClientOutdated},
		{"connect failure", &events.ConnectFailure{Reason: events.ConnectFailureReason(403)}, "state", whatsapp.StateDisconnected, "connect_failure_403"},
		{"keepalive timeout", &events.KeepAliveTimeout{ErrorCount: 2, LastSuccess: time.Unix(0, 0)}, "degraded", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, fc, sink := newTestSession(t)
			s.dispatch(tc.evt)
			calls := sink.Calls()
			require.Len(t, calls, 1)
			require.Equal(t, tc.kind, calls[0].Kind)
			require.Equal(t, tc.state, calls[0].State)
			require.Equal(t, tc.reason, calls[0].Reason)
			if tc.name == "connected" {
				fc.mu.Lock()
				require.Equal(t, 1, fc.presence, "Connected sends presence available")
				fc.mu.Unlock()
			}
			if tc.kind == "degraded" {
				require.ErrorContains(t, calls[0].Err, "keepalive")
			}
		})
	}
}

func TestDispatch_PairSuccessReportsTheIdentityAndCarriesTheJIDForward(t *testing.T) {
	t.Parallel()
	s, _, sink := newTestSession(t)
	s.ref.JID = ""
	s.dispatch(&events.PairSuccess{ID: types.NewADJID("628222", 0, 3), LID: types.NewJID("99", types.HiddenUserServer), Platform: "android"})
	s.dispatch(&events.Connected{})
	calls := sink.Calls()
	require.Len(t, calls, 2)
	require.Equal(t, "linked", calls[0].Kind)
	require.Equal(t, whatsapp.Identity{JID: "628222:3@s.whatsapp.net", LID: "99@lid", Phone: "628222", Platform: "android"}, calls[0].Identity)
	require.Equal(t, "628222:3@s.whatsapp.net", calls[1].Ref.JID, "events after pairing carry the new JID")
}

func TestDispatch_TextMessageResolvesThePhoneFromTheAltJID(t *testing.T) {
	t.Parallel()
	s, _, sink := newTestSession(t)
	msg := &waE2E.Message{Conversation: proto.String("halo")}
	ts := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	s.dispatch(&events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:      types.NewJID("55", types.HiddenUserServer),
				Sender:    types.NewJID("55", types.HiddenUserServer),
				SenderAlt: types.NewJID("628333", types.DefaultUserServer),
			},
			ID: "3EB0A", PushName: "Budi", Timestamp: ts,
		},
		Message:    msg,
		RawMessage: msg,
	})
	calls := sink.Calls()
	require.Len(t, calls, 1)
	m := calls[0].Message
	require.Equal(t, "message", calls[0].Kind)
	require.Equal(t, "3EB0A", m.ID)
	require.Equal(t, "55@lid", m.ChatJID)
	require.Equal(t, "628333", m.SenderPhone)
	require.Equal(t, "text", m.Type)
	require.Equal(t, "halo", m.Body)
	require.Equal(t, "Budi", m.PushName)
	require.Equal(t, ts, m.Timestamp)
	require.NotEmpty(t, m.Raw)
}

func TestDispatch_LIDSenderWithoutAltUsesTheLIDMap(t *testing.T) {
	t.Parallel()
	s, fc, sink := newTestSession(t)
	fc.pn["77@lid"] = "628444@s.whatsapp.net"
	s.dispatch(&events.Message{
		Info:    types.MessageInfo{MessageSource: types.MessageSource{Chat: types.NewJID("1", types.GroupServer), Sender: types.NewJID("77", types.HiddenUserServer), IsGroup: true}, ID: "X"},
		Message: &waE2E.Message{Conversation: proto.String("x")},
	})
	m := sink.Calls()[0].Message
	require.Equal(t, "628444", m.SenderPhone)
	require.True(t, m.IsGroup)
}

func TestDispatch_ImageMessageCarriesMediaKeysAndContext(t *testing.T) {
	t.Parallel()
	s, _, sink := newTestSession(t)
	s.dispatch(&events.Message{
		Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: types.NewJID("628111", types.DefaultUserServer), Sender: types.NewJID("628111", types.DefaultUserServer)}, ID: "IMG"},
		Message: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Mimetype: proto.String("image/jpeg"), Caption: proto.String("cap"), URL: proto.String("https://mmg"),
			DirectPath: proto.String("/v/t62"), MediaKey: []byte{1}, FileSHA256: []byte{2}, FileEncSHA256: []byte{3},
			FileLength:  proto.Uint64(1234),
			ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("Q1"), MentionedJID: []string{"628999@s.whatsapp.net"}},
		}},
	})
	m := sink.Calls()[0].Message
	require.Equal(t, "image", m.Type)
	require.Equal(t, "cap", m.Caption)
	require.Equal(t, &whatsapp.MediaMeta{Kind: "image", Mime: "image/jpeg", Size: 1234, URL: "https://mmg", DirectPath: "/v/t62", MediaKey: []byte{1}, FileSHA256: []byte{2}, FileEncSHA256: []byte{3}}, m.Media)
	require.Equal(t, "Q1", m.QuotedID)
	require.Equal(t, []string{"628999@s.whatsapp.net"}, m.Mentions)
}

func TestDispatch_ReceiptContactPushNameHistory(t *testing.T) {
	t.Parallel()
	s, _, sink := newTestSession(t)
	chat := types.NewJID("628111", types.DefaultUserServer)
	s.dispatch(&events.Receipt{MessageSource: types.MessageSource{Chat: chat, Sender: chat}, MessageIDs: []types.MessageID{"A", "B"}, Type: types.ReceiptTypeRead})
	s.dispatch(&events.Receipt{MessageSource: types.MessageSource{Chat: chat, Sender: chat}, MessageIDs: []types.MessageID{"C"}})
	s.dispatch(&events.Contact{JID: chat, Action: &waSyncAction.ContactAction{FullName: proto.String("Budi Santoso")}})
	s.dispatch(&events.PushName{JID: chat, NewPushName: "Budi"})
	s.dispatch(&events.HistorySync{Data: &waHistorySync.HistorySync{Conversations: []*waHistorySync.Conversation{{
		ID: proto.String("628111@s.whatsapp.net"), Messages: []*waHistorySync.HistorySyncMsg{{}},
	}}}})

	calls := sink.Calls()
	require.Len(t, calls, 5)
	require.Equal(t, whatsapp.Receipt{ChatJID: "628111@s.whatsapp.net", SenderJID: "628111@s.whatsapp.net", MessageIDs: []string{"A", "B"}, Type: "read"}, calls[0].Receipt)
	require.Equal(t, "delivered", calls[1].Receipt.Type)
	require.Equal(t, whatsapp.ContactUpdate{JID: "628111@s.whatsapp.net", Phone: "628111", Name: "Budi Santoso"}, calls[2].Contact)
	require.Equal(t, "Budi", calls[3].Contact.PushName)
	require.Equal(t, "history", calls[4].Kind)
	require.Len(t, calls[4].History, 1)
	require.Equal(t, "HIST1", calls[4].History[0].ID)
}

func TestDispatch_UnknownEventsAreIgnored(t *testing.T) {
	t.Parallel()
	s, _, sink := newTestSession(t)
	s.dispatch(&events.OfflineSyncCompleted{Count: 3})
	s.dispatch(&events.PairError{})
	require.Empty(t, sink.Calls())
}
