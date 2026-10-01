package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

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
	t              *pgent.Chats
	devices        *pgent.Devices
	publicIDUnique string
}

func newPostgresStore(pool pdb.Pool, pc *tenant.PgConn, schema, tablePrefix string) *postgresStore {
	return &postgresStore{pool: pool, pc: pc, t: pgent.NewChats(schema, tablePrefix), devices: pgent.NewDevices(schema, tablePrefix), publicIDUnique: tablePrefix + "chats_public_id_key"}
}

type pgChatRow struct {
	ID                 uuid.UUID  `alias:"chats.id"`
	PublicID           string     `alias:"chats.public_id"`
	OrgID              uuid.UUID  `alias:"chats.org_id"`
	ProjectID          uuid.UUID  `alias:"chats.project_id"`
	DeviceID           uuid.UUID  `alias:"chats.device_id"`
	JID                string     `alias:"chats.jid"`
	LID                string     `alias:"chats.lid"`
	Kind               string     `alias:"chats.kind"`
	Name               string     `alias:"chats.name"`
	LastMessageAt      *time.Time `alias:"chats.last_message_at"`
	LastMessagePreview string     `alias:"chats.last_message_preview"`
	UnreadCount        int        `alias:"chats.unread_count"`
	Archived           bool       `alias:"chats.archived"`
	ActivityAt         time.Time  `alias:"chats.activity_at"`
	Version            int        `alias:"chats.version"`
	CreatedAt          time.Time  `alias:"chats.created_at"`
	UpdatedAt          time.Time  `alias:"chats.updated_at"`
}

func (r *pgChatRow) toChat() *Chat {
	c := &Chat{
		ID: r.ID, PublicID: r.PublicID, OrgID: r.OrgID, ProjectID: r.ProjectID, DeviceID: r.DeviceID,
		JID: r.JID, LID: r.LID, Kind: Kind(r.Kind), Name: r.Name,
		LastMessagePreview: r.LastMessagePreview, UnreadCount: r.UnreadCount, Archived: r.Archived,
		ActivityAt: r.ActivityAt.UTC(), Version: r.Version, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
	if r.LastMessageAt != nil {
		at := r.LastMessageAt.UTC()
		c.LastMessageAt = &at
	}
	return c
}

type pgVersionRow struct {
	Version int `alias:"chats.version"`
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
		return nil, false, tenant.Context{}, fmt.Errorf("chat.postgres: begin: %w", err)
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
		return fmt.Errorf("chat.postgres: commit: %w", cerr)
	}
	return nil
}

func (s *postgresStore) values(c *Chat) []any {
	return []any{
		c.ID, c.PublicID, c.OrgID, c.ProjectID, c.DeviceID, c.JID, c.LID, string(c.Kind), c.Name,
		nullableTime(c.LastMessageAt), c.LastMessagePreview, c.UnreadCount, c.Archived,
		c.Version, c.CreatedAt.UTC(), c.UpdatedAt.UTC(),
	}
}

func (s *postgresStore) Save(ctx context.Context, c *Chat, ifVersion int) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.save(ctx, tx, tc, c, ifVersion))
}

// SECURITY: without RLS in force, a row naming another org, project or project's device would otherwise land there.
func (s *postgresStore) admit(ctx context.Context, tx *sql.Tx, tc tenant.Context, c *Chat) error {
	if c.OrgID != tc.OrgID || c.ProjectID != tc.ProjectID {
		return &NotFoundError{ID: c.ID.String()}
	}
	var row struct {
		ID uuid.UUID `alias:"devices.id"`
	}
	stmt := postgres.SELECT(s.devices.ID).FROM(s.devices).
		WHERE(s.devices.ID.EQ(postgres.UUID(c.DeviceID)).
			AND(s.devices.OrgID.EQ(postgres.UUID(tc.OrgID))).
			AND(s.devices.ProjectID.EQ(postgres.UUID(tc.ProjectID)))).
		LIMIT(1)
	if err := stmt.QueryContext(ctx, tx, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return &NotFoundError{ID: c.DeviceID.String()}
		}
		return fmt.Errorf("chat.postgres: device scope: %w", err)
	}
	return nil
}

