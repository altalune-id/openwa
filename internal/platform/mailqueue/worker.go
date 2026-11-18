package mailqueue

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/mailer"
	"altalune.id/openwa/worker"
)

var _ worker.Worker = (*Worker)(nil)

// WorkerName is the name the send worker registers under on a Supervisor.
const WorkerName = "mailqueue.send"

const (
	defaultTick        = 5 * time.Second
	defaultConcurrency = 4
	defaultSettleGrace = 5 * time.Second
	pruneEvery         = 24 * time.Hour
)

// WorkerOpts tunes one send loop, with every non-positive field taking the package default.
type WorkerOpts struct {
	// Tick is the shortest wait between two sweeps that no wake asked for.
	Tick time.Duration
	// IdlePoll is the longest wait between two sweeps when no wake or retry is due.
	IdlePoll time.Duration
	// Concurrency is how many entries one claim takes and sends at once.
	Concurrency int
	// SettleGrace bounds the Succeed or Fail that records a send outcome during shutdown.
	SettleGrace time.Duration
	// Waker is the signal WakeOnEnqueue sends after a commit; nil gives the worker its own.
	Waker *Waker
}

// Worker claims due entries on a wake, a retry or the idle poll, and sends each through a mailer.
type Worker struct {
	store     WorkerStore
	mailer    mailer.Mailer
	log       *slog.Logger
	opts      WorkerOpts
	nextPrune time.Time

	mu      sync.Mutex
	retries []time.Time
}

// NewWorker returns the send worker, whose Run is a no-op when the store or the mailer is nil.
func NewWorker(s WorkerStore, m mailer.Mailer, log *slog.Logger, opts WorkerOpts) *Worker {
	if log == nil {
		log = slog.Default()
	}
	if opts.Tick <= 0 {
		opts.Tick = defaultTick
	}
	if opts.IdlePoll <= 0 {
		opts.IdlePoll = opts.Tick
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = defaultConcurrency
	}
	if opts.SettleGrace <= 0 {
		opts.SettleGrace = defaultSettleGrace
	}
	if opts.Waker == nil {
		opts.Waker = NewWaker()
	}
	return &Worker{store: s, mailer: m, log: log.With(slog.String("component", WorkerName)), opts: opts}
}

// Name implements worker.Worker.
func (w *Worker) Name() string { return WorkerName }

// Run implements worker.Worker, sweeping at start, on a wake, at the earliest retry it scheduled, and on the idle poll until ctx is done.
func (w *Worker) Run(ctx context.Context) error {
	if w.store == nil || w.mailer == nil {
		w.log.Info("mailqueue: send idle, no mailer configured")
		return nil
	}
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-w.opts.Waker.ch:
		case <-t.C:
		}
		w.sweep(ctx)
		t.Reset(w.nextSweep(time.Now()))
	}
}

func (w *Worker) nextSweep(now time.Time) time.Duration {
	wait := w.opts.IdlePoll
	w.mu.Lock()
	for _, at := range w.retries {
		wait = min(wait, at.Sub(now))
	}
	w.mu.Unlock()
	return max(wait, w.opts.Tick)
}

func (w *Worker) dropDueRetries(now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.retries = slices.DeleteFunc(w.retries, func(at time.Time) bool { return !at.After(now) })
}

func (w *Worker) retryAt(at time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.retries = append(w.retries, at)
}

// NOTE: each claim takes only what it sends at once, so no claimed entry waits out its lease behind others.
func (w *Worker) sweep(ctx context.Context) {
	w.dropDueRetries(time.Now())
	w.prune(ctx)
	for ctx.Err() == nil {
		if n := w.round(ctx); n < w.opts.Concurrency {
			return
		}
	}
}

func (w *Worker) round(ctx context.Context) int {
	claimed, err := w.store.ClaimDue(ctx, time.Now().UTC(), w.opts.Concurrency)
	if err != nil {
		if ctx.Err() == nil {
			w.log.ErrorContext(ctx, "mailqueue: claim due", slog.String("err", err.Error()))
			w.retryAt(time.Now().Add(w.opts.Tick))
		}
		return 0
	}
	var wg sync.WaitGroup
	for _, e := range claimed {
		wg.Go(func() { w.sendOne(ctx, e) })
	}
	wg.Wait()
	return len(claimed)
}

func (w *Worker) prune(ctx context.Context) {
	now := time.Now().UTC()
	if now.Before(w.nextPrune) {
		return
	}
	w.nextPrune = now.Add(pruneEvery)
	n, err := w.store.PruneSettled(ctx, now.Add(-Retention))
	if err != nil {
		if ctx.Err() == nil {
			w.log.ErrorContext(ctx, "mailqueue: prune settled", slog.String("err", err.Error()))
		}
		return
	}
	if n > 0 {
		w.log.InfoContext(ctx, "mailqueue: pruned settled entries", slog.Int64("count", n))
	}
}

