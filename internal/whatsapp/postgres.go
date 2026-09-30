package whatsapp

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

	pdb "altalune.id/openwa/internal/platform/db"
	pgent "altalune.id/openwa/internal/platform/db/entity/postgres"
	"altalune.id/openwa/internal/platform/tenant"
)

type postgresStore struct {
	pool pdb.Pool
	pc   *tenant.PgConn
	t    *pgent.WhatsappSessions
}

func newPostgresStore(pool pdb.Pool, pc *tenant.PgConn, schema, tablePrefix string) *postgresStore {
	return &postgresStore{pool: pool, pc: pc, t: pgent.NewWhatsappSessions(schema, tablePrefix)}
}

type pgSessionRow struct {
	DeviceID        uuid.UUID  `alias:"whatsapp_sessions.device_id"`
	OrgID           uuid.UUID  `alias:"whatsapp_sessions.org_id"`
	ProjectID       uuid.UUID  `alias:"whatsapp_sessions.project_id"`
	Engine          string     `alias:"whatsapp_sessions.engine"`
	JID             string     `alias:"whatsapp_sessions.jid"`
	LID             string     `alias:"whatsapp_sessions.lid"`
	Phone           string     `alias:"whatsapp_sessions.phone"`
	PushName        string     `alias:"whatsapp_sessions.push_name"`
	Platform        string     `alias:"whatsapp_sessions.platform"`
	State           string     `alias:"whatsapp_sessions.state"`
	Reason          string     `alias:"whatsapp_sessions.reason"`
	LastConnectedAt *time.Time `alias:"whatsapp_sessions.last_connected_at"`
	LastSeenAt      *time.Time `alias:"whatsapp_sessions.last_seen_at"`
	LastError       string     `alias:"whatsapp_sessions.last_error"`
	Version         int        `alias:"whatsapp_sessions.version"`
	UpdatedAt       time.Time  `alias:"whatsapp_sessions.updated_at"`
}

func (r *pgSessionRow) toSession() *Session {
	return &Session{
		DeviceID:        r.DeviceID,
		OrgID:           r.OrgID,
		ProjectID:       r.ProjectID,
		Engine:          r.Engine,
		JID:             r.JID,
		LID:             r.LID,
		Phone:           r.Phone,
		PushName:        r.PushName,
		Platform:        r.Platform,
		State:           State(r.State),
		Reason:          r.Reason,
		LastConnectedAt: utcPtr(r.LastConnectedAt),
		LastSeenAt:      utcPtr(r.LastSeenAt),
		LastError:       r.LastError,
		Version:         r.Version,
		UpdatedAt:       r.UpdatedAt.UTC(),
	}
}

type pgSessionVersionRow struct {
	Version int `alias:"whatsapp_sessions.version"`
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
		return nil, false, tenant.Context{}, fmt.Errorf("whatsapp.postgres: begin: %w", err)
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
		return fmt.Errorf("whatsapp.postgres: commit: %w", cerr)
	}
	return nil
}

func (s *postgresStore) Save(ctx context.Context, sess *Session, ifVersion int) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.save(ctx, tx, tc, sess, ifVersion))
}

func (s *postgresStore) save(ctx context.Context, tx *sql.Tx, tc tenant.Context, sess *Session, ifVersion int) error {
	if sess.OrgID != tc.OrgID {
		// SECURITY: without RLS in force, a fresh row naming another org would otherwise land in it.
		return &SessionNotFoundError{ID: sess.DeviceID.String()}
	}
	if ifVersion != 0 {
		return s.update(ctx, tx, tc, sess, ifVersion)
	}
	stmt := s.t.INSERT(s.t.AllColumns).
		VALUES(
			sess.DeviceID, sess.OrgID, sess.ProjectID, sess.Engine,
			sess.JID, sess.LID, sess.Phone, sess.PushName, sess.Platform,
			string(sess.State), sess.Reason,
			pgNullableTime(sess.LastConnectedAt), pgNullableTime(sess.LastSeenAt),
			sess.LastError, sess.Version, sess.UpdatedAt.UTC(),
		).
		ON_CONFLICT(s.t.DeviceID).
		// SECURITY: the conflict clause is guarded by org, or an upsert carrying another tenant's device id would rewrite that row wherever RLS is inert.
		DO_UPDATE(
			postgres.SET(
				s.t.JID.SET(postgres.String(sess.JID)),
				s.t.LID.SET(postgres.String(sess.LID)),
				s.t.Phone.SET(postgres.String(sess.Phone)),
				s.t.PushName.SET(postgres.String(sess.PushName)),
				s.t.Platform.SET(postgres.String(sess.Platform)),
				s.t.State.SET(postgres.String(string(sess.State))),
				s.t.Reason.SET(postgres.String(sess.Reason)),
				s.t.LastConnectedAt.SET(pgNullableTimeExpr(sess.LastConnectedAt)),
				s.t.LastSeenAt.SET(pgNullableTimeExpr(sess.LastSeenAt)),
				s.t.LastError.SET(postgres.String(sess.LastError)),
				s.t.UpdatedAt.SET(postgres.TimestampzT(sess.UpdatedAt.UTC())),
				s.t.Version.SET(s.t.Version.ADD(postgres.Int32(1))),
			).WHERE(s.t.OrgID.EQ(postgres.UUID(tc.OrgID))),
		)
	return s.exec(ctx, tx, tc, stmt, sess.DeviceID, 0)
}

