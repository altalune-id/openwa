package message

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Store is the driven port.
type Store interface {
	// Save upserts m; a nonzero ifVersion that no longer matches returns *VersionMismatchError, another org's row *NotFoundError.
	Save(ctx context.Context, m *Message, ifVersion int) error
	// InsertInbound inserts m unless the device already holds its WhatsApp id.
	InsertInbound(ctx context.Context, m *Message) (bool, error)
	ByID(ctx context.Context, id uuid.UUID) (*Message, error)
	ByWAID(ctx context.Context, deviceID uuid.UUID, waID string) (*Message, error)
	// ByPublicID returns the message of the org on ctx whose public id is publicID.
	ByPublicID(ctx context.Context, publicID string) (*Message, error)
	// PublicIDs maps the given message ids of the org on ctx to their public ids in one query.
	PublicIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error)
	// QuotedByWAID is ByWAID plus the raw engine payload, which only a quote needs.
	QuotedByWAID(ctx context.Context, deviceID uuid.UUID, waID string) (*Message, error)
	// UnsentBefore returns at most limit of the project's outbound rows still queued and created before before, or stuck in sending since before.
	UnsentBefore(ctx context.Context, projectID uuid.UUID, before time.Time, limit int) ([]*Message, error)
	// List pages the caller's project newest first, or returns rows after SinceID oldest first.
	List(ctx context.Context, opts ListOpts) ([]*Message, string, error)
	// ClaimNext locks one queued, or stale sending, outbound row of the device and marks it sending; a stale row already at MaxAttempts is failed and comes back failed instead, which the caller must not send. nil when none. Runs in the caller's transaction.
	ClaimNext(ctx context.Context, deviceID uuid.UUID, staleAfter time.Duration) (*Message, error)
	// DeleteOlderThan deletes at most limit settled rows of the project older than before, returning the chats they belonged to.
	DeleteOlderThan(ctx context.Context, projectID uuid.UUID, before time.Time, limit int) (int64, []uuid.UUID, error)
	CountOlderThan(ctx context.Context, projectID uuid.UUID, before time.Time) (int64, error)
	// CountToday counts the caller's project's messages since since.
	CountToday(ctx context.Context, since time.Time) (int64, error)
	CountUnreadInbound(ctx context.Context, chatID uuid.UUID) (int, error)
	// MarkChatRead stamps read_at on the chat's unread inbound rows with id <= upTo in one statement, returning how many changed.
	MarkChatRead(ctx context.Context, chatID, upTo uuid.UUID, at time.Time) (int64, error)
	// RetentionByProject returns nil when the project has no row of its own.
	RetentionByProject(ctx context.Context, projectID uuid.UUID) (*RetentionPolicy, error)
	SaveRetention(ctx context.Context, p *RetentionPolicy) error
}