func (s *postgresStore) save(ctx context.Context, tx *sql.Tx, tc tenant.Context, c *Chat, ifVersion int) error {
	if err := s.admit(ctx, tx, tc, c); err != nil {
		return err
	}
	v := s.values(c)
	stmt := s.t.INSERT(s.t.MutableColumns).
		VALUES(v[0], v[1:]...).
		ON_CONFLICT(s.t.ID).
		// SECURITY: the conflict clause is guarded by org, or an upsert carrying another tenant's row id would rewrite it wherever RLS is inert.
		DO_UPDATE(
			postgres.SET(
				s.t.JID.SET(postgres.String(c.JID)),
				s.t.LID.SET(postgres.String(c.LID)),
				s.t.Name.SET(postgres.String(c.Name)),
				s.t.LastMessageAt.SET(nullableTimeExpr(c.LastMessageAt)),
				s.t.LastMessagePreview.SET(postgres.String(c.LastMessagePreview)),
				s.t.UnreadCount.SET(postgres.Int64(int64(c.UnreadCount))),
				s.t.Archived.SET(postgres.Bool(c.Archived)),
				s.t.UpdatedAt.SET(postgres.TimestampzT(c.UpdatedAt.UTC())),
				s.t.Version.SET(s.t.Version.ADD(postgres.Int32(1))),
			).WHERE(s.t.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(versionGuard(ifVersion, s.t.Version))),
		)
	res, err := stmt.ExecContext(ctx, tx)
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" && pgErr.ConstraintName == s.publicIDUnique {
			return &PublicIDTakenError{PublicID: c.PublicID}
		}
		return fmt.Errorf("chat.postgres.Save: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("chat.postgres.Save: rows affected: %w", err)
	}
	// NOTE: zero rows means the org guard refused, or the stored version moved on.
	if n == 0 {
		return s.refusal(ctx, tx, tc, c.ID, ifVersion)
	}
	return nil
}

func (s *postgresStore) Insert(ctx context.Context, c *Chat) (bool, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return false, err
	}
	var inserted bool
	err = s.endTx(tx, owned, func() error {
		if admitErr := s.admit(ctx, tx, tc, c); admitErr != nil {
			return admitErr
		}
		v := s.values(c)
		res, execErr := s.t.INSERT(s.t.MutableColumns).VALUES(v[0], v[1:]...).ON_CONFLICT().DO_NOTHING().ExecContext(ctx, tx)
		if execErr != nil {
			return fmt.Errorf("chat.postgres.Insert: %w", execErr)
		}
		n, raErr := res.RowsAffected()
		if raErr != nil {
			return fmt.Errorf("chat.postgres.Insert: rows affected: %w", raErr)
		}
		inserted = n == 1
		return nil
	}())
	return inserted, err
}

func (s *postgresStore) ByID(ctx context.Context, id uuid.UUID) (*Chat, error) {
	return s.one(ctx, "ByID", id.String(), s.t.ID.EQ(postgres.UUID(id)))
}

func (s *postgresStore) ByPublicID(ctx context.Context, publicID string) (*Chat, error) {
	return s.one(ctx, "ByPublicID", publicID, s.t.PublicID.EQ(postgres.String(publicID)))
}

func (s *postgresStore) PublicIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID       uuid.UUID `alias:"chats.id"`
		PublicID string    `alias:"chats.public_id"`
	}
	err = s.endTx(tx, owned, func() error {
		in := make([]postgres.Expression, 0, len(ids))
		for _, id := range ids {
			in = append(in, postgres.UUID(id))
		}
		stmt := postgres.SELECT(s.t.ID, s.t.PublicID).FROM(s.t).WHERE(s.t.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(s.t.ProjectID.EQ(postgres.UUID(tc.ProjectID))).AND(s.t.ID.IN(in...)))
		if qErr := stmt.QueryContext(ctx, tx, &rows); qErr != nil && !errors.Is(qErr, qrm.ErrNoRows) {
			return fmt.Errorf("chat.postgres.PublicIDs: %w", qErr)
		}
		return nil
	}())
	for _, r := range rows {
		out[r.ID] = r.PublicID
	}
	return out, err
}

