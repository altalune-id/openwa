package device

import (
	"context"

	"github.com/google/uuid"
)

// Store is the driven port.
type Store interface {
	// Save upserts d; a nonzero ifVersion that misses returns *StaleVersionError, a duplicate name in the project *NameTakenError, and a duplicate public id *PublicIDTakenError.
	Save(ctx context.Context, d *Device, ifVersion int) error
	ByID(ctx context.Context, id uuid.UUID) (*Device, error)
	// ByPublicID returns the device of the org on ctx whose public id is publicID.
	ByPublicID(ctx context.Context, publicID string) (*Device, error)
	// PublicIDsByIDs maps the project's devices among ids to their public ids in one read; unknown ids are absent.
	PublicIDsByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error)
	// IDsByPublicIDs maps the project's devices among publicIDs to their UUIDs in one read; unknown ids are absent.
	IDsByPublicIDs(ctx context.Context, publicIDs []string) (map[string]uuid.UUID, error)
	// List returns the devices of the org and project on ctx, newest first.
	List(ctx context.Context, opts ListOpts) ([]*Device, error)
	Delete(ctx context.Context, id uuid.UUID) error
}
