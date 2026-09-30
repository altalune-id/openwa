package meow

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"altalune.id/openwa/internal/whatsapp"
)

//nolint:cyclop // one flat translation table from whatsmeow events to sink calls.
func (s *session) dispatch(evt any) {
	ctx, cancel := context.WithTimeout(context.Background(), dispatchBudget)
	defer cancel()
	switch v := evt.(type) {
	case *events.Connected:
		s.sink.OnState(s.ref, whatsapp.StateConnected, "")
		if err := s.cli.SendPresence(ctx, types.PresenceAvailable); err != nil {
			s.log.DebugContext(ctx, "whatsapp: presence not sent", "device_id", s.deviceID, "err", err)
		}
	case *events.Disconnected:
		s.sink.OnState(s.ref, whatsapp.StateDisconnected, whatsapp.ReasonNetwork)
	case *events.KeepAliveTimeout:
		s.sink.OnDegraded(s.ref, fmt.Errorf("keepalive timeout: %d failures since %s", v.ErrorCount, v.LastSuccess.UTC().Format(time.RFC3339)))
	case *events.KeepAliveRestored:
		s.sink.OnState(s.ref, whatsapp.StateConnected, "")
	case *events.LoggedOut:
		s.sink.OnState(s.ref, whatsapp.StateLoggedOut, whatsapp.ReasonLoggedOutByPhone)
	case *events.StreamReplaced:
		s.sink.OnState(s.ref, whatsapp.StateDisconnected, whatsapp.ReasonStreamReplaced)
	case *events.TemporaryBan:
		s.sink.OnState(s.ref, whatsapp.StateDisconnected, whatsapp.ReasonTempBanPrefix+strconv.Itoa(int(v.Code)))
	case *events.ClientOutdated:
		s.sink.OnState(s.ref, whatsapp.StateDisconnected, whatsapp.ReasonClientOutdated)
	case *events.ConnectFailure:
		s.sink.OnState(s.ref, whatsapp.StateDisconnected, whatsapp.ReasonConnectFailurePrefix+v.Reason.NumberString())
	case *events.PairSuccess:
		s.ref.JID = v.ID.String()
		s.sink.OnLinked(s.ref, whatsapp.Identity{JID: v.ID.String(), LID: v.LID.String(), Phone: v.ID.User, Platform: v.Platform})
	case *events.PairError:
		s.log.WarnContext(ctx, "whatsapp: pairing failed locally", "device_id", s.deviceID, "err", v.Error)
	case *events.Message:
		s.sink.OnMessage(s.ref, s.inbound(ctx, v))
	case *events.Receipt:
		s.sink.OnReceipt(s.ref, receiptOf(v))
	case *events.Contact:
		jid := v.JID.ToNonAD().String()
		s.sink.OnContact(s.ref, whatsapp.ContactUpdate{JID: jid, Phone: whatsapp.PhoneFromJID(jid), Name: v.Action.GetFullName()})
	case *events.PushName:
		jid := v.JID.ToNonAD().String()
		phone := whatsapp.ResolvePhone(jid, v.JIDAlt.String(), func(lid string) string { return s.cli.PNForLID(ctx, lid) })
		s.sink.OnContact(s.ref, whatsapp.ContactUpdate{JID: jid, Phone: phone, PushName: v.NewPushName})
	case *events.HistorySync:
		s.sink.OnHistory(s.ref, s.history(ctx, v))
	}
}

func (s *session) inbound(ctx context.Context, v *events.Message) whatsapp.InboundMessage {
	info := v.Info
	m := whatsapp.InboundMessage{
		ID:           info.ID,
		ChatJID:      info.Chat.ToNonAD().String(),
		SenderJID:    info.Sender.String(),
		SenderAltJID: info.SenderAlt.String(),
		PushName:     info.PushName,
		FromMe:       info.IsFromMe,
		IsGroup:      info.IsGroup,
		Timestamp:    info.Timestamp.UTC(),
	}
	m.SenderPhone = whatsapp.ResolvePhone(m.SenderJID, m.SenderAltJID, func(lid string) string { return s.cli.PNForLID(ctx, lid) })
	fillContent(&m, v.Message)
	if v.RawMessage != nil {
		if raw, err := proto.Marshal(v.RawMessage); err == nil {
			m.Raw = raw
		}
	}
	return m
}

