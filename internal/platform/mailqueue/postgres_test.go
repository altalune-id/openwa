package mailqueue_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/config"
	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/platform/mailqueue"
	"altalune.id/openwa/internal/platform/sealer"
	"altalune.id/openwa/internal/testutil/pgtest"
	"altalune.id/openwa/mailer"
	"altalune.id/openwa/schema"
)

type fixture struct {
	db     *sql.DB
	pool   db.Pool
	prefix string
	store  mailqueue.Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)
	cfg := config.Defaults()
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true
	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))

	key, err := sealer.GenerateKey()
	require.NoError(t, err)
	sl, err := sealer.New(key)
	require.NoError(t, err)
	pool := db.Pool{W: sqlDB, R: sqlDB}
	store := mailqueue.NewStore(db.DBConfig{Schema: h.Schema, TablePrefix: cfg.DB.TablePrefix}, pool, sl)
	return &fixture{db: sqlDB, pool: pool, prefix: cfg.DB.TablePrefix, store: store}
}

func message() mailer.Message {
	return mailer.Message{To: "carol@example.com", Subject: "Hello", TextBody: "link https://example.com/t/secret-token",
		Headers: map[string]string{"List-Unsubscribe": "<https://example.com/u?t=abc>"}}
}

type row struct {
	payload   []byte
	status    string
	attempt   int
	lastError string
	settled   sql.NullTime
	next      time.Time
}

func (f *fixture) row(t *testing.T, id uuid.UUID) row {
	t.Helper()
	var r row
	require.NoError(t, f.db.QueryRowContext(t.Context(),
		`SELECT payload, status, attempt, last_error, settled_at, next_attempt_at FROM `+f.prefix+`mail_queue WHERE id = $1`, id).
		Scan(&r.payload, &r.status, &r.attempt, &r.lastError, &r.settled, &r.next))
	return r
}

func (f *fixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	_, err := f.db.ExecContext(t.Context(), strings.ReplaceAll(query, "mail_queue", f.prefix+"mail_queue"), args...)
	require.NoError(t, err)
}

func (f *fixture) count(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, f.db.QueryRowContext(t.Context(), `SELECT count(*) FROM `+f.prefix+`mail_queue`).Scan(&n))
	return n
}

func later() time.Time { return time.Now().UTC().Add(time.Second) }

func (f *fixture) enqueueAndClaim(t *testing.T) (uuid.UUID, mailqueue.Entry) {
	t.Helper()
	id, err := f.store.Enqueue(t.Context(), message())
	require.NoError(t, err)
	got, err := f.store.ClaimDue(t.Context(), later(), 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	return id, got[0]
}

func TestEnqueue_StoresASealedPendingRow(t *testing.T) {
	f := newFixture(t)
	id, err := f.store.Enqueue(t.Context(), message())
	require.NoError(t, err)

	r := f.row(t, id)
	assert.Equal(t, "pending", r.status)
	assert.False(t, json.Valid(r.payload))
	assert.NotContains(t, string(r.payload), "secret-token")
	assert.NotContains(t, string(r.payload), "carol@example.com")
}

func TestEnqueue_RejectsAnIncompleteOrUnsafeMessage(t *testing.T) {
	f := newFixture(t)
	for name, m := range map[string]mailer.Message{
		"no to":     {Subject: "s", TextBody: "b"},
		"no body":   {To: "a@example.com", Subject: "s"},
		"injection": {To: "a@example.com", Subject: "s\r\nBcc: x@evil.test", TextBody: "b"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.store.Enqueue(t.Context(), m)
			require.Error(t, err)
		})
	}
	assert.Zero(t, f.count(t))
}

