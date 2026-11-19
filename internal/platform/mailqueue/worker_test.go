package mailqueue

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/mailer"
)

type failCall struct {
	e       Entry
	retryAt time.Time
	cause   string
	gaveUp  bool
}

type fakeStore struct {
	mu        sync.Mutex
	claimed   []Entry
	claimErr  error
	msg       mailer.Message
	openErr   error
	settleErr error
	succeeded []Entry
	failed    []failCall
	released  []Entry
	limits    []int
	prunedTo  []time.Time
	pruneErr  error
}

func (f *fakeStore) ClaimDue(_ context.Context, _ time.Time, limit int) ([]Entry, error) {
	f.limits = append(f.limits, limit)
	n := min(limit, len(f.claimed))
	out := f.claimed[:n]
	f.claimed = f.claimed[n:]
	return out, f.claimErr
}

func (f *fakeStore) GiveUp(_ context.Context, e Entry, at time.Time, cause string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, failCall{e: e, retryAt: at, cause: cause, gaveUp: true})
	return f.settleErr
}

func (f *fakeStore) PruneSettled(_ context.Context, before time.Time) (int64, error) {
	f.prunedTo = append(f.prunedTo, before)
	return 3, f.pruneErr
}

func (f *fakeStore) Open(Entry) (mailer.Message, error) { return f.msg, f.openErr }

func (f *fakeStore) Succeed(_ context.Context, e Entry, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.succeeded = append(f.succeeded, e)
	return f.settleErr
}

func (f *fakeStore) Fail(_ context.Context, e Entry, retryAt time.Time, cause string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, failCall{e: e, retryAt: retryAt, cause: cause})
	return f.settleErr
}

func (f *fakeStore) Release(_ context.Context, e Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, e)
	return f.settleErr
}

func (f *fakeStore) snapshot() (succeeded []Entry, failed []failCall, released []Entry) { //nolint:nonamedreturns // three lists differ in role
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.succeeded, f.failed, f.released
}

type fakeMailer struct {
	mu   sync.Mutex
	err  error
	sent []mailer.Message
}

func (f *fakeMailer) Send(_ context.Context, m mailer.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return f.err
}

type blockingMailer struct {
	started chan struct{}
}

func (b *blockingMailer) Send(ctx context.Context, _ mailer.Message) error {
	b.started <- struct{}{}
	<-ctx.Done()
	return fmt.Errorf("mailer: smtp data: %w", &net.OpError{Op: "read", Err: errors.New("i/o timeout")})
}

const (
	testTo   = "alice.secret@example.com"
	testBody = "Your number Sales line was disconnected; fix it at https://example.com/fix/tok_abcdef123456"
)

func testMessage() mailer.Message {
	return mailer.Message{To: testTo, Subject: "Sales line was disconnected", TextBody: testBody, HTMLBody: "<p>" + testBody + "</p>"}
}

func claimed(attempt int) Entry {
	return Entry{ID: uuid.Must(uuid.NewV7()), Attempt: attempt, Status: StatusPending, Payload: []byte{1}}
}

func logBuffer() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func TestWorkerIdleWhenUnconfigured(t *testing.T) {
	assert.NoError(t, NewWorker(nil, nil, nil, WorkerOpts{}).Run(context.Background()))
}

func TestWorkerName(t *testing.T) {
	assert.Equal(t, WorkerName, NewWorker(nil, nil, nil, WorkerOpts{}).Name())
}

func TestWorkerSendSucceeds(t *testing.T) {
	store := &fakeStore{claimed: []Entry{claimed(1)}, msg: testMessage()}
	m := &fakeMailer{}
	NewWorker(store, m, nil, WorkerOpts{}).sweep(context.Background())

	require.Len(t, store.succeeded, 1)
	assert.Empty(t, store.failed)
	require.Len(t, m.sent, 1)
	assert.Equal(t, testTo, m.sent[0].To)
}

