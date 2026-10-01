package message

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"

	"altalune.id/openwa/internal/platform/keyset"
	"altalune.id/openwa/internal/platform/tenant"
)

type pgCountRow struct {
	Total int64 `alias:"counts.total"`
}

type pgRetentionRow struct {
	ProjectID uuid.UUID `alias:"message_retention.project_id"`
	OrgID     uuid.UUID `alias:"message_retention.org_id"`
	Days      int       `alias:"message_retention.days"`
	UpdatedAt time.Time `alias:"message_retention.updated_at"`
}

func (s *postgresStore) ByID(ctx context.Context, id uuid.UUID) (*Message, error) {
	return s.one(ctx, "ByID", id.String(), s.projection(), s.t.ID.EQ(postgres.UUID(id)))
}

func (s *postgresStore) ByWAID(ctx context.Context, deviceID uuid.UUID, waID string) (*Message, error) {
	if waID == "" {
		return nil, &NotFoundError{ID: waID}
	}
	return s.one(ctx, "ByWAID", waID, s.projection(), s.t.DeviceID.EQ(postgres.UUID(deviceID)).AND(s.t.WAMessageID.EQ(postgres.String(waID))))
}

func (s *postgresStore) ByPublicID(ctx context.Context, publicID string) (*Message, error) {
	return s.one(ctx, "ByPublicID", publicID, s.projection(), s.t.PublicID.EQ(postgres.String(publicID)))
}

func (s *postgresStore) PublicIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	tx, tc, done, err := s.readTx(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	in := make([]postgres.Expression, 0, len(ids))
	for _, id := range ids {
		in = append(in, postgres.UUID(id))
	}
	var rows []struct {
		ID       uuid.UUID `alias:"messages.id"`
		PublicID string    `alias:"messages.public_id"`
	}
	stmt := postgres.SELECT(s.t.ID, s.t.PublicID).FROM(s.t).WHERE(s.t.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(s.t.ID.IN(in...)))
	if qErr := stmt.QueryContext(ctx, tx, &rows); qErr != nil && !errors.Is(qErr, qrm.ErrNoRows) {
		return nil, fmt.Errorf("message.postgres.PublicIDs: %w", qErr)
	}
	for _, r := range rows {
		out[r.ID] = r.PublicID
	}
	return out, nil
}

func (s *postgresStore) QuotedByWAID(ctx context.Context, deviceID uuid.UUID, waID string) (*Message, error) {
	if waID == "" {
		return nil, &NotFoundError{ID: waID}
	}
	return s.one(ctx, "QuotedByWAID", waID, s.projectionWithRaw(), s.t.DeviceID.EQ(postgres.UUID(deviceID)).AND(s.t.WAMessageID.EQ(postgres.String(waID))))
}

func (s *postgresStore) one(ctx context.Context, op, key string, cols postgres.ProjectionList, cond postgres.BoolExpression) (*Message, error) {
	tx, tc, done, err := s.readTx(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	var row pgMessageRow
	stmt := postgres.SELECT(cols).FROM(s.t).WHERE(cond.AND(s.t.OrgID.EQ(postgres.UUID(tc.OrgID)))).LIMIT(1)
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return nil, &NotFoundError{ID: key}
		}
		return nil, fmt.Errorf("message.postgres.%s: %w", op, qErr)
	}
	return row.toMessage()
}

