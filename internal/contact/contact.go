// Package contact mirrors each device's WhatsApp address book: names, push names and phone identities.
package contact

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// List page bounds.
const (
	DefaultListLimit = 50
	MaxListLimit     = 200
)

// Contact is one person a device knows, keyed by phone-number JID when known and by LID otherwise.
type Contact struct {
	ID           uuid.UUID
	OrgID        uuid.UUID
	ProjectID    uuid.UUID
	DeviceID     uuid.UUID
	JID          string
	LID          string
	Phone        string
	Name         string
	PushName     string
	BusinessName string
	UpdatedAt    time.Time
}

// ListOpts filters and pages Store.List; Search is a case-insensitive prefix on the address-book name.
type ListOpts struct {
	DeviceID  *uuid.UUID
	DeviceIDs []uuid.UUID
	Search    string
	Limit     int
	Cursor    string
}

// WithDefaults clamps Limit and trims Search.
func (o ListOpts) WithDefaults() ListOpts {
	if o.Limit <= 0 {
		o.Limit = DefaultListLimit
	}
	o.Limit = min(o.Limit, MaxListLimit)
	o.Search = strings.TrimSpace(o.Search)
	return o
}

// New builds a contact for jid on a device.
func New(orgID, projectID, deviceID uuid.UUID, jid string) *Contact {
	return &Contact{
		ID:        uuid.Must(uuid.NewV7()),
		OrgID:     orgID,
		ProjectID: projectID,
		DeviceID:  deviceID,
		JID:       strings.TrimSpace(jid),
		UpdatedAt: time.Now().UTC(),
	}
}

// DisplayName resolves Name, then PushName, then BusinessName, then Phone, then the JID's user part.
func (c *Contact) DisplayName() string {
	for _, v := range []string{c.Name, c.PushName, c.BusinessName, c.Phone} {
		if v != "" {
			return v
		}
	}
	user, _, _ := strings.Cut(c.JID, "@")
	return user
}
