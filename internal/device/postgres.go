package device

import (
	"context"
	"database/sql"
	"encoding/json"
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
	"altalune.id/openwa/internal/platform/tenant"
)

type postgresStore struct {
	pool           pdb.Pool
	pc             *tenant.PgConn
	t              *pgent.Devices
	publicIDUnique string
}

func newPostgresStore(pool pdb.Pool, pc *tenant.PgConn, schema, tablePrefix string) *postgresStore {
	return &postgresStore{pool: pool, pc: pc, t: pgent.NewDevices(schema, tablePrefix), publicIDUnique: tablePrefix + "devices_public_id_key"}
}

type rulesDoc struct {
	GroupMode      string   `json:"group_mode"`
	AllowedSenders []string `json:"allowed_senders"`
	AllowedGroups  []string `json:"allowed_groups"`
	TriggerPrefix  string   `json:"trigger_prefix"`
	IgnoreFromMe   bool     `json:"ignore_from_me"`
}

func encodeRules(r Rules) (string, error) {
	doc := rulesDoc{
		GroupMode:      string(r.GroupMode),
		AllowedSenders: append([]string{}, r.AllowedSenders...),
		AllowedGroups:  append([]string{}, r.AllowedGroups...),
		TriggerPrefix:  r.TriggerPrefix,
		IgnoreFromMe:   r.IgnoreFromMe,
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("device.postgres: encode rules: %w", err)
	}
	return string(b), nil
}

func decodeRules(raw string) (Rules, error) {
	var doc rulesDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return Rules{}, fmt.Errorf("device.postgres: decode rules: %w", err)
	}
	out := Rules{
		GroupMode:      GroupMode(doc.GroupMode),
		AllowedSenders: doc.AllowedSenders,
		AllowedGroups:  doc.AllowedGroups,
		TriggerPrefix:  doc.TriggerPrefix,
		IgnoreFromMe:   doc.IgnoreFromMe,
	}
	if out.AllowedSenders == nil {
		out.AllowedSenders = []string{}
	}
	if out.AllowedGroups == nil {
		out.AllowedGroups = []string{}
	}
	return out, nil
}

type pgDeviceRow struct {
	ID        uuid.UUID `alias:"devices.id"`
	PublicID  string    `alias:"devices.public_id"`
	OrgID     uuid.UUID `alias:"devices.org_id"`
	ProjectID uuid.UUID `alias:"devices.project_id"`
	Name      string    `alias:"devices.name"`
	Rules     string    `alias:"devices.rules"`
	Version   int       `alias:"devices.version"`
	CreatedAt time.Time `alias:"devices.created_at"`
	UpdatedAt time.Time `alias:"devices.updated_at"`
}

func (r *pgDeviceRow) toDevice() (*Device, error) {
	rules, err := decodeRules(r.Rules)
	if err != nil {
		return nil, err
	}
	return &Device{
		ID:        r.ID,
		PublicID:  r.PublicID,
		OrgID:     r.OrgID,
		ProjectID: r.ProjectID,
		Name:      r.Name,
		Rules:     rules,
		Version:   r.Version,
		CreatedAt: r.CreatedAt.UTC(),
		UpdatedAt: r.UpdatedAt.UTC(),
	}, nil
}

type pgVersionRow struct {
	Version int `alias:"devices.version"`
}

// NOTE: rules is jsonb; it is read back as text so the row struct needs no driver-specific scanner.
func (s *postgresStore) columns() postgres.ProjectionList {
	return postgres.ProjectionList{
		s.t.ID, s.t.PublicID, s.t.OrgID, s.t.ProjectID, s.t.Name,
		postgres.CAST(s.t.Rules).AS_TEXT().AS("devices.rules"),
		s.t.Version, s.t.CreatedAt, s.t.UpdatedAt,
	}
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
		return nil, false, tenant.Context{}, fmt.Errorf("device.postgres: begin: %w", err)
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
		return fmt.Errorf("device.postgres: commit: %w", cerr)
	}
	return nil
}

func (s *postgresStore) Save(ctx context.Context, d *Device, ifVersion int) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, s.save(ctx, tx, tc, d, ifVersion))
}

