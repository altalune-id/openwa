package meow

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"altalune.id/openwa/internal/whatsapp"
)

const (
	pairDisplayName = "Chrome (Linux)"
	warnEvery       = time.Minute
	dispatchBudget  = 10 * time.Second
)

type waClient interface {
	AddEventHandler(handler whatsmeow.EventHandler) uint32
	RemoveEventHandler(id uint32) bool
	Connect() error
	Disconnect()
	IsConnected() bool
	IsLoggedIn() bool
	GetQRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error)
	PairPhone(ctx context.Context, phone string, showPushNotification bool, clientType whatsmeow.PairClientType, clientDisplayName string) (string, error)
	Logout(ctx context.Context) error
	SendPresence(ctx context.Context, state types.Presence) error
	ParseWebMessage(chat types.JID, msg *waWeb.WebMessageInfo) (*events.Message, error)
	DeleteStore(ctx context.Context) error
	PNForLID(ctx context.Context, lid string) string
}

type clientAdapter struct{ *whatsmeow.Client }

func (c clientAdapter) DeleteStore(ctx context.Context) error { return c.Store.Delete(ctx) }

func (c clientAdapter) PNForLID(ctx context.Context, lid string) string {
	j, err := types.ParseJID(lid)
	if err != nil {
		return ""
	}
	pn, err := c.Store.LIDs.GetPNForLID(ctx, j)
	if err != nil || pn.IsEmpty() {
		return ""
	}
	return pn.String()
}

type session struct {
	deviceID  uuid.UUID
	ref       whatsapp.SessionRef
	cli       waClient
	sink      whatsapp.EventSink
	log       *slog.Logger
	ch        chan any
	done      chan struct{}
	handlerID uint32
	qrSeen    chan struct{}
	qrOnce    sync.Once
	life      context.Context
	lifeStop  context.CancelFunc
	lastWarn  atomic.Int64

	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
	wg        sync.WaitGroup
}

var _ whatsapp.EngineSession = (*session)(nil)

func newSession(ref whatsapp.SessionRef, cli waClient, sink whatsapp.EventSink, queue int, log *slog.Logger) *session {
	life, lifeStop := context.WithCancel(context.Background())
	s := &session{
		life:     life,
		lifeStop: lifeStop,
		deviceID: ref.DeviceID,
		ref:      ref,
		cli:      cli,
		sink:     sink,
		log:      log,
		ch:       make(chan any, max(queue, 1)),
		done:     make(chan struct{}),
		qrSeen:   make(chan struct{}),
	}
	s.handlerID = cli.AddEventHandler(s.handle)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.loop()
	}()
	return s
}

// SECURITY: backpressure, never drop — a dropped message event is a message lost, because whatsmeow has already acked it.
func (s *session) handle(evt any) {
	select {
	case s.ch <- evt:
		return
	case <-s.done:
		return
	default:
	}
	s.warnFull()
	select {
	case s.ch <- evt:
	case <-s.done:
	}
}

func (s *session) warnFull() {
	now := time.Now().UnixNano()
	last := s.lastWarn.Load()
	if now-last < int64(warnEvery) && last != 0 {
		return
	}
	if s.lastWarn.CompareAndSwap(last, now) {
		s.log.Warn("whatsapp: session event queue is full; the whatsmeow handler is blocking", "device_id", s.deviceID)
	}
}

func (s *session) loop() {
	for {
		select {
		case <-s.done:
			return
		default:
		}
		select {
		case <-s.done:
			return
		case evt := <-s.ch:
			s.dispatch(evt)
		}
	}
}

func (s *session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *session) LinkWithPhone(ctx context.Context, phone string) (string, error) {
	if s.isClosed() {
		return "", &whatsapp.NotConnectedError{ID: s.deviceID.String()}
	}
	wctx, cancel := context.WithTimeout(ctx, firstQRWait)
	defer cancel()
	select {
	case <-s.qrSeen:
	case <-s.done:
		return "", &whatsapp.NotConnectedError{ID: s.deviceID.String()}
	case <-wctx.Done():
		return "", translate(s.deviceID, "pair phone", wctx.Err())
	}
	code, err := s.cli.PairPhone(ctx, phone, true, whatsmeow.PairClientChrome, pairDisplayName)
	if err != nil {
		return "", translate(s.deviceID, "pair phone", err)
	}
	return code, nil
}

func (s *session) Unlink(ctx context.Context) error {
	if s.isClosed() {
		return &whatsapp.NotConnectedError{ID: s.deviceID.String()}
	}
	err := s.cli.Logout(ctx)
	if err == nil {
		return nil
	}
	if s.cli.IsConnected() {
		return translate(s.deviceID, "logout", err)
	}
	s.cli.Disconnect()
	if derr := s.cli.DeleteStore(ctx); derr != nil {
		return translate(s.deviceID, "delete store", derr)
	}
	return nil
}

func (s *session) Connected() bool {
	return !s.isClosed() && s.cli.IsConnected() && s.cli.IsLoggedIn()
}

// NOTE: done closes first so a handler parked on a full queue returns and releases whatsmeow's handler lock before the removal needs it. v1 drops events still queued at Close, though whatsmeow already acked them; inbound is unconsumed in v1, revisit when consumers land. Close waits for the session's goroutines only until ctx ends, so a stuck sink cannot block shutdown.
func (s *session) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.done)
		s.mu.Unlock()
		s.lifeStop()
		s.cli.Disconnect()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.cli.RemoveEventHandler(s.handlerID)
		}()
	})
	finished := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
