package meow

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"altalune.id/openwa/internal/whatsapp"
)

type logRecord struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

type recordingHandler struct {
	mu   sync.Mutex
	recs []logRecord
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	rec := logRecord{level: r.Level, msg: r.Message, attrs: map[string]string{}}
	r.Attrs(func(a slog.Attr) bool {
		rec.attrs[a.Key] = a.Value.String()
		return true
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recs = append(h.recs, rec)
	return nil
}
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func TestNewLogger_ForwardsEveryLevelWithTheModule(t *testing.T) {
	t.Parallel()
	h := &recordingHandler{}
	l := newLogger(slog.New(h), "wa-client")
	l.Debugf("d %d", 1)
	l.Infof("i %s", "x")
	l.Warnf("w")
	l.Errorf("e %v", true)
	sub := l.Sub("Socket")
	sub.Infof("sub")

	want := []logRecord{
		{slog.LevelDebug, "d 1", map[string]string{"wa_module": "wa-client"}},
		{slog.LevelInfo, "i x", map[string]string{"wa_module": "wa-client"}},
		{slog.LevelWarn, "w", map[string]string{"wa_module": "wa-client"}},
		{slog.LevelError, "e true", map[string]string{"wa_module": "wa-client"}},
		{slog.LevelInfo, "sub", map[string]string{"wa_module": "wa-client/Socket"}},
	}
	require.Equal(t, want, h.recs)
}

func TestNewLogger_NilFallsBackToTheDefaultLogger(t *testing.T) {
	t.Parallel()
	l, ok := newLogger(nil, "m").(*slogLogger)
	require.True(t, ok)
	require.NotNil(t, l.log)
	require.Equal(t, "m", l.mod)
}

func TestNotUpgradedError_Message(t *testing.T) {
	t.Parallel()
	err := &NotUpgradedError{}
	require.Contains(t, err.Error(), "schema not present")
	require.Contains(t, err.Error(), "openwa migrate")
	require.True(t, IsNotUpgradedError(err))
	require.False(t, IsNotUpgradedError(errLogout))
}

func TestFillContent(t *testing.T) {
	t.Parallel()
	ctxInfo := &waE2E.ContextInfo{StanzaID: proto.String("Q"), MentionedJID: []string{"1@s.whatsapp.net"}}
	cases := []struct {
		name string
		msg  *waE2E.Message
		want whatsapp.InboundMessage
	}{
		{"conversation", &waE2E.Message{Conversation: proto.String("hi")}, whatsapp.InboundMessage{Type: "text", Body: "hi"}},
		{"extended text", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("reply"), ContextInfo: ctxInfo}},
			whatsapp.InboundMessage{Type: "text", Body: "reply", QuotedID: "Q", Mentions: []string{"1@s.whatsapp.net"}}},
		{"extended text without context", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("plain")}},
			whatsapp.InboundMessage{Type: "text", Body: "plain"}},
		{"video", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Caption: proto.String("v"), Mimetype: proto.String("video/mp4"), FileLength: proto.Uint64(9), ContextInfo: ctxInfo}},
			whatsapp.InboundMessage{Type: "video", Caption: "v", Media: &whatsapp.MediaMeta{Kind: "video", Mime: "video/mp4", Size: 9}, QuotedID: "Q", Mentions: []string{"1@s.whatsapp.net"}}},
		{"audio", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/ogg")}},
			whatsapp.InboundMessage{Type: "audio", Media: &whatsapp.MediaMeta{Kind: "audio", Mime: "audio/ogg"}}},
		{"voice note", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{Mimetype: proto.String("audio/ogg"), PTT: proto.Bool(true), ContextInfo: ctxInfo}},
			whatsapp.InboundMessage{Type: "voice", Media: &whatsapp.MediaMeta{Kind: "voice", Mime: "audio/ogg"}, QuotedID: "Q", Mentions: []string{"1@s.whatsapp.net"}}},
		{"document", &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Caption: proto.String("c"), FileName: proto.String("a.pdf"), Mimetype: proto.String("application/pdf")}},
			whatsapp.InboundMessage{Type: "document", Caption: "c", Media: &whatsapp.MediaMeta{Kind: "document", Mime: "application/pdf", FileName: "a.pdf"}}},
		{"sticker", &waE2E.Message{StickerMessage: &waE2E.StickerMessage{Mimetype: proto.String("image/webp")}},
			whatsapp.InboundMessage{Type: "sticker", Media: &whatsapp.MediaMeta{Kind: "sticker", Mime: "image/webp"}}},
		{"location", &waE2E.Message{LocationMessage: &waE2E.LocationMessage{DegreesLatitude: proto.Float64(-6.2), DegreesLongitude: proto.Float64(106.8)}},
			whatsapp.InboundMessage{Type: "location", Body: "-6.2,106.8"}},
		{"reaction", &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Text: proto.String("+1"), Key: &waCommon.MessageKey{ID: proto.String("TARGET")}}},
			whatsapp.InboundMessage{Type: "reaction", Body: "+1", QuotedID: "TARGET"}},
		{"contact", &waE2E.Message{ContactMessage: &waE2E.ContactMessage{DisplayName: proto.String("Budi")}}, whatsapp.InboundMessage{Type: "contact", Body: "Budi"}},
		{"protocol", &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{}}, whatsapp.InboundMessage{Type: "protocol"}},
		{"unknown", &waE2E.Message{}, whatsapp.InboundMessage{Type: "unknown"}},
		{"nil message", nil, whatsapp.InboundMessage{Type: "unknown"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got whatsapp.InboundMessage
			fillContent(&got, tc.msg)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestContextOf_NilLeavesTheMessageUntouched(t *testing.T) {
	t.Parallel()
	m := whatsapp.InboundMessage{QuotedID: "keep"}
	contextOf(&m, nil)
	require.Equal(t, "keep", m.QuotedID)
	require.Empty(t, m.Mentions)
}
