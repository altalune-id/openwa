package message_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/testutil/fakes"
)

const maxMedia = 1 << 20

//nolint:gochecknoglobals // immutable test fixture ids.
var ids = struct{ org, proj, dev, chat uuid.UUID }{uuid.New(), uuid.New(), uuid.New(), uuid.New()}

func outbound(t *testing.T, in message.SendInput) (*message.Message, error) {
	t.Helper()
	return message.NewOutbound(ids.org, ids.proj, ids.dev, ids.chat, fakes.MessagePublicID(), in, maxMedia)
}

func TestWAIDFor_IsDeterministicAndPrefixed(t *testing.T) {
	id := uuid.MustParse("0199c1f0-0000-7000-8000-000000000001")
	require.Equal(t, "3EB00199C1F0000070008000000000000001", message.WAIDFor(id))
	require.Equal(t, message.WAIDFor(id), message.WAIDFor(id))
}

func TestNewOutbound_ValidationMatrix(t *testing.T) {
	jpeg := &message.MediaInput{Bytes: []byte{0xff, 0xd8}, Mime: "image/jpeg", Caption: "look"}
	cases := []struct {
		name    string
		in      message.SendInput
		typ     message.Type
		errPred func(error) bool
	}{
		{"text", message.SendInput{Text: "halo"}, message.TypeText, nil},
		{"nothing", message.SendInput{}, "", message.IsInvalidInputError},
		{"text and media", message.SendInput{Text: "x", Media: jpeg}, "", message.IsInvalidInputError},
		{"media and location", message.SendInput{Media: jpeg, Location: &message.Location{}}, "", message.IsInvalidInputError},
		{"text too long", message.SendInput{Text: strings.Repeat("a", message.MaxTextRunes+1)}, "", message.IsInvalidInputError},
		{"image", message.SendInput{Media: jpeg}, message.TypeImage, nil},
		{"video", message.SendInput{Media: &message.MediaInput{Bytes: []byte{1}, Mime: "video/mp4"}}, message.TypeVideo, nil},
		{"voice", message.SendInput{Media: &message.MediaInput{Bytes: []byte{1}, Mime: "audio/ogg; codecs=opus", Voice: true}}, message.TypeAudio, nil},
		{"voice needs ogg", message.SendInput{Media: &message.MediaInput{Bytes: []byte{1}, Mime: "audio/mpeg", Voice: true}}, "", message.IsInvalidInputError},
		{"document", message.SendInput{Media: &message.MediaInput{Bytes: []byte{1}, Mime: "application/pdf", Filename: "a.pdf"}}, message.TypeDocument, nil},
		{"document needs filename", message.SendInput{Media: &message.MediaInput{Bytes: []byte{1}, Mime: "application/pdf"}}, "", message.IsInvalidInputError},
		{"no mime", message.SendInput{Media: &message.MediaInput{Bytes: []byte{1}}}, "", message.IsUnsupportedMimeError},
		{"empty media", message.SendInput{Media: &message.MediaInput{Mime: "image/png"}}, "", message.IsInvalidInputError},
		{"too large", message.SendInput{Media: &message.MediaInput{Bytes: make([]byte, maxMedia+1), Mime: "image/png"}}, "", message.IsMediaTooLargeError},
		{"location", message.SendInput{Location: &message.Location{Lat: -6.2, Lng: 106.8, Name: "Monas"}}, message.TypeLocation, nil},
		{"location out of range", message.SendInput{Location: &message.Location{Lat: 91}}, "", message.IsInvalidInputError},
		{"mentions digits", message.SendInput{Text: "@628111 hi", Mentions: []string{"628111"}}, message.TypeText, nil},
		{"mentions not digits", message.SendInput{Text: "hi", Mentions: []string{"+62 811"}}, "", message.IsInvalidInputError},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			m, err := outbound(t, tt.in)
			if tt.errPred != nil {
				require.True(t, tt.errPred(err), "got %v", err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.typ, m.Type)
			require.Equal(t, message.StatusQueued, m.Status)
			require.Equal(t, message.DirectionOut, m.Direction)
			require.True(t, m.FromMe)
			require.Equal(t, message.WAIDFor(m.ID), m.WAMessageID)
			require.NotNil(t, m.Mentions)
		})
	}
}

