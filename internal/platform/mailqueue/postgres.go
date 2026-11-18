package mailqueue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"

	"altalune.id/openwa/internal/apperror"
	pdb "altalune.id/openwa/internal/platform/db"
	pgent "altalune.id/openwa/internal/platform/db/entity/postgres"
	"altalune.id/openwa/internal/platform/sealer"
	"altalune.id/openwa/mailer"
)

type postgresStore struct {
	pool   pdb.Pool
	q      *pgent.MailQueue
	sealer sealer.Sealer
}

func newPostgresStore(pool pdb.Pool, sl sealer.Sealer, schema, tablePrefix string) *postgresStore {
	return &postgresStore{pool: pool, q: pgent.NewMailQueue(schema, tablePrefix), sealer: sl}
}

type entryRow struct {
	ID            uuid.UUID `alias:"mail_queue.id"`
	Payload       []byte    `alias:"mail_queue.payload"`
	Status        string    `alias:"mail_queue.status"`
	Attempt       int32     `alias:"mail_queue.attempt"`
	NextAttemptAt time.Time `alias:"mail_queue.next_attempt_at"`
	LastError     string    `alias:"mail_queue.last_error"`
	CreatedAt     time.Time `alias:"mail_queue.created_at"`
}

func (r *entryRow) toEntry() Entry {
	return Entry{
		ID:            r.ID,
		Payload:       r.Payload,
		Status:        Status(r.Status),
		Attempt:       int(r.Attempt),
		NextAttemptAt: r.NextAttemptAt.UTC(),
		LastError:     r.LastError,
		CreatedAt:     r.CreatedAt.UTC(),
	}
}

func (s *postgresStore) writer(ctx context.Context) qrm.DB {
	if tx, ok := pdb.CurrentTx(ctx); ok {
		return tx
	}
	return s.pool.W
}

// Enqueue seals m and stores it pending; it joins the transaction on ctx when there is one.
func (s *postgresStore) Enqueue(ctx context.Context, m mailer.Message) (uuid.UUID, error) {
	if err := m.Validate(); err != nil {
		return uuid.Nil, err
	}
	switch {
	case m.To == "":
		return uuid.Nil, &mailer.IncompleteMessageError{Field: "to"}
	case m.TextBody == "" && m.HTMLBody == "":
		return uuid.Nil, &mailer.IncompleteMessageError{Field: "body"}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("mailqueue: id: %w", err)
	}
	plain, err := json.Marshal(m)
	if err != nil {
		return uuid.Nil, fmt.Errorf("mailqueue: encode: %w", err)
	}
	payload, err := s.sealer.Seal(plain, id[:])
	if err != nil {
		return uuid.Nil, fmt.Errorf("mailqueue: seal: %w", err)
	}
	now := time.Now().UTC()
	stmt := s.q.INSERT(s.q.ID, s.q.Payload, s.q.Status, s.q.Attempt, s.q.NextAttemptAt, s.q.LastError, s.q.CreatedAt).
		VALUES(id, payload, string(StatusPending), 0, now, "", now)
	if _, err := stmt.ExecContext(ctx, s.writer(ctx)); err != nil {
		return uuid.Nil, fmt.Errorf("mailqueue.postgres.Enqueue: %w", err)
	}
	return id, nil
}

// Open unseals the message of e, binding the seal to the entry id.
func (s *postgresStore) Open(e Entry) (mailer.Message, error) {
	if e.Payload == nil {
		return mailer.Message{}, &PayloadError{ID: e.ID.String(), Reason: "missing"}
	}
	plain, err := s.sealer.Open(e.Payload, e.ID[:])
	if err != nil {
		return mailer.Message{}, &PayloadError{ID: e.ID.String(), Reason: "failed authentication (wrong key or tampered)"}
	}
	var m mailer.Message
	if err := json.Unmarshal(plain, &m); err != nil {
		return mailer.Message{}, &PayloadError{ID: e.ID.String(), Reason: "not a message"}
	}
	return m, nil
}

// ClaimDue takes up to limit due entries exclusively, bumping Attempt and leasing each for ClaimLease.
func (s *postgresStore) ClaimDue(ctx context.Context, now time.Time, limit int) ([]Entry, error) {
	var claimed []Entry
	err := pdb.RunInTx(ctx, s.pool, func(ctx context.Context) error {
		tx, _ := pdb.CurrentTx(ctx)
		var inErr error
		claimed, inErr = s.claimDue(ctx, tx, now, limit)
		return inErr
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (s *postgresStore) claimDue(ctx context.Context, tx *sql.Tx, now time.Time, limit int) ([]Entry, error) {
	if err := s.reapExhausted(ctx, tx, now); err != nil {
		return nil, err
	}
	// SECURITY: SKIP LOCKED stops two replicas claiming one entry and sending it twice on a tick.
	due := postgres.SELECT(s.q.ID).
		FROM(s.q).
		WHERE(s.q.Status.EQ(postgres.String(string(StatusPending))).
			AND(s.q.Attempt.LT(postgres.Int(MaxAttempts))).
			AND(s.q.NextAttemptAt.LT_EQ(postgres.TimestampzT(now.UTC())))).
		ORDER_BY(s.q.NextAttemptAt.ASC()).
		LIMIT(claimBatch(limit)).
		FOR(postgres.UPDATE().SKIP_LOCKED())
	// NOTE: locked first, then updated by id; an IN (subquery) may be rescanned and claim more than limit.
	var locked []struct {
		ID uuid.UUID `alias:"mail_queue.id"`
	}
	if err := due.QueryContext(ctx, tx, &locked); err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return nil, fmt.Errorf("mailqueue.postgres.ClaimDue: lock due: %w", err)
	}
	if len(locked) == 0 {
		return []Entry{}, nil
	}
	ids := make([]postgres.Expression, len(locked))
	for i, l := range locked {
		ids[i] = postgres.UUID(l.ID)
	}

	stmt := s.q.UPDATE(s.q.Attempt, s.q.NextAttemptAt).
		SET(s.q.Attempt.ADD(postgres.Int(1)), postgres.TimestampzT(now.UTC().Add(ClaimLease))).
		WHERE(s.q.ID.IN(ids...)).
		RETURNING(s.q.ID, s.q.Payload, s.q.Status, s.q.Attempt, s.q.NextAttemptAt, s.q.LastError, s.q.CreatedAt)

	var rows []entryRow
	if err := stmt.QueryContext(ctx, tx, &rows); err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return nil, fmt.Errorf("mailqueue.postgres.ClaimDue: %w", err)
	}
	out := make([]Entry, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].toEntry())
	}
	return out, nil
}

