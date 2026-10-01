package meow

import (
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"

	"altalune.id/openwa/internal/whatsapp"
)

// NOTE: fills the fields messaging needs beyond plan 03's translation: the chat's alternate address, the quoted sender, a location and voice-note metadata.
func enrichInbound(m *whatsapp.InboundMessage, evt *events.Message) {
	src := evt.Info.MessageSource
	if !src.IsGroup {
		alt := src.SenderAlt
		if src.IsFromMe {
			alt = src.RecipientAlt
		}
		if !alt.IsEmpty() {
			m.ChatAlt = alt.ToNonAD().String()
		}
	}
	msg := evt.Message
	if ci := contextInfoOf(msg); ci != nil && ci.GetStanzaID() != "" {
		m.QuotedSender = ci.GetParticipant()
	}
	if loc := msg.GetLocationMessage(); loc != nil {
		m.Location = &whatsapp.Location{Lat: loc.GetDegreesLatitude(), Lng: loc.GetDegreesLongitude(), Name: loc.GetName(), Address: loc.GetAddress()}
	}
	if m.Media == nil {
		return
	}
	switch {
	case msg.GetAudioMessage() != nil:
		m.Media.Voice, m.Media.Seconds = msg.GetAudioMessage().GetPTT(), int(msg.GetAudioMessage().GetSeconds())
	case msg.GetVideoMessage() != nil:
		v := msg.GetVideoMessage()
		m.Media.Seconds, m.Media.Width, m.Media.Height = int(v.GetSeconds()), int(v.GetWidth()), int(v.GetHeight())
	case msg.GetImageMessage() != nil:
		m.Media.Width, m.Media.Height = int(msg.GetImageMessage().GetWidth()), int(msg.GetImageMessage().GetHeight())
	}
}

func contextInfoOf(msg *waE2E.Message) *waE2E.ContextInfo {
	for _, ci := range []*waE2E.ContextInfo{
		msg.GetExtendedTextMessage().GetContextInfo(), msg.GetImageMessage().GetContextInfo(),
		msg.GetVideoMessage().GetContextInfo(), msg.GetAudioMessage().GetContextInfo(),
		msg.GetDocumentMessage().GetContextInfo(), msg.GetLocationMessage().GetContextInfo(),
	} {
		if ci != nil {
			return ci
		}
	}
	return nil
}
