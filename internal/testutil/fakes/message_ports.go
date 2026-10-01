package fakes

import (
	"context"
	"io"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/message"
)

// MessageDevices is a scripted message.Devices.
type MessageDevices struct {
	mu      sync.Mutex
	Refs    map[uuid.UUID]message.DeviceRef
	Matched bool
	Reasons []string
	Inputs  []message.MatchInput
	Err     error
}

var _ message.Devices = (*MessageDevices)(nil)

func (f *MessageDevices) Ref(_ context.Context, id uuid.UUID) (message.DeviceRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return message.DeviceRef{}, f.Err
	}
	r, ok := f.Refs[id]
	if !ok {
		return message.DeviceRef{}, &message.NotFoundError{ID: "device"}
	}
	return r, nil
}

func (f *MessageDevices) Match(_ context.Context, _ uuid.UUID, in message.MatchInput) (matched bool, reasons []string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Inputs = append(f.Inputs, in)
	return f.Matched, slices.Clone(f.Reasons), f.Err
}

// ChatTouch is one recorded MessageChats.Touch.
type ChatTouch struct {
	ID      uuid.UUID
	At      time.Time
	Preview string
	Inbound bool
}

// ChatRepair is one recorded MessageChats.Repair.
type ChatRepair struct {
	ID      uuid.UUID
	Last    *time.Time
	Preview string
	Unread  int
}

// MessageChats is an in-memory message.Chats keyed by device and JID or LID.
type MessageChats struct {
	mu      sync.Mutex
	rows    map[uuid.UUID]message.ChatRef
	Touches []ChatTouch
	Reads   []uuid.UUID
	Repairs []ChatRepair
	Ensured []message.ChatRef
}

var _ message.Chats = (*MessageChats)(nil)

// NewMessageChats returns an empty chat port.
func NewMessageChats() *MessageChats { return &MessageChats{rows: map[uuid.UUID]message.ChatRef{}} }

// Seed stores c.
func (f *MessageChats) Seed(c message.ChatRef) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[c.ID] = c
}

func (f *MessageChats) EnsureForJID(_ context.Context, deviceID uuid.UUID, jid, lid, kind, name string) (message.ChatRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, c := range f.rows {
		if c.DeviceID == deviceID && (c.JID == jid || (lid != "" && c.LID == lid)) {
			if c.LID == "" {
				c.LID = lid
			}
			f.rows[id] = c
			f.Ensured = append(f.Ensured, c)
			return c, nil
		}
	}
	c := message.ChatRef{ID: uuid.Must(uuid.NewV7()), PublicID: ChatPublicID(), DeviceID: deviceID, JID: jid, LID: lid, Kind: kind, Name: name}
	f.rows[c.ID] = c
	f.Ensured = append(f.Ensured, c)
	return c, nil
}

func (f *MessageChats) Get(_ context.Context, id uuid.UUID) (message.ChatRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.rows[id]
	if !ok {
		return message.ChatRef{}, &message.NotFoundError{ID: "chat:" + id.String()}
	}
	return c, nil
}

func (f *MessageChats) Touch(_ context.Context, id uuid.UUID, at time.Time, preview string, inbound bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Touches = append(f.Touches, ChatTouch{ID: id, At: at, Preview: preview, Inbound: inbound})
	return nil
}

func (f *MessageChats) MarkRead(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Reads = append(f.Reads, id)
	return nil
}

func (f *MessageChats) Repair(_ context.Context, id uuid.UUID, last *time.Time, preview string, unread int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Repairs = append(f.Repairs, ChatRepair{ID: id, Last: last, Preview: preview, Unread: unread})
	return nil
}

// MessageContacts records every UpsertFromMessage.
type MessageContacts struct {
	mu    sync.Mutex
	Calls []message.SenderInput
}

var _ message.Contacts = (*MessageContacts)(nil)

func (f *MessageContacts) UpsertFromMessage(_ context.Context, _ uuid.UUID, in message.SenderInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, in)
	return nil
}

// MessageTenants resolves fixed slugs and a fixed project list.
type MessageTenants struct {
	Org, Project string
	Projects     []uuid.UUID
}

var _ message.Tenants = (*MessageTenants)(nil)

func (f *MessageTenants) Slugs(context.Context, uuid.UUID, uuid.UUID) (org, project string, err error) {
	return f.Org, f.Project, nil
}

func (f *MessageTenants) ProjectIDs(context.Context) ([]uuid.UUID, error) {
	return slices.Clone(f.Projects), nil
}

// ReadCall is one recorded Transport.MarkRead.
type ReadCall struct {
	DeviceID uuid.UUID
	Chat     string
	Sender   string
	WAIDs    []string
	Played   bool
}

// Transport is a scripted message.Transport; set the exported fields before the first call.
type Transport struct {
	mu        sync.Mutex
	Reads     []ReadCall
	Fetches   int
	FetchBody []byte
	FetchFn   func(ctx context.Context, deviceID uuid.UUID, keys message.MediaKeys, kind string) (*os.File, error)
	OnWA      map[string]string
	Err       error
}

var _ message.Transport = (*Transport)(nil)

func (f *Transport) MarkRead(_ context.Context, deviceID uuid.UUID, chat, sender string, ids []string, played bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Reads = append(f.Reads, ReadCall{DeviceID: deviceID, Chat: chat, Sender: sender, WAIDs: slices.Clone(ids), Played: played})
	return f.Err
}

func (f *Transport) FetchMedia(ctx context.Context, deviceID uuid.UUID, keys message.MediaKeys, kind string) (*os.File, error) {
	f.mu.Lock()
	f.Fetches++
	fn, body, fail := f.FetchFn, f.FetchBody, f.Err
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, deviceID, keys, kind)
	}
	if fail != nil {
		return nil, fail
	}
	file, err := os.CreateTemp("", "fake-media-*")
	if err != nil {
		return nil, err
	}
	_ = os.Remove(file.Name())
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return nil, err
	}
	_, err = file.Seek(0, io.SeekStart)
	return file, err
}

func (f *Transport) IsOnWhatsApp(context.Context, uuid.UUID, []string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.OnWA, f.Err
}

// MediaFetcher returns Data and Mime for any URL, or Err.
type MediaFetcher struct {
	Data []byte
	Mime string
	Err  error
	URLs []string
}

var _ message.MediaFetcher = (*MediaFetcher)(nil)

func (f *MediaFetcher) Fetch(_ context.Context, url string) (data []byte, mime string, err error) {
	f.URLs = append(f.URLs, url)
	return f.Data, f.Mime, f.Err
}

// MediaStore opens files through a Transport, like message.WAMedia without the semaphore.
type MediaStore struct{ Transport *Transport }

var _ message.MediaStore = MediaStore{}

func (f MediaStore) Open(ctx context.Context, m *message.Message) (*os.File, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	file, err := f.Transport.FetchMedia(ctx, m.DeviceID, m.Media.Keys, string(m.Type))
	return file, m.Media.Mime, err
}

func (MediaStore) Put(context.Context, *message.Message, io.Reader) (string, error) { return "", nil }

// Waker records every Wake.
type Waker struct {
	mu    sync.Mutex
	Woken []uuid.UUID
}

var _ message.Waker = (*Waker)(nil)

func (f *Waker) Wake(id uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Woken = append(f.Woken, id)
}
