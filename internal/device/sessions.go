package device

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// SessionState mirrors whatsapp.State without importing it.
type SessionState string

// SessionState values.
const (
	SessionUnlinked     SessionState = "unlinked"
	SessionLinking      SessionState = "linking"
	SessionConnected    SessionState = "connected"
	SessionDisconnected SessionState = "disconnected"
	SessionLoggedOut    SessionState = "logged_out"
)

// SessionStatus is what the device module shows of a WhatsApp session.
type SessionStatus struct {
	State    SessionState
	Phone    string
	PushName string
	Reason   string
	LastSeen *time.Time
	Live     bool
}

// LinkMethod is how a pairing attempt links.
type LinkMethod string

// LinkMethod values.
const (
	LinkQR    LinkMethod = "qr"
	LinkPhone LinkMethod = "phone"
)

// LinkOutcome is where a pairing attempt stands.
type LinkOutcome string

// LinkOutcome values.
const (
	LinkPending   LinkOutcome = "pending"
	LinkConnected LinkOutcome = "connected"
	LinkTimeout   LinkOutcome = "timeout"
	LinkFailed    LinkOutcome = "failed"
	LinkNone      LinkOutcome = "none"
)

// LinkState is the device module's view of one pairing attempt; ID is its lnk_ public id.
type LinkState struct {
	ID          string
	Method      LinkMethod
	Outcome     LinkOutcome
	QR          string
	PNG         []byte
	PairingCode string
	ExpiresAt   time.Time
	StartedAt   time.Time
	Error       string
}

// Sessions is the port to the WhatsApp context; its errors pass through unchanged.
type Sessions interface {
	StatusByDevices(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]SessionStatus, error)
	Link(ctx context.Context, deviceID uuid.UUID) (LinkState, error)
	LinkWithPhone(ctx context.Context, deviceID uuid.UUID, phone string) (LinkState, error)
	LinkState(ctx context.Context, deviceID uuid.UUID) (LinkState, error)
	Unlink(ctx context.Context, deviceID uuid.UUID) error
	Forget(ctx context.Context, deviceID uuid.UUID) error
}

// DeviceView is a device with its session status.
type DeviceView struct {
	Device *Device
	Status SessionStatus
}

// Update names the fields one conditional write changes; nil leaves a field alone.
type Update struct {
	Name  *string
	Rules *Rules
}
