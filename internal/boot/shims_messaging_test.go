package boot

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/whatsapp"
)

func seededDevice(t *testing.T, tc tenant.Context, rules device.Rules, state whatsapp.State) (devicesForMessage, uuid.UUID) {
	t.Helper()
	ctx := tenant.Into(t.Context(), tc)
	devs, sessions := fakes.NewDevice(), fakes.NewWhatsAppSessions()
	d, err := device.New(tc.OrgID, tc.ProjectID, fakes.DevicePublicID(), "sales-01")
	require.NoError(t, err)
	require.NoError(t, d.SetRules(rules))
	require.NoError(t, devs.Save(ctx, d, 0))
	s := whatsapp.NewSession(d.ID, tc.OrgID, tc.ProjectID, whatsapp.EngineWhatsmeow)
	s.JID, s.LID, s.Phone, s.State = "628123456789:12@s.whatsapp.net", "99@lid", "628123456789", state
	require.NoError(t, sessions.Save(ctx, s, 0))
	return devicesForMessage{devices: devs, sessions: sessions}, d.ID
}

func TestDevicesForMessage_RefReportsLinkedAndScopesByProject(t *testing.T) {
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()}
	shim, id := seededDevice(t, tc, device.DefaultRules(), whatsapp.StateDisconnected)
	ref, err := shim.Ref(tenant.Into(t.Context(), tc), id)
	require.NoError(t, err)
	require.True(t, ref.Linked, "a paired device that is momentarily disconnected still queues")
	require.Equal(t, "628123456789", ref.Phone)
	require.Equal(t, "sales-01", ref.Name)

	sibling := tenant.Context{OrgID: tc.OrgID, ProjectID: uuid.New()}
	_, err = shim.Ref(tenant.Into(t.Context(), sibling), id)
	require.True(t, device.IsNotFoundError(err), "a device of a sibling project is not found")
}

func TestDevicesForMessage_MatchDetectsMentionsAndRepliesToTheOwnAccount(t *testing.T) {
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()}
	rules := device.DefaultRules()
	shim, id := seededDevice(t, tc, rules, whatsapp.StateConnected)
	ctx := tenant.Into(t.Context(), tc)

	ok, reasons, err := shim.Match(ctx, id, message.MatchInput{IsGroup: true, ChatJID: "1203@g.us", Mentions: []string{"99@lid"}})
	require.NoError(t, err)
	require.True(t, ok)
	require.Contains(t, reasons, "group_mention")

	ok, reasons, err = shim.Match(ctx, id, message.MatchInput{IsGroup: true, ChatJID: "1203@g.us", QuotedSender: "628123456789@s.whatsapp.net"})
	require.NoError(t, err)
	require.True(t, ok)
	require.Contains(t, reasons, "group_reply")

	ok, _, err = shim.Match(ctx, id, message.MatchInput{IsGroup: true, ChatJID: "1203@g.us", Mentions: []string{"628999@s.whatsapp.net"}})
	require.NoError(t, err)
	require.False(t, ok, "mentioning someone else does not match mention mode")
}

func TestInboundInput_MapsEveryField(t *testing.T) {
	ts := time.Now().UTC()
	got := inboundInput(whatsapp.InboundMessage{
		ID: "WA1", ChatJID: "77@lid", ChatAlt: "628111@s.whatsapp.net", SenderJID: "77@lid", SenderAltJID: "628111@s.whatsapp.net",
		SenderPhone: "628111", PushName: "Budi", IsGroup: false, Timestamp: ts, Type: "image", Caption: "c",
		Media:    &whatsapp.MediaMeta{Mime: "image/jpeg", Size: 10, FileName: "a.jpg", DirectPath: "/v", MediaKey: []byte{1}, Voice: false, Width: 4},
		Location: &whatsapp.Location{Lat: 1}, QuotedID: "Q", QuotedSender: "628123@s.whatsapp.net", Mentions: []string{"628123@s.whatsapp.net"}, Raw: []byte("p"),
	})
	require.Equal(t, "WA1", got.WAID)
	require.Equal(t, "628111@s.whatsapp.net", got.ChatAlt)
	require.Equal(t, "628111@s.whatsapp.net", got.SenderAlt)
	require.Equal(t, int64(10), got.Media.Size)
	require.Equal(t, "/v", got.Media.Keys.DirectPath)
	require.Equal(t, uint64(10), got.Media.Keys.Length)
	require.Equal(t, 4, got.Media.Width)
	require.Equal(t, "Q", got.QuotedWAID)
	require.Equal(t, "628123@s.whatsapp.net", got.QuotedSender)
	require.InDelta(t, 1.0, got.Location.Lat, 0)
}

