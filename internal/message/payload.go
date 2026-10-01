package message

import (
	"mime"
	"strings"
	"time"

	"altalune.id/openwa/internal/platform/events"
)

// MediaURL is the authenticated data-plane download of message id; baseURL already carries the base path.
func MediaURL(baseURL, orgSlug, projectSlug, publicID string) string {
	return strings.TrimRight(baseURL, "/") + "/api/v1/orgs/" + orgSlug + "/projects/" + projectSlug + "/messages/" + publicID + "/media"
}

// MediaFilename is the attachment's own filename, or its id with an extension for its mime type.
func MediaFilename(m *Message) string {
	if m.Media != nil && m.Media.Filename != "" {
		return m.Media.Filename
	}
	ext := ""
	if m.Media != nil {
		switch baseMime(m.Media.Mime) {
		case "image/jpeg":
			ext = ".jpg"
		case "audio/ogg":
			ext = ".ogg"
		default:
			if exts, err := mime.ExtensionsByType(baseMime(m.Media.Mime)); err == nil && len(exts) > 0 {
				ext = exts[0]
			}
		}
	}
	return m.PublicID + ext
}

func deviceRefV1(d DeviceRef) events.DeviceRefV1 {
	return events.DeviceRefV1{ID: d.PublicID, Name: d.Name, Phone: d.Phone}
}

func statusPayload(d DeviceRef, m *Message, status, errText string, at time.Time) events.MessageStatusV1 {
	return events.MessageStatusV1{
		Device:  deviceRefV1(d),
		Message: events.MessageRefV1{ID: m.PublicID, WAID: m.WAMessageID},
		Status:  status,
		Error:   errText,
		At:      at.UTC().Truncate(time.Second),
	}
}

func eventPayload(d DeviceRef, chat ChatRef, m *Message, quotedBody, mediaURL string) events.MessageEventV1 {
	msg := events.MessageV1{
		ID: m.PublicID, WAID: m.WAMessageID, Type: string(m.Type), Timestamp: m.WATimestamp.UTC().Truncate(time.Second),
		Mentions: append([]string{}, m.Mentions...),
	}
	switch m.Type {
	case TypeImage, TypeVideo, TypeAudio, TypeDocument:
		msg.Caption = m.Body
	default:
		msg.Body = m.Body
	}
	if m.QuotedWAMessageID != "" {
		msg.Quoted = &events.QuotedV1{WAID: m.QuotedWAMessageID, Body: quotedBody}
	}
	if m.Media != nil {
		msg.Media = &events.MediaV1{Mime: m.Media.Mime, Size: m.Media.Size, Filename: m.Media.Filename, URL: mediaURL, Voice: m.Media.Voice}
	}
	if m.Location != nil {
		msg.Location = &events.LocationV1{Lat: m.Location.Lat, Lng: m.Location.Lng, Name: m.Location.Name, Address: m.Location.Address}
	}
	return events.MessageEventV1{
		Device:  deviceRefV1(d),
		Chat:    events.ChatRefV1{ID: chat.PublicID, JID: chat.JID, Kind: chat.Kind, Name: chat.Name},
		Sender:  events.SenderV1{JID: m.SenderJID, LID: m.SenderLID, Phone: m.SenderPhone, PushName: m.SenderName, FromMe: m.FromMe},
		Message: msg,
	}
}
