// Package message is the message bounded context: inbound records, the outbound queue, media references and retention.
package message

import (
	"encoding/hex"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Direction says whether the device received or sent the message.
type Direction string

// Direction values.
const (
	DirectionIn  Direction = "in"
	DirectionOut Direction = "out"
)

// Type is the content kind.
type Type string

// Type values.
const (
	TypeText     Type = "text"
	TypeImage    Type = "image"
	TypeVideo    Type = "video"
	TypeAudio    Type = "audio"
	TypeDocument Type = "document"
	TypeLocation Type = "location"
	TypeContact  Type = "contact"
	TypeReaction Type = "reaction"
	TypeRevoke   Type = "revoke"
	TypeEdit     Type = "edit"
	TypeUnknown  Type = "unknown"
)

// Status is the delivery state; inbound rows are always received.
type Status string

// Status values.
const (
	StatusQueued    Status = "queued"
	StatusSending   Status = "sending"
	StatusSent      Status = "sent"
	StatusDelivered Status = "delivered"
	StatusRead      Status = "read"
	StatusPlayed    Status = "played"
	StatusFailed    Status = "failed"
	StatusReceived  Status = "received"
)

// ReceiptKind is a delivery receipt this module acts on.
type ReceiptKind string

// ReceiptKind values.
const (
	ReceiptDelivered ReceiptKind = "delivered"
	ReceiptRead      ReceiptKind = "read"
	ReceiptPlayed    ReceiptKind = "played"
)

// Limits and defaults.
const (
	MaxTextRunes     = 65536
	MaxAttempts      = 3
	EditWindow       = 20 * time.Minute
	DefaultListLimit = 50
	MaxListLimit     = 200
	StorageWA        = "wa"
	maxReactionRunes = 16
)

// MediaKeys is the decryption material WhatsApp needs to hand the file back.
type MediaKeys struct {
	URL           string
	DirectPath    string
	MediaKey      []byte
	FileSHA256    []byte
	FileEncSHA256 []byte
	Length        uint64
}

// Media describes an attachment; its download URL is derived at read time, never stored.
type Media struct {
	Mime     string
	Size     int64
	Filename string
	Storage  string
	Ref      string
	Keys     MediaKeys
	Width    int
	Height   int
	Seconds  int
	Voice    bool
}

// Location is a shared map pin.
type Location struct {
	Lat     float64
	Lng     float64
	Name    string
	Address string
}

// Ref names the device and tenant an engine event belongs to.
type Ref struct {
	DeviceID  uuid.UUID
	OrgID     uuid.UUID
	ProjectID uuid.UUID
}

// Message is the aggregate root.
type Message struct {
	ID                uuid.UUID
	PublicID          string
	OrgID             uuid.UUID
	ProjectID         uuid.UUID
	DeviceID          uuid.UUID
	ChatID            uuid.UUID
	Direction         Direction
	WAMessageID       string
	SenderJID         string
	SenderLID         string
	SenderPhone       string
	SenderName        string
	FromMe            bool
	Type              Type
	Body              string
	Media             *Media
	Location          *Location
	QuotedWAMessageID string
	Mentions          []string
	TargetWAMessageID string
	Status            Status
	Error             string
	Attempts          int
	WATimestamp       time.Time
	SentAt            *time.Time
	DeliveredAt       *time.Time
	ReadAt            *time.Time
	Raw               []byte
	Version           int
	CreatedAt         time.Time
	UpdatedAt         time.Time

	rawDropped bool
}

// RawDropped reports whether a transition released the queued bytes, which the store then clears; a row loaded without them keeps them.
func (m *Message) RawDropped() bool { return m.rawDropped }

// MediaInput is an attachment to send: Bytes, or a URL the service fetches first.
type MediaInput struct {
	Bytes    []byte
	URL      string
	Mime     string
	Filename string
	Caption  string
	Voice    bool
}

// SendInput is one outbound message; exactly one of Text, Media and Location is set.
type SendInput struct {
	To            string
	Text          string
	Media         *MediaInput
	Location      *Location
	ReplyTo       string
	Mentions      []string
	MarkReadFirst bool
}

// InboundMedia is the attachment metadata of an inbound message.
type InboundMedia struct {
	Mime     string
	Size     int64
	Filename string
	Keys     MediaKeys
	Width    int
	Height   int
	Seconds  int
	Voice    bool
}

// InboundInput is an inbound message as the engine delivered it.
type InboundInput struct {
	WAID         string
	ChatJID      string
	ChatAlt      string
	SenderJID    string
	SenderAlt    string
	SenderPhone  string
	PushName     string
	FromMe       bool
	IsGroup      bool
	Timestamp    time.Time
	Type         string
	Body         string
	Caption      string
	Media        *InboundMedia
	Location     *Location
	QuotedWAID   string
	QuotedSender string
	Mentions     []string
	Raw          []byte
}

// ListOpts filters and pages Store.List; SinceID returns newer rows in ascending id order instead of a page.
type ListOpts struct {
	ChatID       *uuid.UUID
	DeviceID     *uuid.UUID
	Direction    *Direction
	Status       *Status
	SinceID      *uuid.UUID
	UpdatedAfter *time.Time
	Unread       bool
	Limit        int
	Cursor       string
}

// WithDefaults clamps Limit.
func (o ListOpts) WithDefaults() ListOpts {
	if o.Limit <= 0 {
		o.Limit = DefaultListLimit
	}
	o.Limit = min(o.Limit, MaxListLimit)
	return o
}

// WAIDFor derives the WhatsApp message id of an outbound row, so a resend after a crash carries the same id.
func WAIDFor(id uuid.UUID) string {
	return "3EB0" + strings.ToUpper(hex.EncodeToString(id[:]))
}

// PublicIDPrefix is the prefix of a message's public id.
const PublicIDPrefix = "msg"

func newQueued(orgID, projectID, deviceID, chatID uuid.UUID, publicID string, typ Type) *Message {
	id := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	return &Message{
		ID: id, PublicID: publicID, OrgID: orgID, ProjectID: projectID, DeviceID: deviceID, ChatID: chatID,
		Direction: DirectionOut, WAMessageID: WAIDFor(id), FromMe: true, Type: typ,
		Mentions: []string{}, Status: StatusQueued, WATimestamp: now,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
}

// NewOutbound validates in and returns a queued row; media arrives as bytes and waits in Raw for the sender's upload.
func NewOutbound(orgID, projectID, deviceID, chatID uuid.UUID, publicID string, in SendInput, maxMediaBytes int64) (*Message, error) {
	kinds := 0
	for _, set := range []bool{strings.TrimSpace(in.Text) != "", in.Media != nil, in.Location != nil} {
		if set {
			kinds++
		}
	}
	if kinds != 1 {
		return nil, &InvalidInputError{Field: "content", Reason: "exactly one of text, media and location is required; a media caption goes in media.caption"}
	}
	mentions, err := validMentions(in.Mentions)
	if err != nil {
		return nil, err
	}
	var m *Message
	switch {
	case in.Media != nil:
		m, err = newMedia(orgID, projectID, deviceID, chatID, publicID, *in.Media, maxMediaBytes)
	case in.Location != nil:
		m, err = newLocation(orgID, projectID, deviceID, chatID, publicID, *in.Location)
	default:
		if utf8.RuneCountInString(in.Text) > MaxTextRunes {
			return nil, &InvalidInputError{Field: "text", Reason: "longer than 65536 characters"}
		}
		m = newQueued(orgID, projectID, deviceID, chatID, publicID, TypeText)
		m.Body = in.Text
	}
	if err != nil {
		return nil, err
	}
	m.QuotedWAMessageID = strings.TrimSpace(in.ReplyTo)
	m.Mentions = mentions
	return m, nil
}

func newMedia(orgID, projectID, deviceID, chatID uuid.UUID, publicID string, in MediaInput, maxMediaBytes int64) (*Message, error) {
	if len(in.Bytes) == 0 {
		return nil, &InvalidInputError{Field: "media", Reason: "no bytes"}
	}
	if int64(len(in.Bytes)) > maxMediaBytes {
		return nil, &MediaTooLargeError{Size: int64(len(in.Bytes)), Max: maxMediaBytes}
	}
	if utf8.RuneCountInString(in.Caption) > MaxTextRunes {
		return nil, &InvalidInputError{Field: "media.caption", Reason: "longer than 65536 characters"}
	}
	typ, err := TypeForMime(in.Mime, in.Filename)
	if err != nil {
		return nil, err
	}
	if in.Voice && (typ != TypeAudio || baseMime(in.Mime) != "audio/ogg") {
		return nil, &InvalidInputError{Field: "media.voice", Reason: "a voice note is audio/ogg (opus)"}
	}
	m := newQueued(orgID, projectID, deviceID, chatID, publicID, typ)
	m.Body = in.Caption
	m.Media = &Media{Mime: strings.TrimSpace(in.Mime), Size: int64(len(in.Bytes)), Filename: in.Filename, Storage: StorageWA, Voice: in.Voice}
	m.Raw = in.Bytes
	return m, nil
}

func newLocation(orgID, projectID, deviceID, chatID uuid.UUID, publicID string, loc Location) (*Message, error) {
	if loc.Lat < -90 || loc.Lat > 90 || loc.Lng < -180 || loc.Lng > 180 {
		return nil, &InvalidInputError{Field: "location", Reason: "latitude is -90..90 and longitude -180..180"}
	}
	m := newQueued(orgID, projectID, deviceID, chatID, publicID, TypeLocation)
	m.Location = &loc
	return m, nil
}

// NewReaction queues a reaction to targetWAID; an empty emoji removes the reaction.
func NewReaction(orgID, projectID, deviceID, chatID uuid.UUID, publicID, targetWAID, emoji string) (*Message, error) {
	if strings.TrimSpace(targetWAID) == "" {
		return nil, &InvalidInputError{Field: "target", Reason: "required"}
	}
	if utf8.RuneCountInString(emoji) > maxReactionRunes {
		return nil, &InvalidInputError{Field: "emoji", Reason: "one emoji"}
	}
	m := newQueued(orgID, projectID, deviceID, chatID, publicID, TypeReaction)
	m.TargetWAMessageID, m.Body = targetWAID, emoji
	return m, nil
}

// NewRevoke queues a delete-for-everyone of targetWAID.
func NewRevoke(orgID, projectID, deviceID, chatID uuid.UUID, publicID, targetWAID string) (*Message, error) {
	if strings.TrimSpace(targetWAID) == "" {
		return nil, &InvalidInputError{Field: "target", Reason: "required"}
	}
	m := newQueued(orgID, projectID, deviceID, chatID, publicID, TypeRevoke)
	m.TargetWAMessageID = targetWAID
	return m, nil
}

// NewEdit queues a replacement text for targetWAID.
func NewEdit(orgID, projectID, deviceID, chatID uuid.UUID, publicID, targetWAID, text string) (*Message, error) {
	if strings.TrimSpace(targetWAID) == "" {
		return nil, &InvalidInputError{Field: "target", Reason: "required"}
	}
	if strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > MaxTextRunes {
		return nil, &InvalidInputError{Field: "text", Reason: "1 to 65536 characters"}
	}
	m := newQueued(orgID, projectID, deviceID, chatID, publicID, TypeEdit)
	m.TargetWAMessageID, m.Body = targetWAID, text
	return m, nil
}

// NewInbound maps an engine message; a message the account sent from another device is recorded as sent outbound.
func NewInbound(ref Ref, chatID uuid.UUID, publicID string, in InboundInput) *Message {
	now := time.Now().UTC()
	ts := in.Timestamp.UTC()
	if in.Timestamp.IsZero() {
		ts = now
	}
	typ := knownType(in.Type)
	m := &Message{
		ID: uuid.Must(uuid.NewV7()), PublicID: publicID, OrgID: ref.OrgID, ProjectID: ref.ProjectID, DeviceID: ref.DeviceID, ChatID: chatID,
		Direction: DirectionIn, WAMessageID: in.WAID, SenderJID: in.SenderJID, SenderLID: lidOf(in.SenderJID, in.SenderAlt),
		SenderPhone: in.SenderPhone, SenderName: in.PushName, FromMe: in.FromMe, Type: typ, Body: in.Body,
		Location: in.Location, QuotedWAMessageID: in.QuotedWAID, Mentions: phonesOf(in.Mentions),
		Status: StatusReceived, WATimestamp: ts, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	if in.Caption != "" {
		m.Body = in.Caption
	}
	if in.Media != nil {
		m.Media = &Media{
			Mime: in.Media.Mime, Size: in.Media.Size, Filename: in.Media.Filename, Storage: StorageWA, Keys: in.Media.Keys,
			Width: in.Media.Width, Height: in.Media.Height, Seconds: in.Media.Seconds, Voice: in.Media.Voice,
		}
	}
	if typ != TypeText {
		m.Raw = in.Raw
	}
	if in.FromMe {
		m.Direction, m.Status, m.SentAt = DirectionOut, StatusSent, &ts
	}
	return m
}

// NOTE: the service restamps a freshly built row with its own clock, so persisted times follow Options.Clock; an outbound row's timestamp is its creation time.
func (m *Message) stamp(now time.Time) {
	now = now.UTC()
	m.CreatedAt, m.UpdatedAt = now, now
	if m.Direction == DirectionOut && m.Status == StatusQueued {
		m.WATimestamp = now
	}
}

// MarkSending claims the row for one send attempt.
func (m *Message) MarkSending(now time.Time) {
	m.Status = StatusSending
	m.Attempts++
	m.UpdatedAt = now.UTC()
}

// Release hands a claimed row back to the queue and returns its attempt; the send never reached the engine.
func (m *Message) Release(now time.Time) bool {
	if m.Status != StatusSending {
		return false
	}
	m.Status = StatusQueued
	m.Attempts = max(m.Attempts-1, 0)
	m.UpdatedAt = now.UTC()
	return true
}

// MarkSent records the acknowledgement and the upload keys, drops the queued bytes, and reports whether the status rose to sent; a receipt that arrived first keeps its higher status.
func (m *Message) MarkSent(at time.Time, keys *MediaKeys, now time.Time) bool {
	at = at.UTC()
	m.SentAt, m.Error = &at, ""
	if m.Media != nil {
		m.Raw, m.rawDropped = nil, true
		if keys != nil {
			m.Media.Keys = *keys
		}
	}
	m.UpdatedAt = now.UTC()
	if statusRank[m.Status] >= statusRank[StatusSent] {
		return false
	}
	m.Status = StatusSent
	return true
}

// Expire fails a row still queued, or stuck in sending, past its deadline, reporting whether it changed.
func (m *Message) Expire(reason string, now time.Time) bool {
	if m.Status != StatusQueued && m.Status != StatusSending {
		return false
	}
	m.Status, m.Error, m.Raw, m.rawDropped = StatusFailed, reason, nil, true
	m.UpdatedAt = now.UTC()
	return true
}

// MarkFailed requeues a retryable failure below MaxAttempts and otherwise fails the row, reporting whether it is terminal; a row a receipt or an acknowledgement already advanced is left alone.
func (m *Message) MarkFailed(reason string, retryable bool, now time.Time) bool {
	if statusRank[m.Status] > 0 {
		return false
	}
	m.Error = reason
	m.UpdatedAt = now.UTC()
	if retryable && m.Attempts < MaxAttempts {
		m.Status = StatusQueued
		return false
	}
	m.Status = StatusFailed
	if m.Media != nil {
		m.Raw, m.rawDropped = nil, true
	}
	return true
}

// Receipt advances an outbound row monotonically through delivered, read and played, reporting whether it changed.
func (m *Message) Receipt(kind ReceiptKind, at, now time.Time) bool {
	want := receiptRank[kind]
	if m.Direction != DirectionOut || want == 0 || want <= statusRank[m.Status] {
		return false
	}
	at = at.UTC()
	m.Status = Status(kind)
	if m.SentAt == nil {
		m.SentAt = &at
	}
	if m.DeliveredAt == nil {
		m.DeliveredAt = &at
	}
	if want >= receiptRank[ReceiptRead] && m.ReadAt == nil {
		m.ReadAt = &at
	}
	if m.Media != nil {
		m.Raw, m.rawDropped = nil, true
	}
	m.UpdatedAt = now.UTC()
	return true
}

// MarkReadInbound stamps the moment the device read an inbound row, reporting whether it changed.
func (m *Message) MarkReadInbound(at time.Time) bool {
	if m.Direction != DirectionIn || m.ReadAt != nil {
		return false
	}
	at = at.UTC()
	m.ReadAt, m.UpdatedAt = &at, at
	return true
}

// CheckEditable allows an edit only of the account's own sent text inside EditWindow.
func (m *Message) CheckEditable(now time.Time) error {
	if err := m.CheckRevocable(); err != nil {
		return err
	}
	if m.Type != TypeText {
		return &InvalidInputError{Field: "type", Reason: "only text can be edited"}
	}
	if m.Status == StatusQueued || m.Status == StatusSending || m.Status == StatusFailed {
		return &InvalidInputError{Field: "status", Reason: "the message has not been sent"}
	}
	if now.Sub(m.WATimestamp) > EditWindow {
		return &EditWindowClosedError{}
	}
	return nil
}

// CheckRevocable allows a revoke only of the account's own message.
func (m *Message) CheckRevocable() error {
	if !m.FromMe || m.Direction != DirectionOut {
		return &NotOwnMessageError{}
	}
	return nil
}

// ReceiptKindOf maps an engine receipt type onto the kinds this module acts on; every other type is ignored.
func ReceiptKindOf(engineType string) (ReceiptKind, bool) {
	switch engineType {
	case "", "delivered":
		return ReceiptDelivered, true
	case "read":
		return ReceiptRead, true
	case "played":
		return ReceiptPlayed, true
	}
	return "", false
}

// InlineSafe reports whether a browser may render mime inline: raster images only; everything else, SVG included, is an attachment.
func InlineSafe(mimeType string) bool {
	switch baseMime(mimeType) {
	case "image/jpeg", "image/png", "image/webp", "image/gif":
		return true
	}
	return false
}

// TypeForMime picks the media type for a mime; anything outside the image, video and audio sets is a document and needs a filename.
func TypeForMime(mime, filename string) (Type, error) {
	switch base := baseMime(mime); base {
	case "":
		return "", &UnsupportedMimeError{Mime: mime}
	case "image/jpeg", "image/png", "image/webp":
		return TypeImage, nil
	case "video/mp4":
		return TypeVideo, nil
	case "audio/ogg", "audio/mpeg", "audio/mp4", "audio/aac":
		return TypeAudio, nil
	default:
		if strings.TrimSpace(filename) == "" {
			return "", &InvalidInputError{Field: "media.filename", Reason: "a document needs a filename"}
		}
		return TypeDocument, nil
	}
}

// PreviewFor renders the one-line chat-list preview of m.
func PreviewFor(m *Message) string {
	switch m.Type {
	case TypeText, TypeEdit:
		return m.Body
	case TypeImage, TypeVideo, TypeAudio, TypeDocument:
		return strings.TrimSpace("[" + string(m.Type) + "] " + m.Body)
	case TypeLocation:
		return "[location]"
	case TypeContact:
		return "[contact]"
	case TypeReaction:
		return strings.TrimSpace("[reaction] " + m.Body)
	case TypeRevoke:
		return "[deleted]"
	default:
		return "[message]"
	}
}

//nolint:gochecknoglobals // immutable rank tables.
var (
	statusRank  = map[Status]int{StatusSent: 1, StatusDelivered: 2, StatusRead: 3, StatusPlayed: 4}
	receiptRank = map[ReceiptKind]int{ReceiptDelivered: 2, ReceiptRead: 3, ReceiptPlayed: 4}
)

func knownType(s string) Type {
	switch t := Type(s); t {
	case TypeText, TypeImage, TypeVideo, TypeAudio, TypeDocument, TypeLocation, TypeContact, TypeReaction, TypeRevoke, TypeEdit:
		return t
	}
	return TypeUnknown
}

func baseMime(mime string) string {
	base, _, _ := strings.Cut(mime, ";")
	return strings.ToLower(strings.TrimSpace(base))
}

func validMentions(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, p := range in {
		if len(p) < 5 || len(p) > 15 || strings.IndexFunc(p, func(r rune) bool { return !unicode.IsDigit(r) }) >= 0 {
			return nil, &InvalidInputError{Field: "mentions", Reason: "each mention is a phone number in digits"}
		}
		out = append(out, p)
	}
	return out, nil
}

func phonesOf(jids []string) []string {
	out := make([]string, 0, len(jids))
	for _, j := range jids {
		user, server, ok := strings.Cut(j, "@")
		if !ok || server != "s.whatsapp.net" {
			continue
		}
		user, _, _ = strings.Cut(user, ":")
		out = append(out, user)
	}
	return out
}

func lidOf(jids ...string) string {
	for _, j := range jids {
		if strings.HasSuffix(j, "@lid") {
			return j
		}
	}
	return ""
}

// ValidateRecipient reports whether to is a phone number or a WhatsApp JID the service would accept, without any read.
func ValidateRecipient(to string) error {
	_, _, err := recipient(to)
	return err
}
