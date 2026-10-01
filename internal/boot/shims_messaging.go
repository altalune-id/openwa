package boot

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/contact"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/project"
	"altalune.id/openwa/internal/whatsapp"
)

type groupsForChat struct {
	wa      *whatsapp.Service
	devices device.Store
}

// SECURITY: defence in depth; whatsapp.Service.messaging owns the tenant check for every session call (D21), and this rejects a foreign device before the engine call.
func (s groupsForChat) own(ctx context.Context, deviceID uuid.UUID) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	d, err := s.devices.ByID(ctx, deviceID)
	if err != nil {
		return err
	}
	if d.OrgID != tc.OrgID || d.ProjectID != tc.ProjectID {
		return &device.NotFoundError{ID: deviceID.String()}
	}
	return nil
}

var _ chat.Groups = groupsForChat{}

func toChatGroup(g whatsapp.GroupInfo) chat.GroupInfo {
	return chat.GroupInfo{JID: g.JID, Name: g.Name, Topic: g.Topic, Participants: g.Participants, Announce: g.Announce, Locked: g.Locked, InviteLink: g.InviteLink}
}

func (s groupsForChat) List(ctx context.Context, deviceID uuid.UUID) ([]chat.GroupInfo, error) {
	if err := s.own(ctx, deviceID); err != nil {
		return nil, err
	}
	groups, err := s.wa.GroupList(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	out := make([]chat.GroupInfo, 0, len(groups))
	for _, g := range groups {
		out = append(out, toChatGroup(g))
	}
	return out, nil
}

func (s groupsForChat) Info(ctx context.Context, deviceID uuid.UUID, jid string) (chat.GroupInfo, error) {
	if err := s.own(ctx, deviceID); err != nil {
		return chat.GroupInfo{}, err
	}
	g, err := s.wa.GroupInfo(ctx, deviceID, jid)
	return toChatGroup(g), err
}

func (s groupsForChat) Join(ctx context.Context, deviceID uuid.UUID, link string) (string, error) {
	if err := s.own(ctx, deviceID); err != nil {
		return "", err
	}
	return s.wa.GroupJoin(ctx, deviceID, link)
}

func (s groupsForChat) Leave(ctx context.Context, deviceID uuid.UUID, jid string) error {
	if err := s.own(ctx, deviceID); err != nil {
		return err
	}
	return s.wa.GroupLeave(ctx, deviceID, jid)
}

type chatsForMessage struct{ svc *chat.Service }

var _ message.Chats = chatsForMessage{}

func chatRefOf(c *chat.Chat) message.ChatRef {
	return message.ChatRef{ID: c.ID, PublicID: c.PublicID, DeviceID: c.DeviceID, JID: c.JID, LID: c.LID, Kind: string(c.Kind), Name: c.Name}
}

func (s chatsForMessage) EnsureForJID(ctx context.Context, deviceID uuid.UUID, jid, lid, kind, name string) (message.ChatRef, error) {
	c, err := s.svc.EnsureForJID(ctx, deviceID, jid, lid, chat.Kind(kind), name)
	if err != nil {
		return message.ChatRef{}, err
	}
	return chatRefOf(c), nil
}

func (s chatsForMessage) Get(ctx context.Context, id uuid.UUID) (message.ChatRef, error) {
	c, err := s.svc.Get(ctx, id)
	if err != nil {
		return message.ChatRef{}, err
	}
	return chatRefOf(c), nil
}

func (s chatsForMessage) Touch(ctx context.Context, id uuid.UUID, at time.Time, preview string, inbound bool) error {
	return s.svc.Touch(ctx, id, at, preview, inbound)
}

func (s chatsForMessage) MarkRead(ctx context.Context, id uuid.UUID) error {
	return s.svc.MarkRead(ctx, id)
}

func (s chatsForMessage) Repair(ctx context.Context, id uuid.UUID, last *time.Time, preview string, unread int) error {
	return s.svc.Repair(ctx, id, last, preview, unread)
}

type contactsForMessage struct{ svc *contact.Service }

var _ message.Contacts = contactsForMessage{}

func (s contactsForMessage) UpsertFromMessage(ctx context.Context, deviceID uuid.UUID, in message.SenderInput) error {
	return s.svc.UpsertFromMessage(ctx, deviceID, contact.SenderInput{JID: in.JID, LID: in.LID, Phone: in.Phone, PushName: in.PushName})
}

type devicesForMessage struct {
	devices  device.Store
	sessions whatsapp.Store
}

var _ message.Devices = devicesForMessage{}

func (s devicesForMessage) load(ctx context.Context, id uuid.UUID) (*device.Device, *whatsapp.Session, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, nil, err
	}
	d, err := s.devices.ByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if d.OrgID != tc.OrgID || d.ProjectID != tc.ProjectID {
		return nil, nil, &device.NotFoundError{ID: id.String()}
	}
	sess, err := s.sessions.ByDevice(ctx, id)
	if err != nil && !whatsapp.IsSessionNotFoundError(err) {
		return nil, nil, err
	}
	return d, sess, nil
}

