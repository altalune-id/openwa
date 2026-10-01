package whatsapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	pdb "altalune.id/openwa/internal/platform/db"
	pgent "altalune.id/openwa/internal/platform/db/entity/postgres"
)

type pgLeaseStore struct {
	db    *sql.DB
	t     *pgent.WhatsappLeases
	table string
}

func newPgLeaseStore(pool pdb.Pool, schema, tablePrefix string) *pgLeaseStore {
	if schema == "" {
		schema = "public"
	}
	return &pgLeaseStore{
		db:    pool.W,
		t:     pgent.NewWhatsappLeases(schema, tablePrefix),
		table: pgx.Identifier{schema, tablePrefix + "whatsapp_leases"}.Sanitize(),
	}
}

type pgLeaseRow struct {
	DeviceID  uuid.UUID  `alias:"whatsapp_leases.device_id"`
	OrgID     uuid.UUID  `alias:"whatsapp_leases.org_id"`
	ProjectID uuid.UUID  `alias:"whatsapp_leases.project_id"`
	JID       string     `alias:"whatsapp_leases.jid"`
	Owner     *string    `alias:"whatsapp_leases.owner"`
	ExpiresAt *time.Time `alias:"whatsapp_leases.expires_at"`
}

func (r *pgLeaseRow) toLease() Lease {
	l := Lease{DeviceID: r.DeviceID, OrgID: r.OrgID, ProjectID: r.ProjectID, JID: r.JID, ExpiresAt: utcPtr(r.ExpiresAt)}
	if r.Owner != nil {
		l.Owner = *r.Owner
	}
	return l
}

type pgLeaseIDRow struct {
	DeviceID uuid.UUID `alias:"whatsapp_leases.device_id"`
}

const leaseColumns = `l.device_id AS "whatsapp_leases.device_id", l.org_id AS "whatsapp_leases.org_id", l.project_id AS "whatsapp_leases.project_id", l.jid AS "whatsapp_leases.jid", l.owner AS "whatsapp_leases.owner", l.expires_at AS "whatsapp_leases.expires_at"`

// Upsert writes l as given. NOTE: no org guard on purpose — whatsapp_leases has no RLS and only the runtime writes it (see upsertGuardExemptions).
func (s *pgLeaseStore) Upsert(ctx context.Context, l Lease) error {
	now := time.Now().UTC()
	stmt := s.t.INSERT(s.t.AllColumns).
		VALUES(l.DeviceID, l.OrgID, l.ProjectID, l.JID, nullableString(l.Owner), pgNullableTime(l.ExpiresAt), now).
		ON_CONFLICT(s.t.DeviceID).
		DO_UPDATE(postgres.SET(
			s.t.JID.SET(postgres.String(l.JID)),
			s.t.Owner.SET(nullableStringExpr(l.Owner)),
			s.t.ExpiresAt.SET(pgNullableTimeExpr(l.ExpiresAt)),
			s.t.UpdatedAt.SET(postgres.TimestampzT(now)),
		))
	if _, err := stmt.ExecContext(ctx, s.db); err != nil {
		return fmt.Errorf("whatsapp.leases.Upsert: %w", err)
	}
	return nil
}

func (s *pgLeaseStore) Delete(ctx context.Context, deviceID uuid.UUID) error {
	stmt := s.t.DELETE().WHERE(s.t.DeviceID.EQ(postgres.UUID(deviceID)))
	if _, err := stmt.ExecContext(ctx, s.db); err != nil {
		return fmt.Errorf("whatsapp.leases.Delete: %w", err)
	}
	return nil
}

func (s *pgLeaseStore) Exists(ctx context.Context, deviceID uuid.UUID) (bool, error) {
	stmt := postgres.SELECT(s.t.DeviceID).FROM(s.t).WHERE(s.t.DeviceID.EQ(postgres.UUID(deviceID))).LIMIT(1)
	var row pgLeaseIDRow
	if err := stmt.QueryContext(ctx, s.db, &row); err != nil {
		if errors.Is(err, qrm.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("whatsapp.leases.Exists: %w", err)
	}
	return true, nil
}

// NOTE: an owned lease with a NULL expires_at counts as expired, so it can never be stuck. jet cannot express FOR UPDATE SKIP LOCKED inside an UPDATE's IN-subquery nor ORDER BY ... NULLS FIRST, so this is raw. SECURITY: only the config-supplied identifier is interpolated; every value is a bound argument.
func (s *pgLeaseStore) Claim(ctx context.Context, owner string, ttl time.Duration, limit int) ([]Lease, error) {
	stmt := postgres.RawStatement(`
UPDATE `+s.table+` AS l
   SET owner = #owner, expires_at = now() + make_interval(secs => #secs), updated_at = now()
 WHERE l.device_id IN (
       SELECT device_id FROM `+s.table+`
        WHERE (owner IS NULL OR expires_at IS NULL OR expires_at < now())
          AND owner IS DISTINCT FROM #owner
        ORDER BY expires_at NULLS FIRST
        LIMIT #limit
        FOR UPDATE SKIP LOCKED)
RETURNING `+leaseColumns,
		postgres.RawArgs{"#owner": owner, "#secs": ttl.Seconds(), "#limit": limit})
	var rows []pgLeaseRow
	if err := stmt.QueryContext(ctx, s.db, &rows); err != nil {
		return nil, fmt.Errorf("whatsapp.leases.Claim: %w", err)
	}
	out := make([]Lease, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].toLease())
	}
	return out, nil
}

// NOTE: raw for the same reason as Claim; updated_at is deliberately left alone so a renewal is not mistaken for a change.
func (s *pgLeaseStore) Renew(ctx context.Context, owner string, ttl time.Duration) ([]uuid.UUID, error) {
	stmt := postgres.RawStatement(`
UPDATE `+s.table+` AS l
   SET expires_at = now() + make_interval(secs => #secs)
 WHERE l.owner = #owner
RETURNING l.device_id AS "whatsapp_leases.device_id"`,
		postgres.RawArgs{"#owner": owner, "#secs": ttl.Seconds()})
	var rows []pgLeaseIDRow
	if err := stmt.QueryContext(ctx, s.db, &rows); err != nil {
		return nil, fmt.Errorf("whatsapp.leases.Renew: %w", err)
	}
	out := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.DeviceID)
	}
	return out, nil
}

func (s *pgLeaseStore) Release(ctx context.Context, owner string, deviceID uuid.UUID) error {
	stmt := s.t.UPDATE(s.t.Owner, s.t.ExpiresAt, s.t.UpdatedAt).
		SET(pgent.NullText(), pgent.NullTimestampz(), postgres.NOW()).
		WHERE(s.t.DeviceID.EQ(postgres.UUID(deviceID)).AND(s.t.Owner.EQ(postgres.String(owner))))
	if _, err := stmt.ExecContext(ctx, s.db); err != nil {
		return fmt.Errorf("whatsapp.leases.Release: %w", err)
	}
	return nil
}

func (s *pgLeaseStore) ReleaseAll(ctx context.Context, owner string) error {
	stmt := s.t.UPDATE(s.t.Owner, s.t.ExpiresAt, s.t.UpdatedAt).
		SET(pgent.NullText(), pgent.NullTimestampz(), postgres.NOW()).
		WHERE(s.t.Owner.EQ(postgres.String(owner)))
	if _, err := stmt.ExecContext(ctx, s.db); err != nil {
		return fmt.Errorf("whatsapp.leases.ReleaseAll: %w", err)
	}
	return nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableStringExpr(s string) postgres.StringExpression {
	if s == "" {
		return pgent.NullText()
	}
	return postgres.String(s)
}
