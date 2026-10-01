package message

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	pgent "altalune.id/openwa/internal/platform/db/entity/postgres"
	"altalune.id/openwa/internal/platform/tenant"
)

type pgChatIDRow struct {
	ChatID uuid.UUID `alias:"messages.chat_id"`
}

type pgIDRow struct {
	ID uuid.UUID `alias:"messages.id"`
}

type pgVersionRow struct {
	Version int `alias:"messages.version"`
}

func (s *postgresStore) Save(ctx context.Context, m *Message, ifVersion int) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.save(ctx, tx, tc, m, ifVersion))
}

func (s *postgresStore) save(ctx context.Context, tx *sql.Tx, tc tenant.Context, m *Message, ifVersion int) error {
	// NOTE: only a create (ifVersion 0) binds the queued bytes; an update never ships them again.
	vals, err := s.values(m, ifVersion == 0)
	if err != nil {
		return fmt.Errorf("message.postgres.Save: %w", err)
	}
	media, err := mediaExpr(m.Media)
	if err != nil {
		return fmt.Errorf("message.postgres.Save: %w", err)
	}
	sets := []postgres.ColumnAssigment{
		s.t.Status.SET(postgres.String(string(m.Status))),
		s.t.Error.SET(postgres.String(m.Error)),
		s.t.Attempts.SET(postgres.Int64(int64(m.Attempts))),
		s.t.Media.SET(media),
		s.t.SentAt.SET(timeExpr(m.SentAt)),
		s.t.DeliveredAt.SET(timeExpr(m.DeliveredAt)),
		s.t.ReadAt.SET(timeExpr(m.ReadAt)),
		s.t.UpdatedAt.SET(postgres.TimestampzT(m.UpdatedAt.UTC())),
		s.t.Version.SET(s.t.Version.ADD(postgres.Int32(1))),
	}
	if m.RawDropped() {
		sets = append(sets, s.t.Raw.SET(pgent.NullBytea()))
	}
	stmt := s.t.INSERT(s.t.AllColumns).VALUES(vals[0], vals[1:]...).
		ON_CONFLICT(s.t.ID).
		// SECURITY: the conflict clause is guarded by org, or an upsert carrying another tenant's row id would rewrite it wherever RLS is inert.
		DO_UPDATE(postgres.SET(sets...).WHERE(s.t.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(versionGuard(ifVersion, s.t.Version))))
	res, err := stmt.ExecContext(ctx, tx)
	if err != nil {
		if taken := s.publicIDTaken(err, m); taken != nil {
			return taken
		}
		return fmt.Errorf("message.postgres.Save: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("message.postgres.Save: rows affected: %w", err)
	}
	// NOTE: zero rows means the org guard refused, or the stored version moved on.
	if n == 0 {
		return s.refusal(ctx, tx, tc, m.ID, ifVersion)
	}
	return nil
}

func (s *postgresStore) publicIDTaken(err error, m *Message) error {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.Code != "23505" || pgErr.ConstraintName != s.publicIDUnique {
		return nil
	}
	return &PublicIDTakenError{PublicID: m.PublicID}
}

func (s *postgresStore) InsertInbound(ctx context.Context, m *Message) (bool, error) {
	tx, owned, _, err := s.txAcquire(ctx)
	if err != nil {
		return false, err
	}
	var inserted bool
	err = s.endTx(tx, owned, func() error {
		vals, vErr := s.values(m, true)
		if vErr != nil {
			return fmt.Errorf("message.postgres.InsertInbound: %w", vErr)
		}
		// NOTE: a literal predicate, because Postgres infers the partial arbiter index only from a constant that implies its WHERE.
		stmt := s.t.INSERT(s.t.AllColumns).VALUES(vals[0], vals[1:]...).
			ON_CONFLICT(s.t.DeviceID, s.t.WAMessageID).WHERE(postgres.RawBool("wa_message_id <> ''")).DO_NOTHING()
		res, execErr := stmt.ExecContext(ctx, tx)
		if execErr != nil {
			if taken := s.publicIDTaken(execErr, m); taken != nil {
				return taken
			}
			return fmt.Errorf("message.postgres.InsertInbound: %w", execErr)
		}
		n, raErr := res.RowsAffected()
		if raErr != nil {
			return fmt.Errorf("message.postgres.InsertInbound: rows affected: %w", raErr)
		}
		inserted = n == 1
		return nil
	}())
	return inserted, err
}

//nolint:nilnil // an empty queue is not an error.
func (s *postgresStore) ClaimNext(ctx context.Context, deviceID uuid.UUID, staleAfter time.Duration) (*Message, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	var claimed *Message
	err = s.endTx(tx, owned, func() error {
		stale := postgres.TimestampzT(time.Now().UTC().Add(-staleAfter))
		pending := s.t.Status.EQ(postgres.String(string(StatusQueued))).
			OR(s.t.Status.EQ(postgres.String(string(StatusSending))).AND(s.t.UpdatedAt.LT(stale)))
		stmt := postgres.SELECT(s.projectionWithRaw()).FROM(s.t).
			WHERE(s.t.OrgID.EQ(postgres.UUID(tc.OrgID)).
				AND(s.t.DeviceID.EQ(postgres.UUID(deviceID))).
				AND(s.t.Direction.EQ(postgres.String(string(DirectionOut)))).
				AND(pending)).
			ORDER_BY(s.t.CreatedAt.ASC(), s.t.ID.ASC()).
			LIMIT(1).
			FOR(postgres.UPDATE().SKIP_LOCKED())
		var row pgMessageRow
		if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
			if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("message.postgres.ClaimNext: %w", qErr)
		}
		m, convErr := row.toMessage()
		if convErr != nil {
			return convErr
		}
		now := time.Now()
		// NOTE: a stale sending row at MaxAttempts crashed its sender every time; it is failed and handed back so the caller emits its event, never sent again.
		if m.Status == StatusSending && m.Attempts >= MaxAttempts {
			m.Expire("send attempts exhausted", now)
		} else {
			m.MarkSending(now)
		}
		if saveErr := s.save(ctx, tx, tc, m, m.Version); saveErr != nil {
			return saveErr
		}
		m.Version++
		claimed = m
		return nil
	}())
	return claimed, err
}