func TestWorkerSendsTheEntryIDAsIdempotencyKey(t *testing.T) {
	keys := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys <- r.Header.Get("Idempotency-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	sender, err := mailer.NewResend(mailer.Config{From: "noreply@example.com", Resend: mailer.ResendConfig{APIKey: "re_test", Endpoint: srv.URL, MaxAttempts: 1}})
	require.NoError(t, err)

	e := claimed(1)
	store := &fakeStore{claimed: []Entry{e}, msg: testMessage()}
	NewWorker(store, sender, nil, WorkerOpts{}).sweep(context.Background())

	require.Len(t, store.succeeded, 1)
	assert.Equal(t, e.ID.String(), <-keys)
}

func TestWorkerSendFailsKeepsPendingWithBackoff(t *testing.T) {
	e := claimed(1)
	store := &fakeStore{claimed: []Entry{e}, msg: testMessage()}
	before := time.Now().UTC()
	NewWorker(store, &fakeMailer{err: errors.New("connection refused")}, nil, WorkerOpts{}).sweep(context.Background())

	assert.Empty(t, store.succeeded)
	require.Len(t, store.failed, 1)
	minWait := backoffBase(e.Attempt+1) * 9 / 10
	assert.False(t, store.failed[0].retryAt.Before(before.Add(minWait)), "retry waits at least the backoff")
	assert.Contains(t, store.failed[0].cause, "connection refused")
}

func TestWorkerHonorsLongerRetryAfter(t *testing.T) {
	store := &fakeStore{claimed: []Entry{claimed(1)}, msg: testMessage()}
	err := &mailer.ResendAPIError{StatusCode: 429, RetryAfter: 3 * time.Hour}
	before := time.Now().UTC()
	NewWorker(store, &fakeMailer{err: err}, nil, WorkerOpts{}).sweep(context.Background())

	require.Len(t, store.failed, 1)
	assert.False(t, store.failed[0].retryAt.Before(before.Add(3*time.Hour)))
}

func TestWorkerLastAttemptLogsGiveUp(t *testing.T) {
	log, buf := logBuffer()
	store := &fakeStore{claimed: []Entry{claimed(MaxAttempts)}, msg: testMessage()}
	NewWorker(store, &fakeMailer{err: errors.New("down")}, log, WorkerOpts{}).sweep(context.Background())

	require.Len(t, store.failed, 1)
	assert.True(t, store.failed[0].gaveUp)
	assert.Contains(t, buf.String(), "gave up")
}

func TestWorkerOpenFailureGivesUp(t *testing.T) {
	store := &fakeStore{claimed: []Entry{claimed(1)}, openErr: &PayloadError{ID: "x", Reason: "failed authentication"}}
	m := &fakeMailer{}
	NewWorker(store, m, nil, WorkerOpts{}).sweep(context.Background())

	assert.Empty(t, m.sent)
	require.Len(t, store.failed, 1)
	assert.True(t, store.failed[0].gaveUp, "a payload that cannot be opened never opens on a retry")
	assert.True(t, strings.HasPrefix(store.failed[0].cause, "payload"))
}

func TestWorkerClaimError(t *testing.T) {
	log, buf := logBuffer()
	m := &fakeMailer{}
	NewWorker(&fakeStore{claimErr: errors.New("boom")}, m, log, WorkerOpts{}).sweep(context.Background())
	assert.Empty(t, m.sent)
	assert.Contains(t, buf.String(), "claim due")
}

func TestWorkerSettleErrorIsLogged(t *testing.T) {
	log, buf := logBuffer()
	store := &fakeStore{claimed: []Entry{claimed(1)}, msg: testMessage(), settleErr: &SettleRefusedError{ID: "x"}}
	NewWorker(store, &fakeMailer{}, log, WorkerOpts{}).sweep(context.Background())
	assert.Contains(t, buf.String(), "settle sent")

	buf.Reset()
	store.claimed = []Entry{claimed(1)}
	NewWorker(store, &fakeMailer{err: errors.New("x")}, log, WorkerOpts{}).sweep(context.Background())
	assert.Contains(t, buf.String(), "settle failed send")
}

func TestWorkerSkipsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &fakeStore{claimed: []Entry{claimed(1)}, msg: testMessage()}
	m := &fakeMailer{}
	NewWorker(store, m, nil, WorkerOpts{}).sweep(ctx)
	assert.Empty(t, m.sent)
}

func TestWorkerStoredAndLoggedErrorHoldNoMessageText(t *testing.T) {
	msg := testMessage()
	long := strings.Repeat("x", 4*MaxErrorLen)
	cases := map[string]error{
		"plain":       fmt.Errorf("mailer: smtp rcpt: server said %s is unknown; body %q", msg.To, msg.TextBody),
		"smtp reply":  fmt.Errorf("mailer: smtp rcpt: %w", &textproto.Error{Code: 550, Msg: "5.1.1 <" + msg.To + "> unknown"}),
		"resend body": &mailer.ResendAPIError{StatusCode: 422, Body: `{"message":"invalid to ` + msg.To + `: ` + msg.Subject + `"}`},
		"other addr":  errors.New("bounce for bob.other@example.org " + long),
		"subject":     errors.New("rejected: " + msg.Subject),
		"html line":   errors.New("rejected: " + msg.HTMLBody),
	}
	for name, sendErr := range cases {
		t.Run(name, func(t *testing.T) {
			log, buf := logBuffer()
			store := &fakeStore{claimed: []Entry{claimed(2)}, msg: msg}
			NewWorker(store, &fakeMailer{err: sendErr}, log, WorkerOpts{}).sweep(context.Background())

			require.Len(t, store.failed, 1)
			stored := store.failed[0].cause
			logged := buf.String()
			assert.LessOrEqual(t, len(stored), MaxErrorLen)
			for _, secret := range []string{msg.To, "@example", msg.Subject, "tok_abcdef123456", "Sales line"} {
				assert.NotContains(t, stored, secret)
				assert.NotContains(t, logged, secret)
			}
			assert.Contains(t, logged, store.failed[0].e.ID.String())
		})
	}
}

