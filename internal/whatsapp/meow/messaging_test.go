package meow

import (
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"

	"altalune.id/openwa/internal/whatsapp"
)

func messagingSession(t *testing.T, raw *whatsmeow.Client) *session {
	t.Helper()
	fc := newFakeClient()
	fc.raw = raw
	s := newSession(whatsapp.SessionRef{DeviceID: uuid.New()}, fc, nopSinkForTest{}, 4, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = s.Close(t.Context()) })
	return s
}

type nopSinkForTest struct{}

func (nopSinkForTest) OnState(whatsapp.SessionRef, whatsapp.State, string)    {}
func (nopSinkForTest) OnDegraded(whatsapp.SessionRef, error)                  {}
func (nopSinkForTest) OnLinked(whatsapp.SessionRef, whatsapp.Identity)        {}
func (nopSinkForTest) OnMessage(whatsapp.SessionRef, whatsapp.InboundMessage) {}
func (nopSinkForTest) OnReceipt(whatsapp.SessionRef, whatsapp.Receipt)        {}
func (nopSinkForTest) OnContact(whatsapp.SessionRef, whatsapp.ContactUpdate)  {}
func (nopSinkForTest) OnHistory(whatsapp.SessionRef, []whatsapp.InboundMessage) {
}

func TestMessaging_NoClientIsNotConnected(t *testing.T) {
	s := messagingSession(t, nil)
	ctx := t.Context()
	_, err := s.Send(ctx, whatsapp.OutboundMessage{To: "628111@s.whatsapp.net", Kind: whatsapp.KindText})
	require.True(t, whatsapp.IsNotConnectedError(err))
	require.True(t, whatsapp.IsNotConnectedError(s.MarkRead(ctx, "1@g.us", "", []string{"A"}, false)))
	require.True(t, whatsapp.IsNotConnectedError(s.SendTyping(ctx, "628111@s.whatsapp.net", true)))
	_, err = s.FetchMedia(ctx, whatsapp.MediaKeys{DirectPath: "/v"}, whatsapp.KindImage)
	require.True(t, whatsapp.IsNotConnectedError(err))
	_, err = s.IsOnWhatsApp(ctx, []string{"628111"})
	require.True(t, whatsapp.IsNotConnectedError(err))
	_, err = s.GroupList(ctx)
	require.True(t, whatsapp.IsNotConnectedError(err))
	_, err = s.GroupInfo(ctx, "1@g.us")
	require.True(t, whatsapp.IsNotConnectedError(err))
	_, err = s.GroupJoin(ctx, "AbC")
	require.True(t, whatsapp.IsNotConnectedError(err))
	require.True(t, whatsapp.IsNotConnectedError(s.GroupLeave(ctx, "1@g.us")))
}

func TestMessaging_ClosedSessionIsNotConnected(t *testing.T) {
	s := messagingSession(t, whatsmeow.NewClient(&store.Device{}, nil))
	require.NoError(t, s.Close(t.Context()))
	_, err := s.GroupList(t.Context())
	require.True(t, whatsapp.IsNotConnectedError(err))
}

func TestMessaging_BadInputIsRefusedBeforeTheNetwork(t *testing.T) {
	s := messagingSession(t, whatsmeow.NewClient(&store.Device{}, nil))
	ctx := t.Context()
	_, err := s.Send(ctx, whatsapp.OutboundMessage{To: "a:b:c@x", Kind: whatsapp.KindText})
	require.True(t, whatsapp.IsInvalidJIDError(err))
	_, err = s.Send(ctx, whatsapp.OutboundMessage{To: "628111@s.whatsapp.net", Kind: whatsapp.KindText, Media: &whatsapp.OutboundMedia{}})
	require.True(t, whatsapp.IsUnsupportedError(err), "media on a non-media kind")
	_, err = s.Send(ctx, whatsapp.OutboundMessage{To: "628111@s.whatsapp.net", Kind: "poll"})
	require.True(t, whatsapp.IsUnsupportedError(err))
	require.True(t, whatsapp.IsInvalidJIDError(s.MarkRead(ctx, "a:b:c@x", "", nil, false)))
	require.True(t, whatsapp.IsInvalidJIDError(s.MarkRead(ctx, "1@g.us", "a:b:c@x", nil, false)))
	require.True(t, whatsapp.IsInvalidJIDError(s.SendTyping(ctx, "a:b:c@x", true)))
	_, err = s.GroupInfo(ctx, "a:b:c@x")
	require.True(t, whatsapp.IsInvalidJIDError(err))
	_, err = s.GroupJoin(ctx, "https://evil.example/x")
	require.True(t, whatsapp.IsInvalidJIDError(err))
	require.True(t, whatsapp.IsInvalidJIDError(s.GroupLeave(ctx, "a:b:c@x")))
}

func TestMessaging_OfflineClientFailsRetryably(t *testing.T) {
	s := messagingSession(t, whatsmeow.NewClient(&store.Device{}, nil))
	ctx := t.Context()
	to := "628111@s.whatsapp.net"
	_, err := s.Send(ctx, whatsapp.OutboundMessage{WAID: "A", To: to, Kind: whatsapp.KindText, Text: "hi"})
	require.Error(t, err)
	require.True(t, whatsapp.IsRetryable(err), "%v", err)
	_, err = s.Send(ctx, whatsapp.OutboundMessage{WAID: "B", To: to, Kind: whatsapp.KindImage, Media: &whatsapp.OutboundMedia{Bytes: []byte("x"), Mime: "image/png"}})
	require.Error(t, err)
	require.Error(t, s.MarkRead(ctx, "1@g.us", "3@lid", []string{"A"}, true))
	require.Error(t, s.SendTyping(ctx, to, false))
	_, err = s.IsOnWhatsApp(ctx, []string{"+628111"})
	require.Error(t, err)
	_, err = s.GroupList(ctx)
	require.Error(t, err)
	_, err = s.GroupInfo(ctx, "1@g.us")
	require.Error(t, err)
	_, err = s.GroupJoin(ctx, "AbC")
	require.Error(t, err)
	require.Error(t, s.GroupLeave(ctx, "1@g.us"))
}

func TestOwnJID(t *testing.T) {
	require.True(t, ownJID(&whatsmeow.Client{}).IsEmpty())
	id := types.NewJID("628123", types.DefaultUserServer)
	require.Equal(t, id, ownJID(&whatsmeow.Client{Store: &store.Device{ID: &id}}))
}
