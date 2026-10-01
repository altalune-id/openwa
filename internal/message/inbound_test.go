package message_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/platform/events"
)

func (e *env) ref() message.Ref {
	return message.Ref{DeviceID: e.device, OrgID: e.tc.OrgID, ProjectID: e.tc.ProjectID}
}

func dm(waid string) message.InboundInput {
	return message.InboundInput{
		WAID: waid, ChatJID: "77@lid", ChatAlt: "628111222333@s.whatsapp.net",
		SenderJID: "77@lid", SenderAlt: "628111222333@s.whatsapp.net", SenderPhone: "628111222333", PushName: "Budi",
		Timestamp: time.Date(2026, 9, 28, 10, 15, 0, 0, time.UTC), Type: "text", Body: "halo",
	}
}

func TestRecordInbound_ResolvesThePhoneChatAndEmitsReceived(t *testing.T) {
	e := newEnv(t)
	require.NoError(t, e.svc.Recorder().RecordInbound(t.Context(), e.ref(), dm("WA1")))
	require.Equal(t, "628111222333@s.whatsapp.net", e.chats.Ensured[0].JID, "the chat is keyed by the phone JID")
	require.Equal(t, "77@lid", e.chats.Ensured[0].LID)
	require.Equal(t, "Budi", e.chats.Ensured[0].Name)
	require.Equal(t, "628111222333@s.whatsapp.net", e.contacts.Calls[0].JID)
	require.True(t, e.chats.Touches[0].Inbound)

	calls := e.hooks.Recorded()
	require.Len(t, calls, 1)
	require.Equal(t, events.MessageReceived, calls[0].Type)
	require.True(t, calls[0].InTx, "the event joins the insert's transaction")
	ev := calls[0].Data.(events.MessageEventV1)
	require.Equal(t, "628111222333", ev.Sender.Phone)
	require.Equal(t, "628111222333@s.whatsapp.net", ev.Chat.JID)
	require.Equal(t, "halo", ev.Message.Body)
	require.NotNil(t, ev.Message.Mentions)
	require.Nil(t, ev.Matched)
}

func TestRecordInbound_DedupsOnInsertIgnore(t *testing.T) {
	e := newEnv(t)
	e.store.InsertInboundFn = func(context.Context, *message.Message) (bool, error) { return false, nil }
	require.NoError(t, e.svc.Recorder().RecordInbound(t.Context(), e.ref(), dm("WA1")))
	require.Empty(t, e.hooks.Recorded())
	require.Empty(t, e.chats.Touches)
	require.Empty(t, e.contacts.Calls)
}

func TestRecordInbound_FromMeIsStoredButSilent(t *testing.T) {
	e := newEnv(t)
	in := dm("WA2")
	in.FromMe = true
	require.NoError(t, e.svc.Recorder().RecordInbound(t.Context(), e.ref(), in))
	require.Empty(t, e.hooks.Recorded())
	require.Empty(t, e.devices.Inputs, "rules are not evaluated for our own messages")
	require.False(t, e.chats.Touches[0].Inbound)
	require.Empty(t, e.contacts.Calls)
	all := e.store.All()
	require.Len(t, all, 1)
	require.Equal(t, message.StatusSent, all[0].Status)
}

func TestRecordInbound_MatchedEmitsBothWithReasons(t *testing.T) {
	e := newEnv(t)
	e.devices.Matched, e.devices.Reasons = true, []string{"group_mention"}
	in := message.InboundInput{
		WAID: "G1", ChatJID: "120363@g.us", IsGroup: true, SenderJID: "628111222333@s.whatsapp.net", SenderPhone: "628111222333",
		Type: "image", Caption: "@628123456789 cek", Mentions: []string{"628123456789@s.whatsapp.net"}, QuotedSender: "628123456789@s.whatsapp.net",
		Media: &message.InboundMedia{Mime: "image/jpeg", Size: 10, Keys: message.MediaKeys{DirectPath: "/v/t"}},
	}
	require.NoError(t, e.svc.Recorder().RecordInbound(t.Context(), e.ref(), in))
	calls := e.hooks.Recorded()
	require.Len(t, calls, 2)
	require.Equal(t, events.MessageReceived, calls[0].Type)
	require.Equal(t, events.MessageMatched, calls[1].Type)
	matched := calls[1].Data.(events.MessageEventV1)
	require.Equal(t, []string{"group_mention"}, matched.Matched.Reasons)
	require.True(t, strings.HasPrefix(matched.Message.ID, "msg_"), "the payload names the public id")
	require.Equal(t, "https://wa.example.com/api/v1/orgs/acme/projects/main/messages/"+matched.Message.ID+"/media", matched.Message.Media.URL)
	require.Equal(t, "@628123456789 cek", matched.Message.Caption)
	require.Empty(t, matched.Message.Body)
	require.Equal(t, "group", e.chats.Ensured[0].Kind)
	require.Equal(t, []string{"628123456789@s.whatsapp.net"}, e.devices.Inputs[0].Mentions)
	require.Equal(t, "628123456789@s.whatsapp.net", e.devices.Inputs[0].QuotedSender)
}

func TestRecordBatch_StoresWithoutEvents(t *testing.T) {
	e := newEnv(t)
	require.NoError(t, e.svc.Recorder().RecordBatch(t.Context(), e.ref(), []message.InboundInput{dm("H1"), dm("H2"), dm("H1")}))
	require.Len(t, e.store.All(), 2, "the batch shares record, so dedup holds inside it too")
	require.Empty(t, e.hooks.Recorded())
}
