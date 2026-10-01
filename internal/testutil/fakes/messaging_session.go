package fakes

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"altalune.id/openwa/internal/whatsapp"
)

// MessagingSession is an engine session that implements both EngineSession and MessagingSession and records every call; the exported fields are read under mu, so a test may change them while the session runs.
type MessagingSession struct {
	connected atomic.Bool
	closed    atomic.Bool

	mu        sync.Mutex
	Calls     []string
	SendTimes []time.Time
	SendFn    func(ctx context.Context, out whatsapp.OutboundMessage) (whatsapp.SendResult, error)
	Groups    []whatsapp.GroupInfo
	JoinJID   string
	Reads     [][]string
	OnWA      map[string]string
	Media     []byte
	// CloseGate, when set, makes Close wait for it or for its context.
	CloseGate chan struct{}
	// UnlinkGate, when set, makes Unlink wait for it or for its context.
	UnlinkGate chan struct{}
	// UnlinkErr, when set, is what Unlink returns once the gate opens.
	UnlinkErr error
}

var (
	_ whatsapp.EngineSession    = (*MessagingSession)(nil)
	_ whatsapp.MessagingSession = (*MessagingSession)(nil)
)

// NewMessagingSession returns a connected session.
func NewMessagingSession() *MessagingSession {
	s := &MessagingSession{}
	s.connected.Store(true)
	return s
}

// SetConnected flips Connected().
func (s *MessagingSession) SetConnected(v bool) { s.connected.Store(v) }

// Closed reports whether Close ran.
func (s *MessagingSession) Closed() bool { return s.closed.Load() }

func (s *MessagingSession) record(call string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls = append(s.Calls, call)
}

// Recorded returns a copy of the call log.
func (s *MessagingSession) Recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.Calls...)
}

func (s *MessagingSession) Link(context.Context) (<-chan whatsapp.LinkEvent, error) {
	return nil, &whatsapp.UnsupportedError{Feature: "link"}
}

func (s *MessagingSession) LinkWithPhone(context.Context, string) (string, error) {
	return "", &whatsapp.UnsupportedError{Feature: "link"}
}

func (s *MessagingSession) Unlink(ctx context.Context) error {
	s.mu.Lock()
	gate, err := s.UnlinkGate, s.UnlinkErr
	s.mu.Unlock()
	s.record("unlink:start")
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.record("unlink:done")
	return err
}

func (s *MessagingSession) Close(ctx context.Context) error {
	s.mu.Lock()
	gate := s.CloseGate
	s.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
		}
	}
	s.closed.Store(true)
	s.connected.Store(false)
	s.record("close")
	return nil
}

func (s *MessagingSession) Connected() bool { return s.connected.Load() }

func (s *MessagingSession) Send(ctx context.Context, out whatsapp.OutboundMessage) (whatsapp.SendResult, error) {
	s.record("send:" + out.WAID)
	s.mu.Lock()
	s.SendTimes = append(s.SendTimes, time.Now())
	fn := s.SendFn
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, out)
	}
	return whatsapp.SendResult{At: time.Now(), ChatJID: out.To}, nil
}

func (s *MessagingSession) MarkRead(_ context.Context, _, _ string, ids []string, _ bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Reads = append(s.Reads, append([]string(nil), ids...))
	return nil
}

func (s *MessagingSession) SendTyping(_ context.Context, _ string, on bool) error {
	if on {
		s.record("typing:on")
	} else {
		s.record("typing:off")
	}
	return nil
}

func (s *MessagingSession) FetchMedia(context.Context, whatsapp.MediaKeys, string) (*os.File, error) {
	s.mu.Lock()
	body := append([]byte(nil), s.Media...)
	s.mu.Unlock()
	f, err := os.CreateTemp("", "fake-wa-media-*")
	if err != nil {
		return nil, err
	}
	_ = os.Remove(f.Name())
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return nil, err
	}
	_, err = f.Seek(0, 0)
	return f, err
}

func (s *MessagingSession) IsOnWhatsApp(context.Context, []string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.OnWA, nil
}

func (s *MessagingSession) GroupList(context.Context) ([]whatsapp.GroupInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]whatsapp.GroupInfo(nil), s.Groups...), nil
}

func (s *MessagingSession) GroupInfo(_ context.Context, jid string) (whatsapp.GroupInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.Groups {
		if g.JID == jid {
			return g, nil
		}
	}
	return whatsapp.GroupInfo{}, &whatsapp.SessionNotFoundError{ID: jid}
}

func (s *MessagingSession) GroupJoin(context.Context, string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.JoinJID, nil
}

func (s *MessagingSession) GroupLeave(context.Context, string) error {
	s.record("leave")
	return nil
}