// NOTE: v1 shares one dispatchBudget across a whole history batch, so LID lookups late in a large sync may time out and leave SenderPhone empty.
func (s *session) history(ctx context.Context, v *events.HistorySync) []whatsapp.InboundMessage {
	var out []whatsapp.InboundMessage
	for _, conv := range v.Data.GetConversations() {
		chat, err := types.ParseJID(conv.GetID())
		if err != nil {
			continue
		}
		for _, hm := range conv.GetMessages() {
			evt, err := s.cli.ParseWebMessage(chat, hm.GetMessage())
			if err != nil {
				continue
			}
			out = append(out, s.inbound(ctx, evt))
		}
	}
	return out
}

type mediaMessage interface {
	GetURL() string
	GetDirectPath() string
	GetMediaKey() []byte
	GetFileSHA256() []byte
	GetFileEncSHA256() []byte
	GetFileLength() uint64
	GetMimetype() string
}

func mediaOf(kind string, d mediaMessage) *whatsapp.MediaMeta {
	return &whatsapp.MediaMeta{
		Kind:          kind,
		Mime:          d.GetMimetype(),
		Size:          d.GetFileLength(),
		URL:           d.GetURL(),
		DirectPath:    d.GetDirectPath(),
		MediaKey:      d.GetMediaKey(),
		FileSHA256:    d.GetFileSHA256(),
		FileEncSHA256: d.GetFileEncSHA256(),
	}
}

func contextOf(m *whatsapp.InboundMessage, ci *waE2E.ContextInfo) {
	if ci == nil {
		return
	}
	m.QuotedID = ci.GetStanzaID()
	m.Mentions = ci.GetMentionedJID()
}

//nolint:cyclop // one flat switch over the message content types.
func fillContent(m *whatsapp.InboundMessage, msg *waE2E.Message) {
	switch {
	case msg.GetConversation() != "":
		m.Type, m.Body = "text", msg.GetConversation()
	case msg.GetExtendedTextMessage() != nil:
		t := msg.GetExtendedTextMessage()
		m.Type, m.Body = "text", t.GetText()
		contextOf(m, t.GetContextInfo())
	case msg.GetImageMessage() != nil:
		im := msg.GetImageMessage()
		m.Type, m.Caption, m.Media = "image", im.GetCaption(), mediaOf("image", im)
		contextOf(m, im.GetContextInfo())
	case msg.GetVideoMessage() != nil:
		vm := msg.GetVideoMessage()
		m.Type, m.Caption, m.Media = "video", vm.GetCaption(), mediaOf("video", vm)
		contextOf(m, vm.GetContextInfo())
	case msg.GetAudioMessage() != nil:
		am := msg.GetAudioMessage()
		kind := "audio"
		if am.GetPTT() {
			kind = "voice"
		}
		m.Type, m.Media = kind, mediaOf(kind, am)
		contextOf(m, am.GetContextInfo())
	case msg.GetDocumentMessage() != nil:
		dm := msg.GetDocumentMessage()
		m.Type, m.Caption, m.Media = "document", dm.GetCaption(), mediaOf("document", dm)
		m.Media.FileName = dm.GetFileName()
		contextOf(m, dm.GetContextInfo())
	case msg.GetStickerMessage() != nil:
		sm := msg.GetStickerMessage()
		m.Type, m.Media = "sticker", mediaOf("sticker", sm)
		contextOf(m, sm.GetContextInfo())
	case msg.GetLocationMessage() != nil:
		lm := msg.GetLocationMessage()
		m.Type, m.Body = "location", strconv.FormatFloat(lm.GetDegreesLatitude(), 'f', -1, 64)+","+strconv.FormatFloat(lm.GetDegreesLongitude(), 'f', -1, 64)
	case msg.GetReactionMessage() != nil:
		rm := msg.GetReactionMessage()
		m.Type, m.Body, m.QuotedID = "reaction", rm.GetText(), rm.GetKey().GetID()
	case msg.GetContactMessage() != nil:
		m.Type, m.Body = "contact", msg.GetContactMessage().GetDisplayName()
	case msg.GetProtocolMessage() != nil:
		m.Type = "protocol"
	default:
		m.Type = "unknown"
	}
}

func receiptOf(v *events.Receipt) whatsapp.Receipt {
	ids := make([]string, len(v.MessageIDs))
	copy(ids, v.MessageIDs)
	kind := string(v.Type)
	if v.Type == types.ReceiptTypeDelivered {
		kind = "delivered"
	}
	return whatsapp.Receipt{
		ChatJID:    v.Chat.ToNonAD().String(),
		SenderJID:  v.Sender.String(),
		MessageIDs: ids,
		Type:       kind,
		Timestamp:  v.Timestamp.UTC(),
	}
}
