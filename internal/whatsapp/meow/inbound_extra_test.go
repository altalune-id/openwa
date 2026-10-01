package meow

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"altalune.id/openwa/internal/whatsapp"
)

func TestEnrichInbound_DMAltQuoteAndLocation(t *testing.T) {
	lid := types.NewJID("77", types.HiddenUserServer)
	pn := types.NewJID("628111", types.DefaultUserServer)
	evt := &events.Message{
		Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: lid, Sender: lid, SenderAlt: pn}},
		Message: &waE2E.Message{LocationMessage: &waE2E.LocationMessage{
			DegreesLatitude: proto.Float64(-6.2), DegreesLongitude: proto.Float64(106.8), Name: proto.String("Monas"),
			ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String("Q"), Participant: proto.String("628123456789@s.whatsapp.net")},
		}},
	}
	var m whatsapp.InboundMessage
	enrichInbound(&m, evt)
	require.Equal(t, "628111@s.whatsapp.net", m.ChatAlt)
	require.Equal(t, "628123456789@s.whatsapp.net", m.QuotedSender)
	require.Equal(t, "Monas", m.Location.Name)
}

func TestEnrichInbound_OwnDMUsesRecipientAlt(t *testing.T) {
	lid := types.NewJID("77", types.HiddenUserServer)
	evt := &events.Message{
		Info:    types.MessageInfo{MessageSource: types.MessageSource{Chat: lid, IsFromMe: true, RecipientAlt: types.NewJID("628111", types.DefaultUserServer)}},
		Message: &waE2E.Message{AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(true), Seconds: proto.Uint32(7)}},
	}
	m := whatsapp.InboundMessage{Media: &whatsapp.MediaMeta{}}
	enrichInbound(&m, evt)
	require.Equal(t, "628111@s.whatsapp.net", m.ChatAlt)
	require.True(t, m.Media.Voice)
	require.Equal(t, 7, m.Media.Seconds)
}

func TestEnrichInbound_GroupHasNoChatAlt(t *testing.T) {
	evt := &events.Message{
		Info:    types.MessageInfo{MessageSource: types.MessageSource{Chat: types.NewJID("1203", types.GroupServer), IsGroup: true, SenderAlt: types.NewJID("628111", types.DefaultUserServer)}},
		Message: &waE2E.Message{Conversation: proto.String("hi")},
	}
	var m whatsapp.InboundMessage
	enrichInbound(&m, evt)
	require.Empty(t, m.ChatAlt)
}