func (w *Worker) sendOne(ctx context.Context, e Entry) {
	msg, err := w.store.Open(e)
	if err == nil {
		err = w.send(ctx, e, msg)
	}

	// NOTE: settling is detached from ctx so a cancel mid-send still records the outcome.
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.opts.SettleGrace)
	defer cancel()

	if err != nil && ctx.Err() != nil {
		if rErr := w.store.Release(settleCtx, e); rErr != nil {
			w.log.ErrorContext(settleCtx, "mailqueue: release at shutdown",
				slog.String("entry", e.ID.String()), slog.String("err", rErr.Error()))
		}
		return
	}

	if err == nil {
		if sErr := w.store.Succeed(settleCtx, e, time.Now().UTC()); sErr != nil {
			w.log.ErrorContext(settleCtx, "mailqueue: settle sent",
				slog.String("entry", e.ID.String()), slog.String("err", sErr.Error()))
			w.retryAt(time.Now().Add(ClaimLease))
		}
		return
	}

	f := describe(err, msg)
	// SECURITY: only the entry id and the error kind are logged; the address, subject and body never are.
	attrs := []any{slog.String("entry", e.ID.String()), slog.Int("attempt", e.Attempt), slog.String("kind", f.kind)}
	if f.code != "" {
		attrs = append(attrs, slog.String("code", f.code))
	}
	now := time.Now().UTC()
	var sErr error
	switch {
	case f.permanent:
		w.log.ErrorContext(ctx, "mailqueue: gave up, the error will not go away on a retry", attrs...)
		sErr = w.store.GiveUp(settleCtx, e, now, f.text)
	case e.Attempt >= MaxAttempts:
		w.log.ErrorContext(ctx, "mailqueue: gave up after the last attempt", attrs...)
		sErr = w.store.GiveUp(settleCtx, e, now, f.text)
	default:
		w.log.WarnContext(ctx, "mailqueue: send failed, will retry", attrs...)
		retryAt := now.Add(retryWait(e.Attempt, err))
		if sErr = w.store.Fail(settleCtx, e, retryAt, f.text); sErr == nil {
			w.retryAt(retryAt)
		}
	}
	if sErr != nil {
		w.log.ErrorContext(settleCtx, "mailqueue: settle failed send",
			slog.String("entry", e.ID.String()), slog.String("err", sErr.Error()))
		w.retryAt(time.Now().Add(ClaimLease))
	}
}

// NOTE: a send ends well inside the lease, so no other replica re-claims the entry while it is still in flight.
func (w *Worker) send(ctx context.Context, e Entry, msg mailer.Message) error {
	ctx, cancel := context.WithTimeout(ctx, ClaimLease/2)
	defer cancel()
	return w.mailer.Send(mailer.WithIdempotencyKey(ctx, e.ID.String()), msg)
}

func retryWait(attempt int, err error) time.Duration {
	wait := backoff(attempt + 1)
	if ra := min(retryAfter(err), backoffBase(MaxAttempts)); ra > wait {
		wait = ra
	}
	return wait
}

type retryDelayer interface {
	error
	RetryDelay() time.Duration
}

type httpStatuser interface {
	error
	HTTPStatus() int
}

func retryAfter(err error) time.Duration {
	var re retryDelayer
	if errors.As(err, &re) {
		return re.RetryDelay()
	}
	return 0
}

type failure struct {
	kind      string
	code      string
	text      string
	permanent bool
}

var (
	addressPattern  = regexp.MustCompile(`[^\s<>"'(),;:\[\]]+@[^\s<>"'(),;:\[\]]+`)
	enhancedPattern = regexp.MustCompile(`^\d\.\d{1,3}\.\d{1,3}\b`)
)

// SECURITY: SMTP replies and API error bodies can echo the recipient or the message; only redacted text survives.
func describe(err error, m mailer.Message) failure {
	f := failure{kind: "send"}
	detail := ""
	var he httpStatuser
	var te *textproto.Error
	if errors.As(err, &he) {
		f.kind, f.code = "http_status", strconv.Itoa(he.HTTPStatus())
		f.permanent = permanentHTTP(he.HTTPStatus())
	} else if errors.As(err, &te) {
		f.kind, f.code = "smtp_reply", strconv.Itoa(te.Code)
		f.permanent = te.Code >= 500
		if enh := enhancedPattern.FindString(strings.TrimSpace(te.Msg)); enh != "" {
			f.code += " " + enh
		}
	} else {
		f.kind = kindOf(err)
		f.permanent = f.kind == "invalid_message" || f.kind == "payload"
		detail = redact(err.Error(), m)
	}
	f.text = f.kind
	if f.code != "" {
		f.text += " " + f.code
	}
	if detail != "" {
		f.text += ": " + detail
	}
	f.text = apperror.TruncateCause(f.text, MaxErrorLen)
	return f
}

func permanentHTTP(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooManyRequests:
		return false
	}
	return status >= 400 && status < 500
}

func kindOf(err error) string {
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return "timeout"
	case mailer.IsHeaderInjectionError(err), mailer.IsIncompleteMessageError(err):
		return "invalid_message"
	case IsPayloadError(err):
		return "payload"
	case errors.As(err, &netErr):
		return "network"
	default:
		return "send"
	}
}

const minBodyFragment = 8

func redact(s string, m mailer.Message) string {
	for _, v := range []string{m.TextBody, m.HTMLBody} {
		s = replaceIfLong(s, v, "[message]")
	}
	for _, body := range []string{m.TextBody, m.HTMLBody} {
		for line := range strings.Lines(body) {
			s = replaceIfLong(s, strings.TrimSpace(line), "[message]")
		}
	}
	s = replaceIfLong(s, m.Subject, "[message]")
	for _, v := range []string{m.To, m.From} {
		s = replaceIfLong(s, v, "[address]")
	}
	return addressPattern.ReplaceAllString(s, "[address]")
}

func replaceIfLong(s, v, with string) string {
	if len(v) < minBodyFragment {
		return s
	}
	return strings.ReplaceAll(s, v, with)
}
