package alert

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"

	pdb "altalune.id/openwa/internal/platform/db"
	pgent "altalune.id/openwa/internal/platform/db/entity/postgres"
	"altalune.id/openwa/internal/platform/tenant"
)

const (
	kindTest  = "test"
	kindEmail = "email"
)

type postgresStore struct {
	pc *tenant.PgConn
	s  *pgent.AlertSettings
	o  *pgent.AlertOptouts
	c  *pgent.AlertSendCounts
}

func newPostgresStore(pc *tenant.PgConn, schema, tablePrefix string) *postgresStore {
	return &postgresStore{
		pc: pc,
		s:  pgent.NewAlertSettings(schema, tablePrefix),
		o:  pgent.NewAlertOptouts(schema, tablePrefix),
		c:  pgent.NewAlertSendCounts(schema, tablePrefix),
	}
}

func (st *postgresStore) inTx(ctx context.Context, fn func(tx *sql.Tx, tc tenant.Context) error) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return fn(tx, tc)
	}
	tx, err := st.pc.BeginTenanted(ctx, tc)
	if err != nil {
		return fmt.Errorf("alert.postgres: begin: %w", err)
	}
	if err := fn(tx, tc); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("alert.postgres: commit: %w", err)
	}
	return nil
}

func (st *postgresStore) project(tc tenant.Context) postgres.BoolExpression {
	return st.s.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(st.s.ProjectID.EQ(postgres.UUID(tc.ProjectID)))
}

type settingsRow struct {
	Enabled    bool      `alias:"alert_settings.disconnect_enabled"`
	Recipients string    `alias:"alert_settings.recipients"`
	UpdatedAt  time.Time `alias:"alert_settings.updated_at"`
}

func (st *postgresStore) Settings(ctx context.Context) (Settings, error) {
	var out Settings
	err := st.inTx(ctx, func(tx *sql.Tx, tc tenant.Context) error {
		out = Settings{OrgID: tc.OrgID, ProjectID: tc.ProjectID, Enabled: true, Recipients: []string{}}
		var row settingsRow
		err := postgres.SELECT(st.s.DisconnectEnabled, st.s.Recipients, st.s.UpdatedAt).FROM(st.s).
			WHERE(st.project(tc)).LIMIT(1).QueryContext(ctx, tx, &row)
		if errors.Is(err, qrm.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("alert.postgres.Settings: %w", err)
		}
		if err := json.Unmarshal([]byte(row.Recipients), &out.Recipients); err != nil {
			return fmt.Errorf("alert.postgres.Settings: recipients: %w", err)
		}
		out.Enabled, out.UpdatedAt = row.Enabled, row.UpdatedAt.UTC()
		return nil
	})
	return out, err
}

