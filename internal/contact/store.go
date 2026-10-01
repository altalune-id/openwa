package contact

import (
	"context"

	"github.com/google/uuid"
)

// Store is the driven port.
type Store interface {
	// Upsert inserts c or merges its non-empty fields into the device's row for c.JID; another org's row is *NotFoundError.
	Upsert(ctx context.Context, c *Contact) error
	// ByJID is the only lookup: a contact is its jid on a device.
	ByJID(ctx context.Context, deviceID uuid.UUID, jid string) (*Contact, error)
	// List pages the caller's project by most recently updated, returning the next cursor or "".
	List(ctx context.Context, opts ListOpts) ([]*Contact, string, error)
}
