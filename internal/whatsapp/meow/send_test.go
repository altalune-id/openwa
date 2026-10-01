package meow

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"altalune.id/openwa/internal/whatsapp"
)

type fakeKeys struct {
	reactionSender types.JID
	revokeSender   types.JID
	editID         string
}

func (f *fakeKeys) BuildReaction(chat, sender types.JID, id types.MessageID, reaction string) *waE2E.Message {
	f.reactionSender = sender
	return &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Text: proto.String(reaction)}}
}

func (f *fakeKeys) BuildRevoke(chat, sender types.JID, id types.MessageID) *waE2E.Message {
	f.revokeSender = sender
	return &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_REVOKE.Enum()}}
}

func (f *fakeKeys) BuildEdit(chat types.JID, id types.MessageID, content *waE2E.Message) *waE2E.Message {
	f.editID = id
	return &waE2E.Message{EditedMessage: &waE2E.FutureProofMessage{Message: content}}
}

//nolint:gochecknoglobals // immutable test fixtures.
var (
	to  = types.NewJID("628111", types.DefaultUserServer)
	own = types.NewJID("628123456789", types.DefaultUserServer)
	up  = &whatsmeow.UploadResponse{URL: "https://mmg/x", DirectPath: "/v/t62", MediaKey: []byte{1}, FileEncSHA256: []byte{2}, FileSHA256: []byte{3}, FileLength: 9}
)

func TestBuildMessage_Text(t *testing.T) {
	msg, err := buildMessage(whatsapp.OutboundMessage{Kind: whatsapp.KindText, Text: "hi"}, to, nil, own, &fakeKeys{})
	require.NoError(t, err)
	require.Equal(t, "hi", msg.GetConversation(), "plain text uses Conversation")
	require.Nil(t, msg.GetExtendedTextMessage())
}

func TestBuildMessage_MentionsAndQuoteUseExtendedText(t *testing.T) {
	quoted, err := proto.Marshal(&waE2E.Message{Conversation: proto.String("original")})
	require.NoError(t, err)
	msg, err := buildMessage(whatsapp.OutboundMessage{
		Kind: whatsapp.KindText, Text: "@628222 ok", Mentions: []string{"628222"},
		QuotedWAID: "Q1", QuotedSender: "628222@s.whatsapp.net", QuotedRaw: quoted,
	}, to, nil, own, &fakeKeys{})
	require.NoError(t, err)
	ext := msg.GetExtendedTextMessage()
	require.Equal(t, "@628222 ok", ext.GetText())
	ci := ext.GetContextInfo()
	require.Equal(t, []string{"628222@s.whatsapp.net"}, ci.GetMentionedJID())
	require.Equal(t, "Q1", ci.GetStanzaID())
	require.Equal(t, "628222@s.whatsapp.net", ci.GetParticipant())
	require.Equal(t, "original", ci.GetQuotedMessage().GetConversation())
}

func TestBuildMessage_QuoteOfOwnMessageNamesOwnJIDAndFallsBackToBody(t *testing.T) {
	msg, err := buildMessage(whatsapp.OutboundMessage{Kind: whatsapp.KindText, Text: "x", QuotedWAID: "Q2", QuotedBody: "mine"}, to, nil, own, &fakeKeys{})
	require.NoError(t, err)
	ci := msg.GetExtendedTextMessage().GetContextInfo()
	require.Equal(t, own.String(), ci.GetParticipant())
	require.Equal(t, "mine", ci.GetQuotedMessage().GetConversation())
}

