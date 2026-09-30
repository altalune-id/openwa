package whatsapp

import "time"

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
}

// InboundMessage is an engine-neutral inbound message.
type InboundMessage struct {
	ID           string
	ChatJID      string
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
	QuotedID     string
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
