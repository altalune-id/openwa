package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/platform/outbox"
	"altalune.id/openwa/internal/platform/sealer"
	"altalune.id/openwa/internal/platform/tenant"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer = otel.Tracer("altalune.id/openwa/internal/webhook")

const (
	eventIDPrefix    = "evt_"
	deliveryIDPrefix = "dlv_"
)

// MaxDeliveriesListed caps one Deliveries call.
const MaxDeliveriesListed = 50

// ProjectSlugs resolves a project's current slug for the envelope.
type ProjectSlugs interface {
	SlugOf(ctx context.Context, projectID uuid.UUID) (string, error)
}

// Devices resolves a number's public id to its internal id within the caller's project, or returns a *DeviceNotFoundError.
type Devices interface {
	ResolveID(ctx context.Context, publicID string) (uuid.UUID, error)
}

// Envelope is the JSON body every delivery of one event carries; it is the public wire contract.
type Envelope struct {
	ID         string         `json:"id"`
	Type       events.Type    `json:"type"`
	APIVersion string         `json:"api_version"`
	CreatedAt  time.Time      `json:"created_at"`
	Tenant     EnvelopeTenant `json:"tenant"`
	Data       any            `json:"data"`
}

// EnvelopeTenant names the org and project an event belongs to.
type EnvelopeTenant struct {
	OrgID       uuid.UUID `json:"org_id"`
	ProjectID   uuid.UUID `json:"project_id"`
	ProjectSlug string    `json:"project_slug"`
}

