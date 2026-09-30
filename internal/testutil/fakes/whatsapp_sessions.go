package fakes

import (
	"context"
	"sort"
	"sync"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/whatsapp"
)

// WhatsAppSessions is an in-memory whatsapp.Store mirroring the real store's org predicate and version contract.
type WhatsAppSessions struct {
	mu   sync.Mutex
	rows map[uuid.UUID]*whatsapp.Session

	SaveFn func(ctx context.Context, s *whatsapp.Session, ifVersion int) error
	saves  int
}

// SaveCount reports how many Save calls reached the store.
func (f *WhatsAppSessions) SaveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saves
}

// NewWhatsAppSessions returns an empty in-memory whatsapp.Store.
func NewWhatsAppSessions() *WhatsAppSessions {
	return &WhatsAppSessions{rows: map[uuid.UUID]*whatsapp.Session{}}
}

var _ whatsapp.Store = (*WhatsAppSessions)(nil)

// Seed inserts s without the version check.
func (f *WhatsAppSessions) Seed(s *whatsapp.Session) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *s
	f.rows[s.DeviceID] = &cp
}

// Row returns a copy of the stored row for deviceID.
func (f *WhatsAppSessions) Row(deviceID uuid.UUID) (whatsapp.Session, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.rows[deviceID]
	if !ok {
		return whatsapp.Session{}, false
	}
	return *s, true
}

func (f *WhatsAppSessions) Save(ctx context.Context, s *whatsapp.Session, ifVersion int) error {
	if f.SaveFn != nil {
		return f.SaveFn(ctx, s, ifVersion)
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if s.OrgID != tc.OrgID {
		return &whatsapp.SessionNotFoundError{ID: s.DeviceID.String()}
	}
	f.saves++
	cp := *s
	existing, ok := f.rows[s.DeviceID]
	if !ok && ifVersion != 0 {
		return &whatsapp.SessionNotFoundError{ID: s.DeviceID.String()}
	}
	if ok {
		if existing.OrgID != tc.OrgID {
			return &whatsapp.SessionNotFoundError{ID: s.DeviceID.String()}
		}
		if ifVersion != 0 && existing.Version != ifVersion {
			return &whatsapp.StaleVersionError{Want: ifVersion, Got: existing.Version}
		}
		cp.Version = existing.Version + 1
	}
	f.rows[s.DeviceID] = &cp
	return nil
}

func (f *WhatsAppSessions) ByDevice(ctx context.Context, deviceID uuid.UUID) (*whatsapp.Session, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.rows[deviceID]
	if !ok || s.OrgID != tc.OrgID {
		return nil, &whatsapp.SessionNotFoundError{ID: deviceID.String()}
	}
	cp := *s
	return &cp, nil
}

func (f *WhatsAppSessions) ByDevices(ctx context.Context, ids []uuid.UUID) ([]*whatsapp.Session, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*whatsapp.Session, 0, len(ids))
	for _, id := range ids {
		s, ok := f.rows[id]
		if !ok || s.OrgID != tc.OrgID {
			continue
		}
		cp := *s
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeviceID.String() < out[j].DeviceID.String() })
	return out, nil
}

func (f *WhatsAppSessions) Delete(ctx context.Context, deviceID uuid.UUID) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.rows[deviceID]
	if !ok || s.OrgID != tc.OrgID {
		return &whatsapp.SessionNotFoundError{ID: deviceID.String()}
	}
	delete(f.rows, deviceID)
	return nil
}
