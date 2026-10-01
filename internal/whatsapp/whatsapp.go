// Package whatsapp is the WhatsApp protocol context: one session per device, the lease that owns it, and the engine port behind it.
package whatsapp

import (
	"time"

	"github.com/google/uuid"
)

// State is a session's lifecycle state.
type State string

// State values.
const (
	StateUnlinked     State = "unlinked"
	StateLinking      State = "linking"
	StateConnected    State = "connected"
	StateDisconnected State = "disconnected"
	StateLoggedOut    State = "logged_out"
)

// EngineWhatsmeow is the engine name a session row records for the whatsmeow adapter.
const EngineWhatsmeow = "whatsmeow"

// Session is one device's link to a WhatsApp account.
type Session struct {
	DeviceID        uuid.UUID
	OrgID           uuid.UUID
	ProjectID       uuid.UUID
	Engine          string
	JID             string
	LID             string
	Phone           string
	PushName        string
	Platform        string
	State           State
	Reason          string
	LastConnectedAt *time.Time
	LastSeenAt      *time.Time
	LastError       string
	Version         int
	UpdatedAt       time.Time
}

// NewSession returns a session row in the linking state for deviceID.
func NewSession(deviceID, orgID, projectID uuid.UUID, engine string) *Session {
	return &Session{
		DeviceID:  deviceID,
		OrgID:     orgID,
		ProjectID: projectID,
		Engine:    engine,
		State:     StateLinking,
		Version:   1,
		UpdatedAt: time.Now().UTC(),
	}
}

// Status is the read model a device shows: the persisted state plus whether this process holds the engine session.
type Status struct {
	State    State
	Phone    string
	PushName string
	Reason   string
	LastSeen *time.Time
	Live     bool
}