func TestBuildMessage_MediaKinds(t *testing.T) {
	cases := []struct {
		kind  string
		media whatsapp.OutboundMedia
		check func(t *testing.T, m *waE2E.Message)
	}{
		{whatsapp.KindImage, whatsapp.OutboundMedia{Mime: "image/jpeg", Caption: "c"}, func(t *testing.T, m *waE2E.Message) {
			im := m.GetImageMessage()
			require.Equal(t, "c", im.GetCaption())
			require.Equal(t, "image/jpeg", im.GetMimetype())
			require.Equal(t, "/v/t62", im.GetDirectPath())
			require.Equal(t, uint64(9), im.GetFileLength())
			require.Equal(t, []byte{1}, im.GetMediaKey())
		}},
		{whatsapp.KindVideo, whatsapp.OutboundMedia{Mime: "video/mp4", Caption: "v"}, func(t *testing.T, m *waE2E.Message) {
			require.Equal(t, "v", m.GetVideoMessage().GetCaption())
			require.Equal(t, []byte{3}, m.GetVideoMessage().GetFileSHA256())
		}},
		{whatsapp.KindAudio, whatsapp.OutboundMedia{Mime: "audio/ogg; codecs=opus", Voice: true}, func(t *testing.T, m *waE2E.Message) {
			require.True(t, m.GetAudioMessage().GetPTT())
			require.Equal(t, []byte{2}, m.GetAudioMessage().GetFileEncSHA256())
		}},
		{whatsapp.KindDocument, whatsapp.OutboundMedia{Mime: "application/pdf", Filename: "a.pdf", Caption: "d"}, func(t *testing.T, m *waE2E.Message) {
			doc := m.GetDocumentMessage()
			require.Equal(t, "a.pdf", doc.GetFileName())
			require.Equal(t, "a.pdf", doc.GetTitle())
			require.Equal(t, "d", doc.GetCaption())
		}},
	}
	for _, tt := range cases {
		t.Run(tt.kind, func(t *testing.T) {
			media := tt.media
			msg, err := buildMessage(whatsapp.OutboundMessage{Kind: tt.kind, Media: &media}, to, up, own, &fakeKeys{})
			require.NoError(t, err)
			tt.check(t, msg)
		})
	}
	_, err := buildMessage(whatsapp.OutboundMessage{Kind: whatsapp.KindImage, Media: &whatsapp.OutboundMedia{}}, to, nil, own, &fakeKeys{})
	require.Error(t, err, "media without an upload is refused")
}

func TestBuildMessage_Location(t *testing.T) {
	msg, err := buildMessage(whatsapp.OutboundMessage{Kind: whatsapp.KindLocation, Location: &whatsapp.Location{Lat: -6.2, Lng: 106.8, Name: "Monas", Address: "Jakarta"}}, to, nil, own, &fakeKeys{})
	require.NoError(t, err)
	loc := msg.GetLocationMessage()
	require.InDelta(t, -6.2, loc.GetDegreesLatitude(), 1e-9)
	require.InDelta(t, 106.8, loc.GetDegreesLongitude(), 1e-9)
	require.Equal(t, "Monas", loc.GetName())
}

func TestBuildMessage_ReactionRevokeEditDelegateToTheClientBuilders(t *testing.T) {
	keys := &fakeKeys{}
	msg, err := buildMessage(whatsapp.OutboundMessage{Kind: whatsapp.KindReaction, Text: "👍", TargetWAID: "T", TargetSender: "628222@s.whatsapp.net"}, to, nil, own, keys)
	require.NoError(t, err)
	require.Equal(t, "👍", msg.GetReactionMessage().GetText())
	require.Equal(t, "628222@s.whatsapp.net", keys.reactionSender.String())

	_, err = buildMessage(whatsapp.OutboundMessage{Kind: whatsapp.KindReaction, Text: "👍", TargetWAID: "T", TargetFromMe: true}, to, nil, own, keys)
	require.NoError(t, err)
	require.True(t, keys.reactionSender.IsEmpty(), "a reaction to our own message passes EmptyJID")

	msg, err = buildMessage(whatsapp.OutboundMessage{Kind: whatsapp.KindRevoke, TargetWAID: "T"}, to, nil, own, keys)
	require.NoError(t, err)
	require.Equal(t, waE2E.ProtocolMessage_REVOKE, msg.GetProtocolMessage().GetType())
	require.True(t, keys.revokeSender.IsEmpty())

	msg, err = buildMessage(whatsapp.OutboundMessage{Kind: whatsapp.KindEdit, Text: "fixed", TargetWAID: "T"}, to, nil, own, keys)
	require.NoError(t, err)
	require.Equal(t, "T", keys.editID)
	require.Equal(t, "fixed", msg.GetEditedMessage().GetMessage().GetConversation())

	_, err = buildMessage(whatsapp.OutboundMessage{Kind: "poll"}, to, nil, own, keys)
	require.True(t, whatsapp.IsUnsupportedError(err))
}
