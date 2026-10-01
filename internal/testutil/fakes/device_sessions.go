package fakes

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/device"
)

// DeviceSessions is a scripted device.Sessions that records every call.
type DeviceSessions struct {
	mu          sync.Mutex
	Statuses    map[uuid.UUID]device.SessionStatus
	State       device.LinkState
	LinkErr     error
	UnlinkErr   error
	ForgetErr   error
	ForgetFn    func(ctx context.Context, id uuid.UUID) error
	StatusCalls int
	Linked      []uuid.UUID
	Phones      []string
	Unlinked    []uuid.UUID
	Forgot      []uuid.UUID
}

// NewDeviceSessions returns a DeviceSessions whose attempts answer a pending QR.
func NewDeviceSessions() *DeviceSessions {
	return &DeviceSessions{
		Statuses: map[uuid.UUID]device.SessionStatus{},
		State:    device.LinkState{ID: "lnk_FakeDeviceLink001", Method: device.LinkQR, Outcome: device.LinkPending, QR: "2@fake", PNG: []byte{0x89, 'P', 'N', 'G'}},
	}
}

var _ device.Sessions = (*DeviceSessions)(nil)

func (f *DeviceSessions) StatusByDevices(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]device.SessionStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.StatusCalls++
	out := make(map[uuid.UUID]device.SessionStatus, len(ids))
	for _, id := range ids {
		st, ok := f.Statuses[id]
		if !ok {
			st = device.SessionStatus{State: device.SessionUnlinked}
		}
		out[id] = st
	}
	return out, nil
}

func (f *DeviceSessions) Link(_ context.Context, id uuid.UUID) (device.LinkState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Linked = append(f.Linked, id)
	return f.State, f.LinkErr
}

func (f *DeviceSessions) LinkWithPhone(_ context.Context, id uuid.UUID, phone string) (device.LinkState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Linked = append(f.Linked, id)
	f.Phones = append(f.Phones, phone)
	st := f.State
	st.Method, st.PairingCode = device.LinkPhone, "ABCD-EFGH"
	return st, f.LinkErr
}

func (f *DeviceSessions) LinkState(context.Context, uuid.UUID) (device.LinkState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.State, nil
}

func (f *DeviceSessions) Unlink(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Unlinked = append(f.Unlinked, id)
	return f.UnlinkErr
}

func (f *DeviceSessions) Forget(ctx context.Context, id uuid.UUID) error {
	f.mu.Lock()
	f.Forgot = append(f.Forgot, id)
	fn, err := f.ForgetFn, f.ForgetErr
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, id)
	}
	return err
}
