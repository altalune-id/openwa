package boot

import (
	"context"
	"os"
	"slices"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/contact"
	"altalune.id/openwa/internal/dataplane"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/message"
)

type publicIDLookup interface {
	PublicIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error)
}

func publicIDsOf(ctx context.Context, l publicIDLookup, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]string{}, nil
	}
	uniq := slices.Clone(ids)
	slices.SortFunc(uniq, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	return l.PublicIDs(ctx, slices.Compact(uniq))
}

type messagesForDataplane struct {
	svc     *message.Service
	chats   *chat.Service
	devices *device.Service
}

var _ dataplane.Messages = messagesForDataplane{}

func messageRefOf(m *message.Message, devicePub, chatPub string) dataplane.MessageRef {
	ref := dataplane.MessageRef{
		ID: m.ID, PublicID: m.PublicID, DeviceID: m.DeviceID, DevicePublicID: devicePub, ChatID: m.ChatID, ChatPublicID: chatPub,
		Direction: string(m.Direction), WAID: m.WAMessageID,
		Type: string(m.Type), Status: string(m.Status), Body: m.Body, SenderJID: m.SenderJID, SenderPhone: m.SenderPhone,
		SenderName: m.SenderName, FromMe: m.FromMe, QuotedWAID: m.QuotedWAMessageID, TargetWAID: m.TargetWAMessageID,
		Mentions: m.Mentions, Error: m.Error, Attempts: m.Attempts, Timestamp: m.WATimestamp,
		SentAt: m.SentAt, DeliveredAt: m.DeliveredAt, ReadAt: m.ReadAt,
	}
	if m.Media != nil {
		ref.Media = &dataplane.MediaRef{Mime: m.Media.Mime, Size: m.Media.Size, Filename: m.Media.Filename, Voice: m.Media.Voice}
	}
	if m.Location != nil {
		ref.Location = &dataplane.LocationRef{Lat: m.Location.Lat, Lng: m.Location.Lng, Name: m.Location.Name, Address: m.Location.Address}
	}
	return ref
}

func (s messagesForDataplane) refs(ctx context.Context, items []*message.Message) ([]dataplane.MessageRef, error) {
	deviceIDs := make([]uuid.UUID, 0, len(items))
	chatIDs := make([]uuid.UUID, 0, len(items))
	for _, m := range items {
		deviceIDs, chatIDs = append(deviceIDs, m.DeviceID), append(chatIDs, m.ChatID)
	}
	devicePubs, err := publicIDsOf(ctx, s.devices, deviceIDs)
	if err != nil {
		return nil, err
	}
	chatPubs, err := publicIDsOf(ctx, s.chats, chatIDs)
	if err != nil {
		return nil, err
	}
	out := make([]dataplane.MessageRef, 0, len(items))
	for _, m := range items {
		out = append(out, messageRefOf(m, devicePubs[m.DeviceID], chatPubs[m.ChatID]))
	}
	return out, nil
}

func (s messagesForDataplane) one(ctx context.Context, m *message.Message, err error) (dataplane.MessageRef, error) {
	if err != nil {
		return dataplane.MessageRef{}, err
	}
	refs, err := s.refs(ctx, []*message.Message{m})
	if err != nil {
		return dataplane.MessageRef{}, err
	}
	return refs[0], nil
}

func (s messagesForDataplane) Send(ctx context.Context, deviceID uuid.UUID, in dataplane.SendInput) (dataplane.MessageRef, error) {
	send := message.SendInput{To: in.To, Text: in.Text, ReplyTo: in.ReplyTo, Mentions: in.Mentions, MarkReadFirst: in.MarkReadFirst}
	if in.Media != nil {
		send.Media = &message.MediaInput{Bytes: in.Media.Bytes, URL: in.Media.URL, Mime: in.Media.Mime, Filename: in.Media.Filename, Caption: in.Media.Caption, Voice: in.Media.Voice}
	}
	if in.Location != nil {
		send.Location = &message.Location{Lat: in.Location.Lat, Lng: in.Location.Lng, Name: in.Location.Name, Address: in.Location.Address}
	}
	m, err := s.svc.Send(ctx, deviceID, send)
	return s.one(ctx, m, err)
}

func (s messagesForDataplane) Resolve(ctx context.Context, publicID string) (dataplane.MessageRef, error) {
	m, err := s.svc.Resolve(ctx, publicID)
	return s.one(ctx, m, err)
}

func (s messagesForDataplane) List(ctx context.Context, opts dataplane.MessageListOpts) ([]dataplane.MessageRef, string, error) {
	items, next, err := s.svc.List(ctx, message.ListOpts{DeviceID: opts.DeviceID, ChatID: opts.ChatID, SinceID: opts.SinceID, Cursor: opts.Cursor, Limit: opts.Limit})
	if err != nil {
		return nil, "", err
	}
	out, err := s.refs(ctx, items)
	if err != nil {
		return nil, "", err
	}
	return out, next, nil
}

func (s messagesForDataplane) React(ctx context.Context, id uuid.UUID, emoji string) (dataplane.MessageRef, error) {
	m, err := s.svc.React(ctx, id, emoji)
	return s.one(ctx, m, err)
}

func (s messagesForDataplane) Revoke(ctx context.Context, id uuid.UUID) (dataplane.MessageRef, error) {
	m, err := s.svc.Revoke(ctx, id)
	return s.one(ctx, m, err)
}

