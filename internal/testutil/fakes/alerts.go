package fakes

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/alert"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/mailer"
)

type alertKey struct{ org, project uuid.UUID }

// AlertStore is an in-memory alert.Store keyed by the org and project on ctx, like the real store.
type AlertStore struct {
	mu        sync.Mutex
	settings  map[alertKey]alert.Settings
	optOuts   map[alertKey][]string
	testSlots map[alertKey]int
	sent      map[uuid.UUID]int
}

var _ alert.Store = (*AlertStore)(nil)

// NewAlertStore returns an empty AlertStore.
func NewAlertStore() *AlertStore {
	return &AlertStore{settings: map[alertKey]alert.Settings{}, optOuts: map[alertKey][]string{}, testSlots: map[alertKey]int{}, sent: map[uuid.UUID]int{}}
}

func alertScope(ctx context.Context) (alertKey, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return alertKey{}, err
	}
	return alertKey{tc.OrgID, tc.ProjectID}, nil
}

func (f *AlertStore) Settings(ctx context.Context) (alert.Settings, error) {
	k, err := alertScope(ctx)
	if err != nil {
		return alert.Settings{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.settings[k]; ok {
		return s, nil
	}
	return alert.Settings{OrgID: k.org, ProjectID: k.project, Enabled: true, Recipients: []string{}}, nil
}

func (f *AlertStore) SaveSettings(ctx context.Context, s *alert.Settings) error {
	k, err := alertScope(ctx)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settings[k] = *s
	return nil
}

func (f *AlertStore) OptOuts(ctx context.Context) ([]string, error) {
	k, err := alertScope(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.optOuts[k]), nil
}

func (f *AlertStore) AddOptOut(ctx context.Context, email string) error {
	k, err := alertScope(ctx)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !slices.Contains(f.optOuts[k], email) {
		f.optOuts[k] = append(f.optOuts[k], email)
	}
	return nil
}

func (f *AlertStore) TakeTestSlot(ctx context.Context, limit int) (bool, error) {
	k, err := alertScope(ctx)
	if err != nil {
		return false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.testSlots[k] >= limit {
		return false, nil
	}
	f.testSlots[k]++
	return true, nil
}

func (f *AlertStore) AddSent(ctx context.Context, n int) error {
	k, err := alertScope(ctx)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent[k.org] += n
	return nil
}

func (f *AlertStore) SentToday(ctx context.Context) (int, error) {
	k, err := alertScope(ctx)
	if err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sent[k.org], nil
}

func (f *AlertStore) PruneCounts(context.Context, time.Time) error { return nil }

// MailQueue records every enqueued message.
type MailQueue struct {
	mu   sync.Mutex
	Sent []mailer.Message
}

// Enqueue records m.
func (q *MailQueue) Enqueue(_ context.Context, m mailer.Message) (uuid.UUID, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.Sent = append(q.Sent, m)
	return uuid.New(), nil
}

// Messages returns a copy of what was enqueued.
func (q *MailQueue) Messages() []mailer.Message {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.Sent)
}