func (s devicesForMessage) Ref(ctx context.Context, id uuid.UUID) (message.DeviceRef, error) {
	d, sess, err := s.load(ctx, id)
	if err != nil {
		return message.DeviceRef{}, err
	}
	ref := message.DeviceRef{ID: d.ID, PublicID: d.PublicID, Name: d.Name}
	if sess != nil {
		ref.Phone = sess.Phone
		ref.Linked = sess.JID != "" && (sess.State == whatsapp.StateConnected || sess.State == whatsapp.StateDisconnected)
	}
	return ref, nil
}

func (s devicesForMessage) Match(ctx context.Context, id uuid.UUID, in message.MatchInput) (matched bool, reasons []string, err error) {
	d, sess, err := s.load(ctx, id)
	if err != nil {
		return false, nil, err
	}
	own := map[string]bool{}
	if sess != nil {
		for _, j := range []string{sess.JID, sess.LID, sess.Phone} {
			if u := jidUser(j); u != "" {
				own[u] = true
			}
		}
	}
	mentioned := false
	for _, m := range in.Mentions {
		mentioned = mentioned || own[jidUser(m)]
	}
	matched, reasons = d.Rules.Match(device.MatchInput{
		IsGroup: in.IsGroup, ChatJID: in.ChatJID, SenderPhone: in.SenderPhone, FromMe: in.FromMe, Body: in.Body,
		MentionedMe: mentioned, RepliedToMe: in.QuotedSender != "" && own[jidUser(in.QuotedSender)],
	})
	return matched, reasons, nil
}

func jidUser(j string) string {
	user, _, _ := strings.Cut(j, "@")
	user, _, _ = strings.Cut(user, ":")
	return user
}

type tenantsForMessage struct {
	orgs     org.Store
	projects project.Store
}

var _ message.Tenants = tenantsForMessage{}

func (s tenantsForMessage) Slugs(ctx context.Context, orgID, projectID uuid.UUID) (orgSlug, projectSlug string, err error) {
	o, err := s.orgs.ByID(ctx, orgID)
	if err != nil {
		return "", "", err
	}
	p, err := s.projects.ByID(ctx, projectID)
	if err != nil {
		return "", "", err
	}
	return o.Slug, p.Slug, nil
}

func (s tenantsForMessage) ProjectIDs(ctx context.Context) ([]uuid.UUID, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := s.projects.List(ctx, tc.OrgID)
	if err != nil {
		return nil, err
	}
	out := make([]uuid.UUID, 0, len(projects))
	for _, p := range projects {
		out = append(out, p.ID)
	}
	return out, nil
}

