package fakes

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/whatsapp"
)

// Engine is a scripted whatsapp.Engine; every Open records a *EngineSession the test drives.
type Engine struct {
	mu         sync.Mutex
	openHook   func(ctx context.Context, ref whatsapp.SessionRef) error
	linkScript []whatsapp.LinkEvent
	phoneGate  chan struct{}
	purgeErr   error
	calls      []whatsapp.SessionRef
	opened     []*EngineSession
	// OpenFn, when set, replaces the default scripted session for Open.
	OpenFn func(ctx context.Context, ref whatsapp.SessionRef, sink whatsapp.EventSink) (whatsapp.EngineSession, error)
	purged []whatsapp.SessionRef
}

// NewEngine returns an Engine that opens every session successfully.
func NewEngine() *Engine { return &Engine{} }

var _ whatsapp.Engine = (*Engine)(nil)

// SetOpenHook runs fn at the start of every Open; a non-nil error fails that Open.
func (e *Engine) SetOpenHook(fn func(ctx context.Context, ref whatsapp.SessionRef) error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.openHook = fn
}

// SetLinkScript queues events on the link channel of every unlinked session opened after the call.
func (e *Engine) SetLinkScript(events ...whatsapp.LinkEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.linkScript = events
}

// SetPhoneGate makes LinkWithPhone on sessions opened after the call wait for gate to close.
func (e *Engine) SetPhoneGate(gate chan struct{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.phoneGate = gate
}

// SetPurgeErr makes every Purge fail with err.
func (e *Engine) SetPurgeErr(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.purgeErr = err
}

func (e *Engine) Name() string { return "fake" }

func (e *Engine) Capabilities() whatsapp.Capabilities {
	return whatsapp.Capabilities{QRLink: true, PhoneCodeLink: true, Groups: true, HistorySync: true, Reactions: true, Edits: true}
}

func (e *Engine) Open(ctx context.Context, ref whatsapp.SessionRef, sink whatsapp.EventSink) (whatsapp.EngineSession, error) {
	e.mu.Lock()
	e.calls = append(e.calls, ref)
	hook, script, gate := e.openHook, e.linkScript, e.phoneGate
	e.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, ref); err != nil {
			return nil, err
		}
	}
	e.mu.Lock()
	openFn := e.OpenFn
	e.mu.Unlock()
	if openFn != nil {
		return openFn(ctx, ref, sink)
	}
	s := &EngineSession{Ref: ref, Sink: sink, LinkCh: make(chan whatsapp.LinkEvent, 16), PhoneCode: "ABCD-EFGH", phoneGate: gate}
	if ref.JID == "" {
		for _, ev := range script {
			s.LinkCh <- ev
		}
	}
	s.connected.Store(ref.JID != "")
	e.mu.Lock()
	e.opened = append(e.opened, s)
	e.mu.Unlock()
	return s, nil
}

func (e *Engine) Purge(_ context.Context, ref whatsapp.SessionRef) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.purged = append(e.purged, ref)
	return e.purgeErr
}

// OpenCalls counts Open calls for id, failed ones included.
func (e *Engine) OpenCalls(id uuid.UUID) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, r := range e.calls {
		if r.DeviceID == id {
			n++
		}
	}
	return n
}

// Opened counts successful opens for id.
func (e *Engine) Opened(id uuid.UUID) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, s := range e.opened {
		if s.Ref.DeviceID == id {
			n++
		}
	}
	return n
}

// Last returns the most recent session opened for id.
func (e *Engine) Last(id uuid.UUID) *EngineSession {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := len(e.opened) - 1; i >= 0; i-- {
		if e.opened[i].Ref.DeviceID == id {
			return e.opened[i]
		}
	}
	return nil
}

// OpenSessions counts sessions opened for id that have not been closed.
func (e *Engine) OpenSessions(id uuid.UUID) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, s := range e.opened {
		if s.Ref.DeviceID == id && !s.Closed() {
			n++
		}
	}
	return n
}

// Purged returns every ref Purge was called with.
func (e *Engine) Purged() []whatsapp.SessionRef {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]whatsapp.SessionRef{}, e.purged...)
}

// EngineSession is one fake open session; the test plays the engine through Sink and LinkCh.
type EngineSession struct {
	Ref       whatsapp.SessionRef
	Sink      whatsapp.EventSink
	LinkCh    chan whatsapp.LinkEvent
	PhoneCode string

	mu         sync.Mutex
	phoneGate  chan struct{}
	unlinkErr  error
	onUnlink   func()
	onClose    func()
	closes     int
	unlinks    int
	phoneCalls int
	connected  atomic.Bool
	closed     atomic.Bool
}

var _ whatsapp.EngineSession = (*EngineSession)(nil)

// SetUnlinkErr makes Unlink fail with err.
func (s *EngineSession) SetUnlinkErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unlinkErr = err
}

// SetOnClose runs fn inside Close, before it returns, as the engine's own events would.
func (s *EngineSession) SetOnClose(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onClose = fn
}

// SetOnUnlink runs fn inside Unlink, before it returns, as the engine's own events would.
func (s *EngineSession) SetOnUnlink(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onUnlink = fn
}

func (s *EngineSession) Link(context.Context) (<-chan whatsapp.LinkEvent, error) {
	return s.LinkCh, nil
}

func (s *EngineSession) LinkWithPhone(ctx context.Context, _ string) (string, error) {
	s.mu.Lock()
	s.phoneCalls++
	gate := s.phoneGate
	s.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return s.PhoneCode, nil
}

func (s *EngineSession) Unlink(context.Context) error {
	s.mu.Lock()
	s.unlinks++
	hook, err := s.onUnlink, s.unlinkErr
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	return err
}

func (s *EngineSession) Close(context.Context) error {
	s.mu.Lock()
	s.closes++
	hook := s.onClose
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	s.closed.Store(true)
	return nil
}

func (s *EngineSession) Connected() bool { return s.connected.Load() && !s.closed.Load() }

// SetConnected flips what Connected reports.
func (s *EngineSession) SetConnected(on bool) { s.connected.Store(on) }

// Closed reports whether Close ran.
func (s *EngineSession) Closed() bool { return s.closed.Load() }

// Unlinks counts Unlink calls.
func (s *EngineSession) Unlinks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unlinks
}

// PhoneCalls counts LinkWithPhone calls.
func (s *EngineSession) PhoneCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phoneCalls
}
