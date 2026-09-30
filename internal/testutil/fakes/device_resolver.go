package fakes

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/apikey"
)

// DeviceResolver is an in-memory apikey.DeviceResolver over a fixed public id to UUID table.
type DeviceResolver struct {
	mu   sync.Mutex
	byID map[string]uuid.UUID
}

// NewDeviceResolver returns a resolver that knows no devices.
func NewDeviceResolver() *DeviceResolver { return &DeviceResolver{byID: map[string]uuid.UUID{}} }

var _ apikey.DeviceResolver = (*DeviceResolver)(nil)

// Add registers one device.
func (f *DeviceResolver) Add(publicID string, id uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byID[publicID] = id
}

func (f *DeviceResolver) DeviceIDs(_ context.Context, publicIDs []string) (map[string]uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]uuid.UUID{}
	for _, p := range publicIDs {
		if id, ok := f.byID[p]; ok {
			out[p] = id
		}
	}
	return out, nil
}

func (f *DeviceResolver) DevicePublicIDs(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[uuid.UUID]string{}
	for p, id := range f.byID {
		for _, want := range ids {
			if want == id {
				out[id] = p
			}
		}
	}
	return out, nil
}