func (s *postgresStore) save(ctx context.Context, tx *sql.Tx, tc tenant.Context, d *Device, ifVersion int) error {
	if d.OrgID != tc.OrgID {
		// SECURITY: without RLS in force, a fresh row naming another org would otherwise land in it.
		return &NotFoundError{ID: d.ID.String()}
	}
	rules, err := encodeRules(d.Rules)
	if err != nil {
		return err
	}
	rulesExpr := postgres.StringExp(postgres.CAST(postgres.String(rules)).AS("jsonb"))
	var stmt postgres.Statement
	if ifVersion != 0 {
		// NOTE: a conditional write is an UPDATE, so it can never resurrect a row deleted since the caller read it.
		stmt = s.t.UPDATE(s.t.Name, s.t.Rules, s.t.UpdatedAt, s.t.Version).
			SET(postgres.String(d.Name), rulesExpr, postgres.TimestampzT(d.UpdatedAt.UTC()), s.t.Version.ADD(postgres.Int32(1))).
			WHERE(s.t.ID.EQ(postgres.UUID(d.ID)).
				AND(s.t.OrgID.EQ(postgres.UUID(tc.OrgID))).
				AND(versionGuard(ifVersion, s.t.Version)))
	} else {
		stmt = s.t.INSERT(s.t.AllColumns).
			VALUES(d.ID, d.PublicID, d.OrgID, d.ProjectID, d.Name, rulesExpr, d.Version, d.CreatedAt.UTC(), d.UpdatedAt.UTC()).
			ON_CONFLICT(s.t.ID).
			// SECURITY: the conflict clause is guarded by org, or an upsert carrying another tenant's row id would rewrite that row wherever RLS is inert.
			DO_UPDATE(
				postgres.SET(
					s.t.Name.SET(postgres.String(d.Name)),
					s.t.Rules.SET(rulesExpr),
					s.t.UpdatedAt.SET(postgres.TimestampzT(d.UpdatedAt.UTC())),
					s.t.Version.SET(s.t.Version.ADD(postgres.Int32(1))),
				).WHERE(s.t.OrgID.EQ(postgres.UUID(tc.OrgID))),
			)
	}
	res, execErr := stmt.ExecContext(ctx, tx)
	if execErr != nil {
		if mapped := s.mapPgError(execErr, d); mapped != nil {
			return mapped
		}
		return fmt.Errorf("device.postgres.Save: %w", execErr)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return fmt.Errorf("device.postgres.Save: rows affected: %w", raErr)
	}
	if n == 0 {
		return s.refusalError(ctx, tx, tc, d.ID, ifVersion)
	}
	return nil
}

// SECURITY: the read is org-scoped — another org's row reports not-found, since a version answer would confirm it exists.
func (s *postgresStore) refusalError(ctx context.Context, tx *sql.Tx, tc tenant.Context, id uuid.UUID, ifVersion int) error {
	stmt := postgres.SELECT(s.t.Version).FROM(s.t).
		WHERE(s.t.ID.EQ(postgres.UUID(id)).AND(s.t.OrgID.EQ(postgres.UUID(tc.OrgID)))).LIMIT(1)
	var row pgVersionRow
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return &NotFoundError{ID: id.String()}
		}
		return fmt.Errorf("device.postgres.Save: current version: %w", qErr)
	}
	if ifVersion != 0 {
		return &StaleVersionError{Want: ifVersion, Got: row.Version}
	}
	return &NotFoundError{ID: id.String()}
}

func (s *postgresStore) ByID(ctx context.Context, id uuid.UUID) (*Device, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(s.columns()).FROM(s.t).
		WHERE(s.t.ID.EQ(postgres.UUID(id)).AND(s.t.OrgID.EQ(postgres.UUID(tc.OrgID)))).LIMIT(1)
	var row pgDeviceRow
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return nil, &NotFoundError{ID: id.String()}
		}
		return nil, fmt.Errorf("device.postgres.ByID: %w", qErr)
	}
	return row.toDevice()
}

