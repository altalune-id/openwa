package webhook_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/platform/outbox"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/webhook"
)

type pgDelivery struct {
	store  webhook.Store
	tc     tenant.Context
	e      *webhook.Endpoint
	secret string
	d      *webhook.Deliverer
	logs   *bytes.Buffer
}

func newPgDelivery(t *testing.T, client *http.Client, rawURL string) *pgDelivery {
	t.Helper()
	f := newPgFixture(t)
	sl := newSealer(t)
	e, err := webhook.New(f.tc.OrgID, f.tc.ProjectID, rawURL, "orders", []events.Type{events.PostPublished})
	require.NoError(t, err)
	secret, err := webhook.NewSecret()
	require.NoError(t, err)
	e.Secrets.Primary, err = webhook.SealSecret(sl, e.ID, webhook.SlotPrimary, secret)
	require.NoError(t, err)
	require.NoError(t, f.store.Save(tenant.Into(t.Context(), f.tc), e))
	logs := &bytes.Buffer{}
	d := webhook.NewDeliverer(f.store, sl, client, slog.New(slog.NewJSONHandler(logs, nil)))
	return &pgDelivery{store: f.store, tc: f.tc, e: e, secret: secret, d: d, logs: logs}
}

func (s *pgDelivery) entry(t *testing.T) outbox.Entry {
	t.Helper()
	x := &delivery{orgID: s.tc.OrgID, project: s.tc.ProjectID}
	return x.entry(t, s.e)
}

func (s *pgDelivery) attempts(t *testing.T, deliveryID uuid.UUID) []webhook.Attempt {
	t.Helper()
	got, err := s.store.ListAttempts(tenant.Into(t.Context(), s.tc), s.e.ID, deliveryID)
	require.NoError(t, err)
	return got
}

func TestPostgres_DelivererRecordsAttempt(t *testing.T) {
	r := newReceiver(t, http.StatusAccepted)
	s := newPgDelivery(t, r.srv.Client(), hookURL(r))
	entry := s.entry(t)

	require.NoError(t, s.d.Deliver(tenant.Into(t.Context(), tenant.Context{OrgID: s.tc.OrgID}), entry))

	got := r.request(t)
	assert.Equal(t, webhook.Sign(s.secret, got.header.Get("X-Openwa-Timestamp"), got.body), got.header.Get("X-Openwa-Signature"))
	attempts := s.attempts(t, entry.ID)
	require.Len(t, attempts, 1)
	assert.Equal(t, http.StatusAccepted, attempts[0].StatusCode)
	assert.Equal(t, events.PostPublished, attempts[0].EventType)
	assert.Equal(t, entry.EventID, attempts[0].EventID)
}

func TestPostgres_DelivererRecordsAttemptWhenCancelledMidPost(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", func(w http.ResponseWriter, _ *http.Request) {
		cancel()
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	s := newPgDelivery(t, srv.Client(), srv.URL+"/hook")
	entry := s.entry(t)

	_ = s.d.Deliver(tenant.Into(ctx, tenant.Context{OrgID: s.tc.OrgID}), entry)

	require.Error(t, ctx.Err())
	assert.Len(t, s.attempts(t, entry.ID), 1, "logs: %s", s.logs.String())
	assert.NotContains(t, s.logs.String(), "webhook: record attempt")
}

func unexpectedFails(t *testing.T) apperror.UnexpectedFunc {
	t.Helper()
	return func(_ context.Context, msg string, err error, _ ...any) *apperror.AppError {
		t.Errorf("unexpected report %q: %v", msg, err)
		return apperror.New("openwa.unexpected", err.Error(), codes.Internal)
	}
}

func TestPostgres_EnqueueWritesRowsForTheCallerProjectOnly(t *testing.T) {
	f := newPgFixture(t)
	cfg := db.DBConfig{Schema: f.schema, TablePrefix: f.prefix}
	pool := db.Pool{W: f.sqlDB, R: f.sqlDB}
	pc := tenant.NewPgConn(f.sqlDB)
	ob := outbox.NewStore(cfg, pool, pc)
	uow := tenant.NewUnitOfWork(pc)
	slugs := &slugStub{slug: "altalune"}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := webhook.NewService(f.store, log, unexpectedFails(t), newSealer(t), ob, slugs)

	ctxP := tenant.Into(t.Context(), f.tc)
	e, _, err := svc.Create(ctxP, validURL, "", []events.Type{events.PostPublished})
	require.NoError(t, err)

	enqueue := func(ctx context.Context) error {
		return uow(ctx, func(ctx context.Context) error {
			return svc.Enqueue(ctx, events.PostPublished, postPublished())
		})
	}
	require.NoError(t, enqueue(ctxP))

	rows, err := ob.ListByTarget(ctxP, e.ID.String(), outbox.MaxListLimit)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, f.tc.ProjectID, rows[0].ProjectID)
	assert.Equal(t, f.tc.OrgID, rows[0].OrgID)
	assert.Equal(t, "blog.post.published", decodeEnvelope(t, rows[0].Payload)["type"])

	q := f.tc
	q.ProjectID = seedPgProject(t, f.sqlDB, f.prefix, f.tc)
	require.NoError(t, enqueue(tenant.Into(t.Context(), q)))

	rows, err = ob.ListByTarget(ctxP, e.ID.String(), outbox.MaxListLimit)
	require.NoError(t, err)
	assert.Len(t, rows, 1, "project Q must not reach project P's endpoint")
	assert.Equal(t, 1, slugs.calls)
}
