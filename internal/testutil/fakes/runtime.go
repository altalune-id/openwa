package fakes

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/whatsapp"
)

// Runtime is a scripted whatsapp.SessionRuntime that records every call.
type Runtime struct {
	mu sync.Mutex

	LinkFn          func(ctx context.Context, ref whatsapp.SessionRef) (whatsapp.LinkState, error)
	LinkWithPhoneFn func(ctx context.Context, ref whatsapp.SessionRef, phone string) (whatsapp.LinkState, error)
	UnlinkErr       error
	ForgetErr       error

	States  map[uuid.UUID]whatsapp.LinkState
	LiveSet map[uuid.UUID]bool

	Links   []whatsapp.SessionRef
	Phones  []string
	Unlinks []uuid.UUID
	Forgets []whatsapp.SessionRef

	held map[uuid.UUID]heldSession
}

// NewRuntime returns a Runtime whose Link answers a pending QR attempt.
func NewRuntime() *Runtime {
	return &Runtime{States: map[uuid.UUID]whatsapp.LinkState{}, LiveSet: map[uuid.UUID]bool{}}
}

var _ whatsapp.SessionRuntime = (*Runtime)(nil)

func (f *Runtime) Link(ctx context.Context, ref whatsapp.SessionRef) (whatsapp.LinkState, error) {
	f.mu.Lock()
	f.Links = append(f.Links, ref)
	fn := f.LinkFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, ref)
	}
	st := whatsapp.LinkState{ID: "lnk_FakeQRAttempt00001", Method: whatsapp.LinkMethodQR, Outcome: whatsapp.OutcomePending, QR: "2@fake", PNG: []byte{0x89, 'P', 'N', 'G'}}
	f.mu.Lock()
	f.States[ref.DeviceID] = st
	f.mu.Unlock()
	return st, nil
}

func (f *Runtime) LinkWithPhone(ctx context.Context, ref whatsapp.SessionRef, phone string) (whatsapp.LinkState, error) {
	f.mu.Lock()
	f.Links = append(f.Links, ref)
	f.Phones = append(f.Phones, phone)
	fn := f.LinkWithPhoneFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, ref, phone)
	}
	st := whatsapp.LinkState{ID: "lnk_FakePhoneAttempt01", Method: whatsapp.LinkMethodPhone, Outcome: whatsapp.OutcomePending, PairingCode: "ABCD-EFGH"}
	f.mu.Lock()
	f.States[ref.DeviceID] = st
	f.mu.Unlock()
	return st, nil
}

func (f *Runtime) LinkState(deviceID uuid.UUID) whatsapp.LinkState {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.States[deviceID]
	if !ok {
		return whatsapp.LinkState{Outcome: whatsapp.OutcomeNone}
	}
	return st
}

func (f *Runtime) Unlink(_ context.Context, deviceID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Unlinks = append(f.Unlinks, deviceID)
	return f.UnlinkErr
}

func (f *Runtime) Forget(_ context.Context, ref whatsapp.SessionRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Forgets = append(f.Forgets, ref)
	return f.ForgetErr
}

func (f *Runtime) Live(deviceID uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.LiveSet[deviceID]
}

// Hold makes the fake report sess as held under ref.
func (f *Runtime) Hold(ref whatsapp.SessionRef, sess whatsapp.EngineSession) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held == nil {
		f.held = map[uuid.UUID]heldSession{}
	}
	f.held[ref.DeviceID] = heldSession{ref: ref, sess: sess}
}

func (f *Runtime) Held(deviceID uuid.UUID) (whatsapp.SessionRef, whatsapp.EngineSession, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.held[deviceID]
	return h.ref, h.sess, ok
}

type heldSession struct {
	ref  whatsapp.SessionRef
	sess whatsapp.EngineSession
}
