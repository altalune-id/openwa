package handlers

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/platform/keyset"
	"altalune.id/openwa/internal/web/templates"
)

//nolint:gochecknoglobals // the fixed reaction picker.
var inboxReactions = []string{"👍", "❤️", "😂", "😮", "😢", "🙏"}

func chatRows(items []*chat.Chat, active string) []templates.ChatRowView {
	rows := make([]templates.ChatRowView, 0, len(items))
	for _, c := range items {
		name := c.Name
		if name == "" {
			name, _, _ = strings.Cut(c.JID, "@")
		}
		rows = append(rows, templates.ChatRowView{
			ID: c.PublicID, Name: name, JID: c.JID, Kind: string(c.Kind), Preview: c.LastMessagePreview,
			At: c.LastMessageAt, Unread: c.UnreadCount, Active: c.PublicID == active,
		})
	}
	return rows
}

// NOTE: turns messages (oldest first) into bubbles; reactions and quoted text are looked up in around as well, so a bubble drawn from a small slice keeps both. Edits and revokes are not drawn.
func bubbles(msgs, around []*message.Message, mediaURL func(publicID string) string) []templates.BubbleView {
	known := slices.Concat(around, msgs)
	slices.SortStableFunc(known, func(a, b *message.Message) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	known = slices.CompactFunc(known, func(a, b *message.Message) bool { return a.ID == b.ID })
	reactions := map[string][]string{}
	bodyByWAID := map[string]string{}
	for _, m := range known {
		if m.Type == message.TypeReaction && m.Body != "" {
			reactions[m.TargetWAMessageID] = append(reactions[m.TargetWAMessageID], m.Body)
		}
		bodyByWAID[m.WAMessageID] = m.Body
	}
	out := make([]templates.BubbleView, 0, len(msgs))
	for _, m := range msgs {
		if m.Type == message.TypeReaction || m.Type == message.TypeRevoke || m.Type == message.TypeEdit {
			continue
		}
		b := templates.BubbleView{
			ID: bubbleID(m), WAID: m.WAMessageID, Outbound: m.Direction == message.DirectionOut, Type: string(m.Type),
			Body: m.Body, Status: string(m.Status), At: m.WATimestamp, Error: m.Error, Reactions: reactions[m.WAMessageID],
			Own: m.FromMe && m.Direction == message.DirectionOut,
		}
		if m.QuotedWAMessageID != "" {
			b.Quoted = bodyByWAID[m.QuotedWAMessageID]
		}
		if m.Media != nil && m.Media.Keys.DirectPath != "" {
			b.MediaURL, b.Filename = mediaURL(m.PublicID), message.MediaFilename(m)
			b.IsImage = message.InlineSafe(m.Media.Mime)
		}
		if m.Location != nil {
			b.Location = fmt.Sprintf("%s %.5f, %.5f", m.Location.Name, m.Location.Lat, m.Location.Lng)
		}
		out = append(out, b)
	}
	return out
}

func bubbleID(m *message.Message) string { return m.PublicID }

// NOTE: the poll's since is an opaque keyset token; it is base64 and may embed internal ids by design, so clients never parse it.
func firstPollToken() string { return string(keyset.Encode(time.Time{}, uuid.Nil)) }

func newestID(msgs []*message.Message, fallback string) string {
	if len(msgs) == 0 {
		return fallback
	}
	newest := slices.MaxFunc(msgs, func(a, b *message.Message) int { return strings.Compare(a.ID.String(), b.ID.String()) })
	return string(keyset.Encode(newest.WATimestamp, newest.ID))
}

// NOTE: the next poll asks for rows updated strictly after this mark; the overlap covers rows that commit just after the query, so a row is re-read at most once more and a quiet thread answers 204.
func nextWatermark(queriedAt time.Time) string {
	return queriedAt.Add(-pollOverlap).UTC().Format(time.RFC3339Nano)
}
