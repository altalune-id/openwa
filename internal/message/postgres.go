package message

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"

	pdb "altalune.id/openwa/internal/platform/db"
	pgent "altalune.id/openwa/internal/platform/db/entity/postgres"
	"altalune.id/openwa/internal/platform/keyset"
	"altalune.id/openwa/internal/platform/tenant"
)

// Cursor is this module's page cursor.
type Cursor = keyset.Cursor

type postgresStore struct {
	pool           pdb.Pool
	pc             *tenant.PgConn
	t              *pgent.Messages
	r              *pgent.MessageRetention
	publicIDUnique string
}

func newPostgresStore(pool pdb.Pool, pc *tenant.PgConn, schema, tablePrefix string) *postgresStore {
	return &postgresStore{pool: pool, pc: pc, t: pgent.NewMessages(schema, tablePrefix), r: pgent.NewMessageRetention(schema, tablePrefix), publicIDUnique: tablePrefix + "messages_public_id_key"}
}

type pgMedia struct {
	Mime          string `json:"mime"`
	Size          int64  `json:"size"`
	Filename      string `json:"filename"`
	Storage       string `json:"storage"`
	Ref           string `json:"ref"`
	URL           string `json:"url"`
	DirectPath    string `json:"direct_path"`
	MediaKey      []byte `json:"media_key"`
	FileSHA256    []byte `json:"file_sha256"`
	FileEncSHA256 []byte `json:"file_enc_sha256"`
	Length        uint64 `json:"length"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	Seconds       int    `json:"seconds"`
	Voice         bool   `json:"voice"`
}

type pgLocation struct {
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
	Name    string  `json:"name"`
	Address string  `json:"address"`
}

type pgMessageRow struct {
	ID                uuid.UUID  `alias:"messages.id"`
	PublicID          string     `alias:"messages.public_id"`
	OrgID             uuid.UUID  `alias:"messages.org_id"`
	ProjectID         uuid.UUID  `alias:"messages.project_id"`
	DeviceID          uuid.UUID  `alias:"messages.device_id"`
	ChatID            uuid.UUID  `alias:"messages.chat_id"`
	Direction         string     `alias:"messages.direction"`
	WAMessageID       string     `alias:"messages.wa_message_id"`
	SenderJID         string     `alias:"messages.sender_jid"`
	SenderLID         string     `alias:"messages.sender_lid"`
	SenderPhone       string     `alias:"messages.sender_phone"`
	SenderName        string     `alias:"messages.sender_name"`
	FromMe            bool       `alias:"messages.from_me"`
	Type              string     `alias:"messages.type"`
	Body              string     `alias:"messages.body"`
	Media             *string    `alias:"messages.media"`
	Location          *string    `alias:"messages.location"`
	QuotedWAMessageID string     `alias:"messages.quoted_wa_message_id"`
	Mentions          string     `alias:"messages.mentions"`
	TargetWAMessageID string     `alias:"messages.target_wa_message_id"`
	Status            string     `alias:"messages.status"`
	Error             string     `alias:"messages.error"`
	Attempts          int        `alias:"messages.attempts"`
	WATimestamp       time.Time  `alias:"messages.wa_timestamp"`
	SentAt            *time.Time `alias:"messages.sent_at"`
	DeliveredAt       *time.Time `alias:"messages.delivered_at"`
	ReadAt            *time.Time `alias:"messages.read_at"`
	Raw               []byte     `alias:"messages.raw"`
	Version           int        `alias:"messages.version"`
	CreatedAt         time.Time  `alias:"messages.created_at"`
	UpdatedAt         time.Time  `alias:"messages.updated_at"`
}

func (r *pgMessageRow) toMessage() (*Message, error) {
	m := &Message{
		ID: r.ID, PublicID: r.PublicID, OrgID: r.OrgID, ProjectID: r.ProjectID, DeviceID: r.DeviceID, ChatID: r.ChatID,
		Direction: Direction(r.Direction), WAMessageID: r.WAMessageID, SenderJID: r.SenderJID, SenderLID: r.SenderLID,
		SenderPhone: r.SenderPhone, SenderName: r.SenderName, FromMe: r.FromMe, Type: Type(r.Type), Body: r.Body,
		QuotedWAMessageID: r.QuotedWAMessageID, TargetWAMessageID: r.TargetWAMessageID, Status: Status(r.Status),
		Error: r.Error, Attempts: r.Attempts, WATimestamp: r.WATimestamp.UTC(), SentAt: utcPtr(r.SentAt),
		DeliveredAt: utcPtr(r.DeliveredAt), ReadAt: utcPtr(r.ReadAt), Raw: r.Raw, Version: r.Version,
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), Mentions: []string{},
	}
	if err := json.Unmarshal([]byte(r.Mentions), &m.Mentions); err != nil {
		return nil, fmt.Errorf("message.postgres: mentions: %w", err)
	}
	if r.Media != nil {
		var pm pgMedia
		if err := json.Unmarshal([]byte(*r.Media), &pm); err != nil {
			return nil, fmt.Errorf("message.postgres: media: %w", err)
		}
		m.Media = &Media{
			Mime: pm.Mime, Size: pm.Size, Filename: pm.Filename, Storage: pm.Storage, Ref: pm.Ref,
			Keys:  MediaKeys{URL: pm.URL, DirectPath: pm.DirectPath, MediaKey: pm.MediaKey, FileSHA256: pm.FileSHA256, FileEncSHA256: pm.FileEncSHA256, Length: pm.Length},
			Width: pm.Width, Height: pm.Height, Seconds: pm.Seconds, Voice: pm.Voice,
		}
	}
	if r.Location != nil {
		var pl pgLocation
		if err := json.Unmarshal([]byte(*r.Location), &pl); err != nil {
			return nil, fmt.Errorf("message.postgres: location: %w", err)
		}
		m.Location = &Location{Lat: pl.Lat, Lng: pl.Lng, Name: pl.Name, Address: pl.Address}
	}
	return m, nil
}

// NOTE: jsonb columns are selected as text so the row needs no driver-specific scanner.
// NOTE: raw is left out, because a queued media row carries up to whatsapp.mediaMaxBytes there and every list, receipt and status write would load it.
func (s *postgresStore) projection() postgres.ProjectionList {
	return postgres.ProjectionList{
		s.t.AllColumns.Except(s.t.Media, s.t.Location, s.t.Mentions, s.t.Raw),
		postgres.CAST(s.t.Media).AS_TEXT().AS("messages.media"),
		postgres.CAST(s.t.Location).AS_TEXT().AS("messages.location"),
		postgres.CAST(s.t.Mentions).AS_TEXT().AS("messages.mentions"),
	}
}

func (s *postgresStore) projectionWithRaw() postgres.ProjectionList {
	return append(s.projection(), s.t.Raw)
}

func jsonb(v any) (postgres.StringExpression, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return postgres.StringExp(postgres.CAST(postgres.String(string(b))).AS("jsonb")), nil
}

func mediaExpr(md *Media) (postgres.StringExpression, error) {
	if md == nil {
		return pgent.NullJSONB(), nil
	}
	return jsonb(pgMedia{
		Mime: md.Mime, Size: md.Size, Filename: md.Filename, Storage: md.Storage, Ref: md.Ref,
		URL: md.Keys.URL, DirectPath: md.Keys.DirectPath, MediaKey: md.Keys.MediaKey, FileSHA256: md.Keys.FileSHA256,
		FileEncSHA256: md.Keys.FileEncSHA256, Length: md.Keys.Length, Width: md.Width, Height: md.Height, Seconds: md.Seconds, Voice: md.Voice,
	})
}

func locationExpr(l *Location) (postgres.StringExpression, error) {
	if l == nil {
		return pgent.NullJSONB(), nil
	}
	return jsonb(pgLocation{Lat: l.Lat, Lng: l.Lng, Name: l.Name, Address: l.Address})
}

func rawExpr(b []byte) postgres.ByteaExpression {
	if b == nil {
		return pgent.NullBytea()
	}
	return postgres.Bytea(b)
}

func (s *postgresStore) values(m *Message, withRaw bool) ([]any, error) {
	media, err := mediaExpr(m.Media)
	if err != nil {
		return nil, err
	}
	loc, err := locationExpr(m.Location)
	if err != nil {
		return nil, err
	}
	mentions := m.Mentions
	if mentions == nil {
		mentions = []string{}
	}
	ment, err := jsonb(mentions)
	if err != nil {
		return nil, err
	}
	raw := pgent.NullBytea()
	if withRaw {
		raw = rawExpr(m.Raw)
	}
	return []any{
		m.ID, m.PublicID, m.OrgID, m.ProjectID, m.DeviceID, m.ChatID, string(m.Direction), m.WAMessageID,
		m.SenderJID, m.SenderLID, m.SenderPhone, m.SenderName, m.FromMe, string(m.Type), m.Body,
		media, loc, m.QuotedWAMessageID, ment, m.TargetWAMessageID, string(m.Status), m.Error, m.Attempts,
		m.WATimestamp.UTC(), timeOrNil(m.SentAt), timeOrNil(m.DeliveredAt), timeOrNil(m.ReadAt), raw,
		m.Version, m.CreatedAt.UTC(), m.UpdatedAt.UTC(),
	}, nil
}

func (s *postgresStore) txAcquire(ctx context.Context) (*sql.Tx, bool, tenant.Context, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, false, tenant.Context{}, err
	}
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx, false, tc, nil
	}
	tx, err := s.pc.BeginTenanted(ctx, tc)
	if err != nil {
		return nil, false, tenant.Context{}, fmt.Errorf("message.postgres: begin: %w", err)
	}
	return tx, true, tc, nil
}

func (s *postgresStore) endTx(tx *sql.Tx, owned bool, err error) error {
	if !owned {
		return err
	}
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if cerr := tx.Commit(); cerr != nil {
		return fmt.Errorf("message.postgres: commit: %w", cerr)
	}
	return nil
}

func (s *postgresStore) readTx(ctx context.Context) (*sql.Tx, tenant.Context, func(), error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, tenant.Context{}, nil, err
	}
	done := func() {}
	if owned {
		done = func() { _ = tx.Rollback() }
	}
	return tx, tc, done, nil
}

func versionGuard(ifVersion int, col postgres.ColumnInteger) postgres.BoolExpression {
	if ifVersion == 0 {
		return postgres.Bool(true)
	}
	// SECURITY: a version outside int32 must match no row; narrowing it could match a live version.
	if ifVersion < 0 || ifVersion > math.MaxInt32 {
		return postgres.Bool(false)
	}
	return col.EQ(postgres.Int32(int32(ifVersion)))
}

func timeOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func timeExpr(t *time.Time) postgres.TimestampzExpression {
	if t == nil {
		return pgent.NullTimestampz()
	}
	return postgres.TimestampzT(t.UTC())
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