func TestNewOutbound_MediaKeepsPendingBytesAndCaption(t *testing.T) {
	m, err := outbound(t, message.SendInput{Media: &message.MediaInput{Bytes: []byte("img"), Mime: "image/png", Caption: "cap"}})
	require.NoError(t, err)
	require.Equal(t, []byte("img"), m.Raw, "the queued row carries the bytes until the upload")
	require.Equal(t, "cap", m.Body)
	require.Equal(t, int64(3), m.Media.Size)
	require.Equal(t, message.StorageWA, m.Media.Storage)
	m.MarkSending(time.Now())
	keys := message.MediaKeys{URL: "https://mmg/x", DirectPath: "/v/t62", MediaKey: []byte{1}, Length: 3}
	require.True(t, m.MarkSent(time.Now(), &keys, time.Now()))
	require.Nil(t, m.Raw, "sent media drops its pending bytes")
	require.Equal(t, keys, m.Media.Keys, "the upload keys make the sent file fetchable again")
}

func TestMarkSent_NeverMovesStatusBackwards(t *testing.T) {
	m, err := outbound(t, message.SendInput{Text: "x"})
	require.NoError(t, err)
	m.MarkSending(time.Now())
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	require.True(t, m.Receipt(message.ReceiptDelivered, at.Add(time.Second), time.Now()), "a receipt can beat the sender's acknowledgement")
	require.False(t, m.MarkSent(at, nil, time.Now()), "no status change, so no sent event")
	require.Equal(t, message.StatusDelivered, m.Status)
	require.Equal(t, at, *m.SentAt, "the acknowledgement time is still recorded")
}

func TestExpire_FailsQueuedAndSendingRows(t *testing.T) {
	m, err := outbound(t, message.SendInput{Media: &message.MediaInput{Bytes: []byte("img"), Mime: "image/png"}})
	require.NoError(t, err)
	require.True(t, m.Expire("expired", time.Now()))
	require.Equal(t, message.StatusFailed, m.Status)
	require.Nil(t, m.Raw)
	require.False(t, m.Expire("expired", time.Now()), "a failed row does not expire twice")

	stuck, err := outbound(t, message.SendInput{Text: "x"})
	require.NoError(t, err)
	stuck.MarkSending(time.Now())
	require.True(t, stuck.Expire("expired", time.Now()), "a row stuck in sending expires too")
}

func TestRelease_ReturnsTheAttempt(t *testing.T) {
	m, err := outbound(t, message.SendInput{Text: "x"})
	require.NoError(t, err)
	m.MarkSending(time.Now())
	require.True(t, m.Release(time.Now()))
	require.Equal(t, message.StatusQueued, m.Status)
	require.Zero(t, m.Attempts, "a send that never reached the engine does not count")
	require.False(t, m.Release(time.Now()), "only a sending row can be released")
}

func TestMarkFailed_NeverRegressesAnAdvancedRow(t *testing.T) {
	m, err := outbound(t, message.SendInput{Text: "x"})
	require.NoError(t, err)
	m.MarkSending(time.Now())
	m.MarkSent(time.Now(), nil, time.Now())
	require.True(t, m.Receipt(message.ReceiptDelivered, time.Now(), time.Now()))
	require.False(t, m.MarkFailed("late", false, time.Now()))
	require.Equal(t, message.StatusDelivered, m.Status)
}