type waTransport interface {
	MarkRead(ctx context.Context, deviceID uuid.UUID, chat, sender string, ids []string, played bool) error
	FetchMedia(ctx context.Context, deviceID uuid.UUID, keys whatsapp.MediaKeys, kind string) (*os.File, error)
	IsOnWhatsApp(ctx context.Context, deviceID uuid.UUID, phones []string) (map[string]string, error)
}

type transportForMessage struct{ wa waTransport }

var _ message.Transport = transportForMessage{}

func (s transportForMessage) MarkRead(ctx context.Context, deviceID uuid.UUID, chatJID, senderJID string, ids []string, played bool) error {
	return s.wa.MarkRead(ctx, deviceID, chatJID, senderJID, ids, played)
}

func (s transportForMessage) FetchMedia(ctx context.Context, deviceID uuid.UUID, keys message.MediaKeys, kind string) (*os.File, error) {
	f, err := s.wa.FetchMedia(ctx, deviceID, whatsapp.MediaKeys{
		URL: keys.URL, DirectPath: keys.DirectPath, MediaKey: keys.MediaKey, FileSHA256: keys.FileSHA256, FileEncSHA256: keys.FileEncSHA256, Length: keys.Length,
	}, kind)
	if whatsapp.IsMediaUnavailableError(err) {
		return nil, &message.MediaUnavailableError{ID: deviceID.String()}
	}
	return f, err
}

func (s transportForMessage) IsOnWhatsApp(ctx context.Context, deviceID uuid.UUID, phones []string) (map[string]string, error) {
	return s.wa.IsOnWhatsApp(ctx, deviceID, phones)
}

type outboundForRuntime struct{ svc *message.Service }

var _ whatsapp.Outbound = outboundForRuntime{}

//nolint:nilnil // an empty queue is not an error.
func (s outboundForRuntime) ClaimNext(ctx context.Context, deviceID uuid.UUID) (*whatsapp.OutboundRow, error) {
	c, err := s.svc.ClaimNext(ctx, deviceID)
	if err != nil || c == nil {
		return nil, err
	}
	out := whatsapp.OutboundMessage{
		WAID: c.WAID, Kind: string(c.Kind), To: c.ChatJID, Text: c.Text,
		QuotedWAID: c.QuotedWAID, QuotedSender: c.QuotedSender, QuotedBody: c.QuotedBody, QuotedRaw: c.QuotedRaw,
		Mentions: c.Mentions, TargetWAID: c.TargetWAID, TargetSender: c.TargetSender, TargetFromMe: c.TargetFromMe,
	}
	if c.Media != nil {
		out.Media = &whatsapp.OutboundMedia{Bytes: c.Media.Bytes, Mime: c.Media.Mime, Filename: c.Media.Filename, Caption: c.Media.Caption, Voice: c.Media.Voice}
	}
	if c.Location != nil {
		out.Location = &whatsapp.Location{Lat: c.Location.Lat, Lng: c.Location.Lng, Name: c.Location.Name, Address: c.Location.Address}
	}
	return &whatsapp.OutboundRow{ID: c.ID, Message: out}, nil
}

func (s outboundForRuntime) MarkSent(ctx context.Context, id uuid.UUID, at time.Time, media *whatsapp.MediaKeys) error {
	var keys *message.MediaKeys
	if media != nil {
		keys = &message.MediaKeys{URL: media.URL, DirectPath: media.DirectPath, MediaKey: media.MediaKey,
			FileSHA256: media.FileSHA256, FileEncSHA256: media.FileEncSHA256, Length: media.Length}
	}
	return s.svc.MarkSent(ctx, id, at, keys)
}

func (s outboundForRuntime) Requeue(ctx context.Context, id uuid.UUID) error {
	return s.svc.Requeue(ctx, id)
}

// NOTE: the webhook carries only the failure class; the sender logs the full error.
func (s outboundForRuntime) MarkFailed(ctx context.Context, id uuid.UUID, cause error, retryable bool) error {
	return s.svc.MarkFailed(ctx, id, whatsapp.FailureClass(cause), retryable)
}

