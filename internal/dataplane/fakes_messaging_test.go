package dataplane_test

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/dataplane"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/platform/keyset"
	"altalune.id/openwa/internal/testutil/fakes"
)

type fakeMessages struct {
	mu       sync.Mutex
	rows     map[uuid.UUID]dataplane.MessageRef
	sends    []dataplane.SendInput
	media    []byte
	gone     bool
	mediaErr error
	reads    []uuid.UUID
	resolves int

	sendErr   error
	gate      chan struct{}
	entered   chan struct{}
	active    int
	maxActive int
}

func newFakeMessages() *fakeMessages {
	return &fakeMessages{rows: map[uuid.UUID]dataplane.MessageRef{}}
}

func (f *fakeMessages) seed(m dataplane.MessageRef) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[m.ID] = m
}

func (f *fakeMessages) Send(_ context.Context, deviceID uuid.UUID, in dataplane.SendInput) (dataplane.MessageRef, error) {
	f.mu.Lock()
	f.active++
	f.maxActive = max(f.maxActive, f.active)
	gate, entered := f.gate, f.entered
	f.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
	}
	if gate != nil {
		<-gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active--
	if f.sendErr != nil {
		err := f.sendErr
		f.sendErr = nil
		return dataplane.MessageRef{}, err
	}
	f.sends = append(f.sends, in)
	m := dataplane.MessageRef{ID: uuid.Must(uuid.NewV7()), PublicID: fakes.MessagePublicID(), DeviceID: deviceID, ChatID: uuid.New(), ChatPublicID: fakes.ChatPublicID(), Direction: "out", Type: "text",
		Status: "queued", Body: in.Text, WAID: "3EB0X", Mentions: []string{}, Timestamp: time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)}
	if in.Media != nil {
		m.Type, m.Body = "image", in.Media.Caption
		m.Media = &dataplane.MediaRef{Mime: in.Media.Mime, Size: int64(len(in.Media.Bytes)), Filename: in.Media.Filename}
	}
	f.rows[m.ID] = m
	return m, nil
}

func (f *fakeMessages) Resolve(_ context.Context, publicID string) (dataplane.MessageRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolves++
	for _, m := range f.rows {
		if m.PublicID == publicID {
			return m, nil
		}
	}
	return dataplane.MessageRef{}, &message.NotFoundError{ID: publicID}
}

func (f *fakeMessages) get(id uuid.UUID) (dataplane.MessageRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.rows[id]
	if !ok {
		return dataplane.MessageRef{}, &message.NotFoundError{ID: id.String()}
	}
	return m, nil
}

func (f *fakeMessages) List(_ context.Context, opts dataplane.MessageListOpts) ([]dataplane.MessageRef, string, error) {
	if opts.Cursor == "garbage" {
		return nil, "", &keyset.InvalidCursorError{Cursor: opts.Cursor}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []dataplane.MessageRef{}
	for _, m := range f.rows {
		if (opts.DeviceID == nil || m.DeviceID == *opts.DeviceID) && (opts.ChatID == nil || m.ChatID == *opts.ChatID) {
			out = append(out, m)
		}
	}
	return out, "", nil
}

func (f *fakeMessages) child(id uuid.UUID, typ string) (dataplane.MessageRef, error) {
	target, err := f.get(id)
	if err != nil {
		return dataplane.MessageRef{}, err
	}
	m := dataplane.MessageRef{ID: uuid.Must(uuid.NewV7()), PublicID: fakes.MessagePublicID(), DeviceID: target.DeviceID, DevicePublicID: target.DevicePublicID, ChatID: target.ChatID, ChatPublicID: target.ChatPublicID, Direction: "out", Type: typ, Status: "queued", TargetWAID: target.WAID, Mentions: []string{}}
	f.seed(m)
	return m, nil
}

func (f *fakeMessages) React(_ context.Context, id uuid.UUID, _ string) (dataplane.MessageRef, error) {
	return f.child(id, "reaction")
}

func (f *fakeMessages) Revoke(_ context.Context, id uuid.UUID) (dataplane.MessageRef, error) {
	return f.child(id, "revoke")
}

func (f *fakeMessages) Edit(_ context.Context, id uuid.UUID, _ string) (dataplane.MessageRef, error) {
	return f.child(id, "edit")
}

func (f *fakeMessages) MarkRead(_ context.Context, chatID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, chatID)
	return nil
}