func TestChildRows(t *testing.T) {
	r, err := message.NewReaction(ids.org, ids.proj, ids.dev, ids.chat, fakes.MessagePublicID(), "3EB0X", "👍")
	require.NoError(t, err)
	require.Equal(t, message.TypeReaction, r.Type)
	require.Equal(t, "3EB0X", r.TargetWAMessageID)
	require.Equal(t, "👍", r.Body)
	_, err = message.NewReaction(ids.org, ids.proj, ids.dev, ids.chat, fakes.MessagePublicID(), "", "👍")
	require.True(t, message.IsInvalidInputError(err))

	v, err := message.NewRevoke(ids.org, ids.proj, ids.dev, ids.chat, fakes.MessagePublicID(), "3EB0X")
	require.NoError(t, err)
	require.Equal(t, message.TypeRevoke, v.Type)

	e, err := message.NewEdit(ids.org, ids.proj, ids.dev, ids.chat, fakes.MessagePublicID(), "3EB0X", "fixed")
	require.NoError(t, err)
	require.Equal(t, message.TypeEdit, e.Type)
	require.Equal(t, "fixed", e.Body)
	_, err = message.NewEdit(ids.org, ids.proj, ids.dev, ids.chat, fakes.MessagePublicID(), "3EB0X", "")
	require.True(t, message.IsInvalidInputError(err))
}

func TestMarkFailed_RetriesUntilTheCap(t *testing.T) {
	m, err := outbound(t, message.SendInput{Text: "x"})
	require.NoError(t, err)
	for attempt := 1; attempt < message.MaxAttempts; attempt++ {
		m.MarkSending(time.Now())
		require.False(t, m.MarkFailed("timeout", true, time.Now()), "attempt %d requeues", attempt)
		require.Equal(t, message.StatusQueued, m.Status)
	}
	m.MarkSending(time.Now())
	require.True(t, m.MarkFailed("timeout", true, time.Now()))
	require.Equal(t, message.StatusFailed, m.Status)

	n, err := outbound(t, message.SendInput{Text: "x"})
	require.NoError(t, err)
	n.MarkSending(time.Now())
	require.True(t, n.MarkFailed("reachout_timelock", false, time.Now()), "a non-retryable failure is terminal at once")
	require.Equal(t, "reachout_timelock", n.Error)
}

func TestReceipt_IsMonotonic(t *testing.T) {
	m, err := outbound(t, message.SendInput{Text: "x"})
	require.NoError(t, err)
	m.MarkSending(time.Now())
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	m.MarkSent(at, nil, time.Now())
	require.True(t, m.Receipt(message.ReceiptRead, at.Add(2*time.Second), time.Now()))
	require.Equal(t, message.StatusRead, m.Status)
	require.NotNil(t, m.DeliveredAt, "read implies delivered")
	require.False(t, m.Receipt(message.ReceiptDelivered, at.Add(time.Second), time.Now()), "an earlier receipt kind is ignored")
	require.Equal(t, message.StatusRead, m.Status)
	require.True(t, m.Receipt(message.ReceiptPlayed, at.Add(3*time.Second), time.Now()))
	require.False(t, m.Receipt(message.ReceiptPlayed, at.Add(4*time.Second), time.Now()), "a repeat changes nothing")

	in := message.NewInbound(message.Ref{DeviceID: ids.dev, OrgID: ids.org, ProjectID: ids.proj}, ids.chat, fakes.MessagePublicID(), message.InboundInput{WAID: "A", Type: "text", Body: "hi"})
	require.False(t, in.Receipt(message.ReceiptRead, at, time.Now()), "inbound rows take no delivery receipts")
}

func TestInlineSafe(t *testing.T) {
	for _, m := range []string{"image/jpeg", "image/png; q=1", "IMAGE/WEBP", "image/gif"} {
		require.True(t, message.InlineSafe(m), m)
	}
	for _, m := range []string{"image/svg+xml", "text/html", "application/pdf", "image/x-icon", ""} {
		require.False(t, message.InlineSafe(m), m)
	}
}

func TestReceiptKindOf(t *testing.T) {
	for engine, want := range map[string]message.ReceiptKind{"": message.ReceiptDelivered, "delivered": message.ReceiptDelivered, "read": message.ReceiptRead, "played": message.ReceiptPlayed} {
		got, ok := message.ReceiptKindOf(engine)
		require.True(t, ok)
		require.Equal(t, want, got)
	}
	for _, ignored := range []string{"sender", "retry", "inactive", "peer_msg", "hist_sync", "server-error", "read-self", "played-self"} {
		_, ok := message.ReceiptKindOf(ignored)
		require.False(t, ok, ignored)
	}
}

