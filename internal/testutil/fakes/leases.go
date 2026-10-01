package fakes

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/whatsapp"
)

// LeaseStore is an in-memory whatsapp.LeaseStore with the real store's claim predicate.
type LeaseStore struct {
	mu        sync.Mutex
	rows      map[uuid.UUID]whatsapp.Lease
	claimFn   func(ctx context.Context, owner string, ttl time.Duration, limit int) ([]whatsapp.Lease, error)
	renewHook func(ctx context.Context) error
	renewErr  error
	upsertErr error
	deleteErr error
	renews    int
}

// NewLeaseStore returns an empty in-memory lease store.
func NewLeaseStore() *LeaseStore { return &LeaseStore{rows: map[uuid.UUID]whatsapp.Lease{}} }

var _ whatsapp.LeaseStore = (*LeaseStore)(nil)

// SetClaimFn replaces Claim; nil restores the default predicate.
func (f *LeaseStore) SetClaimFn(fn func(ctx context.Context, owner string, ttl time.Duration, limit int) ([]whatsapp.Lease, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimFn = fn
}

// SetRenewErr makes every Renew fail with err until it is reset to nil.
func (f *LeaseStore) SetRenewErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renewErr = err
}

// SetRenewHook runs fn at the start of every Renew; a non-nil error fails that Renew.
func (f *LeaseStore) SetRenewHook(fn func(ctx context.Context) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renewHook = fn
}

// SetUpsertErr makes every Upsert fail with err until it is reset to nil.
func (f *LeaseStore) SetUpsertErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upsertErr = err
}

// SetDeleteErr makes every Delete fail with err until it is reset to nil.
func (f *LeaseStore) SetDeleteErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteErr = err
}

// Renews counts Renew calls, failed ones included.
func (f *LeaseStore) Renews() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.renews
}

// Seed stores l as given.
func (f *LeaseStore) Seed(l whatsapp.Lease) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[l.DeviceID] = l
}

// Get returns the stored lease for id.
func (f *LeaseStore) Get(id uuid.UUID) (whatsapp.Lease, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.rows[id]
	return l, ok
}

// Steal hands id's lease to owner, as another replica's Claim would.
func (f *LeaseStore) Steal(id uuid.UUID, owner string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l := f.rows[id]
	exp := time.Now().Add(time.Hour)
	l.Owner, l.ExpiresAt = owner, &exp
	f.rows[id] = l
}

func (f *LeaseStore) Upsert(_ context.Context, l whatsapp.Lease) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.rows[l.DeviceID] = l
	return nil
}

func (f *LeaseStore) Delete(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.rows, id)
	return nil
}

func (f *LeaseStore) Exists(_ context.Context, id uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.rows[id]
	return ok, nil
}

func (f *LeaseStore) Claim(ctx context.Context, owner string, ttl time.Duration, limit int) ([]whatsapp.Lease, error) {
	f.mu.Lock()
	fn := f.claimFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, owner, ttl, limit)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	free := make([]whatsapp.Lease, 0, len(f.rows))
	for _, l := range f.rows {
		expired := l.Owner == "" || (l.ExpiresAt != nil && l.ExpiresAt.Before(now))
		if expired && l.Owner != owner {
			free = append(free, l)
		}
	}
	sort.Slice(free, func(i, j int) bool {
		if free[i].ExpiresAt == nil || free[j].ExpiresAt == nil {
			return free[i].ExpiresAt == nil && free[j].ExpiresAt != nil
		}
		return free[i].ExpiresAt.Before(*free[j].ExpiresAt)
	})
	out := make([]whatsapp.Lease, 0, min(limit, len(free)))
	for _, l := range free[:min(limit, len(free))] {
		exp := now.Add(ttl)
		l.Owner, l.ExpiresAt = owner, &exp
		f.rows[l.DeviceID] = l
		out = append(out, l)
	}
	return out, nil
}

func (f *LeaseStore) Renew(ctx context.Context, owner string, ttl time.Duration) ([]uuid.UUID, error) {
	f.mu.Lock()
	f.renews++
	hook := f.renewHook
	f.mu.Unlock()
	if hook != nil {
		if err := hook(ctx); err != nil {
			return nil, err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.renewErr != nil {
		return nil, f.renewErr
	}
	now := time.Now()
	var out []uuid.UUID
	for id, l := range f.rows {
		if l.Owner != owner {
			continue
		}
		exp := now.Add(ttl)
		l.ExpiresAt = &exp
		f.rows[id] = l
		out = append(out, id)
	}
	slices.SortFunc(out, compareUUID)
	return out, nil
}

func (f *LeaseStore) Release(_ context.Context, owner string, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if l, ok := f.rows[id]; ok && l.Owner == owner {
		l.Owner, l.ExpiresAt = "", nil
		f.rows[id] = l
	}
	return nil
}

// ReleaseAll honours ctx like the real store, so a caller passing a spent context sees the failure.
func (f *LeaseStore) ReleaseAll(ctx context.Context, owner string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, l := range f.rows {
		if l.Owner == owner {
			l.Owner, l.ExpiresAt = "", nil
			f.rows[id] = l
		}
	}
	return nil
}

func compareUUID(a, b uuid.UUID) int {
	switch as, bs := a.String(), b.String(); {
	case as < bs:
		return -1
	case as > bs:
		return 1
	}
	return 0
}