// NOTE: without it a sender that dies on the last attempt strands the row, and its payload, in pending.
func (s *postgresStore) reapExhausted(ctx context.Context, tx *sql.Tx, now time.Time) error {
	stmt := s.q.UPDATE(s.q.Status, s.q.Payload, s.q.SettledAt).
		SET(postgres.String(string(StatusFailed)), pgent.NullBytea(), postgres.TimestampzT(now.UTC())).
		WHERE(s.q.Status.EQ(postgres.String(string(StatusPending))).
			AND(s.q.Attempt.GT_EQ(postgres.Int(MaxAttempts))).
			AND(s.q.NextAttemptAt.LT_EQ(postgres.TimestampzT(now.UTC()))))
	if _, err := stmt.ExecContext(ctx, tx); err != nil {
		return fmt.Errorf("mailqueue.postgres.ClaimDue: reap exhausted: %w", err)
	}
	return nil
}

// Succeed settles the claimed entry e as sent and erases its payload.
func (s *postgresStore) Succeed(ctx context.Context, e Entry, at time.Time) error {
	stmt := s.q.UPDATE(s.q.Status, s.q.Payload, s.q.SettledAt, s.q.LastError).
		SET(postgres.String(string(StatusSent)), pgent.NullBytea(), postgres.TimestampzT(at.UTC()), postgres.String("")).
		WHERE(s.claimedRow(e))
	return s.settle(ctx, e, stmt)
}

// Fail reschedules the claimed entry e for retryAt, or settles it as failed with its payload erased at MaxAttempts.
func (s *postgresStore) Fail(ctx context.Context, e Entry, retryAt time.Time, cause string) error {
	if e.Attempt >= MaxAttempts {
		return s.GiveUp(ctx, e, retryAt, cause)
	}
	stmt := s.q.UPDATE(s.q.NextAttemptAt, s.q.LastError).
		SET(postgres.TimestampzT(retryAt.UTC()), postgres.String(apperror.TruncateCause(cause, MaxErrorLen))).
		WHERE(s.claimedRow(e))
	return s.settle(ctx, e, stmt)
}

// GiveUp settles the claimed entry e as failed at at, erasing its payload.
func (s *postgresStore) GiveUp(ctx context.Context, e Entry, at time.Time, cause string) error {
	stmt := s.q.UPDATE(s.q.Status, s.q.Payload, s.q.SettledAt, s.q.LastError).
		SET(postgres.String(string(StatusFailed)), pgent.NullBytea(), postgres.TimestampzT(at.UTC()),
			postgres.String(apperror.TruncateCause(cause, MaxErrorLen))).
		WHERE(s.claimedRow(e))
	return s.settle(ctx, e, stmt)
}

// Release hands the claimed entry e back as due now and returns the attempt its claim took.
func (s *postgresStore) Release(ctx context.Context, e Entry) error {
	stmt := s.q.UPDATE(s.q.Attempt, s.q.NextAttemptAt).
		SET(s.q.Attempt.SUB(postgres.Int(1)), postgres.TimestampzT(time.Now().UTC())).
		WHERE(s.claimedRow(e).AND(s.q.Attempt.GT(postgres.Int(0))))
	return s.settle(ctx, e, stmt)
}

// PruneSettled deletes sent and failed entries settled before before and returns how many it deleted.
func (s *postgresStore) PruneSettled(ctx context.Context, before time.Time) (int64, error) {
	stmt := s.q.DELETE().
		WHERE(s.q.Status.NOT_EQ(postgres.String(string(StatusPending))).
			AND(s.q.SettledAt.LT(postgres.TimestampzT(before.UTC()))))
	res, err := stmt.ExecContext(ctx, s.writer(ctx))
	if err != nil {
		return 0, fmt.Errorf("mailqueue.postgres.PruneSettled: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("mailqueue.postgres.PruneSettled: rows affected: %w", err)
	}
	return n, nil
}

// SECURITY: the attempt fences the settle, so a sender whose lease expired cannot overwrite a re-claim.
func (s *postgresStore) claimedRow(e Entry) postgres.BoolExpression {
	return s.q.ID.EQ(postgres.UUID(e.ID)).
		AND(s.q.Status.EQ(postgres.String(string(StatusPending)))).
		AND(s.q.Attempt.EQ(postgres.Int(int64(e.Attempt))))
}

type pgExecutable interface {
	ExecContext(ctx context.Context, db qrm.Executable) (sql.Result, error)
}

func (s *postgresStore) settle(ctx context.Context, e Entry, stmt pgExecutable) error {
	res, err := stmt.ExecContext(ctx, s.writer(ctx))
	if err != nil {
		return fmt.Errorf("mailqueue.postgres: settle: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("mailqueue.postgres: settle: rows affected: %w", err)
	}
	if n == 0 {
		return &SettleRefusedError{ID: e.ID.String()}
	}
	return nil
}
