package whatsapp

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/platform/tenant"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer = otel.Tracer("altalune.id/openwa/internal/whatsapp")

// SessionRuntime is the process-local runtime the service delegates engine work to.
type SessionRuntime interface {
	Link(ctx context.Context, ref SessionRef) (LinkState, error)
	LinkWithPhone(ctx context.Context, ref SessionRef, phone string) (LinkState, error)
	LinkState(deviceID uuid.UUID) LinkState
	Unlink(ctx context.Context, deviceID uuid.UUID) error
	Forget(ctx context.Context, ref SessionRef) error
	Live(deviceID uuid.UUID) bool
	Held(deviceID uuid.UUID) (SessionRef, EngineSession, bool)
}

// Webhooks is the port a transition enqueues its outbound event through, inside the transition's unit of work.
type Webhooks interface {
	Enqueue(ctx context.Context, t events.Type, data any) error
}

// DeviceDescriber resolves the public id and display name a device event payload carries.
type DeviceDescriber interface {
	Describe(ctx context.Context, deviceID uuid.UUID) (publicID, name string, err error)
}

// Inbound records messaging events; nil means they are dropped with a debug log.
type Inbound interface {
	RecordInbound(ctx context.Context, ref SessionRef, m InboundMessage) error
	RecordReceipt(ctx context.Context, ref SessionRef, r Receipt) error
	UpsertContact(ctx context.Context, ref SessionRef, c ContactUpdate) error
}

// Service is the WhatsApp sessions driving port and the runtime's event sink.
type Service struct {
	store      Store
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
	uow        tenant.UnitOfWork
	runtime    SessionRuntime
	webhooks   Webhooks
	devices    DeviceDescriber
	clock      func() time.Time
	inbound    Inbound
}

// NewService binds the service to its dependencies.
func NewService(store Store, log *slog.Logger, unexpected apperror.UnexpectedFunc, uow tenant.UnitOfWork, runtime SessionRuntime, webhooks Webhooks, devices DeviceDescriber, clock func() time.Time) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{
		store:      store,
		log:        log.With("module", "whatsapp"),
		unexpected: unexpected,
		uow:        uow,
		runtime:    runtime,
		webhooks:   webhooks,
		devices:    devices,
		clock:      clock,
	}
}

// SetInbound attaches the messaging ports; it is set once, before the runtime runs.
func (s *Service) SetInbound(in Inbound) error {
	if s.inbound != nil {
		return &SetupError{Reason: "SetInbound called twice"}
	}
	s.inbound = in
	return nil
}

// StatusByDevices returns one Status per id; a device with no session row reads as unlinked.
func (s *Service) StatusByDevices(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Status, error) {
	ctx, span := tracer.Start(ctx, "whatsapp.StatusByDevices", trace.WithAttributes(attribute.Int("device.count", len(ids))))
	defer span.End()

	if _, err := tenant.From(ctx); err != nil {
		return nil, record(span, err)
	}
	rows, err := s.store.ByDevices(ctx, ids)
	if err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "whatsapp.StatusByDevices: load", err)
	}
	out := make(map[uuid.UUID]Status, len(ids))
	for _, id := range ids {
		out[id] = Status{State: StateUnlinked, Live: s.runtime.Live(id)}
	}
	for _, row := range rows {
		out[row.DeviceID] = Status{
			State:    row.State,
			Phone:    row.Phone,
			PushName: row.PushName,
			Reason:   row.Reason,
			LastSeen: row.LastSeenAt,
			Live:     s.runtime.Live(row.DeviceID),
		}
	}
	return out, nil
}

// Link starts a QR link attempt for deviceID and records the session as linking.
func (s *Service) Link(ctx context.Context, deviceID uuid.UUID) (LinkState, error) {
	return s.link(ctx, "whatsapp.Link", deviceID, s.runtime.Link)
}

// LinkWithPhone starts or reuses a link attempt and returns its 8-character pairing code.
func (s *Service) LinkWithPhone(ctx context.Context, deviceID uuid.UUID, phone string) (LinkState, error) {
	return s.link(ctx, "whatsapp.LinkWithPhone", deviceID, func(ctx context.Context, ref SessionRef) (LinkState, error) {
		return s.runtime.LinkWithPhone(ctx, ref, phone)
	})
}

// LinkState returns the in-memory state of deviceID's link attempt.
func (s *Service) LinkState(ctx context.Context, deviceID uuid.UUID) (LinkState, error) {
	_, span := tracer.Start(ctx, "whatsapp.LinkState", deviceAttr(deviceID))
	defer span.End()

	if _, err := tenant.From(ctx); err != nil {
		return LinkState{}, record(span, err)
	}
	return s.runtime.LinkState(deviceID), nil
}

// Unlink logs the account out; a device that was never linked is a no-op.
func (s *Service) Unlink(ctx context.Context, deviceID uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "whatsapp.Unlink", deviceAttr(deviceID))
	defer span.End()

	row, err := s.row(ctx, span, "whatsapp.Unlink", deviceID)
	if err != nil {
		return err
	}
	err = s.runtime.Unlink(ctx, deviceID)
	if err == nil {
		return nil
	}
	if IsNotOwnedError(err) && (row == nil || row.JID == "") {
		return nil
	}
	return record(span, err)
}

