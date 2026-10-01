package fakes

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/contact"
	"altalune.id/openwa/internal/platform/keyset"
	"altalune.id/openwa/internal/platform/tenant"
)

// Contact is an in-memory contact.Store mirroring the Postgres merge rule and org and project guards.
type Contact struct {
	mu   sync.Mutex
	rows map[uuid.UUID]*contact.Contact
}

var _ contact.Store = (*Contact)(nil)

// NewContact returns an empty store.
func NewContact() *Contact { return &Contact{rows: map[uuid.UUID]*contact.Contact{}} }

// Seed stores c without any check.
func (f *Contact) Seed(c *contact.Contact) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *c
	f.rows[c.ID] = &cp
}

// NOTE: the fake has no device table, so a device holding another scope's contact stands in for a foreign device.
func (f *Contact) Upsert(ctx context.Context, c *contact.Contact) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if c.OrgID != tc.OrgID || c.ProjectID != tc.ProjectID {
		return &contact.NotFoundError{ID: c.JID}
	}
	for _, cur := range f.rows {
		if cur.DeviceID != c.DeviceID {
			continue
		}
		if cur.OrgID != tc.OrgID || cur.ProjectID != tc.ProjectID {
			return &contact.NotFoundError{ID: c.JID}
		}
	}
	for _, cur := range f.rows {
		if cur.DeviceID != c.DeviceID || cur.JID != c.JID {
			continue
		}
		for dst, src := range map[*string]string{&cur.LID: c.LID, &cur.Phone: c.Phone, &cur.Name: c.Name, &cur.PushName: c.PushName, &cur.BusinessName: c.BusinessName} {
			if src != "" {
				*dst = src
			}
		}
		cur.UpdatedAt = c.UpdatedAt
		return nil
	}
	cp := *c
	f.rows[c.ID] = &cp
	return nil
}

func (f *Contact) ByJID(ctx context.Context, deviceID uuid.UUID, jid string) (*contact.Contact, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.rows {
		if c.OrgID == tc.OrgID && c.ProjectID == tc.ProjectID && c.DeviceID == deviceID && c.JID == jid {
			cp := *c
			return &cp, nil
		}
	}
	return nil, &contact.NotFoundError{ID: jid}
}

func (f *Contact) List(ctx context.Context, opts contact.ListOpts) ([]*contact.Contact, string, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, "", err
	}
	opts = opts.WithDefaults()
	q := strings.ToLower(opts.Search)
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*contact.Contact, 0, len(f.rows))
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
		if q != "" && !strings.HasPrefix(strings.ToLower(c.Name), q) {
			continue
		}
		cp := *c
		out = append(out, &cp)
	}
	slices.SortFunc(out, func(a, b *contact.Contact) int {
		if d := b.UpdatedAt.Compare(a.UpdatedAt); d != 0 {
			return d
		}
		return cmp.Compare(b.ID.String(), a.ID.String())
	})
	if opts.Cursor != "" {
		ts, id, decErr := keyset.Decode(keyset.Cursor(opts.Cursor))
		if decErr != nil {
			return nil, "", decErr
		}
		out = slices.DeleteFunc(out, func(c *contact.Contact) bool {
			return !c.UpdatedAt.Before(ts) && (!c.UpdatedAt.Equal(ts) || c.ID.String() >= id.String())
		})
	}
	next := ""
	if len(out) > opts.Limit {
		out = out[:opts.Limit]
		last := out[len(out)-1]
		next = string(keyset.Encode(last.UpdatedAt, last.ID))
	}
	return out, next, nil
}
