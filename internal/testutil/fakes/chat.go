package fakes

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/platform/keyset"
	"altalune.id/openwa/internal/platform/publicid"
	"altalune.id/openwa/internal/platform/tenant"
)

// Chat is an in-memory chat.Store that filters exactly as the Postgres store does: by org, and by project on List.
type Chat struct {
	mu   sync.Mutex
	rows map[uuid.UUID]*chat.Chat
	devs map[uuid.UUID]tenant.Context

	SaveFn   func(ctx context.Context, c *chat.Chat, ifVersion int) error
	InsertFn func(ctx context.Context, c *chat.Chat) (bool, error)
}

var _ chat.Store = (*Chat)(nil)

// NewChat returns an empty store.
func NewChat() *Chat {
	return &Chat{rows: map[uuid.UUID]*chat.Chat{}, devs: map[uuid.UUID]tenant.Context{}}
}

// ChatPublicID mints a fresh cht_ public id for a test fixture.
func ChatPublicID() string {
	id, err := publicid.New(chat.PublicIDPrefix)
	if err != nil {
		panic(err)
	}
	return id
}

func (f *Chat) PublicIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[uuid.UUID]string, len(ids))
	for _, id := range ids {
		if c, ok := f.rows[id]; ok && c.OrgID == tc.OrgID && c.ProjectID == tc.ProjectID {
			out[id] = c.PublicID
		}
	}
	return out, nil
}

func (f *Chat) ByPublicID(ctx context.Context, publicID string) (*chat.Chat, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.rows {
		if c.PublicID == publicID && c.OrgID == tc.OrgID {
			cp := *c
			return &cp, nil
		}
	}
	return nil, &chat.NotFoundError{ID: publicID}
}

// SeedDevice registers a device's scope; a write naming a registered device of another scope is refused, unregistered devices are accepted.
func (f *Chat) SeedDevice(id, orgID, projectID uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.devs[id] = tenant.Context{OrgID: orgID, ProjectID: projectID}
}

func (f *Chat) admit(tc tenant.Context, c *chat.Chat) error {
	if c.OrgID != tc.OrgID || c.ProjectID != tc.ProjectID {
		return &chat.NotFoundError{ID: c.ID.String()}
	}
	if d, ok := f.devs[c.DeviceID]; ok && (d.OrgID != tc.OrgID || d.ProjectID != tc.ProjectID) {
		return &chat.NotFoundError{ID: c.DeviceID.String()}
	}
	return nil
}

// Seed stores c without any check.
func (f *Chat) Seed(c *chat.Chat) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *c
	f.rows[c.ID] = &cp
}

func (f *Chat) Save(ctx context.Context, c *chat.Chat, ifVersion int) error {
	if f.SaveFn != nil {
		return f.SaveFn(ctx, c, ifVersion)
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.admit(tc, c); err != nil {
		return err
	}
	for _, other := range f.rows {
		if other.ID != c.ID && other.PublicID == c.PublicID {
			return &chat.PublicIDTakenError{PublicID: c.PublicID}
		}
		if other.ID != c.ID && other.DeviceID == c.DeviceID && (other.JID == c.JID || (c.LID != "" && other.LID == c.LID)) {
			return fmt.Errorf("fakes.Chat: duplicate key on (device_id, jid) or (device_id, lid) for %s", c.ID)
		}
	}
	cp := *c
	if cur, ok := f.rows[c.ID]; ok {
		if cur.OrgID != tc.OrgID {
			return &chat.NotFoundError{ID: c.ID.String()}
		}
		if ifVersion != 0 && cur.Version != ifVersion {
			return &chat.VersionMismatchError{Want: ifVersion, Got: cur.Version}
		}
		cp.Version = cur.Version + 1
	}
	f.rows[c.ID] = &cp
	return nil
}

func (f *Chat) Insert(ctx context.Context, c *chat.Chat) (bool, error) {
	if f.InsertFn != nil {
		return f.InsertFn(ctx, c)
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.admit(tc, c); err != nil {
		return false, err
	}
	for _, cur := range f.rows {
		if cur.ID == c.ID || cur.PublicID == c.PublicID || (cur.DeviceID == c.DeviceID && (cur.JID == c.JID || (c.LID != "" && cur.LID == c.LID))) {
			return false, nil
		}
	}
	cp := *c
	f.rows[c.ID] = &cp
	return true, nil
}

// Len reports how many chats are stored.
func (f *Chat) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

func (f *Chat) ByID(ctx context.Context, id uuid.UUID) (*chat.Chat, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.rows[id]
	if !ok || c.OrgID != tc.OrgID {
		return nil, &chat.NotFoundError{ID: id.String()}
	}
	cp := *c
	return &cp, nil
}

func (f *Chat) ByJID(ctx context.Context, deviceID uuid.UUID, jid string) (*chat.Chat, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.rows {
		if jid != "" && c.OrgID == tc.OrgID && c.DeviceID == deviceID && (c.JID == jid || c.LID == jid) {
			cp := *c
			return &cp, nil
		}
	}
	return nil, &chat.NotFoundError{ID: jid}
}

func (f *Chat) List(ctx context.Context, opts chat.ListOpts) ([]*chat.Chat, string, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, "", err
	}
	opts = opts.WithDefaults()
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*chat.Chat, 0, len(f.rows))
	for _, c := range f.rows {
		if c.OrgID != tc.OrgID || c.ProjectID != tc.ProjectID {
			continue
		}
		if opts.DeviceID != nil && c.DeviceID != *opts.DeviceID {
			continue
		}
		if len(opts.DeviceIDs) > 0 && !slices.Contains(opts.DeviceIDs, c.DeviceID) {
			continue
		}
		if opts.Kind != nil && c.Kind != *opts.Kind {
			continue
		}
		if opts.Search != "" && !strings.HasPrefix(strings.ToLower(c.Name), strings.ToLower(opts.Search)) {
			continue
		}
		cp := *c
		out = append(out, &cp)
	}
	slices.SortFunc(out, func(a, b *chat.Chat) int {
		if d := b.ActivityAt.Compare(a.ActivityAt); d != 0 {
			return d
		}
		return cmp.Compare(b.ID.String(), a.ID.String())
	})
	if opts.Cursor != "" {
		ts, id, decErr := keyset.Decode(keyset.Cursor(opts.Cursor))
		if decErr != nil {
			return nil, "", decErr
		}
		out = slices.DeleteFunc(out, func(c *chat.Chat) bool {
			return !c.ActivityAt.Before(ts) && (!c.ActivityAt.Equal(ts) || c.ID.String() >= id.String())
		})
	}
	next := ""
	if len(out) > opts.Limit {
		out = out[:opts.Limit]
		last := out[len(out)-1]
		next = string(keyset.Encode(last.ActivityAt, last.ID))
	}
	return out, next, nil
}

func (f *Chat) Delete(ctx context.Context, id uuid.UUID) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.rows[id]
	if !ok || c.OrgID != tc.OrgID {
		return &chat.NotFoundError{ID: id.String()}
	}
	delete(f.rows, id)
	return nil
}