// Forget removes every trace of deviceID's session: engine state, lease and session row.
func (s *Service) Forget(ctx context.Context, deviceID uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "whatsapp.Forget", deviceAttr(deviceID))
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return record(span, err)
	}
	row, err := s.row(ctx, span, "whatsapp.Forget", deviceID)
	if err != nil {
		return err
	}
	ref := SessionRef{DeviceID: deviceID, OrgID: tc.OrgID, ProjectID: tc.ProjectID}
	if row != nil {
		ref.JID = row.JID
	}
	if err := s.runtime.Forget(ctx, ref); err != nil {
		return record(span, err)
	}
	return s.inTx(ctx, span, "whatsapp.Forget", deviceID, func(ctx context.Context) error {
		if err := s.store.Delete(ctx, deviceID); err != nil && !IsSessionNotFoundError(err) {
			return err
		}
		return nil
	})
}

// NOTE: the linking row is written before the runtime starts, so an early OnState(unlinked) from the attempt finds a row to move; a refusal restores the row as it was.
func (s *Service) link(ctx context.Context, op string, deviceID uuid.UUID, start func(context.Context, SessionRef) (LinkState, error)) (LinkState, error) {
	ctx, span := tracer.Start(ctx, op, deviceAttr(deviceID))
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return LinkState{}, record(span, err)
	}
	prior, err := s.row(ctx, span, op, deviceID)
	if err != nil {
		return LinkState{}, err
	}
	if prior != nil && prior.JID != "" {
		return LinkState{}, record(span, &AlreadyLinkedError{ID: deviceID.String()})
	}
	var mark linkMark
	if err := s.uow(ctx, func(ctx context.Context) error {
		var err error
		mark, err = s.markLinking(ctx, tc, deviceID)
		return err
	}); err != nil {
		if IsAlreadyLinkedError(err) {
			return LinkState{}, record(span, err)
		}
		span.RecordError(err)
		return LinkState{}, s.unexpected(ctx, op+": unit of work", err, "device_id", deviceID)
	}
	st, err := start(ctx, SessionRef{DeviceID: deviceID, OrgID: tc.OrgID, ProjectID: tc.ProjectID})
	if err == nil {
		return st, nil
	}
	if IsEngineError(err) {
		s.log.WarnContext(ctx, "whatsapp: link failed in the engine", "device_id", deviceID, "err", err)
	}
	if rerr := s.inTx(ctx, span, op+": rollback", deviceID, func(ctx context.Context) error {
		return s.restore(ctx, deviceID, mark)
	}); rerr != nil {
		s.log.ErrorContext(ctx, "whatsapp: linking row rollback failed", "device_id", deviceID, "err", rerr)
	}
	return LinkState{}, record(span, err)
}

type linkMark struct {
	prior *Session
	wrote bool
}

// NOTE: the row is re-read inside the unit of work; a row that turned linked, or moved, since the caller's read means another link won the race, reported as AlreadyLinkedError.
func (s *Service) markLinking(ctx context.Context, tc tenant.Context, deviceID uuid.UUID) (linkMark, error) {
	prior, err := s.store.ByDevice(ctx, deviceID)
	if err != nil && !IsSessionNotFoundError(err) {
		return linkMark{}, err
	}
	if prior == nil || err != nil {
		prior = nil
		if err := s.store.Save(ctx, NewSession(deviceID, tc.OrgID, tc.ProjectID, EngineWhatsmeow), 0); err != nil {
			return linkMark{}, err
		}
		return linkMark{wrote: true}, nil
	}
	if prior.JID != "" {
		return linkMark{}, &AlreadyLinkedError{ID: deviceID.String()}
	}
	next := *prior
	out, err := next.Linking()
	if err != nil || !out.Changed {
		return linkMark{prior: prior}, err
	}
	if err := s.store.Save(ctx, &next, prior.Version); err != nil {
		if IsStaleVersionError(err) {
			return linkMark{}, &AlreadyLinkedError{ID: deviceID.String()}
		}
		return linkMark{}, err
	}
	return linkMark{prior: prior, wrote: true}, nil
}

// NOTE: restore undoes only a write this link made, and only while the row is still the linking row it wrote; a state the engine reported since is left alone.
func (s *Service) restore(ctx context.Context, deviceID uuid.UUID, mark linkMark) error {
	if !mark.wrote {
		return nil
	}
	current, err := s.store.ByDevice(ctx, deviceID)
	if err != nil {
		if IsSessionNotFoundError(err) {
			return nil
		}
		return err
	}
	if current.State != StateLinking {
		return nil
	}
	if mark.prior == nil {
		return s.store.Delete(ctx, deviceID)
	}
	back := *mark.prior
	back.Version = current.Version
	return s.store.Save(ctx, &back, current.Version)
}

func (s *Service) row(ctx context.Context, span trace.Span, op string, deviceID uuid.UUID) (*Session, error) {
	row, err := s.store.ByDevice(ctx, deviceID)
	if err == nil {
		return row, nil
	}
	if IsSessionNotFoundError(err) {
		return nil, nil //nolint:nilnil // no row is a normal state for an unlinked device.
	}
	span.RecordError(err)
	return nil, s.unexpected(ctx, op+": load", err, "device_id", deviceID)
}

func (s *Service) inTx(ctx context.Context, span trace.Span, op string, deviceID uuid.UUID, fn func(ctx context.Context) error) error {
	if err := s.uow(ctx, fn); err != nil {
		span.RecordError(err)
		return s.unexpected(ctx, op+": unit of work", err, "device_id", deviceID)
	}
	return nil
}

func deviceAttr(id uuid.UUID) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String("device.id", id.String()))
}

func record(span trace.Span, err error) error {
	span.RecordError(err)
	return err
}