// NOTE: a conditional write is an UPDATE, so it can never resurrect a row deleted since the caller read it.
func (s *postgresStore) update(ctx context.Context, tx *sql.Tx, tc tenant.Context, sess *Session, ifVersion int) error {
	stmt := s.t.UPDATE(
		s.t.JID, s.t.LID, s.t.Phone, s.t.PushName, s.t.Platform, s.t.State, s.t.Reason,
		s.t.LastConnectedAt, s.t.LastSeenAt, s.t.LastError, s.t.UpdatedAt, s.t.Version,
	).SET(
		postgres.String(sess.JID), postgres.String(sess.LID), postgres.String(sess.Phone),
		postgres.String(sess.PushName), postgres.String(sess.Platform), postgres.String(string(sess.State)),
		postgres.String(sess.Reason), pgNullableTimeExpr(sess.LastConnectedAt), pgNullableTimeExpr(sess.LastSeenAt),
		postgres.String(sess.LastError), postgres.TimestampzT(sess.UpdatedAt.UTC()), s.t.Version.ADD(postgres.Int32(1)),
	).WHERE(s.t.DeviceID.EQ(postgres.UUID(sess.DeviceID)).
		AND(s.t.OrgID.EQ(postgres.UUID(tc.OrgID))).
		AND(versionGuard(ifVersion, s.t.Version)))
	return s.exec(ctx, tx, tc, stmt, sess.DeviceID, ifVersion)
}

func (s *postgresStore) exec(ctx context.Context, tx *sql.Tx, tc tenant.Context, stmt postgres.Statement, id uuid.UUID, ifVersion int) error {
	res, execErr := stmt.ExecContext(ctx, tx)
	if execErr != nil {
		return fmt.Errorf("whatsapp.postgres.Save: %w", execErr)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return fmt.Errorf("whatsapp.postgres.Save: rows affected: %w", raErr)
	}
	if n == 0 {
		return s.refusalError(ctx, tx, tc, id, ifVersion)
	}
	return nil
}

// SECURITY: the read is org-scoped — another org's row reports not-found, since a version answer would confirm it exists.
func (s *postgresStore) refusalError(ctx context.Context, tx *sql.Tx, tc tenant.Context, id uuid.UUID, ifVersion int) error {
	stmt := postgres.SELECT(s.t.Version).FROM(s.t).
		WHERE(s.t.DeviceID.EQ(postgres.UUID(id)).AND(s.t.OrgID.EQ(postgres.UUID(tc.OrgID)))).LIMIT(1)
	var row pgSessionVersionRow
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return &SessionNotFoundError{ID: id.String()}
		}
		return fmt.Errorf("whatsapp.postgres.Save: current version: %w", qErr)
	}
	if ifVersion != 0 {
		return &StaleVersionError{Want: ifVersion, Got: row.Version}
	}
	return &SessionNotFoundError{ID: id.String()}
}

func (s *postgresStore) ByDevice(ctx context.Context, deviceID uuid.UUID) (*Session, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(s.t.AllColumns).FROM(s.t).
		WHERE(s.t.DeviceID.EQ(postgres.UUID(deviceID)).AND(s.t.OrgID.EQ(postgres.UUID(tc.OrgID)))).LIMIT(1)
	var row pgSessionRow
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return nil, &SessionNotFoundError{ID: deviceID.String()}
		}
		return nil, fmt.Errorf("whatsapp.postgres.ByDevice: %w", qErr)
	}
	return row.toSession(), nil
}

func (s *postgresStore) ByDevices(ctx context.Context, ids []uuid.UUID) ([]*Session, error) {
	if len(ids) == 0 {
		if _, err := tenant.From(ctx); err != nil {
			return nil, err
		}
		return []*Session{}, nil
	}
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	wanted := make([]postgres.Expression, 0, len(ids))
	for _, id := range ids {
		wanted = append(wanted, postgres.UUID(id))
	}
	stmt := postgres.SELECT(s.t.AllColumns).FROM(s.t).
		WHERE(s.t.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(s.t.DeviceID.IN(wanted...))).
		ORDER_BY(s.t.DeviceID.ASC())
	var rows []pgSessionRow
	if qErr := stmt.QueryContext(ctx, tx, &rows); qErr != nil {
		return nil, fmt.Errorf("whatsapp.postgres.ByDevices: %w", qErr)
	}
	out := make([]*Session, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].toSession())
	}
	return out, nil
}

func (s *postgresStore) Delete(ctx context.Context, deviceID uuid.UUID) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	stmt := s.t.DELETE().WHERE(s.t.DeviceID.EQ(postgres.UUID(deviceID)).AND(s.t.OrgID.EQ(postgres.UUID(tc.OrgID))))
	res, execErr := stmt.ExecContext(ctx, tx)
	if execErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("whatsapp.postgres.Delete: %w", execErr))
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("whatsapp.postgres.Delete: rows affected: %w", raErr))
	}
	if n == 0 {
		return s.endTx(tx, owned, &SessionNotFoundError{ID: deviceID.String()})
	}
	return s.endTx(tx, owned, nil)
}

func versionGuard(ifVersion int, col postgres.ColumnInteger) postgres.BoolExpression {
	if ifVersion == 0 {
		return postgres.Bool(true)
	}
	// SECURITY: a version outside the column's range must match no row; narrowing would wrap and could match a live version.
	if ifVersion < 0 || ifVersion > math.MaxInt32 {
		return postgres.Bool(false)
	}
	return col.EQ(postgres.Int32(int32(ifVersion)))
}

func pgNullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func pgNullableTimeExpr(t *time.Time) postgres.TimestampzExpression {
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