func TestEnqueue_RollsBackWithTheCallersTransaction(t *testing.T) {
	f := newFixture(t)
	errAbort := errors.New("abort")
	err := db.RunInTx(t.Context(), f.pool, func(ctx context.Context) error {
		if _, err := f.store.Enqueue(ctx, message()); err != nil {
			return err
		}
		return errAbort
	})
	require.ErrorIs(t, err, errAbort)
	assert.Zero(t, f.count(t))

	require.NoError(t, db.RunInTx(t.Context(), f.pool, func(ctx context.Context) error {
		_, err := f.store.Enqueue(ctx, message())
		return err
	}))
	assert.Equal(t, 1, f.count(t))
}

func TestEnqueue_WakesOnlyAfterTheCommit(t *testing.T) {
	f := newFixture(t)
	waker := mailqueue.NewWaker()
	store := mailqueue.WakeOnEnqueue(f.store, waker)
	woke := func() bool {
		select {
		case <-mailqueue.WakerChan(waker):
			return true
		default:
			return false
		}
	}

	_ = db.RunInTx(t.Context(), f.pool, func(ctx context.Context) error {
		_, err := store.Enqueue(ctx, message())
		require.NoError(t, err)
		assert.False(t, woke(), "no wake before the commit")
		return errors.New("roll back")
	})
	assert.False(t, woke(), "a rolled-back enqueue never wakes")

	require.NoError(t, db.RunInTx(t.Context(), f.pool, func(ctx context.Context) error {
		_, err := store.Enqueue(ctx, message())
		return err
	}))
	assert.True(t, woke())
}

func TestClaimDue_OpenSucceedErasesThePayload(t *testing.T) {
	f := newFixture(t)
	id, e := f.enqueueAndClaim(t)
	assert.Equal(t, id, e.ID)
	assert.Equal(t, 1, e.Attempt)

	msg, err := f.store.Open(e)
	require.NoError(t, err)
	assert.Equal(t, message(), msg)

	require.NoError(t, f.store.Succeed(t.Context(), e, time.Now()))
	r := f.row(t, id)
	assert.Equal(t, "sent", r.status)
	assert.Nil(t, r.payload)
	assert.True(t, r.settled.Valid)

	assert.True(t, mailqueue.IsSettleRefusedError(f.store.Succeed(t.Context(), e, time.Now())))
}

func TestOpen_RefusesAPayloadMovedToAnotherEntry(t *testing.T) {
	f := newFixture(t)
	_, e := f.enqueueAndClaim(t)
	e.ID = uuid.Must(uuid.NewV7())
	_, err := f.store.Open(e)
	assert.True(t, mailqueue.IsPayloadError(err), "the seal is bound to the entry id")
}

func TestFail_KeepsPendingThenFailsForGood(t *testing.T) {
	f := newFixture(t)
	id, e := f.enqueueAndClaim(t)
	retryAt := time.Now().UTC().Add(time.Hour)
	require.NoError(t, f.store.Fail(t.Context(), e, retryAt, "network: dial"))

	r := f.row(t, id)
	assert.Equal(t, "pending", r.status)
	assert.NotNil(t, r.payload)
	assert.Equal(t, "network: dial", r.lastError)
	assert.WithinDuration(t, retryAt, r.next, time.Millisecond)

	none, err := f.store.ClaimDue(t.Context(), later(), 10)
	require.NoError(t, err)
	assert.Empty(t, none, "backoff holds the entry until retryAt")

	f.exec(t, `UPDATE mail_queue SET attempt = $2, next_attempt_at = now() WHERE id = $1`, id, mailqueue.MaxAttempts-1)
	last, err := f.store.ClaimDue(t.Context(), later(), 10)
	require.NoError(t, err)
	require.Len(t, last, 1)
	assert.Equal(t, mailqueue.MaxAttempts, last[0].Attempt)
	require.NoError(t, f.store.Fail(t.Context(), last[0], retryAt, "smtp_reply 550"))

	r = f.row(t, id)
	assert.Equal(t, "failed", r.status)
	assert.Nil(t, r.payload)
	assert.True(t, r.settled.Valid)
}

