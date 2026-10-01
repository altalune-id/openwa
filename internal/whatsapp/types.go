package whatsapp

import (
	"time"

	"github.com/google/uuid"
)

// MediaMeta is the metadata and decryption material of an inbound media message.
type MediaMeta struct {
	Kind          string
	Mime          string
	Size          uint64
	FileName      string
	URL           string
	DirectPath    string
	MediaKey      []byte
	FileSHA256    []byte
	FileEncSHA256 []byte
	Width         int
	Height        int
	Seconds       int
	Voice         bool
}

// InboundMessage is an engine-neutral inbound message.
type InboundMessage struct {
	ID           string
	ChatJID      string
	ChatAlt      string
	SenderJID    string
	SenderAltJID string
	SenderPhone  string
	PushName     string
	FromMe       bool
	IsGroup      bool
	Timestamp    time.Time
	Type         string
	Body         string
	Caption      string
	Media        *MediaMeta
	Location     *Location
	QuotedID     string
	QuotedSender string
	Mentions     []string
	Raw          []byte
}

// Receipt is a delivery, read or played receipt for one or more messages.
type Receipt struct {
	ChatJID    string
	SenderJID  string
	MessageIDs []string
	Type       string
	Timestamp  time.Time
}

// ContactUpdate is a name or push-name change for one contact.
type ContactUpdate struct {
	JID          string
	Phone        string
	Name         string
	PushName     string
	BusinessName string
}

// LinkIDPrefix is the prefix of every link attempt's public id (lnk_ + 16 nanoid characters).
const LinkIDPrefix = "lnk"

// LinkMethod is how a link attempt pairs.
type LinkMethod string

// LinkMethod values.
const (
	LinkMethodQR    LinkMethod = "qr"
	LinkMethodPhone LinkMethod = "phone"
)

// LinkOutcome is where a link attempt stands.
type LinkOutcome string

// LinkOutcome values.
const (
	OutcomePending   LinkOutcome = "pending"
	OutcomeConnected LinkOutcome = "connected"
	OutcomeTimeout   LinkOutcome = "timeout"
	OutcomeFailed    LinkOutcome = "failed"
	OutcomeNone      LinkOutcome = "none"
)

// LinkState is the in-memory view of one link attempt; Outcome is OutcomeNone when no attempt exists, and ID is the attempt's lnk_ public id.
type LinkState struct {
	ID          string
	Method      LinkMethod
	Outcome     LinkOutcome
	QR          string
	PNG         []byte
	PairingCode string
	ExpiresAt   time.Time
	StartedAt   time.Time
	Err         error
}

// Location is a shared map pin.
type Location struct {
	Lat     float64
	Lng     float64
	Name    string
	Address string
}

// MediaKeys is the decryption material the engine needs to download a file.
type MediaKeys struct {
	URL           string
	DirectPath    string
	MediaKey      []byte
	FileSHA256    []byte
	FileEncSHA256 []byte
	Length        uint64
}

// Outbound message kinds.
const (
	KindText     = "text"
	KindImage    = "image"
	KindVideo    = "video"
	KindAudio    = "audio"
	KindDocument = "document"
	KindLocation = "location"
	KindReaction = "reaction"
	KindRevoke   = "revoke"
	KindEdit     = "edit"
)

// OutboundMedia is an attachment to upload and send.
type OutboundMedia struct {
	Bytes    []byte
	Mime     string
	Filename string
	Caption  string
	Voice    bool
}

// OutboundMessage is one send; WAID is fixed by the queue so a resend is idempotent for recipients.
type OutboundMessage struct {
	WAID         string
	Kind         string
	To           string
	Text         string
	Media        *OutboundMedia
	Location     *Location
	QuotedWAID   string
	QuotedSender string
	QuotedBody   string
	QuotedRaw    []byte
	Mentions     []string
	TargetWAID   string
	TargetSender string
	TargetFromMe bool
}

// OutboundRow is a claimed queue row.
type OutboundRow struct {
	ID      uuid.UUID
	Message OutboundMessage
}

// SendResult is the engine's acknowledgement of a send; Media carries the upload keys of an attachment so it can be downloaded again.
type SendResult struct {
	At      time.Time
	ChatJID string
	Media   *MediaKeys
}

// GroupInfo is what the engine reports about a group.
type GroupInfo struct {
	JID          string
	Name         string
	Topic        string
	Participants int
	Announce     bool
	Locked       bool
	InviteLink   string
}
