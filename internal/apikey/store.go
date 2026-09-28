package apikey

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Store is the driven port for API key persistence.
type Store interface {
	Save(ctx context.Context, k *APIKey) error
	ByID(ctx context.Context, id uuid.UUID) (*APIKey, error)
	BySecretHash(ctx context.Context, hash [32]byte) (*APIKey, error)
	List(ctx context.Context, projectID uuid.UUID) ([]*APIKey, error)
	TouchLastUsed(ctx context.Context, id uuid.UUID, at time.Time) error
}