func (s *postgresStore) List(ctx context.Context, opts ListOpts) ([]*Message, string, error) {
	tx, tc, done, err := s.readTx(ctx)
	if err != nil {
		return nil, "", err
	}
	defer done()
	opts = opts.WithDefaults()
	cond := s.t.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(s.t.ProjectID.EQ(postgres.UUID(tc.ProjectID)))
	if opts.ChatID != nil {
		cond = cond.AND(s.t.ChatID.EQ(postgres.UUID(*opts.ChatID)))
	}
	if opts.DeviceID != nil {
		cond = cond.AND(s.t.DeviceID.EQ(postgres.UUID(*opts.DeviceID)))
	}
	if opts.Direction != nil {
		cond = cond.AND(s.t.Direction.EQ(postgres.String(string(*opts.Direction))))
	}
	if opts.Status != nil {
		cond = cond.AND(s.t.Status.EQ(postgres.String(string(*opts.Status))))
	}
	if opts.Unread {
		cond = cond.AND(s.t.Direction.EQ(postgres.String(string(DirectionIn)))).AND(s.t.ReadAt.IS_NULL())
	}
	if opts.UpdatedAfter != nil {
		cond = cond.AND(s.t.UpdatedAt.GT(postgres.TimestampzT(opts.UpdatedAfter.UTC())))
	}
	order := []postgres.OrderByClause{s.t.WATimestamp.DESC(), s.t.ID.DESC()}
	if opts.SinceID != nil {
		cond = cond.AND(s.t.ID.GT(postgres.UUID(*opts.SinceID)))
		order = []postgres.OrderByClause{s.t.ID.ASC()}
	} else if opts.Cursor != "" {
		ts, id, decErr := keyset.Decode(Cursor(opts.Cursor))
		if decErr != nil {
			return nil, "", decErr
		}
		at := postgres.TimestampzT(ts)
		cond = cond.AND(s.t.WATimestamp.LT(at).OR(s.t.WATimestamp.EQ(at).AND(s.t.ID.LT(postgres.UUID(id)))))
	}
	stmt := postgres.SELECT(s.projection()).FROM(s.t).WHERE(cond).ORDER_BY(order...).LIMIT(int64(opts.Limit + 1))
	var rows []pgMessageRow
	if qErr := stmt.QueryContext(ctx, tx, &rows); qErr != nil {
		return nil, "", fmt.Errorf("message.postgres.List: %w", qErr)
	}
	next := ""
	if len(rows) > opts.Limit {
		rows = rows[:opts.Limit]
		if opts.SinceID == nil {
			last := rows[len(rows)-1]
			next = string(keyset.Encode(last.WATimestamp, last.ID))
		}
	}
	out := make([]*Message, 0, len(rows))
	for i := range rows {
		m, convErr := rows[i].toMessage()
		if convErr != nil {
			return nil, "", convErr
		}
		out = append(out, m)
	}
	return out, next, nil
}

func (s *postgresStore) count(ctx context.Context, op string, cond func(tc tenant.Context) postgres.BoolExpression) (int64, error) {
	tx, tc, done, err := s.readTx(ctx)
	if err != nil {
		return 0, err
	}
	defer done()
	var row pgCountRow
	stmt := postgres.SELECT(postgres.COUNT(postgres.STAR).AS("counts.total")).FROM(s.t).WHERE(cond(tc))
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		return 0, fmt.Errorf("message.postgres.%s: %w", op, qErr)
	}
	return row.Total, nil
}

func (s *postgresStore) CountOlderThan(ctx context.Context, projectID uuid.UUID, before time.Time) (int64, error) {
	return s.count(ctx, "CountOlderThan", func(tc tenant.Context) postgres.BoolExpression {
		return s.t.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(s.t.ProjectID.EQ(postgres.UUID(projectID))).
			AND(s.t.WATimestamp.LT(postgres.TimestampzT(before.UTC()))).AND(s.settled())
	})
}

func (s *postgresStore) CountToday(ctx context.Context, since time.Time) (int64, error) {
	return s.count(ctx, "CountToday", func(tc tenant.Context) postgres.BoolExpression {
		return s.t.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(s.t.ProjectID.EQ(postgres.UUID(tc.ProjectID))).
			AND(s.t.WATimestamp.GT_EQ(postgres.TimestampzT(since.UTC())))
	})
}

func (s *postgresStore) CountUnreadInbound(ctx context.Context, chatID uuid.UUID) (int, error) {
	n, err := s.count(ctx, "CountUnreadInbound", func(tc tenant.Context) postgres.BoolExpression {
		return s.t.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(s.t.ChatID.EQ(postgres.UUID(chatID))).
			AND(s.t.Direction.EQ(postgres.String(string(DirectionIn)))).AND(s.t.ReadAt.IS_NULL())
	})
	return int(n), err
}

//nolint:nilnil // an absent row means the configured default, which is not an error.
func (s *postgresStore) RetentionByProject(ctx context.Context, projectID uuid.UUID) (*RetentionPolicy, error) {
	tx, tc, done, err := s.readTx(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	var row pgRetentionRow
	stmt := postgres.SELECT(s.r.AllColumns).FROM(s.r).
		WHERE(s.r.ProjectID.EQ(postgres.UUID(projectID)).AND(s.r.OrgID.EQ(postgres.UUID(tc.OrgID)))).LIMIT(1)
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("message.postgres.RetentionByProject: %w", qErr)
	}
	return &RetentionPolicy{ProjectID: row.ProjectID, OrgID: row.OrgID, Days: row.Days, UpdatedAt: row.UpdatedAt.UTC()}, nil
}

func (s *postgresStore) settled() postgres.BoolExpression {
	return s.t.Status.NOT_IN(postgres.String(string(StatusQueued)), postgres.String(string(StatusSending)))
}
