package alert

import (
	"context"
	"time"
)

// Store persists alert settings, opt-outs and send counts for the project on ctx.
type Store interface {
	// Settings returns the project's settings, enabled with no addresses when none were saved.
	Settings(ctx context.Context) (Settings, error)
	// SaveSettings upserts s; last write wins.
	SaveSettings(ctx context.Context, s *Settings) error
	// OptOuts lists the project's unsubscribed addresses.
	OptOuts(ctx context.Context) ([]string, error)
	// AddOptOut records email as unsubscribed from the project; recording it twice is a no-op.
	AddOptOut(ctx context.Context, email string) error
	// TakeTestSlot counts one test send in the current UTC hour, or reports false once limit are counted.
	TakeTestSlot(ctx context.Context, limit int) (bool, error)
	// AddSent adds n emails to the project's count for the current UTC day.
	AddSent(ctx context.Context, n int) error
	// SentToday sums the emails the whole org sent in the current UTC day.
	SentToday(ctx context.Context) (int, error)
	// PruneCounts deletes the org's counts whose window started before before.
	PruneCounts(ctx context.Context, before time.Time) error
}
