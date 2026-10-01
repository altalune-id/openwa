package meow

import (
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"altalune.id/openwa/internal/whatsapp"
)

// NOTE: the part of *whatsmeow.Client that needs the account's identity to build a message key.
type keyBuilder interface {
	BuildReaction(chat, sender types.JID, id types.MessageID, reaction string) *waE2E.Message
	BuildRevoke(chat, sender types.JID, id types.MessageID) *waE2E.Message
	BuildEdit(chat types.JID, id types.MessageID, newContent *waE2E.Message) *waE2E.Message
}

var _ keyBuilder = (*whatsmeow.Client)(nil)

func buildMessage(out whatsapp.OutboundMessage, to types.JID, up *whatsmeow.UploadResponse, own types.JID, kb keyBuilder) (*waE2E.Message, error) {
	ci := contextInfo(out, own)
	switch out.Kind {
	case whatsapp.KindText:
		if ci == nil {
			return &waE2E.Message{Conversation: proto.String(out.Text)}, nil
		}
		return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String(out.Text), ContextInfo: ci}}, nil
	case whatsapp.KindImage, whatsapp.KindVideo, whatsapp.KindAudio, whatsapp.KindDocument:
		return buildMedia(out, up, ci)
	case whatsapp.KindLocation:
		if out.Location == nil {
			return nil, &whatsapp.UnsupportedError{Feature: "location without coordinates"}
		}
		return &waE2E.Message{LocationMessage: &waE2E.LocationMessage{
			DegreesLatitude: proto.Float64(out.Location.Lat), DegreesLongitude: proto.Float64(out.Location.Lng),
			Name: optString(out.Location.Name), Address: optString(out.Location.Address), ContextInfo: ci,
		}}, nil
	case whatsapp.KindReaction:
		sender := types.EmptyJID
		if !out.TargetFromMe && out.TargetSender != "" {
			parsed, err := types.ParseJID(out.TargetSender)
			if err != nil {
				return nil, &whatsapp.InvalidJIDError{Raw: out.TargetSender}
			}
			sender = parsed.ToNonAD()
		}
		return kb.BuildReaction(to, sender, out.TargetWAID, out.Text), nil
	case whatsapp.KindRevoke:
		return kb.BuildRevoke(to, types.EmptyJID, out.TargetWAID), nil
	case whatsapp.KindEdit:
		return kb.BuildEdit(to, out.TargetWAID, &waE2E.Message{Conversation: proto.String(out.Text)}), nil
	}
	return nil, &whatsapp.UnsupportedError{Feature: "send " + out.Kind}
}

func buildMedia(out whatsapp.OutboundMessage, up *whatsmeow.UploadResponse, ci *waE2E.ContextInfo) (*waE2E.Message, error) {
	if up == nil || out.Media == nil {
		return nil, &whatsapp.UnsupportedError{Feature: "media without an upload"}
	}
	md := out.Media
	switch out.Kind {
	case whatsapp.KindImage:
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Caption: optString(md.Caption), Mimetype: proto.String(md.Mime), URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: proto.Uint64(up.FileLength), ContextInfo: ci,
		}}, nil
	case whatsapp.KindVideo:
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			Caption: optString(md.Caption), Mimetype: proto.String(md.Mime), URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: proto.Uint64(up.FileLength), ContextInfo: ci,
		}}, nil
	case whatsapp.KindAudio:
		return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			Mimetype: proto.String(md.Mime), URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), PTT: proto.Bool(md.Voice),
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: proto.Uint64(up.FileLength), ContextInfo: ci,
		}}, nil
	default:
		return &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			Caption: optString(md.Caption), Mimetype: proto.String(md.Mime), FileName: proto.String(md.Filename), Title: proto.String(md.Filename),
			URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: proto.Uint64(up.FileLength), ContextInfo: ci,
		}}, nil
	}
}

func contextInfo(out whatsapp.OutboundMessage, own types.JID) *waE2E.ContextInfo {
	if out.QuotedWAID == "" && len(out.Mentions) == 0 {
		return nil
	}
	ci := &waE2E.ContextInfo{}
	for _, phone := range out.Mentions {
		ci.MentionedJID = append(ci.MentionedJID, phone+"@"+types.DefaultUserServer)
	}
	if out.QuotedWAID != "" {
		participant := out.QuotedSender
		if participant == "" {
			participant = own.ToNonAD().String()
		}
		quoted := &waE2E.Message{}
		if len(out.QuotedRaw) == 0 || proto.Unmarshal(out.QuotedRaw, quoted) != nil {
			quoted = &waE2E.Message{Conversation: proto.String(out.QuotedBody)}
		}
		ci.StanzaID, ci.Participant, ci.QuotedMessage = proto.String(out.QuotedWAID), proto.String(participant), quoted
	}
	return ci
}

func mediaTypeFor(kind string) (whatsmeow.MediaType, bool) {
	switch kind {
	case whatsapp.KindImage:
		return whatsmeow.MediaImage, true
	case whatsapp.KindVideo:
		return whatsmeow.MediaVideo, true
	case whatsapp.KindAudio:
		return whatsmeow.MediaAudio, true
	case whatsapp.KindDocument:
		return whatsmeow.MediaDocument, true
	}
	return "", false
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return proto.String(s)
}
