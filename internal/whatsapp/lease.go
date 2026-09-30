package whatsapp

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Lease is the row that says which process runs a linked device.
type Lease struct {
	DeviceID  uuid.UUID
	OrgID     uuid.UUID
	ProjectID uuid.UUID
	JID       string
	Owner     string
	ExpiresAt *time.Time
}

// LeaseStore is the cross-tenant lease registry; only the Runtime writes it.
type LeaseStore interface {
	// Upsert writes l as given; the caller sets Owner and ExpiresAt.
	Upsert(ctx context.Context, l Lease) error
	Delete(ctx context.Context, deviceID uuid.UUID) error
	Exists(ctx context.Context, deviceID uuid.UUID) (bool, error)
	// Claim takes up to limit free or expired leases not already owned by owner and returns them as claimed.
	Claim(ctx context.Context, owner string, ttl time.Duration, limit int) ([]Lease, error)
	// Renew extends every lease owner holds and returns their ids; an id absent from the result was lost.
	Renew(ctx context.Context, owner string, ttl time.Duration) ([]uuid.UUID, error)
	// Release frees deviceID's lease only when owner holds it.
	Release(ctx context.Context, owner string, deviceID uuid.UUID) error
	// ReleaseAll frees every lease owner holds.
	ReleaseAll(ctx context.Context, owner string) error
}
