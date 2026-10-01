package contact

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"

	pdb "altalune.id/openwa/internal/platform/db"
	pgent "altalune.id/openwa/internal/platform/db/entity/postgres"
	"altalune.id/openwa/internal/platform/keyset"
	"altalune.id/openwa/internal/platform/tenant"
)

type postgresStore struct {
	pool pdb.Pool
	pc   *tenant.PgConn
	t    *pgent.Contacts
	d    *pgent.Devices
}

func newPostgresStore(pool pdb.Pool, pc *tenant.PgConn, schema, tablePrefix string) *postgresStore {
	return &postgresStore{pool: pool, pc: pc, t: pgent.NewContacts(schema, tablePrefix), d: pgent.NewDevices(schema, tablePrefix)}
}

type pgContactRow struct {
	ID           uuid.UUID `alias:"contacts.id"`
	OrgID        uuid.UUID `alias:"contacts.org_id"`
	ProjectID    uuid.UUID `alias:"contacts.project_id"`
	DeviceID     uuid.UUID `alias:"contacts.device_id"`
	JID          string    `alias:"contacts.jid"`
	LID          string    `alias:"contacts.lid"`
	Phone        string    `alias:"contacts.phone"`
	Name         string    `alias:"contacts.name"`
	PushName     string    `alias:"contacts.push_name"`
	BusinessName string    `alias:"contacts.business_name"`
	UpdatedAt    time.Time `alias:"contacts.updated_at"`
}

func (r *pgContactRow) toContact() *Contact {
	return &Contact{
		ID: r.ID, OrgID: r.OrgID, ProjectID: r.ProjectID, DeviceID: r.DeviceID,
		JID: r.JID, LID: r.LID, Phone: r.Phone, Name: r.Name, PushName: r.PushName, BusinessName: r.BusinessName,
		UpdatedAt: r.UpdatedAt.UTC(),
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
		return nil, false, tenant.Context{}, fmt.Errorf("contact.postgres: begin: %w", err)
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
		return fmt.Errorf("contact.postgres: commit: %w", cerr)
	}
	return nil
}

// NOTE: keeps the stored value for every empty incoming field.
func mergeSet(col postgres.ColumnString, v string) postgres.ColumnAssigment {
	if v == "" {
		return col.SET(col)
	}
	return col.SET(postgres.String(v))
}

func (s *postgresStore) Upsert(ctx context.Context, c *Contact) error {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	return s.endTx(tx, owned, func() error {
		if devErr := s.requireDevice(ctx, tx, tc, c); devErr != nil {
			return devErr
		}
		stmt := s.t.INSERT(s.t.AllColumns).
			VALUES(c.ID, c.OrgID, c.ProjectID, c.DeviceID, c.JID, c.LID, c.Phone, c.Name, c.PushName, c.BusinessName, c.UpdatedAt.UTC()).
			ON_CONFLICT(s.t.DeviceID, s.t.JID).
			// SECURITY: guarded by org and project, so a device id borrowed from another tenant or project cannot rewrite its contacts.
			DO_UPDATE(postgres.SET(
				mergeSet(s.t.LID, c.LID),
				mergeSet(s.t.Phone, c.Phone),
				mergeSet(s.t.Name, c.Name),
				mergeSet(s.t.PushName, c.PushName),
				mergeSet(s.t.BusinessName, c.BusinessName),
				s.t.UpdatedAt.SET(postgres.TimestampzT(c.UpdatedAt.UTC())),
			).WHERE(s.t.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(s.t.ProjectID.EQ(postgres.UUID(tc.ProjectID)))))
		res, execErr := stmt.ExecContext(ctx, tx)
		if execErr != nil {
			return fmt.Errorf("contact.postgres.Upsert: %w", execErr)
		}
		n, raErr := res.RowsAffected()
		if raErr != nil {
			return fmt.Errorf("contact.postgres.Upsert: rows affected: %w", raErr)
		}
		if n == 0 {
			return &NotFoundError{ID: c.JID}
		}
		return nil
	}())
}

// SECURITY: a contact may only be written for a device of the caller's own org and project.
func (s *postgresStore) requireDevice(ctx context.Context, tx *sql.Tx, tc tenant.Context, c *Contact) error {
	if c.OrgID != tc.OrgID || c.ProjectID != tc.ProjectID {
		return &NotFoundError{ID: c.JID}
	}
	var row struct {
		ID uuid.UUID `alias:"devices.id"`
	}
	stmt := postgres.SELECT(s.d.ID).FROM(s.d).WHERE(
		s.d.ID.EQ(postgres.UUID(c.DeviceID)).
			AND(s.d.OrgID.EQ(postgres.UUID(tc.OrgID))).
			AND(s.d.ProjectID.EQ(postgres.UUID(tc.ProjectID)))).LIMIT(1)
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return &NotFoundError{ID: c.JID}
		}
		return fmt.Errorf("contact.postgres.Upsert: device: %w", qErr)
	}
	return nil
}

func (s *postgresStore) ByJID(ctx context.Context, deviceID uuid.UUID, jid string) (*Contact, error) {
	tx, owned, tc, err := s.txAcquire(ctx)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	var row pgContactRow
	cond := s.t.DeviceID.EQ(postgres.UUID(deviceID)).
		AND(s.t.JID.EQ(postgres.String(jid))).
		AND(s.t.OrgID.EQ(postgres.UUID(tc.OrgID))).
		AND(s.t.ProjectID.EQ(postgres.UUID(tc.ProjectID)))
	stmt := postgres.SELECT(s.t.AllColumns).FROM(s.t).WHERE(cond).LIMIT(1)
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return nil, &NotFoundError{ID: jid}
		}
		return nil, fmt.Errorf("contact.postgres.ByJID: %w", qErr)
	}
	return row.toContact(), nil
}

func (s *postgresStore) List(ctx context.Context, opts ListOpts) ([]*Contact, string, error) {
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
		ids := make([]postgres.Expression, 0, len(opts.DeviceIDs))
		for _, id := range opts.DeviceIDs {
			ids = append(ids, postgres.UUID(id))
		}
		cond = cond.AND(s.t.DeviceID.IN(ids...))
	}
	if opts.Search != "" {
		cond = cond.AND(postgres.LOWER(s.t.Name).LIKE(postgres.String(pgent.LikePrefix(opts.Search))))
	}
	if opts.Cursor != "" {
		ts, id, decErr := keyset.Decode(keyset.Cursor(opts.Cursor))
		if decErr != nil {
			return nil, "", decErr
		}
		at := postgres.TimestampzT(ts)
		cond = cond.AND(s.t.UpdatedAt.LT(at).OR(s.t.UpdatedAt.EQ(at).AND(s.t.ID.LT(postgres.UUID(id)))))
	}
	stmt := postgres.SELECT(s.t.AllColumns).FROM(s.t).WHERE(cond).
		ORDER_BY(s.t.UpdatedAt.DESC(), s.t.ID.DESC()).LIMIT(int64(opts.Limit + 1))
	var rows []pgContactRow
	if qErr := stmt.QueryContext(ctx, tx, &rows); qErr != nil {
		return nil, "", fmt.Errorf("contact.postgres.List: %w", qErr)
	}
	next := ""
	if len(rows) > opts.Limit {
		rows = rows[:opts.Limit]
		last := rows[len(rows)-1]
		next = string(keyset.Encode(last.UpdatedAt, last.ID))
	}
	out := make([]*Contact, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].toContact())
	}
	return out, next, nil
}