func (s messagesForDataplane) Edit(ctx context.Context, id uuid.UUID, text string) (dataplane.MessageRef, error) {
	m, err := s.svc.Edit(ctx, id, text)
	return s.one(ctx, m, err)
}

func (s messagesForDataplane) MarkRead(ctx context.Context, chatID uuid.UUID) error {
	return s.svc.MarkRead(ctx, chatID)
}

func (s messagesForDataplane) OpenMedia(ctx context.Context, id uuid.UUID) (file *os.File, mimeType, filename string, err error) {
	return s.svc.OpenMedia(ctx, id)
}

type chatsForDataplane struct {
	svc     *chat.Service
	devices *device.Service
}

var _ dataplane.Chats = chatsForDataplane{}

func chatRefForDataplane(c *chat.Chat, devicePub string) dataplane.ChatRef {
	return dataplane.ChatRef{
		ID: c.ID, PublicID: c.PublicID, DeviceID: c.DeviceID, DevicePublicID: devicePub, JID: c.JID, Kind: string(c.Kind), Name: c.Name,
		LastMessageAt: c.LastMessageAt, LastMessagePreview: c.LastMessagePreview, UnreadCount: c.UnreadCount, Archived: c.Archived,
	}
}

func (s chatsForDataplane) refs(ctx context.Context, items []*chat.Chat) ([]dataplane.ChatRef, error) {
	ids := make([]uuid.UUID, 0, len(items))
	for _, c := range items {
		ids = append(ids, c.DeviceID)
	}
	pubs, err := publicIDsOf(ctx, s.devices, ids)
	if err != nil {
		return nil, err
	}
	out := make([]dataplane.ChatRef, 0, len(items))
	for _, c := range items {
		out = append(out, chatRefForDataplane(c, pubs[c.DeviceID]))
	}
	return out, nil
}

func (s chatsForDataplane) one(ctx context.Context, c *chat.Chat, err error) (dataplane.ChatRef, error) {
	if err != nil {
		return dataplane.ChatRef{}, err
	}
	refs, err := s.refs(ctx, []*chat.Chat{c})
	if err != nil {
		return dataplane.ChatRef{}, err
	}
	return refs[0], nil
}

func (s chatsForDataplane) List(ctx context.Context, opts dataplane.ChatListOpts) ([]dataplane.ChatRef, string, error) {
	lo := chat.ListOpts{DeviceID: opts.DeviceID, DeviceIDs: opts.DeviceIDs, Search: opts.Search, Cursor: opts.Cursor, Limit: opts.Limit}
	if opts.Kind != "" {
		k := chat.Kind(opts.Kind)
		lo.Kind = &k
	}
	items, next, err := s.svc.List(ctx, lo)
	if err != nil {
		return nil, "", err
	}
	out, err := s.refs(ctx, items)
	if err != nil {
		return nil, "", err
	}
	return out, next, nil
}

func (s chatsForDataplane) Resolve(ctx context.Context, publicID string) (dataplane.ChatRef, error) {
	c, err := s.svc.Resolve(ctx, publicID)
	return s.one(ctx, c, err)
}

func (s chatsForDataplane) GroupInfo(ctx context.Context, chatID uuid.UUID) (dataplane.GroupRef, error) {
	g, err := s.svc.GroupInfo(ctx, chatID)
	if err != nil {
		return dataplane.GroupRef{}, err
	}
	return dataplane.GroupRef{JID: g.JID, Name: g.Name, Topic: g.Topic, Participants: g.Participants, Announce: g.Announce, Locked: g.Locked, InviteLink: g.InviteLink}, nil
}

func (s chatsForDataplane) JoinGroup(ctx context.Context, deviceID uuid.UUID, link string) (dataplane.ChatRef, error) {
	c, err := s.svc.JoinGroup(ctx, deviceID, link)
	return s.one(ctx, c, err)
}

func (s chatsForDataplane) LeaveGroup(ctx context.Context, chatID uuid.UUID) (dataplane.ChatRef, error) {
	c, err := s.svc.LeaveGroup(ctx, chatID)
	return s.one(ctx, c, err)
}

type contactsForDataplane struct {
	svc     *contact.Service
	devices *device.Service
}

var _ dataplane.Contacts = contactsForDataplane{}

func (s contactsForDataplane) List(ctx context.Context, opts dataplane.ContactListOpts) ([]dataplane.ContactRef, string, error) {
	items, next, err := s.svc.List(ctx, contact.ListOpts{DeviceID: opts.DeviceID, DeviceIDs: opts.DeviceIDs, Search: opts.Search, Cursor: opts.Cursor, Limit: opts.Limit})
	if err != nil {
		return nil, "", err
	}
	ids := make([]uuid.UUID, 0, len(items))
	for _, c := range items {
		ids = append(ids, c.DeviceID)
	}
	pubs, err := publicIDsOf(ctx, s.devices, ids)
	if err != nil {
		return nil, "", err
	}
	out := make([]dataplane.ContactRef, 0, len(items))
	for _, c := range items {
		out = append(out, dataplane.ContactRef{ID: c.ID, DeviceID: c.DeviceID, DevicePublicID: pubs[c.DeviceID], JID: c.JID, Phone: c.Phone, Name: c.Name, PushName: c.PushName, BusinessName: c.BusinessName, UpdatedAt: c.UpdatedAt})
	}
	return out, next, nil
}
