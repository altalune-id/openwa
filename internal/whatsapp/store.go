package whatsapp

import (
	"context"

	"github.com/google/uuid"
)

// Store is the driven port for session rows.
type Store interface {
	// Save upserts s; a nonzero ifVersion that misses returns *StaleVersionError, and a row outside the caller's org reports *SessionNotFoundError.
	Save(ctx context.Context, s *Session, ifVersion int) error
	ByDevice(ctx context.Context, deviceID uuid.UUID) (*Session, error)
	ByDevices(ctx context.Context, ids []uuid.UUID) ([]*Session, error)
	Delete(ctx context.Context, deviceID uuid.UUID) error
}