func TestDescribe(t *testing.T) {
	msg := testMessage()
	cases := []struct {
		name string
		err  error
		kind string
		code string
	}{
		{"http status", &mailer.ResendAPIError{StatusCode: 503}, "http_status", "503"},
		{"smtp", &textproto.Error{Code: 550, Msg: "5.7.1 relay denied"}, "smtp_reply", "550 5.7.1"},
		{"smtp no enhanced", &textproto.Error{Code: 421, Msg: "busy"}, "smtp_reply", "421"},
		{"deadline", fmt.Errorf("wrap: %w", context.DeadlineExceeded), "timeout", ""},
		{"canceled", fmt.Errorf("wrap: %w", context.Canceled), "canceled", ""},
		{"incomplete", &mailer.IncompleteMessageError{Field: "to"}, "invalid_message", ""},
		{"injection", &mailer.HeaderInjectionError{Field: "To"}, "invalid_message", ""},
		{"payload", &PayloadError{ID: "x", Reason: "missing"}, "payload", ""},
		{"other", errors.New("boom"), "send", ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := describe(tt.err, msg)
			assert.Equal(t, tt.kind, f.kind)
			assert.Equal(t, tt.code, f.code)
			assert.True(t, strings.HasPrefix(f.text, tt.kind))
		})
	}
}

func TestWorkerClaimsOnlyWhatItSendsAtOnce(t *testing.T) {
	var due []Entry
	for range 9 {
		due = append(due, claimed(1))
	}
	store := &fakeStore{claimed: due, msg: testMessage()}
	m := &fakeMailer{}
	NewWorker(store, m, nil, WorkerOpts{Concurrency: 4}).sweep(context.Background())

	assert.Equal(t, []int{4, 4, 4}, store.limits, "full rounds repeat until a round comes back short")
	assert.Len(t, m.sent, 9)
	assert.Len(t, store.succeeded, 9)
}

func TestWorkerGivesUpOnlyOnPermanentErrors(t *testing.T) {
	cases := map[string]struct {
		err    error
		gaveUp bool
	}{
		"http 422":         {&mailer.ResendAPIError{StatusCode: 422}, true},
		"http 401":         {&mailer.ResendAPIError{StatusCode: 401}, true},
		"http 429":         {&mailer.ResendAPIError{StatusCode: 429}, false},
		"http 409":         {&mailer.ResendAPIError{StatusCode: 409}, false},
		"http 408":         {&mailer.ResendAPIError{StatusCode: 408}, false},
		"http 503":         {&mailer.ResendAPIError{StatusCode: 503}, false},
		"smtp 550":         {&textproto.Error{Code: 550, Msg: "no such user"}, true},
		"smtp 451":         {&textproto.Error{Code: 451, Msg: "try later"}, false},
		"invalid message":  {&mailer.IncompleteMessageError{Field: "to"}, true},
		"network":          {&net.OpError{Op: "dial", Err: errors.New("refused")}, false},
		"plain send error": {errors.New("connection reset"), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &fakeStore{claimed: []Entry{claimed(1)}, msg: testMessage()}
			NewWorker(store, &fakeMailer{err: tc.err}, nil, WorkerOpts{}).sweep(context.Background())
			require.Len(t, store.failed, 1)
			assert.Equal(t, tc.gaveUp, store.failed[0].gaveUp)
		})
	}
}

func TestWorkerCapsRetryAfter(t *testing.T) {
	store := &fakeStore{claimed: []Entry{claimed(1)}, msg: testMessage()}
	err := &mailer.ResendAPIError{StatusCode: 429, RetryAfter: 1000 * time.Hour}
	before := time.Now().UTC()
	NewWorker(store, &fakeMailer{err: err}, nil, WorkerOpts{}).sweep(context.Background())

	require.Len(t, store.failed, 1)
	assert.False(t, store.failed[0].gaveUp)
	assert.WithinDuration(t, before.Add(backoffBase(MaxAttempts)), store.failed[0].retryAt, time.Minute)
}