func TestCheckEditable(t *testing.T) {
	m, err := outbound(t, message.SendInput{Text: "x"})
	require.NoError(t, err)
	m.MarkSending(time.Now())
	m.MarkSent(m.WATimestamp, nil, time.Now())
	require.NoError(t, m.CheckEditable(m.WATimestamp.Add(19*time.Minute)))
	require.True(t, message.IsEditWindowClosedError(m.CheckEditable(m.WATimestamp.Add(21*time.Minute))))

	in := message.NewInbound(message.Ref{DeviceID: ids.dev, OrgID: ids.org, ProjectID: ids.proj}, ids.chat, fakes.MessagePublicID(), message.InboundInput{WAID: "A", Type: "text", Body: "hi", Timestamp: time.Now()})
	require.True(t, message.IsNotOwnMessageError(in.CheckEditable(time.Now())))
	require.True(t, message.IsNotOwnMessageError(in.CheckRevocable()))

	img, err := outbound(t, message.SendInput{Media: &message.MediaInput{Bytes: []byte{1}, Mime: "image/png"}})
	require.NoError(t, err)
	img.MarkSending(time.Now())
	img.MarkSent(img.WATimestamp, nil, time.Now())
	require.True(t, message.IsInvalidInputError(img.CheckEditable(img.WATimestamp)), "only text is editable")
}

func TestNewInbound_MapsInputAndFromMe(t *testing.T) {
	ref := message.Ref{DeviceID: ids.dev, OrgID: ids.org, ProjectID: ids.proj}
	ts := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	m := message.NewInbound(ref, ids.chat, fakes.MessagePublicID(), message.InboundInput{
		WAID: "WA1", SenderJID: "77@lid", SenderAlt: "628111@s.whatsapp.net", SenderPhone: "628111", PushName: "budi",
		Timestamp: ts, Type: "image", Caption: "cap", Raw: []byte("proto"),
		Media:    &message.InboundMedia{Mime: "image/jpeg", Size: 10, Keys: message.MediaKeys{DirectPath: "/v/t"}},
		Mentions: []string{"628222@s.whatsapp.net", "99@lid"},
	})
	require.Equal(t, message.DirectionIn, m.Direction)
	require.Equal(t, message.StatusReceived, m.Status)
	require.Equal(t, "77@lid", m.SenderLID)
	require.Equal(t, "cap", m.Body)
	require.Equal(t, []string{"628222"}, m.Mentions, "mentions are stored as phones")
	require.Equal(t, message.StorageWA, m.Media.Storage)
	require.Equal(t, []byte("proto"), m.Raw)
	require.Equal(t, ts, m.WATimestamp)

	text := message.NewInbound(ref, ids.chat, fakes.MessagePublicID(), message.InboundInput{WAID: "WA2", Type: "text", Body: "hi", Raw: []byte("proto")})
	require.Nil(t, text.Raw, "text keeps no raw proto")

	mine := message.NewInbound(ref, ids.chat, fakes.MessagePublicID(), message.InboundInput{WAID: "WA3", Type: "text", Body: "sent from phone", FromMe: true, Timestamp: ts})
	require.Equal(t, message.DirectionOut, mine.Direction)
	require.Equal(t, message.StatusSent, mine.Status)
	require.Equal(t, ts, *mine.SentAt)

	odd := message.NewInbound(ref, ids.chat, fakes.MessagePublicID(), message.InboundInput{WAID: "WA4", Type: "poll"})
	require.Equal(t, message.TypeUnknown, odd.Type)
}

func TestPreviewFor(t *testing.T) {
	cases := map[message.Type]string{message.TypeImage: "[image] cap", message.TypeLocation: "[location]", message.TypeRevoke: "[deleted]"}
	for typ, want := range cases {
		body := ""
		if typ == message.TypeImage {
			body = "cap"
		}
		require.Equal(t, want, message.PreviewFor(&message.Message{Type: typ, Body: body}))
	}
	require.Equal(t, "hi", message.PreviewFor(&message.Message{Type: message.TypeText, Body: "hi"}))
}