func (st *postgresStore) SaveSettings(ctx context.Context, s *Settings) error {
	raw, err := json.Marshal(s.Recipients)
	if err != nil {
		return fmt.Errorf("alert.postgres.SaveSettings: recipients: %w", err)
	}
	return st.inTx(ctx, func(tx *sql.Tx, tc tenant.Context) error {
		stmt := st.s.INSERT(st.s.AllColumns).
			VALUES(tc.ProjectID, tc.OrgID, s.Enabled, string(raw), s.UpdatedAt.UTC()).
			ON_CONFLICT(st.s.ProjectID).
			// SECURITY: guarded by org, so another tenant naming this project id cannot rewrite its settings.
			DO_UPDATE(postgres.SET(
				st.s.DisconnectEnabled.SET(postgres.Bool(s.Enabled)),
				st.s.Recipients.SET(postgres.String(string(raw))),
				st.s.UpdatedAt.SET(postgres.TimestampzT(s.UpdatedAt.UTC())),
			).WHERE(st.s.OrgID.EQ(postgres.UUID(tc.OrgID))))
		res, err := stmt.ExecContext(ctx, tx)
		if err != nil {
			return fmt.Errorf("alert.postgres.SaveSettings: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return errors.Join(fmt.Errorf("alert.postgres.SaveSettings: project %s not in org", tc.ProjectID), err)
		}
		return nil
	})
}

func (st *postgresStore) OptOuts(ctx context.Context) ([]string, error) {
	var out []string
	err := st.inTx(ctx, func(tx *sql.Tx, tc tenant.Context) error {
		var rows []struct {
			Email string `alias:"alert_optouts.email"`
		}
		err := postgres.SELECT(st.o.Email).FROM(st.o).
			WHERE(st.o.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(st.o.ProjectID.EQ(postgres.UUID(tc.ProjectID)))).
			ORDER_BY(st.o.Email.ASC()).QueryContext(ctx, tx, &rows)
		if err != nil && !errors.Is(err, qrm.ErrNoRows) {
			return fmt.Errorf("alert.postgres.OptOuts: %w", err)
		}
		out = make([]string, len(rows))
		for i, r := range rows {
			out[i] = r.Email
		}
		return nil
	})
	return out, err
}

func (st *postgresStore) AddOptOut(ctx context.Context, email string) error {
	return st.inTx(ctx, func(tx *sql.Tx, tc tenant.Context) error {
		_, err := st.o.INSERT(st.o.AllColumns).
			VALUES(tc.ProjectID, tc.OrgID, email, time.Now().UTC()).
			ON_CONFLICT(st.o.ProjectID, st.o.Email).DO_NOTHING().
			ExecContext(ctx, tx)
		if err != nil {
			return fmt.Errorf("alert.postgres.AddOptOut: %w", err)
		}
		return nil
	})
}

// NOTE: one conditional upsert, so concurrent clicks can never pass the limit.
func (st *postgresStore) TakeTestSlot(ctx context.Context, limit int) (bool, error) {
	var ok bool
	err := st.inTx(ctx, func(tx *sql.Tx, tc tenant.Context) error {
		window := time.Now().UTC().Truncate(time.Hour)
		res, err := st.c.INSERT(st.c.AllColumns).
			VALUES(tc.ProjectID, tc.OrgID, kindTest, window, 1).
			ON_CONFLICT(st.c.ProjectID, st.c.Kind, st.c.WindowStart).
			DO_UPDATE(postgres.SET(st.c.Sent.SET(st.c.Sent.ADD(postgres.Int(1)))).
				WHERE(st.c.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(st.c.Sent.LT(postgres.Int(int64(limit)))))).
			ExecContext(ctx, tx)
		if err != nil {
			return fmt.Errorf("alert.postgres.TakeTestSlot: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("alert.postgres.TakeTestSlot: rows affected: %w", err)
		}
		ok = n == 1
		return nil
	})
	return ok, err
}

func (st *postgresStore) AddSent(ctx context.Context, n int) error {
	if n == 0 {
		return nil
	}
	return st.inTx(ctx, func(tx *sql.Tx, tc tenant.Context) error {
		res, err := st.c.INSERT(st.c.AllColumns).
			VALUES(tc.ProjectID, tc.OrgID, kindEmail, utcDay(time.Now()), n).
			ON_CONFLICT(st.c.ProjectID, st.c.Kind, st.c.WindowStart).
			DO_UPDATE(postgres.SET(st.c.Sent.SET(st.c.Sent.ADD(postgres.Int(int64(n))))).
				WHERE(st.c.OrgID.EQ(postgres.UUID(tc.OrgID)))).
			ExecContext(ctx, tx)
		if err != nil {
			return fmt.Errorf("alert.postgres.AddSent: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return errors.Join(fmt.Errorf("alert.postgres.AddSent: project %s not in org", tc.ProjectID), err)
		}
		return nil
	})
}

func (st *postgresStore) SentToday(ctx context.Context) (int, error) {
	var total int
	err := st.inTx(ctx, func(tx *sql.Tx, tc tenant.Context) error {
		var row struct {
			Total int64 `alias:"total"`
		}
		err := postgres.SELECT(postgres.COALESCE(postgres.SUM(st.c.Sent), postgres.Int(0)).AS("total")).FROM(st.c).
			WHERE(st.c.OrgID.EQ(postgres.UUID(tc.OrgID)).
				AND(st.c.Kind.EQ(postgres.String(kindEmail))).
				AND(st.c.WindowStart.EQ(postgres.TimestampzT(utcDay(time.Now()))))).
			QueryContext(ctx, tx, &row)
		if err != nil {
			return fmt.Errorf("alert.postgres.SentToday: %w", err)
		}
		total = int(row.Total)
		return nil
	})
	return total, err
}

func (st *postgresStore) PruneCounts(ctx context.Context, before time.Time) error {
	return st.inTx(ctx, func(tx *sql.Tx, tc tenant.Context) error {
		_, err := st.c.DELETE().
			WHERE(st.c.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(st.c.WindowStart.LT(postgres.TimestampzT(before.UTC())))).
			ExecContext(ctx, tx)
		if err != nil {
			return fmt.Errorf("alert.postgres.PruneCounts: %w", err)
		}
		return nil
	})
}

func utcDay(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