func TestWorkerPrunesSettledEntriesOncePerDay(t *testing.T) {
	log, buf := logBuffer()
	store := &fakeStore{}
	w := NewWorker(store, &fakeMailer{}, log, WorkerOpts{})
	before := time.Now().UTC()
	w.sweep(context.Background())
	w.sweep(context.Background())

	require.Len(t, store.prunedTo, 1, "the second sweep is within the day")
	assert.WithinDuration(t, before.Add(-Retention), store.prunedTo[0], time.Minute)
	assert.Contains(t, buf.String(), "pruned settled entries")

	w.nextPrune = time.Time{}
	store.pruneErr = errors.New("db down")
	w.sweep(context.Background())
	assert.Len(t, store.prunedTo, 2)
	assert.Contains(t, buf.String(), "prune settled")
}

func TestWorkerRunCancelReleasesTheInFlightSend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &fakeStore{claimed: []Entry{claimed(1)}, msg: testMessage()}
		m := &blockingMailer{started: make(chan struct{}, 1)}
		w := NewWorker(store, m, slog.New(slog.DiscardHandler), idleOpts())

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		<-m.started
		cancel()
		require.NoError(t, <-done)

		succeeded, failed, released := store.snapshot()
		assert.Empty(t, succeeded)
		assert.Empty(t, failed, "a shutdown must not cost the entry an attempt")
		assert.Len(t, released, 1)
	})
}

func TestWorkerSendPastHalfTheLeaseFailsWhileRunning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &fakeStore{claimed: []Entry{claimed(1)}, msg: testMessage()}
		m := &blockingMailer{started: make(chan struct{}, 1)}
		w := NewWorker(store, m, slog.New(slog.DiscardHandler), idleOpts())

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		<-m.started
		time.Sleep(ClaimLease/2 + time.Second)
		synctest.Wait()

		_, failed, released := store.snapshot()
		assert.Empty(t, released, "a send that outlives half the lease is a timeout, not a shutdown")
		require.Len(t, failed, 1)
		assert.False(t, failed[0].gaveUp)

		cancel()
		require.NoError(t, <-done)
	})
}

type dueEntry struct {
	e   Entry
	due time.Time
}

type queueStore struct {
	WorkerStore
	mu          sync.Mutex
	due         map[uuid.UUID]*dueEntry
	sent        int
	claimErrs   int
	succeedErrs int
}

func (q *queueStore) add(e Entry, due time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.due == nil {
		q.due = map[uuid.UUID]*dueEntry{}
	}
	q.due[e.ID] = &dueEntry{e: e, due: due}
}

func (q *queueStore) ClaimDue(_ context.Context, now time.Time, limit int) ([]Entry, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.claimErrs > 0 {
		q.claimErrs--
		return nil, errors.New("cold start")
	}
	var out []Entry
	for _, d := range q.due {
		if len(out) < limit && !d.due.After(now) {
			d.e.Attempt++
			d.due = now.Add(ClaimLease)
			out = append(out, d.e)
		}
	}
	return out, nil
}

func (q *queueStore) Open(Entry) (mailer.Message, error) { return testMessage(), nil }

func (q *queueStore) PruneSettled(context.Context, time.Time) (int64, error) { return 0, nil }

func (q *queueStore) Succeed(_ context.Context, e Entry, _ time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.succeedErrs > 0 {
		q.succeedErrs--
		return errors.New("cold start")
	}
	delete(q.due, e.ID)
	q.sent++
	return nil
}

func (q *queueStore) Fail(_ context.Context, e Entry, retryAt time.Time, _ string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.due[e.ID].due = retryAt
	return nil
}

func (q *queueStore) sentCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.sent
}

type flakyMailer struct {
	mu    sync.Mutex
	fails int
}

func (f *flakyMailer) Send(context.Context, mailer.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fails > 0 {
		f.fails--
		return errors.New("try later")
	}
	return nil
}

func idleOpts() WorkerOpts {
	return WorkerOpts{Tick: 5 * time.Second, IdlePoll: 10 * time.Minute}
}

func runWorker(t *testing.T, w *Worker) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})
	synctest.Wait()
}

func quietLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestWorkerRunSweepsAtStartup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &queueStore{}
		store.add(claimed(0), time.Now())
		runWorker(t, NewWorker(store, &flakyMailer{}, quietLog(), idleOpts()))

		assert.Equal(t, 1, store.sentCount())
	})
}

func TestWorkerWakeSendsBeforeIdlePoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &queueStore{}
		opts := idleOpts()
		opts.Waker = NewWaker()
		runWorker(t, NewWorker(store, &flakyMailer{}, quietLog(), opts))

		store.add(claimed(0), time.Now())
		opts.Waker.Wake()
		synctest.Wait()

		assert.Equal(t, 1, store.sentCount(), "a wake sweeps at once, without waiting for the idle poll")
	})
}

func TestWakerWakeNeverBlocks(t *testing.T) {
	w := NewWaker()
	for range 3 {
		w.Wake()
	}
}

func TestWorkerIdlePollCatchesAMissedWake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &queueStore{}
		runWorker(t, NewWorker(store, &flakyMailer{}, quietLog(), idleOpts()))

		store.add(claimed(0), time.Now())
		time.Sleep(10*time.Minute - time.Second)
		synctest.Wait()
		assert.Zero(t, store.sentCount(), "nothing sweeps before the idle poll")

		time.Sleep(2 * time.Second)
		synctest.Wait()
		assert.Equal(t, 1, store.sentCount())
	})
}

func TestWorkerRetriesBeforeIdlePoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &queueStore{}
		store.add(claimed(0), time.Now())
		runWorker(t, NewWorker(store, &flakyMailer{fails: 1}, quietLog(), idleOpts()))
		require.Zero(t, store.sentCount(), "the first send fails")

		time.Sleep(backoffBase(2) + backoffBase(2)/10 + time.Second)
		synctest.Wait()
		assert.Equal(t, 1, store.sentCount(), "the retry runs at its next attempt time, not at the idle poll")
	})
}

func TestWorkerRetriesOneTickAfterAClaimError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &queueStore{claimErrs: 1}
		store.add(claimed(0), time.Now())
		runWorker(t, NewWorker(store, &flakyMailer{}, quietLog(), idleOpts()))

		time.Sleep(idleOpts().Tick)
		synctest.Wait()
		assert.Equal(t, 1, store.sentCount(), "a failed claim is tried again after a tick, not at the idle poll")
	})
}

func TestWorkerRetriesAtTheLeaseAfterASettleError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &queueStore{succeedErrs: 1}
		store.add(claimed(0), time.Now())
		runWorker(t, NewWorker(store, &flakyMailer{}, quietLog(), idleOpts()))

		time.Sleep(ClaimLease + time.Second)
		synctest.Wait()
		assert.Equal(t, 1, store.sentCount(), "an unsettled claim is swept again when its lease ends, not at the idle poll")
	})
}

func TestWorkerTracksEveryRetryTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &queueStore{}
		store.add(claimed(0), time.Now())
		store.add(claimed(2), time.Now())
		opts := idleOpts()
		opts.IdlePoll = 24 * time.Hour
		runWorker(t, NewWorker(store, &flakyMailer{fails: 2}, quietLog(), opts))

		time.Sleep(backoffBase(2) + backoffBase(2)/10 + time.Second)
		synctest.Wait()
		require.Equal(t, 1, store.sentCount(), "the earlier retry runs first")
		time.Sleep(backoffBase(4) + backoffBase(4)/10)
		synctest.Wait()
		assert.Equal(t, 2, store.sentCount(), "the later retry is not lost once the earlier one ran")
	})
}

func TestBackoffBase_Schedule(t *testing.T) {
	for attempt, want := range map[int]time.Duration{
		0: 30 * time.Second, 1: 30 * time.Second, 2: 30 * time.Second, 3: 5 * time.Minute,
		4: 30 * time.Minute, 5: 2 * time.Hour, 6: 5 * time.Hour, 7: 10 * time.Hour, 9: 10 * time.Hour,
	} {
		assert.Equal(t, want, backoffBase(attempt), "attempt %d", attempt)
	}
}

func TestBackoff_JittersWithinTenPercent(t *testing.T) {
	for attempt := 1; attempt <= 10; attempt++ {
		base := backoffBase(attempt)
		for range 20 {
			got := backoff(attempt)
			assert.LessOrEqual(t, got, base+base/10, "attempt %d", attempt)
			assert.GreaterOrEqual(t, got, base-base/10, "attempt %d", attempt)
		}
	}
}

func TestErrors(t *testing.T) {
	assert.True(t, IsPayloadError(fmt.Errorf("w: %w", &PayloadError{ID: "a", Reason: "missing"})))
	assert.Equal(t, "mailqueue: entry a payload: missing", (&PayloadError{ID: "a", Reason: "missing"}).Error())
	assert.True(t, IsSettleRefusedError(fmt.Errorf("w: %w", &SettleRefusedError{ID: "a"})))
	assert.False(t, IsPayloadError(errors.New("x")))
}