type inboundForWhatsApp struct {
	recorder *message.Recorder
	messages *message.Service
	contacts *contact.Service
}

var _ whatsapp.Inbound = inboundForWhatsApp{}

func refOf(r whatsapp.SessionRef) message.Ref {
	return message.Ref{DeviceID: r.DeviceID, OrgID: r.OrgID, ProjectID: r.ProjectID}
}

func (s inboundForWhatsApp) RecordInbound(ctx context.Context, ref whatsapp.SessionRef, m whatsapp.InboundMessage) error {
	return s.recorder.RecordInbound(ctx, refOf(ref), inboundInput(m))
}

func (s inboundForWhatsApp) RecordReceipt(ctx context.Context, ref whatsapp.SessionRef, r whatsapp.Receipt) error {
	return s.messages.RecordReceipt(ctx, refOf(ref), message.ReceiptInput{
		ChatJID: r.ChatJID, SenderJID: r.SenderJID, WAIDs: r.MessageIDs, Type: r.Type, At: r.Timestamp,
	})
}

func (s inboundForWhatsApp) UpsertContact(ctx context.Context, ref whatsapp.SessionRef, c whatsapp.ContactUpdate) error {
	ctx = tenant.Into(ctx, tenant.Context{OrgID: ref.OrgID, ProjectID: ref.ProjectID})
	return s.contacts.UpsertFromEngine(ctx, contact.ContactInput{
		DeviceID: ref.DeviceID, JID: c.JID, Phone: c.Phone, Name: c.Name, PushName: c.PushName, BusinessName: c.BusinessName,
	})
}

// NOTE: voice becomes audio with Voice set; sticker becomes a webp image (D23).
func inboundType(m whatsapp.InboundMessage) (typ string, voice bool, mimeType string) {
	if m.Media != nil {
		mimeType = m.Media.Mime
	}
	switch m.Type {
	case "voice":
		return string(message.TypeAudio), true, mimeType
	case "sticker":
		if mimeType == "" {
			mimeType = "image/webp"
		}
		return string(message.TypeImage), false, mimeType
	}
	voice = m.Media != nil && m.Media.Voice
	return m.Type, voice, mimeType
}

func inboundInput(m whatsapp.InboundMessage) message.InboundInput {
	typ, voice, mimeType := inboundType(m)
	in := message.InboundInput{
		WAID: m.ID, ChatJID: m.ChatJID, ChatAlt: m.ChatAlt, SenderJID: m.SenderJID, SenderAlt: m.SenderAltJID,
		SenderPhone: m.SenderPhone, PushName: m.PushName, FromMe: m.FromMe, IsGroup: m.IsGroup, Timestamp: m.Timestamp,
		Type: typ, Body: m.Body, Caption: m.Caption, QuotedWAID: m.QuotedID, QuotedSender: m.QuotedSender,
		Mentions: m.Mentions, Raw: m.Raw,
	}
	if m.Media != nil {
		in.Media = &message.InboundMedia{
			Mime: mimeType, Size: int64(m.Media.Size), Filename: m.Media.FileName, //nolint:gosec // G115: WhatsApp media sizes are far below int64 max.
			Keys: message.MediaKeys{URL: m.Media.URL, DirectPath: m.Media.DirectPath, MediaKey: m.Media.MediaKey,
				FileSHA256: m.Media.FileSHA256, FileEncSHA256: m.Media.FileEncSHA256, Length: m.Media.Size},
			Width: m.Media.Width, Height: m.Media.Height, Seconds: m.Media.Seconds, Voice: voice,
		}
	}
	if m.Location != nil {
		in.Location = &message.Location{Lat: m.Location.Lat, Lng: m.Location.Lng, Name: m.Location.Name, Address: m.Location.Address}
	}
	return in
}
