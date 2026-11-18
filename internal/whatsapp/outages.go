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

	pdb "altalune.id/openwa/internal/platform/db"
	pgent "altalune.id/openwa/internal/platform/db/entity/postgres"
	"altalune.id/openwa/internal/platform/tenant"
)

// Outage is one number a disconnect alert reports.
type Outage struct {
	DeviceID       uuid.UUID
	PublicID       string
	Name           string
	Phone          string
	State          State
	DisconnectedAt time.Time
}

// Attention is one number the project banner names.
type Attention struct {
	PublicID string
	Name     string
}

// Outages finds the numbers that stay disconnected past a grace, and claims each outage for one alert.
type Outages struct {
	pc *tenant.PgConn
	s  *pgent.WhatsappSessions
	d  *pgent.Devices
}

// NewOutages builds the Postgres outage queries.
func NewOutages(cfg pdb.DBConfig, pc *tenant.PgConn) *Outages {
	return &Outages{pc: pc, s: pgent.NewWhatsappSessions(cfg.Schema, cfg.TablePrefix), d: pgent.NewDevices(cfg.Schema, cfg.TablePrefix)}
}

func (o *Outages) down(now time.Time, grace time.Duration) postgres.BoolExpression {
	return o.s.State.IN(postgres.String(string(StateDisconnected)), postgres.String(string(StateLoggedOut))).
		AND(o.s.LastConnectedAt.IS_NOT_NULL()).
		AND(o.s.Reason.NOT_EQ(postgres.String(ReasonPaired))).
		AND(o.s.DisconnectedAt.LT_EQ(postgres.TimestampzT(now.Add(-grace))))
}

func (o *Outages) due(now time.Time, grace, cooldown time.Duration) postgres.BoolExpression {
	return o.down(now, grace).AND(
		o.s.DisconnectNotifiedAt.IS_NULL().OR(
			o.s.DisconnectNotifiedAt.LT(o.s.LastConnectedAt).
				AND(o.s.DisconnectNotifiedAt.LT_EQ(postgres.TimestampzT(now.Add(-cooldown))))))
}

func (o *Outages) inTx(ctx context.Context, fn func(tx *sql.Tx, tc tenant.Context) error) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return fn(tx, tc)
	}
	tx, err := o.pc.BeginTenanted(ctx, tc)
	if err != nil {
		return fmt.Errorf("whatsapp.outages: begin: %w", err)
	}
	if err := fn(tx, tc); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("whatsapp.outages: commit: %w", err)
	}
	return nil
}

// ProjectsNeedingAlert lists the projects of the org on ctx with an outage due an alert.
func (o *Outages) ProjectsNeedingAlert(ctx context.Context, grace, cooldown time.Duration) ([]uuid.UUID, error) {
	var out []uuid.UUID
	err := o.inTx(ctx, func(tx *sql.Tx, tc tenant.Context) error {
		var rows []struct {
			ProjectID uuid.UUID `alias:"whatsapp_sessions.project_id"`
		}
		err := postgres.SELECT(o.s.ProjectID).DISTINCT().FROM(o.s).
			WHERE(o.s.OrgID.EQ(postgres.UUID(tc.OrgID)).AND(o.due(time.Now().UTC(), grace, cooldown))).
			QueryContext(ctx, tx, &rows)
		if err != nil && !errors.Is(err, qrm.ErrNoRows) {
			return fmt.Errorf("whatsapp.outages.ProjectsNeedingAlert: %w", err)
		}
		out = make([]uuid.UUID, len(rows))
		for i, r := range rows {
			out[i] = r.ProjectID
		}
		return nil
	})
	return out, err
}

type outageRow struct {
	DeviceID       uuid.UUID `alias:"whatsapp_sessions.device_id"`
	Phone          string    `alias:"whatsapp_sessions.phone"`
	State          string    `alias:"whatsapp_sessions.state"`
	DisconnectedAt time.Time `alias:"whatsapp_sessions.disconnected_at"`
	PublicID       string    `alias:"devices.public_id"`
	Name           string    `alias:"devices.name"`
}

// Claim marks every due outage of the project on ctx notified and returns them; a concurrent claim of the same rows returns none. It never bumps the session version.
func (o *Outages) Claim(ctx context.Context, grace, cooldown time.Duration) ([]Outage, error) {
	var out []Outage
	err := o.inTx(ctx, func(tx *sql.Tx, tc tenant.Context) error {
		// NOTE: the app clock, as for last_connected_at and the outage start, so the reconnect check never compares two clocks.
		now := time.Now().UTC()
		var rows []outageRow
		err := o.s.UPDATE(o.s.DisconnectNotifiedAt).SET(postgres.TimestampzT(now)).
			FROM(o.d).
			WHERE(o.d.ID.EQ(o.s.DeviceID).AND(o.d.OrgID.EQ(o.s.OrgID)).
				AND(o.s.OrgID.EQ(postgres.UUID(tc.OrgID))).
				AND(o.s.ProjectID.EQ(postgres.UUID(tc.ProjectID))).
				AND(o.due(now, grace, cooldown))).
			RETURNING(o.s.DeviceID, o.s.Phone, o.s.State, o.s.DisconnectedAt, o.d.PublicID, o.d.Name).
			QueryContext(ctx, tx, &rows)
		if err != nil && !errors.Is(err, qrm.ErrNoRows) {
			return fmt.Errorf("whatsapp.outages.Claim: %w", err)
		}
		out = make([]Outage, len(rows))
		for i, r := range rows {
			out[i] = Outage{DeviceID: r.DeviceID, PublicID: r.PublicID, Name: r.Name, Phone: r.Phone, State: State(r.State), DisconnectedAt: r.DisconnectedAt.UTC()}
		}
		return nil
	})
	return out, err
}

// NeedingAttention lists the numbers of the project on ctx that have been down past grace, by name.
func (o *Outages) NeedingAttention(ctx context.Context, grace time.Duration) ([]Attention, error) {
	var out []Attention
	err := o.inTx(ctx, func(tx *sql.Tx, tc tenant.Context) error {
		var rows []struct {
			PublicID string `alias:"devices.public_id"`
			Name     string `alias:"devices.name"`
		}
		err := postgres.SELECT(o.d.PublicID, o.d.Name).
			FROM(o.s.INNER_JOIN(o.d, o.d.ID.EQ(o.s.DeviceID).AND(o.d.OrgID.EQ(o.s.OrgID)))).
			WHERE(o.s.OrgID.EQ(postgres.UUID(tc.OrgID)).
				AND(o.s.ProjectID.EQ(postgres.UUID(tc.ProjectID))).
				AND(o.down(time.Now().UTC(), grace))).
			ORDER_BY(o.d.Name.ASC(), o.d.PublicID.ASC()).
			QueryContext(ctx, tx, &rows)
		if err != nil && !errors.Is(err, qrm.ErrNoRows) {
			return fmt.Errorf("whatsapp.outages.NeedingAttention: %w", err)
		}
		out = make([]Attention, len(rows))
		for i, r := range rows {
			out[i] = Attention{PublicID: r.PublicID, Name: r.Name}
		}
		return nil
	})
	return out, err
}