func (s *postgresStore) ByPublicID(ctx context.Context, publicID string) (*Device, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(s.columns()).FROM(s.t).
		WHERE(s.t.PublicID.EQ(postgres.String(publicID)).AND(s.t.OrgID.EQ(postgres.UUID(tc.OrgID)))).LIMIT(1)
	var row pgDeviceRow
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return nil, &NotFoundError{ID: publicID}
		}
		return nil, fmt.Errorf("device.postgres.ByPublicID: %w", qErr)
	}
	return row.toDevice()
}

type pgIDPair struct {
	ID       uuid.UUID `alias:"devices.id"`
	PublicID string    `alias:"devices.public_id"`
}

func (s *postgresStore) PublicIDsByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	in := make([]postgres.Expression, 0, len(ids))
	for _, id := range ids {
		in = append(in, postgres.UUID(id))
	}
	pairs, err := s.idPairs(ctx, s.t.ID.IN(in...))
	if err != nil {
		return nil, fmt.Errorf("device.postgres.PublicIDsByIDs: %w", err)
	}
	for _, p := range pairs {
		out[p.ID] = p.PublicID
	}
	return out, nil
}

func (s *postgresStore) IDsByPublicIDs(ctx context.Context, publicIDs []string) (map[string]uuid.UUID, error) {
	out := make(map[string]uuid.UUID, len(publicIDs))
	if len(publicIDs) == 0 {
		return out, nil
	}
	in := make([]postgres.Expression, 0, len(publicIDs))
	for _, p := range publicIDs {
		in = append(in, postgres.String(p))
	}
	pairs, err := s.idPairs(ctx, s.t.PublicID.IN(in...))
	if err != nil {
		return nil, fmt.Errorf("device.postgres.IDsByPublicIDs: %w", err)
	}
	for _, p := range pairs {
		out[p.PublicID] = p.ID
	}
	return out, nil
}

func (s *postgresStore) idPairs(ctx context.Context, match postgres.BoolExpression) ([]pgIDPair, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(s.t.ID, s.t.PublicID).FROM(s.t).
		WHERE(match.AND(s.t.OrgID.EQ(postgres.UUID(tc.OrgID))).AND(s.t.ProjectID.EQ(postgres.UUID(tc.ProjectID))))
	var rows []pgIDPair
	if qErr := stmt.QueryContext(ctx, tx, &rows); qErr != nil && !errors.Is(qErr, qrm.ErrNoRows) {
		return nil, qErr
	}
	return rows, nil
}

func (s *postgresStore) List(ctx context.Context, _ ListOpts) ([]*Device, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(s.columns()).FROM(s.t).
		WHERE(s.t.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(s.t.ProjectID.EQ(postgres.UUID(tc.ProjectID)))).
		ORDER_BY(s.t.CreatedAt.DESC(), s.t.ID.DESC())
	var rows []pgDeviceRow
	if qErr := stmt.QueryContext(ctx, tx, &rows); qErr != nil {
		return nil, fmt.Errorf("device.postgres.List: %w", qErr)
	}
	out := make([]*Device, 0, len(rows))
	for i := range rows {
		d, dErr := rows[i].toDevice()
		if dErr != nil {
			return nil, dErr
		}
		out = append(out, d)
	}
	return out, nil
}

func (s *postgresStore) Delete(ctx context.Context, id uuid.UUID) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	stmt := s.t.DELETE().WHERE(s.t.ID.EQ(postgres.UUID(id)).AND(s.t.OrgID.EQ(postgres.UUID(tc.OrgID))))
	res, execErr := stmt.ExecContext(ctx, tx)
	if execErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("device.postgres.Delete: %w", execErr))
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("device.postgres.Delete: rows affected: %w", raErr))
	}
	if n == 0 {
		return s.endTx(tx, owned, &NotFoundError{ID: id.String()})
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

func (s *postgresStore) mapPgError(err error, d *Device) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return nil
	}
	if pgErr.ConstraintName == s.publicIDUnique {
		return &PublicIDTakenError{PublicID: d.PublicID}
	}
	return &NameTakenError{Name: d.Name}
}
