package fakes

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/platform/keyset"
	"altalune.id/openwa/internal/platform/publicid"
	"altalune.id/openwa/internal/platform/tenant"
)

// Message is an in-memory message.Store mirroring the Postgres predicates, version contract, dedup and claim rules.
type Message struct {
	mu        sync.Mutex
	rows      map[uuid.UUID]*message.Message
	retention map[uuid.UUID]*message.RetentionPolicy

	InsertInboundFn func(ctx context.Context, m *message.Message) (bool, error)
	SaveFn          func(ctx context.Context, m *message.Message, ifVersion int) error
}

var _ message.Store = (*Message)(nil)

// NewMessage returns an empty store.
func NewMessage() *Message {
	return &Message{rows: map[uuid.UUID]*message.Message{}, retention: map[uuid.UUID]*message.RetentionPolicy{}}
}

func cloneMessage(m *message.Message) *message.Message {
	cp := *m
	cp.Mentions = slices.Clone(m.Mentions)
	cp.Raw = slices.Clone(m.Raw)
	if m.Media != nil {
		md := *m.Media
		cp.Media = &md
	}
	if m.Location != nil {
		l := *m.Location
		cp.Location = &l
	}
	return &cp
}

// NOTE: mirrors the Postgres projection, which never reads the queued bytes.
func withoutRaw(m *message.Message) *message.Message {
	cp := cloneMessage(m)
	cp.Raw = nil
	return cp
}

// Seed stores m without any check.
func (f *Message) Seed(m *message.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[m.ID] = cloneMessage(m)
}

// All returns every stored row.
func (f *Message) All() []*message.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*message.Message, 0, len(f.rows))
	for _, m := range f.rows {
		out = append(out, cloneMessage(m))
	}
	return out
}

func (f *Message) Save(ctx context.Context, m *message.Message, ifVersion int) error {
	if f.SaveFn != nil {
		return f.SaveFn(ctx, m, ifVersion)
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, other := range f.rows {
		if id != m.ID && other.PublicID == m.PublicID {
			return &message.PublicIDTakenError{PublicID: m.PublicID}
		}
	}
	cp := cloneMessage(m)
	if cur, ok := f.rows[m.ID]; ok {
		if cur.OrgID != tc.OrgID {
			return &message.NotFoundError{ID: m.PublicID}
		}
		if ifVersion != 0 && cur.Version != ifVersion {
			return &message.VersionMismatchError{Want: ifVersion, Got: cur.Version}
		}
		cp.Version = cur.Version + 1
		// NOTE: like the Postgres upsert, an update only ever clears the queued bytes.
		if !m.RawDropped() {
			cp.Raw = slices.Clone(cur.Raw)
		}
	}
	f.rows[m.ID] = cp
	return nil
}

func (f *Message) InsertInbound(ctx context.Context, m *message.Message) (bool, error) {
	if f.InsertInboundFn != nil {
		return f.InsertInboundFn(ctx, m)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, cur := range f.rows {
		if m.WAMessageID != "" && cur.DeviceID == m.DeviceID && cur.WAMessageID == m.WAMessageID {
			return false, nil
		}
	}
	f.rows[m.ID] = cloneMessage(m)
	return true, nil
}

func (f *Message) ByID(ctx context.Context, id uuid.UUID) (*message.Message, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.rows[id]
	if !ok || m.OrgID != tc.OrgID {
		return nil, &message.NotFoundError{ID: id.String()}
	}
	return withoutRaw(m), nil
}

func (f *Message) ByWAID(ctx context.Context, deviceID uuid.UUID, waID string) (*message.Message, error) {
	return f.byWAID(ctx, deviceID, waID, withoutRaw)
}

// MessagePublicID mints a fresh msg_ public id for a test fixture.
func MessagePublicID() string {
	id, err := publicid.New(message.PublicIDPrefix)
	if err != nil {
		panic(err)
	}
	return id
}

func (f *Message) ByPublicID(ctx context.Context, publicID string) (*message.Message, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.rows {
		if m.PublicID == publicID && m.OrgID == tc.OrgID {
			return withoutRaw(m), nil
		}
	}
	return nil, &message.NotFoundError{ID: publicID}
}

func (f *Message) PublicIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[uuid.UUID]string, len(ids))
	for _, id := range ids {
		if m, ok := f.rows[id]; ok && m.OrgID == tc.OrgID {
			out[id] = m.PublicID
		}
	}
	return out, nil
}

