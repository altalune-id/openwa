package dataplane

import (
	"context"
	"os"
	"time"

	"github.com/google/uuid"
)

// MediaRef describes a message attachment.
type MediaRef struct {
	Mime     string
	Size     int64
	Filename string
	Voice    bool
}

// LocationRef is a shared map pin.
type LocationRef struct {
	Lat     float64
	Lng     float64
	Name    string
	Address string
}

// MessageRef is the message this surface needs; the shim fills the public ids, which are the only ids a view renders.
type MessageRef struct {
	ID             uuid.UUID
	PublicID       string
	DeviceID       uuid.UUID
	DevicePublicID string
	ChatID         uuid.UUID
	ChatPublicID   string
	Direction      string
	WAID           string
	Type           string
	Status         string
	Body           string
	SenderJID      string
	SenderPhone    string
	SenderName     string
	FromMe         bool
	Media          *MediaRef
	Location       *LocationRef
	QuotedWAID     string
	TargetWAID     string
	Mentions       []string
	Error          string
	Attempts       int
	Timestamp      time.Time
	SentAt         *time.Time
	DeliveredAt    *time.Time
	ReadAt         *time.Time
}

// SendMedia is an attachment given as bytes or as a URL the service fetches.
type SendMedia struct {
	Bytes    []byte
	URL      string
	Mime     string
	Filename string
	Caption  string
	Voice    bool
}

// SendInput is one send request.
type SendInput struct {
	To            string
	Text          string
	Media         *SendMedia
	Location      *LocationRef
	ReplyTo       string
	Mentions      []string
	MarkReadFirst bool
}

// MessageListOpts filters a message list; SinceID returns newer rows oldest first.
type MessageListOpts struct {
	DeviceID *uuid.UUID
	ChatID   *uuid.UUID
	SinceID  *uuid.UUID
	Cursor   string
	Limit    int
}

// ChatRef is the chat this surface needs.
type ChatRef struct {
	ID                 uuid.UUID
	PublicID           string
	DeviceID           uuid.UUID
	DevicePublicID     string
	JID                string
	Kind               string
	Name               string
	LastMessageAt      *time.Time
	LastMessagePreview string
	UnreadCount        int
	Archived           bool
}

// ChatListOpts filters a chat list; DeviceIDs confines a device-bound key.
type ChatListOpts struct {
	DeviceID  *uuid.UUID
	DeviceIDs []uuid.UUID
	Kind      string
	Search    string
	Cursor    string
	Limit     int
}

// ContactRef is the contact this surface needs.
type ContactRef struct {
	ID             uuid.UUID
	DeviceID       uuid.UUID
	DevicePublicID string
	JID            string
	Phone          string
	Name           string
	PushName       string
	BusinessName   string
	UpdatedAt      time.Time
}

// ContactListOpts filters a contact list.
type ContactListOpts struct {
	DeviceID  *uuid.UUID
	DeviceIDs []uuid.UUID
	Search    string
	Cursor    string
	Limit     int
}

// Messages is the driven port the data plane sends and reads messages through.
type Messages interface {
	Send(ctx context.Context, deviceID uuid.UUID, in SendInput) (MessageRef, error)
	// Resolve returns the caller's project message whose public id is publicID.
	Resolve(ctx context.Context, publicID string) (MessageRef, error)
	List(ctx context.Context, opts MessageListOpts) ([]MessageRef, string, error)
	React(ctx context.Context, id uuid.UUID, emoji string) (MessageRef, error)
	Revoke(ctx context.Context, id uuid.UUID) (MessageRef, error)
	Edit(ctx context.Context, id uuid.UUID, text string) (MessageRef, error)
	MarkRead(ctx context.Context, chatID uuid.UUID) error
	OpenMedia(ctx context.Context, id uuid.UUID) (*os.File, string, string, error)
}

// Chats is the driven port the data plane reads chats through.
type Chats interface {
	List(ctx context.Context, opts ChatListOpts) ([]ChatRef, string, error)
	// Resolve returns the caller's project chat whose public id is publicID.
	Resolve(ctx context.Context, publicID string) (ChatRef, error)
	GroupInfo(ctx context.Context, chatID uuid.UUID) (GroupRef, error)
	JoinGroup(ctx context.Context, deviceID uuid.UUID, inviteLink string) (ChatRef, error)
	LeaveGroup(ctx context.Context, chatID uuid.UUID) (ChatRef, error)
}

// GroupRef is what the engine reports about one group.
type GroupRef struct {
	JID          string
	Name         string
	Topic        string
	Participants int
	Announce     bool
	Locked       bool
	InviteLink   string
}

type groupView struct {
	JID          string `json:"jid"`
	Name         string `json:"name"`
	Topic        string `json:"topic"`
	Participants int    `json:"participants"`
	Announce     bool   `json:"announce"`
	Locked       bool   `json:"locked"`
	InviteLink   string `json:"invite_link"`
}