func (f *fakeMessages) OpenMedia(_ context.Context, id uuid.UUID) (*os.File, string, string, error) {
	f.mu.Lock()
	gone, body, row := f.gone, append([]byte(nil), f.media...), f.rows[id]
	f.mu.Unlock()
	if f.mediaErr != nil {
		return nil, "", "", f.mediaErr
	}
	if gone {
		return nil, "", "", &message.MediaUnavailableError{ID: row.PublicID}
	}
	mimeType := "image/jpeg"
	if row.Media != nil && row.Media.Mime != "" {
		mimeType = row.Media.Mime
	}
	file, err := os.CreateTemp("", "dp-media-*")
	if err != nil {
		return nil, "", "", err
	}
	_ = os.Remove(file.Name())
	_, _ = file.Write(body)
	_, _ = file.Seek(0, 0)
	return file, mimeType, row.PublicID + ".bin", nil
}

type fakeChats struct {
	rows map[uuid.UUID]dataplane.ChatRef
	last dataplane.ChatListOpts
}

func (f *fakeChats) List(_ context.Context, opts dataplane.ChatListOpts) ([]dataplane.ChatRef, string, error) {
	f.last = opts
	out := []dataplane.ChatRef{}
	for _, c := range f.rows {
		if len(opts.DeviceIDs) > 0 {
			keep := false
			for _, d := range opts.DeviceIDs {
				keep = keep || d == c.DeviceID
			}
			if !keep {
				continue
			}
		}
		out = append(out, c)
	}
	return out, "next-page", nil
}

func (f *fakeChats) Resolve(_ context.Context, publicID string) (dataplane.ChatRef, error) {
	for _, c := range f.rows {
		if c.PublicID == publicID {
			return c, nil
		}
	}
	return dataplane.ChatRef{}, &chat.NotFoundError{ID: publicID}
}

type fakeContacts struct{ last dataplane.ContactListOpts }

func (f *fakeContacts) List(_ context.Context, opts dataplane.ContactListOpts) ([]dataplane.ContactRef, string, error) {
	f.last = opts
	return []dataplane.ContactRef{{ID: uuid.New(), JID: "628111@s.whatsapp.net", Phone: "628111", Name: "Budi"}}, "", nil
}

// NOTE: mirrors device.Service.Resolve, which scopes by project: the sibling's row exists in the org, and this project never sees it.
type projectDevices struct {
	*fakeDevices
	sibling     dataplane.DeviceRef
	siblingHits int
}

func (p *projectDevices) Resolve(ctx context.Context, publicID string) (dataplane.DeviceRef, error) {
	if publicID == p.sibling.PublicID {
		p.siblingHits++
		return dataplane.DeviceRef{}, &device.NotFoundError{ID: publicID}
	}
	return p.fakeDevices.Resolve(ctx, publicID)
}

func (f *fakeChats) GroupInfo(_ context.Context, id uuid.UUID) (dataplane.GroupRef, error) {
	c, ok := f.rows[id]
	if !ok {
		return dataplane.GroupRef{}, &chat.NotFoundError{ID: id.String()}
	}
	if c.Kind != "group" {
		return dataplane.GroupRef{}, &chat.NotAGroupError{ID: c.PublicID}
	}
	return dataplane.GroupRef{JID: c.JID, Name: c.Name, Participants: 3}, nil
}

func (f *fakeChats) JoinGroup(_ context.Context, deviceID uuid.UUID, link string) (dataplane.ChatRef, error) {
	c := dataplane.ChatRef{ID: uuid.New(), PublicID: fakes.ChatPublicID(), DeviceID: deviceID, JID: "999@g.us", Kind: "group", Name: link}
	f.rows[c.ID] = c
	return c, nil
}

func (f *fakeChats) LeaveGroup(_ context.Context, id uuid.UUID) (dataplane.ChatRef, error) {
	c := f.rows[id]
	c.Archived = true
	f.rows[id] = c
	return c, nil
}