func (s *postgresStore) MarkChatRead(ctx context.Context, chatID, upTo uuid.UUID, at time.Time) (int64, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return 0, err
	}
	var n int64
	err = s.endTx(tx, owned, func() error {
		ts := postgres.TimestampzT(at.UTC())
		res, execErr := s.t.UPDATE(s.t.ReadAt, s.t.UpdatedAt, s.t.Version).
			SET(ts, ts, s.t.Version.ADD(postgres.Int32(1))).
			WHERE(s.t.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(s.t.ChatID.EQ(postgres.UUID(chatID))).
				AND(s.t.Direction.EQ(postgres.String(string(DirectionIn)))).AND(s.t.ReadAt.IS_NULL()).
				AND(s.t.ID.LT_EQ(postgres.UUID(upTo)))).
			ExecContext(ctx, tx)
		if execErr != nil {
			return fmt.Errorf("message.postgres.MarkChatRead: %w", execErr)
		}
		n, execErr = res.RowsAffected()
		return execErr
	}())
	return n, err
}

func (s *postgresStore) UnsentBefore(ctx context.Context, projectID uuid.UUID, before time.Time, limit int) ([]*Message, error) {
	tx, tc, done, err := s.readTx(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	var rows []pgMessageRow
	stmt := postgres.SELECT(s.projection()).FROM(s.t).
		WHERE(s.t.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(s.t.ProjectID.EQ(postgres.UUID(projectID))).
			AND(s.t.Direction.EQ(postgres.String(string(DirectionOut)))).
			AND(s.t.Status.EQ(postgres.String(string(StatusQueued))).AND(s.t.CreatedAt.LT(postgres.TimestampzT(before.UTC()))).
				OR(s.t.Status.EQ(postgres.String(string(StatusSending))).AND(s.t.UpdatedAt.LT(postgres.TimestampzT(before.UTC())))))).
		ORDER_BY(s.t.CreatedAt.ASC(), s.t.ID.ASC()).
		LIMIT(int64(limit))
	if qErr := stmt.QueryContext(ctx, tx, &rows); qErr != nil && !errors.Is(qErr, qrm.ErrNoRows) {
		return nil, fmt.Errorf("message.postgres.UnsentBefore: %w", qErr)
	}
	out := make([]*Message, 0, len(rows))
	for _, r := range rows {
		m, convErr := r.toMessage()
		if convErr != nil {
			return nil, convErr
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *postgresStore) DeleteOlderThan(ctx context.Context, projectID uuid.UUID, before time.Time, limit int) (int64, []uuid.UUID, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return 0, nil, err
	}
	var rows []pgChatIDRow
	err = s.endTx(tx, owned, func() error {
		org := postgres.UUID(tc.OrgID)
		// NOTE: two statements, because jet wraps an IN sub-select in a second pair of parentheses, which Postgres reads as a scalar subquery.
		var batch []pgIDRow
		pick := postgres.SELECT(s.t.ID).FROM(s.t).
			WHERE(s.t.OrgID.EQ(org).AND(s.t.ProjectID.EQ(postgres.UUID(projectID))).
				AND(s.t.WATimestamp.LT(postgres.TimestampzT(before.UTC()))).AND(s.settled())).
			ORDER_BY(s.t.WATimestamp.ASC(), s.t.ID.ASC()).
			LIMIT(int64(limit))
		if qErr := pick.QueryContext(ctx, tx, &batch); qErr != nil && !errors.Is(qErr, qrm.ErrNoRows) {
			return fmt.Errorf("message.postgres.DeleteOlderThan: select: %w", qErr)
		}
		if len(batch) == 0 {
			return nil
		}
		ids := make([]postgres.Expression, 0, len(batch))
		for _, r := range batch {
			ids = append(ids, postgres.UUID(r.ID))
		}
		stmt := s.t.DELETE().WHERE(s.t.ID.IN(ids...).AND(s.t.OrgID.EQ(org))).RETURNING(s.t.ChatID)
		if qErr := stmt.QueryContext(ctx, tx, &rows); qErr != nil && !errors.Is(qErr, qrm.ErrNoRows) {
			return fmt.Errorf("message.postgres.DeleteOlderThan: delete: %w", qErr)
		}
		return nil
	}())
	if err != nil {
		return 0, nil, err
	}
	chats := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		if !slices.Contains(chats, r.ChatID) {
			chats = append(chats, r.ChatID)
		}
	}
	return int64(len(rows)), chats, nil
}

func (s *postgresStore) SaveRetention(ctx context.Context, p *RetentionPolicy) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, func() error {
		stmt := s.r.INSERT(s.r.AllColumns).
			VALUES(p.ProjectID, tc.OrgID, p.Days, p.UpdatedAt.UTC()).
			ON_CONFLICT(s.r.ProjectID).
			// SECURITY: guarded by org, so another tenant naming this project id cannot rewrite its retention.
			DO_UPDATE(postgres.SET(
				s.r.Days.SET(postgres.Int64(int64(p.Days))),
				s.r.UpdatedAt.SET(postgres.TimestampzT(p.UpdatedAt.UTC())),
			).WHERE(s.r.OrgID.EQ(postgres.UUID(tc.OrgID))))
		res, execErr := stmt.ExecContext(ctx, tx)
		if execErr != nil {
			return fmt.Errorf("message.postgres.SaveRetention: %w", execErr)
		}
		n, raErr := res.RowsAffected()
		if raErr != nil {
			return fmt.Errorf("message.postgres.SaveRetention: rows affected: %w", raErr)
		}
		if n == 0 {
			return &NotFoundError{ID: "retention:" + p.ProjectID.String()}
		}
		return nil
	}())
}

// SECURITY: org-scoped, so another org's row reports not-found rather than confirming it exists.
func (s *postgresStore) refusal(ctx context.Context, tx *sql.Tx, tc tenant.Context, id uuid.UUID, ifVersion int) error {
	stmt := postgres.SELECT(s.t.Version).FROM(s.t).
		WHERE(s.t.ID.EQ(postgres.UUID(id)).AND(s.t.OrgID.EQ(postgres.UUID(tc.OrgID)))).LIMIT(1)
	var row pgVersionRow
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return &NotFoundError{ID: id.String()}
		}
		return fmt.Errorf("message.postgres.Save: current version: %w", qErr)
	}
	if ifVersion != 0 {
		return &VersionMismatchError{Want: ifVersion, Got: row.Version}
	}
	return &NotFoundError{ID: id.String()}
}