func TestInboundInput_MapsVoiceAndStickerOntoFetchableTypes(t *testing.T) {
	voice := inboundInput(whatsapp.InboundMessage{ID: "V", Type: "voice", Media: &whatsapp.MediaMeta{Mime: "audio/ogg; codecs=opus", DirectPath: "/v"}})
	require.Equal(t, string(message.TypeAudio), voice.Type)
	require.True(t, voice.Media.Voice)
	sticker := inboundInput(whatsapp.InboundMessage{ID: "S", Type: "sticker", Media: &whatsapp.MediaMeta{DirectPath: "/v"}})
	require.Equal(t, string(message.TypeImage), sticker.Type)
	require.Equal(t, "image/webp", sticker.Media.Mime)
}

func shimMessages(t *testing.T) (*message.Service, *fakes.Message, whatsapp.SessionRef, context.Context) {
	t.Helper()
	ref := whatsapp.SessionRef{DeviceID: uuid.New(), OrgID: uuid.New(), ProjectID: uuid.New()}
	store := fakes.NewMessage()
	unexpected := func(_ context.Context, msg string, cause error, _ ...any) *apperror.AppError {
		return apperror.New("GEN900", msg, codes.Internal).WithCause(cause)
	}
	tr := &fakes.Transport{}
	svc := message.NewService(store, slog.New(slog.NewTextHandler(io.Discard, nil)), unexpected, fakes.UnitOfWork, message.Deps{
		Devices: &fakes.MessageDevices{Refs: map[uuid.UUID]message.DeviceRef{ref.DeviceID: {ID: ref.DeviceID, Name: "sales-01", Linked: true}}},
		Chats:   fakes.NewMessageChats(), Contacts: &fakes.MessageContacts{}, Transport: tr, Media: fakes.MediaStore{Transport: tr},
		Fetcher: &fakes.MediaFetcher{}, Waker: &fakes.Waker{}, Webhooks: &fakes.Webhooks{}, Tenants: &fakes.MessageTenants{Org: "acme", Project: "main"},
	}, message.Options{BaseURL: "https://wa.example.com", MaxMediaBytes: 1 << 20, StaleAfter: time.Minute})
	return svc, store, ref, tenant.Into(t.Context(), tenant.Context{OrgID: ref.OrgID, ProjectID: ref.ProjectID})
}

func TestInboundShim_DeliveredReceiptReachesTheMessage(t *testing.T) {
	svc, store, ref, ctx := shimMessages(t)
	m, err := svc.Send(ctx, ref.DeviceID, message.SendInput{To: "628111222333", Text: "halo"})
	require.NoError(t, err)
	claimed, err := svc.ClaimNext(ctx, ref.DeviceID)
	require.NoError(t, err)
	require.NoError(t, svc.MarkSent(ctx, claimed.ID, time.Now(), nil))
	shim := inboundForWhatsApp{recorder: svc.Recorder(), messages: svc}
	require.NoError(t, shim.RecordReceipt(t.Context(), ref, whatsapp.Receipt{MessageIDs: []string{m.WAMessageID}, Type: "delivered", Timestamp: time.Now()}),
		"plan 03's receiptOf rewrites whatsmeow's empty delivered type to \"delivered\"")
	got, err := store.ByID(ctx, m.ID)
	require.NoError(t, err)
	require.Equal(t, message.StatusDelivered, got.Status)
}

func TestInboundShim_VoiceNoteIsStoredAsFetchableAudio(t *testing.T) {
	svc, store, ref, ctx := shimMessages(t)
	shim := inboundForWhatsApp{recorder: svc.Recorder(), messages: svc}
	require.NoError(t, shim.RecordInbound(t.Context(), ref, whatsapp.InboundMessage{
		ID: "V1", ChatJID: "628111@s.whatsapp.net", SenderJID: "628111@s.whatsapp.net", SenderPhone: "628111", Type: "voice", Timestamp: time.Now(),
		Media: &whatsapp.MediaMeta{Mime: "audio/ogg; codecs=opus", Size: 10, DirectPath: "/v", MediaKey: []byte{1}},
	}))
	got, err := store.ByWAID(ctx, ref.DeviceID, "V1")
	require.NoError(t, err)
	require.Equal(t, message.TypeAudio, got.Type)
	require.True(t, got.Media.Voice)
}