func (f *Message) QuotedByWAID(ctx context.Context, deviceID uuid.UUID, waID string) (*message.Message, error) {
	return f.byWAID(ctx, deviceID, waID, cloneMessage)
}

func (f *Message) byWAID(ctx context.Context, deviceID uuid.UUID, waID string, copyOf func(*message.Message) *message.Message) (*message.Message, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.rows {
		if waID != "" && m.OrgID == tc.OrgID && m.DeviceID == deviceID && m.WAMessageID == waID {
			return copyOf(m), nil
		}
	}
	return nil, &message.NotFoundError{ID: waID}
}

func (f *Message) MarkChatRead(ctx context.Context, chatID, upTo uuid.UUID, at time.Time) (int64, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, m := range f.rows {
		if m.OrgID == tc.OrgID && m.ChatID == chatID && m.Direction == message.DirectionIn && m.ReadAt == nil && m.ID.String() <= upTo.String() {
			ts := at.UTC()
			m.ReadAt, m.UpdatedAt = &ts, ts
			m.Version++
			n++
		}
	}
	return n, nil
}

func (f *Message) UnsentBefore(ctx context.Context, projectID uuid.UUID, before time.Time, limit int) ([]*message.Message, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*message.Message, 0)
	for _, m := range f.rows {
		stuck := (m.Status == message.StatusQueued && m.CreatedAt.Before(before)) ||
			(m.Status == message.StatusSending && m.UpdatedAt.Before(before))
		if m.OrgID == tc.OrgID && m.ProjectID == projectID && m.Direction == message.DirectionOut && stuck {
			out = append(out, withoutRaw(m))
		}
	}
	slices.SortFunc(out, func(a, b *message.Message) int { return a.CreatedAt.Compare(b.CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *Message) List(ctx context.Context, opts message.ListOpts) ([]*message.Message, string, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, "", err
	}
	opts = opts.WithDefaults()
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*message.Message, 0)
	for _, m := range f.rows {
		switch {
		case m.OrgID != tc.OrgID || m.ProjectID != tc.ProjectID,
			opts.ChatID != nil && m.ChatID != *opts.ChatID,
			opts.DeviceID != nil && m.DeviceID != *opts.DeviceID,
			opts.Direction != nil && m.Direction != *opts.Direction,
			opts.Status != nil && m.Status != *opts.Status,
			opts.Unread && (m.Direction != message.DirectionIn || m.ReadAt != nil),
			opts.UpdatedAfter != nil && !m.UpdatedAt.After(*opts.UpdatedAfter),
			opts.SinceID != nil && m.ID.String() <= opts.SinceID.String():
			continue
		}
		out = append(out, withoutRaw(m))
	}
	if opts.SinceID != nil {
		slices.SortFunc(out, func(a, b *message.Message) int { return cmp.Compare(a.ID.String(), b.ID.String()) })
		if len(out) > opts.Limit {
			out = out[:opts.Limit]
		}
		return out, "", nil
	}
	slices.SortFunc(out, func(a, b *message.Message) int {
		if d := b.WATimestamp.Compare(a.WATimestamp); d != 0 {
			return d
		}
		return cmp.Compare(b.ID.String(), a.ID.String())
	})
	if opts.Cursor != "" {
		ts, id, decErr := keyset.Decode(keyset.Cursor(opts.Cursor))
		if decErr != nil {
			return nil, "", decErr
		}
		out = slices.DeleteFunc(out, func(m *message.Message) bool {
			return !m.WATimestamp.Before(ts) && (!m.WATimestamp.Equal(ts) || m.ID.String() >= id.String())
		})
	}
	next := ""
	if len(out) > opts.Limit {
		out = out[:opts.Limit]
		last := out[len(out)-1]
		next = string(keyset.Encode(last.WATimestamp, last.ID))
	}
	return out, next, nil
}