func (s *postgresStore) ByJID(ctx context.Context, deviceID uuid.UUID, jid string) (*Chat, error) {
	if jid == "" {
		return nil, &NotFoundError{ID: jid}
	}
	key := postgres.String(jid)
	return s.one(ctx, "ByJID", jid, s.t.DeviceID.EQ(postgres.UUID(deviceID)).
		AND(s.t.JID.EQ(key).OR(s.t.LID.EQ(key))))
}

func (s *postgresStore) one(ctx context.Context, op, key string, cond postgres.BoolExpression) (*Chat, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(s.t.AllColumns).FROM(s.t).
		WHERE(cond.AND(s.t.OrgID.EQ(postgres.UUID(tc.OrgID)))).
		LIMIT(1)
	var row pgChatRow
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return nil, &NotFoundError{ID: key}
		}
		return nil, fmt.Errorf("chat.postgres.%s: %w", op, qErr)
	}
	return row.toChat(), nil
}

func (s *postgresStore) List(ctx context.Context, opts ListOpts) ([]*Chat, string, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, "", err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	opts = opts.WithDefaults()
	cond := s.t.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(s.t.ProjectID.EQ(postgres.UUID(tc.ProjectID)))
	if opts.DeviceID != nil {
		cond = cond.AND(s.t.DeviceID.EQ(postgres.UUID(*opts.DeviceID)))
	}
	if len(opts.DeviceIDs) > 0 {
		cond = cond.AND(s.t.DeviceID.IN(uuidList(opts.DeviceIDs)...))
	}
	if opts.Kind != nil {
		cond = cond.AND(s.t.Kind.EQ(postgres.String(string(*opts.Kind))))
	}
	if opts.Search != "" {
		cond = cond.AND(postgres.LOWER(s.t.Name).LIKE(postgres.String(pgent.LikePrefix(opts.Search))))
	}
	if opts.Cursor != "" {
		ts, id, decErr := keyset.Decode(Cursor(opts.Cursor))
		if decErr != nil {
			return nil, "", decErr
		}
		at := postgres.TimestampzT(ts)
		cond = cond.AND(s.t.ActivityAt.LT(at).OR(s.t.ActivityAt.EQ(at).AND(s.t.ID.LT(postgres.UUID(id)))))
	}
	stmt := postgres.SELECT(s.t.AllColumns).FROM(s.t).WHERE(cond).
		ORDER_BY(s.t.ActivityAt.DESC(), s.t.ID.DESC()).
		LIMIT(int64(opts.Limit + 1))
	var rows []pgChatRow
	if qErr := stmt.QueryContext(ctx, tx, &rows); qErr != nil {
		return nil, "", fmt.Errorf("chat.postgres.List: %w", qErr)
	}
	next := ""
	if len(rows) > opts.Limit {
		rows = rows[:opts.Limit]
		last := rows[len(rows)-1]
		next = string(keyset.Encode(last.ActivityAt, last.ID))
	}
	out := make([]*Chat, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].toChat())
	}
	return out, next, nil
}

func (s *postgresStore) Delete(ctx context.Context, id uuid.UUID) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, func() error {
		res, execErr := s.t.DELETE().
			WHERE(s.t.ID.EQ(postgres.UUID(id)).AND(s.t.OrgID.EQ(postgres.UUID(tc.OrgID)))).
			ExecContext(ctx, tx)
		if execErr != nil {
			return fmt.Errorf("chat.postgres.Delete: %w", execErr)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return &NotFoundError{ID: id.String()}
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
		return fmt.Errorf("chat.postgres.Save: current version: %w", qErr)
	}
	if ifVersion != 0 {
		return &VersionMismatchError{Want: ifVersion, Got: row.Version}
	}
	return &NotFoundError{ID: id.String()}
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

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func nullableTimeExpr(t *time.Time) postgres.TimestampzExpression {
	if t == nil {
		return pgent.NullTimestampz()
	}
	return postgres.TimestampzT(t.UTC())
}

func uuidList(ids []uuid.UUID) []postgres.Expression {
	out := make([]postgres.Expression, 0, len(ids))
	for _, id := range ids {
		out = append(out, postgres.UUID(id))
	}
	return out
}