type stubWA struct{ err error }

func (s stubWA) MarkRead(context.Context, uuid.UUID, string, string, []string, bool) error {
	return s.err
}

func (s stubWA) FetchMedia(context.Context, uuid.UUID, whatsapp.MediaKeys, string) (*os.File, error) {
	return nil, s.err
}

func (s stubWA) IsOnWhatsApp(context.Context, uuid.UUID, []string) (map[string]string, error) {
	return nil, s.err
}

func TestTransportForMessage_MapsExpiredMedia(t *testing.T) {
	tr := transportForMessage{wa: stubWA{err: &whatsapp.MediaUnavailableError{ID: "d"}}}
	_, err := tr.FetchMedia(t.Context(), uuid.New(), message.MediaKeys{DirectPath: "/v"}, "image")
	require.True(t, message.IsMediaUnavailableError(err), "the data plane answers 410 from the message error")
}

func TestChatsForMessage_RefusesChatsOfASiblingProject(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	unexpected := func(_ context.Context, msg string, cause error, _ ...any) *apperror.AppError {
		return apperror.New("GEN900", msg, codes.Internal).WithCause(cause)
	}
	shim := chatsForMessage{svc: chat.NewService(fakes.NewChat(), log, unexpected, fakes.UnitOfWork, &fakes.Groups{})}
	owner := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()}
	sibling := tenant.Context{OrgID: owner.OrgID, ProjectID: uuid.New()}
	ownerCtx, siblingCtx := tenant.Into(t.Context(), owner), tenant.Into(t.Context(), sibling)

	ref, err := shim.EnsureForJID(ownerCtx, uuid.New(), "628111@s.whatsapp.net", "", "dm", "Budi")
	require.NoError(t, err)
	got, err := shim.Get(ownerCtx, ref.ID)
	require.NoError(t, err)
	require.Equal(t, ref.ID, got.ID)

	_, err = shim.Get(siblingCtx, ref.ID)
	require.True(t, chat.IsNotFoundError(err), "a chat of a sibling project is not found")
	require.True(t, chat.IsNotFoundError(shim.Touch(siblingCtx, ref.ID, time.Now(), "x", true)))
	require.True(t, chat.IsNotFoundError(shim.MarkRead(siblingCtx, ref.ID)))
	require.True(t, chat.IsNotFoundError(shim.Repair(siblingCtx, ref.ID, nil, "", 0)))
}

func TestOutboundForRuntime_MapsTheClaimedRowAndTheFailureClass(t *testing.T) {
	svc, _, ref, ctx := shimMessages(t)
	_, err := svc.Send(ctx, ref.DeviceID, message.SendInput{To: "628111222333", Text: "halo"})
	require.NoError(t, err)
	out := outboundForRuntime{svc: svc}

	row, err := out.ClaimNext(ctx, ref.DeviceID)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Equal(t, "halo", row.Message.Text)
	require.NoError(t, out.MarkFailed(ctx, row.ID, &whatsapp.MediaUnavailableError{ID: "x"}, false))

	empty, err := out.ClaimNext(ctx, ref.DeviceID)
	require.NoError(t, err)
	require.Nil(t, empty)
}

// SECURITY: a device of a sibling project never reaches the engine.
func TestGroupsForChat_RefusesAForeignDevice(t *testing.T) {
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()}
	shim, id := seededDevice(t, tc, device.DefaultRules(), whatsapp.StateConnected)
	groups := groupsForChat{devices: shim.devices}
	sibling := tenant.Into(t.Context(), tenant.Context{OrgID: tc.OrgID, ProjectID: uuid.New()})
	_, err := groups.Join(sibling, id, "https://chat.whatsapp.com/AbC")
	require.True(t, device.IsNotFoundError(err), "got %v", err)
	_, err = groups.List(sibling, id)
	require.True(t, device.IsNotFoundError(err), "got %v", err)
	_, err = groups.Info(sibling, id, "1@g.us")
	require.True(t, device.IsNotFoundError(err), "got %v", err)
	require.True(t, device.IsNotFoundError(groups.Leave(sibling, id, "1@g.us")), "got %v", err)
}
