package fakes

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/publicid"
	"altalune.id/openwa/internal/platform/tenant"
)

// Device is an in-memory device.Store mirroring the real store's org predicate and version contract.
type Device struct {
	mu         sync.Mutex
	data       map[uuid.UUID]*device.Device
	batchReads int

	SaveFn   func(ctx context.Context, d *device.Device, ifVersion int) error
	ByIDFn   func(ctx context.Context, id uuid.UUID) (*device.Device, error)
	DeleteFn func(ctx context.Context, id uuid.UUID) error
}

// NewDevice returns an empty in-memory device.Store.
func NewDevice() *Device { return &Device{data: map[uuid.UUID]*device.Device{}} }

// DevicePublicID mints a fresh dev_ public id for a test fixture.
func DevicePublicID() string {
	id, err := publicid.New(device.PublicIDPrefix)
	if err != nil {
		panic(err)
	}
	return id
}

var _ device.Store = (*Device)(nil)

// Seed inserts d without the uniqueness or version checks.
func (f *Device) Seed(d *device.Device) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[d.ID] = cloneDevice(d)
}

// Len reports how many devices are stored.
func (f *Device) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.data)
}

func (f *Device) Save(ctx context.Context, d *device.Device, ifVersion int) error {
	if f.SaveFn != nil {
		return f.SaveFn(ctx, d, ifVersion)
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	if d.OrgID != tc.OrgID {
		return &device.NotFoundError{ID: d.ID.String()}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, existing := range f.data {
		if id != d.ID && existing.PublicID == d.PublicID {
			return &device.PublicIDTakenError{PublicID: d.PublicID}
		}
		if id != d.ID && existing.ProjectID == d.ProjectID && strings.EqualFold(existing.Name, d.Name) {
			return &device.NameTakenError{Name: d.Name}
		}
	}
	stored := cloneDevice(d)
	existing, ok := f.data[d.ID]
	if !ok && ifVersion != 0 {
		return &device.NotFoundError{ID: d.ID.String()}
	}
	// NOTE: the real store inserts with the caller's version and, on conflict, sets version = version + 1 behind an org guard.
	if ok {
		if existing.OrgID != tc.OrgID {
			return &device.NotFoundError{ID: d.ID.String()}
		}
		if ifVersion != 0 && existing.Version != ifVersion {
			return &device.StaleVersionError{Want: ifVersion, Got: existing.Version}
		}
		stored.Version = existing.Version + 1
	}
	f.data[d.ID] = stored
	return nil
}

func (f *Device) ByID(ctx context.Context, id uuid.UUID) (*device.Device, error) {
	if f.ByIDFn != nil {
		return f.ByIDFn(ctx, id)
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.data[id]
	if !ok || d.OrgID != tc.OrgID {
		return nil, &device.NotFoundError{ID: id.String()}
	}
	return cloneDevice(d), nil
}

func (f *Device) ByPublicID(ctx context.Context, publicID string) (*device.Device, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.data {
		if d.PublicID == publicID && d.OrgID == tc.OrgID {
			return cloneDevice(d), nil
		}
	}
	return nil, &device.NotFoundError{ID: publicID}
}

func (f *Device) PublicIDsByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batchReads++
	out := map[uuid.UUID]string{}
	for _, id := range ids {
		if d, ok := f.data[id]; ok && d.OrgID == tc.OrgID && d.ProjectID == tc.ProjectID {
			out[id] = d.PublicID
		}
	}
	return out, nil
}

func (f *Device) IDsByPublicIDs(ctx context.Context, publicIDs []string) (map[string]uuid.UUID, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batchReads++
	out := map[string]uuid.UUID{}
	for _, d := range f.data {
		if d.OrgID == tc.OrgID && d.ProjectID == tc.ProjectID && slices.Contains(publicIDs, d.PublicID) {
			out[d.PublicID] = d.ID
		}
	}
	return out, nil
}

// BatchReads counts PublicIDsByIDs and IDsByPublicIDs calls.
func (f *Device) BatchReads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.batchReads
}

func (f *Device) List(ctx context.Context, _ device.ListOpts) ([]*device.Device, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*device.Device, 0, len(f.data))
	for _, d := range f.data {
		if d.OrgID != tc.OrgID || d.ProjectID != tc.ProjectID {
			continue
		}
		out = append(out, cloneDevice(d))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID.String() > out[j].ID.String()
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (f *Device) Delete(ctx context.Context, id uuid.UUID) error {
	if f.DeleteFn != nil {
		return f.DeleteFn(ctx, id)
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.data[id]
	if !ok || d.OrgID != tc.OrgID {
		return &device.NotFoundError{ID: id.String()}
	}
	delete(f.data, id)
	return nil
}

func cloneDevice(d *device.Device) *device.Device {
	cp := *d
	cp.Rules.AllowedSenders = append([]string{}, d.Rules.AllowedSenders...)
	cp.Rules.AllowedGroups = append([]string{}, d.Rules.AllowedGroups...)
	return &cp
}