func TestListOpts_WithDefaults(t *testing.T) {
	require.Equal(t, message.DefaultListLimit, message.ListOpts{}.WithDefaults().Limit)
	require.Equal(t, message.MaxListLimit, message.ListOpts{Limit: 1000}.WithDefaults().Limit)
}

func TestMarkReadInbound(t *testing.T) {
	in := message.NewInbound(message.Ref{DeviceID: ids.dev, OrgID: ids.org, ProjectID: ids.proj}, ids.chat, fakes.MessagePublicID(), message.InboundInput{WAID: "A", Type: "text"})
	at := time.Now()
	require.True(t, in.MarkReadInbound(at))
	require.False(t, in.MarkReadInbound(at), "a second read changes nothing")
	out, err := outbound(t, message.SendInput{Text: "x"})
	require.NoError(t, err)
	require.False(t, out.MarkReadInbound(at), "outbound rows are never inbound-read")
}

func TestPreviewFor_AllTypes(t *testing.T) {
	cases := map[message.Type]string{
		message.TypeContact: "[contact]", message.TypeReaction: "[reaction]", message.TypeUnknown: "[message]",
		message.TypeEdit: "", message.TypeDocument: "[document]",
	}
	for typ, want := range cases {
		require.Equal(t, want, message.PreviewFor(&message.Message{Type: typ}), typ)
	}
}

func TestCheckEditable_UnsentAndFailedRowsAreRefused(t *testing.T) {
	m, err := outbound(t, message.SendInput{Text: "x"})
	require.NoError(t, err)
	require.True(t, message.IsInvalidInputError(m.CheckEditable(m.WATimestamp)), "queued")
	m.MarkSending(time.Now())
	m.MarkFailed("boom", false, time.Now())
	require.True(t, message.IsInvalidInputError(m.CheckEditable(m.WATimestamp)), "failed")
}

func TestMarkFailed_TerminalMediaDropsRawAndMarksIt(t *testing.T) {
	m, err := outbound(t, message.SendInput{Media: &message.MediaInput{Bytes: []byte("b"), Mime: "image/png"}})
	require.NoError(t, err)
	require.False(t, m.RawDropped())
	m.MarkSending(time.Now())
	require.True(t, m.MarkFailed("fatal", false, time.Now()))
	require.Nil(t, m.Raw)
	require.True(t, m.RawDropped())
}

func TestMarkFailed_RetryKeepsQueuedBytes(t *testing.T) {
	m, err := outbound(t, message.SendInput{Media: &message.MediaInput{Bytes: []byte("b"), Mime: "image/png"}})
	require.NoError(t, err)
	m.MarkSending(time.Now())
	require.False(t, m.MarkFailed("net", true, time.Now()))
	require.Equal(t, []byte("b"), m.Raw, "a requeue keeps the plaintext for the next attempt")
	require.False(t, m.RawDropped())
}

func TestNewOutbound_ReplyAndLongCaption(t *testing.T) {
	m, err := outbound(t, message.SendInput{Text: "x", ReplyTo: " 3EB0AA "})
	require.NoError(t, err)
	require.Equal(t, "3EB0AA", m.QuotedWAMessageID)
	_, err = outbound(t, message.SendInput{Media: &message.MediaInput{Bytes: []byte{1}, Mime: "image/png", Caption: strings.Repeat("a", message.MaxTextRunes+1)}})
	require.True(t, message.IsInvalidInputError(err))
	_, err = message.NewReaction(ids.org, ids.proj, ids.dev, ids.chat, fakes.MessagePublicID(), "T", strings.Repeat("x", 17))
	require.True(t, message.IsInvalidInputError(err))
	_, err = message.NewRevoke(ids.org, ids.proj, ids.dev, ids.chat, fakes.MessagePublicID(), " ")
	require.True(t, message.IsInvalidInputError(err))
	_, err = message.NewEdit(ids.org, ids.proj, ids.dev, ids.chat, fakes.MessagePublicID(), " ", "x")
	require.True(t, message.IsInvalidInputError(err))
}