//nolint:nilnil // an empty queue is not an error.
func (f *Message) ClaimNext(ctx context.Context, deviceID uuid.UUID, staleAfter time.Duration) (*message.Message, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	stale := time.Now().UTC().Add(-staleAfter)
	var pick *message.Message
	for _, m := range f.rows {
		if m.OrgID != tc.OrgID || m.DeviceID != deviceID || m.Direction != message.DirectionOut {
			continue
		}
		if m.Status != message.StatusQueued && (m.Status != message.StatusSending || !m.UpdatedAt.Before(stale)) {
			continue
		}
		if pick == nil || m.CreatedAt.Before(pick.CreatedAt) || (m.CreatedAt.Equal(pick.CreatedAt) && m.ID.String() < pick.ID.String()) {
			pick = m
		}
	}
	if pick == nil {
		return nil, nil
	}
	if pick.Status == message.StatusSending && pick.Attempts >= message.MaxAttempts {
		pick.Expire("send attempts exhausted", time.Now())
	} else {
		pick.MarkSending(time.Now())
	}
	pick.Version++
	return cloneMessage(pick), nil
}

func (f *Message) DeleteOlderThan(ctx context.Context, projectID uuid.UUID, before time.Time, limit int) (int64, []uuid.UUID, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return 0, nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	chats := []uuid.UUID{}
	for id, m := range f.rows {
		if int(n) == limit {
			break
		}
		if m.OrgID != tc.OrgID || m.ProjectID != projectID || !m.WATimestamp.Before(before) || m.Status == message.StatusQueued || m.Status == message.StatusSending {
			continue
		}
		delete(f.rows, id)
		n++
		if !slices.Contains(chats, m.ChatID) {
			chats = append(chats, m.ChatID)
		}
	}
	return n, chats, nil
}

func (f *Message) CountOlderThan(ctx context.Context, projectID uuid.UUID, before time.Time) (int64, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, m := range f.rows {
		if m.OrgID == tc.OrgID && m.ProjectID == projectID && m.WATimestamp.Before(before) && m.Status != message.StatusQueued && m.Status != message.StatusSending {
			n++
		}
	}
	return n, nil
}

func (f *Message) CountToday(ctx context.Context, since time.Time) (int64, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, m := range f.rows {
		if m.OrgID == tc.OrgID && m.ProjectID == tc.ProjectID && !m.WATimestamp.Before(since) {
			n++
		}
	}
	return n, nil
}

func (f *Message) CountUnreadInbound(ctx context.Context, chatID uuid.UUID) (int, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.rows {
		if m.OrgID == tc.OrgID && m.ChatID == chatID && m.Direction == message.DirectionIn && m.ReadAt == nil {
			n++
		}
	}
	return n, nil
}

//nolint:nilnil // an absent row means the configured default.
func (f *Message) RetentionByProject(ctx context.Context, projectID uuid.UUID) (*message.RetentionPolicy, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.retention[projectID]
	if !ok || p.OrgID != tc.OrgID {
		return nil, nil
	}
	cp := *p
	return &cp, nil
}

func (f *Message) SaveRetention(ctx context.Context, p *message.RetentionPolicy) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if cur, ok := f.retention[p.ProjectID]; ok && cur.OrgID != tc.OrgID {
		return &message.NotFoundError{ID: "retention:" + p.ProjectID.String()}
	}
	cp := *p
	cp.OrgID = tc.OrgID
	f.retention[p.ProjectID] = &cp
	return nil
}
