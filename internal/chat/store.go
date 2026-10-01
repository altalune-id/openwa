package chat

import (
	"context"

	"github.com/google/uuid"
)

// Store is the driven port.
type Store interface {
	// Save upserts c, refusing a row outside the scope on ctx with *NotFoundError; a nonzero ifVersion that no longer matches returns *VersionMismatchError, another org's row *NotFoundError.
	Save(ctx context.Context, c *Chat, ifVersion int) error
	// Insert creates c unless a unique key already holds a row, reporting whether it did.
	Insert(ctx context.Context, c *Chat) (bool, error)
	ByID(ctx context.Context, id uuid.UUID) (*Chat, error)
	// ByPublicID returns the chat of the org on ctx whose public id is publicID.
	ByPublicID(ctx context.Context, publicID string) (*Chat, error)
	// PublicIDs maps the given chat ids of the org and project on ctx to their public ids in one query.
	PublicIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error)
	// ByJID matches jid against the jid or the lid column of one device's chats.
	ByJID(ctx context.Context, deviceID uuid.UUID, jid string) (*Chat, error)
	// List pages the caller's project, newest activity first, returning the next cursor or "".
	List(ctx context.Context, opts ListOpts) ([]*Chat, string, error)
	Delete(ctx context.Context, id uuid.UUID) error
}