// Contacts is the driven port the data plane reads contacts through.
type Contacts interface {
	List(ctx context.Context, opts ContactListOpts) ([]ContactRef, string, error)
}

type listEnvelope[T any] struct {
	Data       []T    `json:"data"`
	NextCursor string `json:"next_cursor"`
}

type mediaView struct {
	Mime     string `json:"mime"`
	Size     int64  `json:"size"`
	Filename string `json:"filename"`
	Voice    bool   `json:"voice"`
	URL      string `json:"url"`
}

type locationView struct {
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
	Name    string  `json:"name"`
	Address string  `json:"address"`
}

type messageView struct {
	ID          string        `json:"id"`
	URL         string        `json:"url"`
	DeviceID    string        `json:"device_id"`
	ChatID      string        `json:"chat_id"`
	Direction   string        `json:"direction"`
	WAID        string        `json:"wa_id"`
	Type        string        `json:"type"`
	Status      string        `json:"status"`
	Body        string        `json:"body"`
	SenderJID   string        `json:"sender_jid"`
	SenderPhone string        `json:"sender_phone"`
	SenderName  string        `json:"sender_name"`
	FromMe      bool          `json:"from_me"`
	Media       *mediaView    `json:"media"`
	Location    *locationView `json:"location"`
	QuotedWAID  string        `json:"quoted_wa_id,omitempty"`
	TargetWAID  string        `json:"target_wa_id,omitempty"`
	Mentions    []string      `json:"mentions"`
	Error       string        `json:"error,omitempty"`
	Attempts    int           `json:"attempts"`
	Timestamp   time.Time     `json:"timestamp"`
	SentAt      *time.Time    `json:"sent_at"`
	DeliveredAt *time.Time    `json:"delivered_at"`
	ReadAt      *time.Time    `json:"read_at"`
}

type chatView struct {
	ID                 string     `json:"id"`
	URL                string     `json:"url"`
	DeviceID           string     `json:"device_id"`
	JID                string     `json:"jid"`
	Kind               string     `json:"kind"`
	Name               string     `json:"name"`
	LastMessageAt      *time.Time `json:"last_message_at"`
	LastMessagePreview string     `json:"last_message_preview"`
	UnreadCount        int        `json:"unread_count"`
	Archived           bool       `json:"archived"`
}

type contactView struct {
	DeviceID     string    `json:"device_id"`
	JID          string    `json:"jid"`
	Phone        string    `json:"phone"`
	Name         string    `json:"name"`
	PushName     string    `json:"push_name"`
	BusinessName string    `json:"business_name"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (h *Handler) projectURL(req request, suffix string) string {
	return h.basePath + "/orgs/" + req.orgSlug + "/projects/" + req.projectSlug + suffix
}

func (h *Handler) messageViewOf(req request, m MessageRef) messageView {
	v := messageView{
		ID: m.PublicID, URL: h.projectURL(req, "/messages/"+m.PublicID), DeviceID: m.DevicePublicID, ChatID: m.ChatPublicID,
		Direction: m.Direction, WAID: m.WAID, Type: m.Type, Status: m.Status, Body: m.Body,
		SenderJID: m.SenderJID, SenderPhone: m.SenderPhone, SenderName: m.SenderName, FromMe: m.FromMe,
		QuotedWAID: m.QuotedWAID, TargetWAID: m.TargetWAID, Mentions: append([]string{}, m.Mentions...), Error: m.Error,
		Attempts: m.Attempts, Timestamp: m.Timestamp, SentAt: m.SentAt, DeliveredAt: m.DeliveredAt, ReadAt: m.ReadAt,
	}
	if m.Media != nil {
		v.Media = &mediaView{Mime: m.Media.Mime, Size: m.Media.Size, Filename: m.Media.Filename, Voice: m.Media.Voice, URL: v.URL + "/media"}
	}
	if m.Location != nil {
		v.Location = &locationView{Lat: m.Location.Lat, Lng: m.Location.Lng, Name: m.Location.Name, Address: m.Location.Address}
	}
	return v
}

func (h *Handler) chatViewOf(req request, c ChatRef) chatView {
	return chatView{
		ID: c.PublicID, URL: h.projectURL(req, "/chats/"+c.PublicID), DeviceID: c.DevicePublicID, JID: c.JID, Kind: c.Kind,
		Name: c.Name, LastMessageAt: c.LastMessageAt, LastMessagePreview: c.LastMessagePreview, UnreadCount: c.UnreadCount, Archived: c.Archived,
	}
}

func contactViewOf(c ContactRef) contactView {
	return contactView{
		DeviceID: c.DevicePublicID, JID: c.JID, Phone: c.Phone, Name: c.Name,
		PushName: c.PushName, BusinessName: c.BusinessName, UpdatedAt: c.UpdatedAt,
	}
}
