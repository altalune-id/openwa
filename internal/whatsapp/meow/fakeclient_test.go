package meow

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// NOTE: handlersMu mirrors whatsmeow's eventHandlersLock: emit holds the read lock while a handler runs, RemoveEventHandler needs the write lock.
type fakeClient struct {
	handlersMu sync.RWMutex
	handlers   map[uint32]whatsmeow.EventHandler
	next       uint32

	mu          sync.Mutex
	connected   bool
	loggedIn    bool
	presence    int
	disconnects int
	logoutErr   error
	deleted     bool
	pairCalls   []string
	onConnect   func()
	qr          chan whatsmeow.QRChannelItem
	pn          map[string]string
	qrCtx       context.Context
}

func newFakeClient() *fakeClient {
	return &fakeClient{handlers: map[uint32]whatsmeow.EventHandler{}, qr: make(chan whatsmeow.QRChannelItem, 8), pn: map[string]string{}}
}

func (f *fakeClient) AddEventHandler(h whatsmeow.EventHandler) uint32 {
	f.handlersMu.Lock()
	defer f.handlersMu.Unlock()
	f.next++
	f.handlers[f.next] = h
	return f.next
}

func (f *fakeClient) RemoveEventHandler(id uint32) bool {
	f.handlersMu.Lock()
	defer f.handlersMu.Unlock()
	delete(f.handlers, id)
	return true
}

func (f *fakeClient) emit(evt any) {
	f.handlersMu.RLock()
	defer f.handlersMu.RUnlock()
	for _, h := range f.handlers {
		h(evt)
	}
}

func (f *fakeClient) handlerCount() int {
	f.handlersMu.RLock()
	defer f.handlersMu.RUnlock()
	return len(f.handlers)
}

func (f *fakeClient) Connect() error {
	f.mu.Lock()
	hook := f.onConnect
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = true
	return nil
}

func (f *fakeClient) Disconnect() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = false
	f.disconnects++
}

func (f *fakeClient) IsConnected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}

func (f *fakeClient) IsLoggedIn() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loggedIn
}

func (f *fakeClient) GetQRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	f.mu.Lock()
	f.qrCtx = ctx
	f.mu.Unlock()
	return f.qr, nil
}

func (f *fakeClient) PairPhone(_ context.Context, phone string, _ bool, _ whatsmeow.PairClientType, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pairCalls = append(f.pairCalls, phone)
	return "WXYZ-1234", nil
}

func (f *fakeClient) Logout(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logoutErr
}

func (f *fakeClient) SendPresence(context.Context, types.Presence) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.presence++
	return nil
}

func (f *fakeClient) ParseWebMessage(chat types.JID, _ *waWeb.WebMessageInfo) (*events.Message, error) {
	return &events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: chat, Sender: chat}, ID: "HIST1"}}, nil
}

func (f *fakeClient) DeleteStore(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = true
	return nil
}

func (f *fakeClient) PNForLID(_ context.Context, lid string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pn[lid]
}

var errLogout = errors.New("logout iq timed out")

func qrCode(code string) whatsmeow.QRChannelItem {
	return whatsmeow.QRChannelItem{Event: whatsmeow.QRChannelEventCode, Code: code, Timeout: 60 * time.Second}
}

func qrSuccess() whatsmeow.QRChannelItem { return whatsmeow.QRChannelSuccess }
