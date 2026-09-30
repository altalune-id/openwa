package whatsapp

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Capabilities says which features an engine implements.
type Capabilities struct {
	QRLink        bool
	PhoneCodeLink bool
	Groups        bool
	HistorySync   bool
	Reactions     bool
	Edits         bool
}

// SessionRef names the device a session belongs to; JID is "" for a fresh link.
type SessionRef struct {
	DeviceID  uuid.UUID
	OrgID     uuid.UUID
	ProjectID uuid.UUID
	JID       string
}

// Identity is what the engine learns about the account on pairing.
type Identity struct {
	JID      string
	LID      string
	Phone    string
	PushName string
	Platform string
}

// LinkEventKind classifies a LinkEvent.
type LinkEventKind string

// LinkEventKind values.
const (
	LinkEventCode        LinkEventKind = "code"
	LinkEventSuccess     LinkEventKind = "success"
	LinkEventTimeout     LinkEventKind = "timeout"
	LinkEventError       LinkEventKind = "error"
	LinkEventUnsupported LinkEventKind = "unsupported"
)

// LinkEvent is one item of a link attempt: a QR payload to show, or how the attempt ended.
type LinkEvent struct {
	Kind    LinkEventKind
	Code    string
	Expires time.Time
	Err     error
}

// EngineSession is the link-lifecycle contract of one open engine session.
type EngineSession interface {
	Link(ctx context.Context) (<-chan LinkEvent, error)
	LinkWithPhone(ctx context.Context, phone string) (string, error)
	Unlink(ctx context.Context) error
	Close(ctx context.Context) error
	Connected() bool
}

// EventSink receives engine-neutral events, called from a session's own engine goroutine (never concurrently for one session); implementations must return promptly and never block the engine.
type EventSink interface {
	OnState(ref SessionRef, st State, reason string)
	OnDegraded(ref SessionRef, err error)
	OnLinked(ref SessionRef, id Identity)
	OnMessage(ref SessionRef, m InboundMessage)
	OnReceipt(ref SessionRef, r Receipt)
	OnContact(ref SessionRef, c ContactUpdate)
	OnHistory(ref SessionRef, batch []InboundMessage)
}

// Engine opens sessions and purges engine-side state; each open session runs an engine-owned goroutine that delivers every EventSink call and stops on Close.
type Engine interface {
	Name() string
	Capabilities() Capabilities
	// Open returns *SessionGoneError when the engine no longer has ref.JID.
	Open(ctx context.Context, ref SessionRef, sink EventSink) (EngineSession, error)
	// Purge deletes engine-side state for ref; it is idempotent.
	Purge(ctx context.Context, ref SessionRef) error
}

// Reasons a session transition records; engine adapters and the runtime share them.
const (
	ReasonNetwork              = "network"
	ReasonLeaseLost            = "lease_lost"
	ReasonLeaseUnverified      = "lease_unverified"
	ReasonOpenFailedPrefix     = "open_failed: "
	ReasonDeviceMissing        = "device_missing"
	ReasonLoggedOutByPhone     = "logged_out_by_phone"
	ReasonUnlinked             = "unlinked"
	ReasonStreamReplaced       = "stream_replaced"
	ReasonClientOutdated       = "client_outdated"
	ReasonTempBanPrefix        = "temp_ban_"
	ReasonConnectFailurePrefix = "connect_failure_"
	ReasonLinkTimeout          = "link_timeout"
	ReasonQRTimeout            = "qr_timeout"
	ReasonLinkFailed           = "link_failed"
	ReasonLinkCancelled        = "link_cancelled"
	ReasonUnsupported          = "unsupported"
	ReasonLeaseWriteFailed     = "lease_write_failed"
)