func envelopeType(payload []byte) (events.Type, error) {
	var env struct {
		Type events.Type `json:"type"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return "", fmt.Errorf("decode envelope type: %w", err)
	}
	if env.Type == "" {
		return "", errors.New("decode envelope type: type is empty")
	}
	return env.Type, nil
}

// Service is the webhooks driving port.
type Service struct {
	store      Store
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
	sealer     sealer.Sealer
	outbox     outbox.Store
	projects   ProjectSlugs
	devices    Devices

	allowInsecureURL bool
}

// ServiceOption tunes a Service at construction.
type ServiceOption func(*Service)

// WithAllowInsecureURL lets endpoints use http:// URLs; self-hosted dev only.
func WithAllowInsecureURL() ServiceOption { return func(s *Service) { s.allowInsecureURL = true } }

func (s *Service) validateOpts() []ValidateOption {
	if s.allowInsecureURL {
		return []ValidateOption{AllowInsecureURL()}
	}
	return nil
}

// NewService binds the service to its dependencies.
func NewService(store Store, log *slog.Logger, unexpected apperror.UnexpectedFunc, sl sealer.Sealer, ob outbox.Store, projects ProjectSlugs, devices Devices, opts ...ServiceOption) *Service {
	svc := &Service{
		store:      store,
		log:        log.With("module", "webhook"),
		unexpected: unexpected,
		sealer:     sl,
		outbox:     ob,
		projects:   projects,
		devices:    devices,
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

// Enqueue fans a project-wide t out to every active endpoint of the caller's project subscribed to it, inside the caller's unit of work.
func (s *Service) Enqueue(ctx context.Context, t events.Type, data any) error {
	return s.enqueue(ctx, "webhook.Enqueue", t, nil, data)
}

// EnqueueForDevice fans a device-bound t from deviceID out to the project-wide endpoints and those scoped to that device, inside the caller's unit of work.
func (s *Service) EnqueueForDevice(ctx context.Context, t events.Type, deviceID uuid.UUID, data any) error {
	return s.enqueue(ctx, "webhook.EnqueueForDevice", t, &deviceID, data)
}

func (s *Service) enqueue(ctx context.Context, op string, t events.Type, deviceID *uuid.UUID, data any) error {
	ctx, span := tracer.Start(ctx, op,
		trace.WithAttributes(attribute.String("event.type", string(t))))
	defer span.End()

	if _, ok := db.CurrentTx(ctx); !ok {
		return record(span, &NoUnitOfWorkError{})
	}
	spec, ok := events.Lookup(t)
	if !ok {
		return record(span, &events.UnknownTypeError{Type: t})
	}
	// SECURITY: a device-bound event without its device would reach every scoped endpoint's filter as project-wide.
	if spec.DeviceBound && deviceID == nil {
		return record(span, &DeviceRequiredError{Type: t})
	}
	if !spec.DeviceBound && deviceID != nil {
		return record(span, &DeviceNotBoundError{Type: t})
	}
	if err := events.CheckPayload(t, data); err != nil {
		return record(span, err)
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return record(span, err)
	}
	endpoints, err := s.store.List(ctx, tc.OrgID, tc.ProjectID)
	if err != nil {
		return record(span, fmt.Errorf("%s: %w", op, err))
	}
	targets := slices.DeleteFunc(endpoints, func(e *Endpoint) bool { return !e.Receives(t, deviceID) })
	span.SetAttributes(attribute.Int("webhook.targets", len(targets)))
	if len(targets) == 0 {
		return nil
	}
	if err := s.fanOut(ctx, tc.OrgID, tc.ProjectID, spec, data, targets); err != nil {
		return record(span, fmt.Errorf("%s: %w", op, err))
	}
	return nil
}

// Create persists a new endpoint in the caller's project, scoped to the number devicePublicID names or to every number when it is empty, and returns its signing secret, shown only this once.
func (s *Service) Create(ctx context.Context, rawURL, description string, types []events.Type, devicePublicID string) (*Endpoint, string, error) {
	ctx, span := tracer.Start(ctx, "webhook.Create")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, "", record(span, err)
	}
	deviceID, err := s.resolveDevice(ctx, span, devicePublicID)
	if err != nil {
		return nil, "", err
	}
	e, err := New(tc.OrgID, tc.ProjectID, deviceID, rawURL, description, types, s.validateOpts()...)
	if err != nil {
		return nil, "", record(span, err)
	}
	existing, err := s.store.List(ctx, tc.OrgID, tc.ProjectID)
	if err != nil {
		span.RecordError(err)
		return nil, "", s.unexpected(ctx, "webhook.Create: list", err,
			"org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	if len(existing) >= MaxEndpointsPerProject {
		return nil, "", record(span, &EndpointLimitError{Limit: MaxEndpointsPerProject})
	}
	secret, err := newSecret()
	if err != nil {
		span.RecordError(err)
		return nil, "", s.unexpected(ctx, "webhook.Create: new secret", err, "endpoint_id", e.ID)
	}
	e.Secrets.PrimaryHint = secretHint(secret)
	if e.Secrets.Primary, err = sealSecret(s.sealer, e.ID, slotPrimary, secret); err != nil {
		return nil, "", s.sealFailure(ctx, span, "webhook.Create: seal", err, e.ID)
	}
	if err := s.save(ctx, span, "webhook.Create", e); err != nil {
		return nil, "", err
	}
	span.SetAttributes(attribute.String("endpoint.id", e.ID.String()))
	return e, secret, nil
}

// Update replaces the editable fields of the identified endpoint.
func (s *Service) Update(ctx context.Context, id uuid.UUID, rawURL, description string, types []events.Type, active bool) (*Endpoint, error) {
	ctx, span := startWithID(ctx, "webhook.Update", id)
	defer span.End()

	e, err := s.load(ctx, span, "webhook.Update", id)
	if err != nil {
		return nil, err
	}
	if err := e.Update(rawURL, description, types, active, s.validateOpts()...); err != nil {
		return nil, record(span, err)
	}
	if err := s.save(ctx, span, "webhook.Update", e); err != nil {
		return nil, err
	}
	return e, nil
}

// Delete removes the identified endpoint; its pending deliveries settle as no-ops.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	ctx, span := startWithID(ctx, "webhook.Delete", id)
	defer span.End()

	if _, err := s.load(ctx, span, "webhook.Delete", id); err != nil {
		return err
	}
	if err := s.store.Delete(ctx, id); err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) {
			return err
		}
		return s.unexpected(ctx, "webhook.Delete: delete", err, "endpoint_id", id)
	}
	return nil
}

// ByID returns the identified endpoint when it belongs to the caller's tenant scope.
func (s *Service) ByID(ctx context.Context, id uuid.UUID) (*Endpoint, error) {
	ctx, span := startWithID(ctx, "webhook.ByID", id)
	defer span.End()

	return s.load(ctx, span, "webhook.ByID", id)
}

// List returns the endpoints of the caller's project, newest first.
func (s *Service) List(ctx context.Context) ([]*Endpoint, error) {
	ctx, span := tracer.Start(ctx, "webhook.List")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, record(span, err)
	}
	out, err := s.store.List(ctx, tc.OrgID, tc.ProjectID)
	if err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "webhook.List: list", err,
			"org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	return out, nil
}

// Rotate issues a new primary secret, keeping the old one as the secondary when it can still be opened.
func (s *Service) Rotate(ctx context.Context, id uuid.UUID) (string, error) {
	ctx, span := startWithID(ctx, "webhook.Rotate", id)
	defer span.End()

	e, err := s.load(ctx, span, "webhook.Rotate", id)
	if err != nil {
		return "", err
	}
	secret, err := newSecret()
	if err != nil {
		span.RecordError(err)
		return "", s.unexpected(ctx, "webhook.Rotate: new secret", err, "endpoint_id", id)
	}
	primary, err := sealSecret(s.sealer, e.ID, slotPrimary, secret)
	if err != nil {
		return "", s.sealFailure(ctx, span, "webhook.Rotate: seal primary", err, id)
	}
	next := SealedSecrets{Primary: primary, PrimaryHint: secretHint(secret)}
	if old, ok := s.openPrimary(ctx, e); ok {
		if next.Secondary, err = sealSecret(s.sealer, e.ID, slotSecondary, old); err != nil {
			return "", s.sealFailure(ctx, span, "webhook.Rotate: seal secondary", err, id)
		}
		next.SecondaryHint = secretHint(old)
	}
	if err := s.saveSecrets(ctx, span, "webhook.Rotate", e, next); err != nil {
		return "", err
	}
	return secret, nil
}

// RetireSecondary drops the secondary secret, ending a rotation.
func (s *Service) RetireSecondary(ctx context.Context, id uuid.UUID) error {
	ctx, span := startWithID(ctx, "webhook.RetireSecondary", id)
	defer span.End()

	e, err := s.load(ctx, span, "webhook.RetireSecondary", id)
	if err != nil {
		return err
	}
	if e.Secrets.Secondary == nil {
		return nil
	}
	return s.saveSecrets(ctx, span, "webhook.RetireSecondary", e, SealedSecrets{Primary: e.Secrets.Primary, PrimaryHint: e.Secrets.PrimaryHint})
}

// SendTest queues one webhook.ping to the identified endpoint, whatever it subscribes to.
func (s *Service) SendTest(ctx context.Context, id uuid.UUID) error {
	ctx, span := startWithID(ctx, "webhook.SendTest", id)
	defer span.End()

	e, err := s.load(ctx, span, "webhook.SendTest", id)
	if err != nil {
		return err
	}
	if !e.Active {
		return record(span, &EndpointInactiveError{ID: id.String()})
	}
	spec, ok := events.Lookup(events.WebhookPing)
	if !ok {
		err := &events.UnknownTypeError{Type: events.WebhookPing}
		span.RecordError(err)
		return s.unexpected(ctx, "webhook.SendTest: lookup", err, "endpoint_id", id)
	}
	ping := events.WebhookPingV1{EndpointID: e.ID}
	if err := s.fanOut(ctx, e.OrgID, e.ProjectID, spec, ping, []*Endpoint{e}); err != nil {
		span.RecordError(err)
		return s.unexpected(ctx, "webhook.SendTest: enqueue", err, "endpoint_id", id)
	}
	return nil
}

// Delivery is one outbox delivery to an endpoint, with the event type read from its envelope.
type Delivery struct {
	outbox.Entry
	EventType events.Type
}

// Delivered reports whether any endpoint of the caller's project ever accepted a t delivery; a later failure does not undo it.
func (s *Service) Delivered(ctx context.Context, t events.Type) (bool, error) {
	ctx, span := tracer.Start(ctx, "webhook.Delivered", trace.WithAttributes(attribute.String("event.type", string(t))))
	defer span.End()

	ok, err := s.store.Delivered(ctx, t)
	if err != nil {
		span.RecordError(err)
		return false, s.unexpected(ctx, "webhook.Delivered: read", err, "event_type", t)
	}
	return ok, nil
}

// Failing reports whether the delivery gave up, or is waiting to retry after a failed try.
func (d Delivery) Failing() bool {
	return d.Status == outbox.StatusFailed || (d.Status == outbox.StatusPending && d.Attempt > 0)
}

// LatestDeliveries returns the newest delivery of each endpoint of the caller's project, keyed by endpoint id, in one read.
func (s *Service) LatestDeliveries(ctx context.Context) (map[uuid.UUID]Delivery, error) {
	ctx, span := tracer.Start(ctx, "webhook.LatestDeliveries")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, record(span, err)
	}
	endpoints, err := s.store.List(ctx, tc.OrgID, tc.ProjectID)
	if err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "webhook.LatestDeliveries: list", err, "org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	targets := make([]string, len(endpoints))
	for i, e := range endpoints {
		targets[i] = e.ID.String()
	}
	latest, err := s.outbox.LatestByTargets(ctx, targets)
	if err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "webhook.LatestDeliveries: latest", err, "project_id", tc.ProjectID)
	}
	out := make(map[uuid.UUID]Delivery, len(latest))
	for _, e := range endpoints {
		if entry, ok := latest[e.ID.String()]; ok {
			out[e.ID] = s.toDelivery(ctx, e.ID, entry)
		}
	}
	return out, nil
}

// Deliveries returns up to limit deliveries of the identified endpoint, newest first, capped at MaxDeliveriesListed.
func (s *Service) Deliveries(ctx context.Context, id uuid.UUID, limit int) ([]Delivery, error) {
	ctx, span := startWithID(ctx, "webhook.Deliveries", id)
	defer span.End()

	if _, err := s.load(ctx, span, "webhook.Deliveries", id); err != nil {
		return nil, err
	}
	entries, err := s.outbox.ListByTarget(ctx, id.String(), min(limit, MaxDeliveriesListed))
	if err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "webhook.Deliveries: list", err, "endpoint_id", id)
	}
	out := make([]Delivery, len(entries))
	for i, e := range entries {
		out[i] = s.toDelivery(ctx, id, e)
	}
	return out, nil
}

func (s *Service) toDelivery(ctx context.Context, endpointID uuid.UUID, e outbox.Entry) Delivery {
	typ, err := envelopeType(e.Payload)
	if err != nil {
		s.log.WarnContext(ctx, "webhook: delivery envelope has no readable type",
			"endpoint_id", endpointID, "delivery_id", e.ID, "err", err)
		return Delivery{Entry: e}
	}
	return Delivery{Entry: e, EventType: typ}
}

// Delivery returns one delivery of the identified endpoint, or *DeliveryNotFoundError when the endpoint has none by that id.
func (s *Service) Delivery(ctx context.Context, id, deliveryID uuid.UUID) (Delivery, error) {
	ctx, span := startWithID(ctx, "webhook.Delivery", id)
	defer span.End()

	if _, err := s.load(ctx, span, "webhook.Delivery", id); err != nil {
		return Delivery{}, err
	}
	e, err := s.outbox.ByID(ctx, deliveryID, id.String())
	if err != nil {
		if outbox.IsNotFoundError(err) {
			return Delivery{}, record(span, &DeliveryNotFoundError{ID: deliveryID.String()})
		}
		span.RecordError(err)
		return Delivery{}, s.unexpected(ctx, "webhook.Delivery: byID", err, "endpoint_id", id, "delivery_id", deliveryID)
	}
	return s.toDelivery(ctx, id, e), nil
}

// Attempts returns the recorded attempts of one delivery to the identified endpoint, newest first.
func (s *Service) Attempts(ctx context.Context, id, deliveryID uuid.UUID) ([]Attempt, error) {
	ctx, span := startWithID(ctx, "webhook.Attempts", id)
	defer span.End()

	if _, err := s.load(ctx, span, "webhook.Attempts", id); err != nil {
		return nil, err
	}
	out, err := s.store.ListAttempts(ctx, id, deliveryID)
	if err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "webhook.Attempts: list", err,
			"endpoint_id", id, "delivery_id", deliveryID)
	}
	return out, nil
}

// Retry requeues one failed delivery of the identified endpoint.
func (s *Service) Retry(ctx context.Context, id, deliveryID uuid.UUID) error {
	ctx, span := startWithID(ctx, "webhook.Retry", id)
	defer span.End()

	if _, err := s.load(ctx, span, "webhook.Retry", id); err != nil {
		return err
	}
	err := s.outbox.Requeue(ctx, deliveryID, id.String())
	switch {
	case err == nil:
		return nil
	case outbox.IsNotFailedError(err):
		return record(span, &DeliveryNotRetryableError{ID: deliveryID.String()})
	case outbox.IsNotFoundError(err):
		return record(span, &DeliveryNotFoundError{ID: deliveryID.String()})
	}
	span.RecordError(err)
	return s.unexpected(ctx, "webhook.Retry: requeue", err, "endpoint_id", id, "delivery_id", deliveryID)
}

// RetryFailed requeues every failed delivery of the identified endpoint and returns how many.
func (s *Service) RetryFailed(ctx context.Context, id uuid.UUID) (int, error) {
	ctx, span := startWithID(ctx, "webhook.RetryFailed", id)
	defer span.End()

	if _, err := s.load(ctx, span, "webhook.RetryFailed", id); err != nil {
		return 0, err
	}
	n, err := s.outbox.RequeueFailed(ctx, id.String())
	if err != nil {
		span.RecordError(err)
		return 0, s.unexpected(ctx, "webhook.RetryFailed: requeue", err, "endpoint_id", id)
	}
	return n, nil
}

func (s *Service) fanOut(ctx context.Context, orgID, projectID uuid.UUID, spec events.Spec, data any, targets []*Endpoint) error {
	slug, err := s.projects.SlugOf(ctx, projectID)
	if err != nil {
		return fmt.Errorf("project slug: %w", err)
	}
	eventID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("event id: %w", err)
	}
	body, err := json.Marshal(Envelope{
		ID:         eventIDPrefix + eventID.String(),
		Type:       spec.Type,
		APIVersion: spec.APIVersion(),
		CreatedAt:  time.Now().UTC().Truncate(time.Second),
		Tenant:     EnvelopeTenant{OrgID: orgID, ProjectID: projectID, ProjectSlug: slug},
		Data:       data,
	})
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	for _, e := range targets {
		entryID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("entry id: %w", err)
		}
		if err := s.outbox.Enqueue(ctx, outbox.Entry{
			ID:        entryID,
			EventID:   eventID,
			OrgID:     orgID,
			ProjectID: projectID,
			Target:    e.ID.String(),
			Payload:   body,
		}); err != nil {
			return fmt.Errorf("outbox enqueue: endpoint_id=%s: %w", e.ID, err)
		}
	}
	return nil
}

// SECURITY: Devices resolves within the caller's project, so a sibling project's number is refused here.
func (s *Service) resolveDevice(ctx context.Context, span trace.Span, publicID string) (*uuid.UUID, error) {
	if publicID == "" {
		return nil, nil
	}
	id, err := s.devices.ResolveID(ctx, publicID)
	if err != nil {
		span.RecordError(err)
		if IsDeviceNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "webhook.Create: resolve device", err, "device_public_id", publicID)
	}
	return &id, nil
}

// SECURITY: the store filters by org only, so the project check here is what keeps a sibling project's endpoint out of reach.
func (s *Service) load(ctx context.Context, span trace.Span, method string, id uuid.UUID) (*Endpoint, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, record(span, err)
	}
	e, err := s.store.ByID(ctx, id)
	if err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, method+": byID", err, "endpoint_id", id)
	}
	if e.OrgID != tc.OrgID || e.ProjectID != tc.ProjectID {
		return nil, record(span, &NotFoundError{ID: id.String()})
	}
	return e, nil
}

func (s *Service) save(ctx context.Context, span trace.Span, method string, e *Endpoint) error {
	if err := s.store.Save(ctx, e); err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) || IsDuplicateEndpointError(err) {
			return err
		}
		return s.unexpected(ctx, method+": save", err, "endpoint_id", e.ID)
	}
	return nil
}

func (s *Service) saveSecrets(ctx context.Context, span trace.Span, method string, e *Endpoint, next SealedSecrets) error {
	err := s.store.SaveSecrets(ctx, e.ID, e.Secrets, next)
	if err == nil {
		e.SetSecrets(next)
		return nil
	}
	span.RecordError(err)
	if IsNotFoundError(err) || IsSecretConflictError(err) {
		return err
	}
	return s.unexpected(ctx, method+": save secrets", err, "endpoint_id", e.ID)
}

func (s *Service) sealFailure(ctx context.Context, span trace.Span, msg string, err error, id uuid.UUID) error {
	span.RecordError(err)
	if sealer.IsUnavailableError(err) {
		return err
	}
	return s.unexpected(ctx, msg, err, "endpoint_id", id)
}

// BackfillSecretHints fills the missing secret hints of the caller's org's endpoints, once per secret; an unreadable secret keeps no hint and is reported once per run.
func (s *Service) BackfillSecretHints(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "webhook.BackfillSecretHints")
	defer span.End()

	missing, err := s.store.ListMissingHints(ctx)
	if err != nil {
		return record(span, fmt.Errorf("webhook.BackfillSecretHints: %w", err))
	}
	var unreadable []string
	filled := 0
	for _, e := range missing {
		next, ok := s.hintsOf(e)
		if !ok {
			unreadable = append(unreadable, e.ID.String())
		}
		if next.PrimaryHint == e.Secrets.PrimaryHint && next.SecondaryHint == e.Secrets.SecondaryHint {
			continue
		}
		if err := s.store.SetHints(ctx, e.ID, next); err != nil {
			return record(span, fmt.Errorf("webhook.BackfillSecretHints: %w", err))
		}
		filled++
	}
	span.SetAttributes(attribute.Int("webhook.hints_filled", filled), attribute.Int("webhook.hints_unreadable", len(unreadable)))
	if len(unreadable) > 0 {
		err := &SecretUnavailableError{}
		span.RecordError(err)
		_ = s.unexpected(ctx, "webhook.BackfillSecretHints: secrets cannot be opened", err,
			"count", len(unreadable), "endpoint_ids", unreadable)
	}
	return nil
}

// SECURITY: the opened secrets stay in this function; only their last SecretHintLen characters leave it.
func (s *Service) hintsOf(e *Endpoint) (SealedSecrets, bool) {
	next, ok := e.Secrets, true
	if next.PrimaryHint == "" {
		if secret, err := openSecret(s.sealer, e.ID, slotPrimary, next.Primary); err == nil {
			next.PrimaryHint = secretHint(secret)
		} else {
			ok = false
		}
	}
	if len(next.Secondary) > 0 && next.SecondaryHint == "" {
		if secret, err := openSecret(s.sealer, e.ID, slotSecondary, next.Secondary); err == nil {
			next.SecondaryHint = secretHint(secret)
		} else {
			ok = false
		}
	}
	return next, ok
}

func secretHint(secret string) string { return secret[max(len(secret)-SecretHintLen, 0):] }

func (s *Service) openPrimary(ctx context.Context, e *Endpoint) (string, bool) {
	old, err := openSecret(s.sealer, e.ID, slotPrimary, e.Secrets.Primary)
	if err != nil {
		s.log.WarnContext(ctx, "webhook: old primary secret cannot be opened, rotating without a secondary",
			"endpoint_id", e.ID, "err", err)
		return "", false
	}
	return old, true
}

func startWithID(ctx context.Context, name string, id uuid.UUID) (context.Context, trace.Span) {
	return tracer.Start(ctx, name, trace.WithAttributes(attribute.String("endpoint.id", id.String())))
}

func record(span trace.Span, err error) error {
	span.RecordError(err)
	return err
}