func TestFail_TruncatesTheCause(t *testing.T) {
	f := newFixture(t)
	id, e := f.enqueueAndClaim(t)
	require.NoError(t, f.store.Fail(t.Context(), e, time.Now(), strings.Repeat("x", 4*mailqueue.MaxErrorLen)))
	assert.Len(t, f.row(t, id).lastError, mailqueue.MaxErrorLen)
}

func TestClaimDue_LeaseExpiryMakesTheRowClaimableAgain(t *testing.T) {
	f := newFixture(t)
	id, first := f.enqueueAndClaim(t)

	none, err := f.store.ClaimDue(t.Context(), later(), 10)
	require.NoError(t, err)
	assert.Empty(t, none, "a leased row is not claimable")

	again, err := f.store.ClaimDue(t.Context(), time.Now().UTC().Add(mailqueue.ClaimLease+time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, again, 1)
	assert.Equal(t, id, again[0].ID)
	assert.Equal(t, 2, again[0].Attempt)

	assert.True(t, mailqueue.IsSettleRefusedError(f.store.Succeed(t.Context(), first, time.Now())),
		"a superseded claim cannot settle")
	require.NoError(t, f.store.Succeed(t.Context(), again[0], time.Now()))
}

func TestClaimDue_ReapsAnExhaustedLease(t *testing.T) {
	f := newFixture(t)
	id, err := f.store.Enqueue(t.Context(), message())
	require.NoError(t, err)
	f.exec(t, `UPDATE mail_queue SET attempt = $2 WHERE id = $1`, id, mailqueue.MaxAttempts)

	got, err := f.store.ClaimDue(t.Context(), later(), 10)
	require.NoError(t, err)
	assert.Empty(t, got)
	r := f.row(t, id)
	assert.Equal(t, "failed", r.status)
	assert.Nil(t, r.payload)
	assert.True(t, r.settled.Valid)
}

func TestClaimDue_TwoClaimersNeverShareARow(t *testing.T) {
	f := newFixture(t)
	const total = 40
	for range total {
		_, err := f.store.Enqueue(t.Context(), message())
		require.NoError(t, err)
	}

	var (
		mu   sync.Mutex
		seen = map[uuid.UUID]int{}
		wg   sync.WaitGroup
	)
	start := make(chan struct{})
	for range 4 {
		wg.Go(func() {
			<-start
			for {
				got, err := f.store.ClaimDue(context.Background(), later(), 3)
				if err != nil || len(got) == 0 {
					return
				}
				mu.Lock()
				for _, e := range got {
					seen[e.ID]++
				}
				mu.Unlock()
			}
		})
	}
	close(start)
	wg.Wait()

	assert.Len(t, seen, total)
	for id, n := range seen {
		assert.Equal(t, 1, n, "entry %s claimed twice", id)
	}
}

func TestRelease_ReturnsTheAttemptAndMakesTheEntryDue(t *testing.T) {
	f := newFixture(t)
	id, e := f.enqueueAndClaim(t)
	f.exec(t, `UPDATE mail_queue SET last_error = 'network: dial' WHERE id = $1`, id)
	require.Equal(t, 1, f.row(t, id).attempt)

	require.NoError(t, f.store.Release(t.Context(), e))
	r := f.row(t, id)
	assert.Equal(t, "pending", r.status)
	assert.Equal(t, 0, r.attempt, "a shutdown does not cost an attempt")
	assert.Equal(t, "network: dial", r.lastError)
	assert.NotNil(t, r.payload)

	again, err := f.store.ClaimDue(t.Context(), later(), 10)
	require.NoError(t, err)
	require.Len(t, again, 1, "a released entry is due at once")
	assert.Equal(t, 1, again[0].Attempt)
}

func TestRelease_RefusesAStaleClaim(t *testing.T) {
	f := newFixture(t)
	id, first := f.enqueueAndClaim(t)
	again, err := f.store.ClaimDue(t.Context(), time.Now().UTC().Add(mailqueue.ClaimLease+time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, again, 1)

	assert.True(t, mailqueue.IsSettleRefusedError(f.store.Release(t.Context(), first)))
	assert.Equal(t, 2, f.row(t, id).attempt, "the stale release left the re-claim alone")
}

func TestRelease_RefusesASettledEntry(t *testing.T) {
	f := newFixture(t)
	_, e := f.enqueueAndClaim(t)
	require.NoError(t, f.store.Succeed(t.Context(), e, time.Now()))
	assert.True(t, mailqueue.IsSettleRefusedError(f.store.Release(t.Context(), e)))
}

func TestRelease_NeverTakesTheAttemptBelowZero(t *testing.T) {
	f := newFixture(t)
	id, e := f.enqueueAndClaim(t)
	f.exec(t, `UPDATE mail_queue SET attempt = 0 WHERE id = $1`, id)
	e.Attempt = 0
	assert.True(t, mailqueue.IsSettleRefusedError(f.store.Release(t.Context(), e)))
	assert.Equal(t, 0, f.row(t, id).attempt)
}

func TestGiveUp_SettlesFailedOnTheFirstAttempt(t *testing.T) {
	f := newFixture(t)
	id, e := f.enqueueAndClaim(t)

	at := time.Now().UTC().Truncate(time.Microsecond)
	require.NoError(t, f.store.GiveUp(t.Context(), e, at, "http_status 422"))
	r := f.row(t, id)
	assert.Equal(t, "failed", r.status)
	assert.Nil(t, r.payload, "a given-up entry keeps no message")
	assert.Equal(t, "http_status 422", r.lastError)
	assert.WithinDuration(t, at, r.settled.Time, time.Millisecond)
	assert.Equal(t, 1, r.attempt)

	assert.True(t, mailqueue.IsSettleRefusedError(f.store.GiveUp(t.Context(), e, at, "again")), "a settled entry cannot be settled twice")
}

func TestPruneSettled_DeletesOnlyOldSettledEntries(t *testing.T) {
	f := newFixture(t)
	pending, err := f.store.Enqueue(t.Context(), message())
	require.NoError(t, err)
	oldSent, err := f.store.Enqueue(t.Context(), message())
	require.NoError(t, err)
	newSent, err := f.store.Enqueue(t.Context(), message())
	require.NoError(t, err)
	f.exec(t, `UPDATE mail_queue SET status = 'sent', payload = NULL, settled_at = now() - interval '40 days' WHERE id = $1`, oldSent)
	f.exec(t, `UPDATE mail_queue SET status = 'failed', payload = NULL, settled_at = now() WHERE id = $1`, newSent)

	n, err := f.store.PruneSettled(t.Context(), time.Now().Add(-mailqueue.Retention))
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	assert.Equal(t, 2, f.count(t))
	assert.Equal(t, "pending", f.row(t, pending).status)
	assert.Equal(t, "failed", f.row(t, newSent).status)
}

func TestSchema_RefusesASettledRowThatKeepsItsPayload(t *testing.T) {
	f := newFixture(t)
	id, err := f.store.Enqueue(t.Context(), message())
	require.NoError(t, err)
	_, err = f.db.ExecContext(t.Context(), `UPDATE `+f.prefix+`mail_queue SET status = 'sent' WHERE id = $1`, id)
	require.Error(t, err, "the CHECK constraint keeps a settled row from holding its message")
}

func TestWorker_EndToEnd(t *testing.T) {
	f := newFixture(t)
	id, err := f.store.Enqueue(t.Context(), message())
	require.NoError(t, err)

	m := &recordingMailer{}
	w := mailqueue.NewWorker(f.store, m, nil, mailqueue.WorkerOpts{Tick: 10 * time.Millisecond})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	require.Eventually(t, func() bool { return f.row(t, id).status == "sent" }, 5*time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)
	assert.Equal(t, 1, m.count())
}

type recordingMailer struct {
	mu sync.Mutex
	n  int
}

func (r *recordingMailer) Send(context.Context, mailer.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.n++
	return nil
}

func (r *recordingMailer) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}
