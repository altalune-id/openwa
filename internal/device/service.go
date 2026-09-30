package device

import (
	"cmp"
	"context"
	"log/slog"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/platform/publicid"
	"altalune.id/openwa/internal/platform/tenant"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer = otel.Tracer("altalune.id/openwa/internal/device")

// Service is the devices driving port.
type Service struct {
	store      Store
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
	uow        tenant.UnitOfWork
	sessions   Sessions
}

// NewService binds the service to its dependencies.
func NewService(store Store, log *slog.Logger, unexpected apperror.UnexpectedFunc, uow tenant.UnitOfWork, sessions Sessions) *Service {
	return &Service{store: store, log: log.With("module", "device"), unexpected: unexpected, uow: uow, sessions: sessions}
}

// Create adds a device to the caller's project.
func (s *Service) Create(ctx context.Context, name string) (*Device, error) {
	ctx, span := tracer.Start(ctx, "device.Create")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, record(span, err)
	}
	for attempt := 0; ; attempt++ {
		pub, idErr := publicid.New(PublicIDPrefix)
		if idErr != nil {
			span.RecordError(idErr)
			return nil, s.unexpected(ctx, "device.Create: mint public id", idErr)
		}
		d, err := New(tc.OrgID, tc.ProjectID, pub, name)
		if err != nil {
			return nil, record(span, err)
		}
		err = s.store.Save(ctx, d, 0)
		switch {
		case err == nil:
			span.SetAttributes(attribute.String("device.id", d.ID.String()), attribute.String("device.public_id", d.PublicID))
			return d, nil
		case IsNameTakenError(err):
			return nil, record(span, err)
		case IsPublicIDTakenError(err) && attempt == 0:
			continue
		}
		span.RecordError(err)
		return nil, s.unexpected(ctx, "device.Create: save", err, "org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
}

// Resolve returns the caller's project's device whose public id is publicID; a malformed id is not-found without a query.
func (s *Service) Resolve(ctx context.Context, publicID string) (*Device, error) {
	ctx, span := tracer.Start(ctx, "device.Resolve", trace.WithAttributes(attribute.String("device.public_id", publicID)))
	defer span.End()

	tc, d, err := s.locatePublic(ctx, span, "device.Resolve", publicID)
	if err != nil {
		return nil, err
	}
	if d.ProjectID != tc.ProjectID {
		return nil, record(span, &NotFoundError{ID: publicID})
	}
	return d, nil
}

// Get returns one device of the caller's project with its session status.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (DeviceView, error) {
	ctx, span := startWithID(ctx, "device.Get", id)
	defer span.End()

	d, err := s.load(ctx, span, "device.Get", id)
	if err != nil {
		return DeviceView{}, err
	}
	statuses, err := s.sessions.StatusByDevices(ctx, []uuid.UUID{id})
	if err != nil {
		return DeviceView{}, record(span, publicErr(err, d.PublicID))
	}
	return DeviceView{Device: d, Status: statuses[id]}, nil
}

// Locate returns the caller's org's device whose public id is publicID, for callers that re-scope to the device's own project.
func (s *Service) Locate(ctx context.Context, publicID string) (*Device, error) {
	ctx, span := tracer.Start(ctx, "device.Locate", trace.WithAttributes(attribute.String("device.public_id", publicID)))
	defer span.End()

	_, d, err := s.locatePublic(ctx, span, "device.Locate", publicID)
	return d, err
}

// PublicIDs maps the caller's project's devices among ids to their public ids in one read, without loading session status.
func (s *Service) PublicIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	ctx, span := tracer.Start(ctx, "device.PublicIDs")
	defer span.End()

	out, err := s.store.PublicIDsByIDs(ctx, ids)
	if err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "device.PublicIDs: load", err)
	}
	return out, nil
}

// IDs maps the caller's project's devices among publicIDs to their UUIDs in one read; malformed ids are dropped before the query.
func (s *Service) IDs(ctx context.Context, publicIDs []string) (map[string]uuid.UUID, error) {
	ctx, span := tracer.Start(ctx, "device.IDs")
	defer span.End()

	valid := make([]string, 0, len(publicIDs))
	for _, p := range publicIDs {
		if publicid.Valid(PublicIDPrefix, p) {
			valid = append(valid, p)
		}
	}
	if len(valid) == 0 {
		return map[string]uuid.UUID{}, nil
	}
	out, err := s.store.IDsByPublicIDs(ctx, valid)
	if err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "device.IDs: load", err)
	}
	return out, nil
}

// SECURITY: a malformed public id is answered not-found before any query, so the id space cannot be probed through the store.
func (s *Service) locatePublic(ctx context.Context, span trace.Span, op, publicID string) (tenant.Context, *Device, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return tenant.Context{}, nil, record(span, err)
	}
	if !publicid.Valid(PublicIDPrefix, publicID) {
		return tenant.Context{}, nil, record(span, &NotFoundError{ID: publicID})
	}
	d, err := s.store.ByPublicID(ctx, publicID)
	if err != nil {
		if IsNotFoundError(err) {
			return tenant.Context{}, nil, record(span, err)
		}
		span.RecordError(err)
		return tenant.Context{}, nil, s.unexpected(ctx, op+": load", err, "device_public_id", publicID)
	}
	if d.OrgID != tc.OrgID {
		return tenant.Context{}, nil, record(span, &NotFoundError{ID: publicID})
	}
	return tc, d, nil
}

// List returns the project's devices, newest first, with their statuses from one batched lookup.
func (s *Service) List(ctx context.Context) ([]DeviceView, error) {
	ctx, span := tracer.Start(ctx, "device.List")
	defer span.End()

	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, record(span, err)
	}
	ds, err := s.store.List(ctx, ListOpts{})
	if err != nil {
		span.RecordError(err)
		return nil, s.unexpected(ctx, "device.List: list", err, "org_id", tc.OrgID, "project_id", tc.ProjectID)
	}
	ids := make([]uuid.UUID, 0, len(ds))
	for _, d := range ds {
		ids = append(ids, d.ID)
	}
	statuses, err := s.sessions.StatusByDevices(ctx, ids)
	if err != nil {
		return nil, record(span, err)
	}
	out := make([]DeviceView, 0, len(ds))
	for _, d := range ds {
		out = append(out, DeviceView{Device: d, Status: statuses[d.ID]})
	}
	return out, nil
}

// Update applies u in one conditional write; ifVersion 0 guards on the loaded version.
func (s *Service) Update(ctx context.Context, id uuid.UUID, u Update, ifVersion int) (*Device, error) {
	ctx, span := startWithID(ctx, "device.Update", id)
	defer span.End()

	d, err := s.load(ctx, span, "device.Update", id)
	if err != nil {
		return nil, err
	}
	if ifVersion != 0 && ifVersion != d.Version {
		return nil, record(span, &StaleVersionError{Want: ifVersion, Got: d.Version})
	}
	if u.Name != nil {
		if err := d.Rename(*u.Name); err != nil {
			return nil, record(span, err)
		}
	}
	if u.Rules != nil {
		if err := d.SetRules(*u.Rules); err != nil {
			return nil, record(span, err)
		}
	}
	if err := s.store.Save(ctx, d, cmp.Or(ifVersion, d.Version)); err != nil {
		if IsNameTakenError(err) || IsStaleVersionError(err) || IsNotFoundError(err) {
			return nil, record(span, publicErr(err, d.PublicID))
		}
		span.RecordError(err)
		return nil, s.unexpected(ctx, "device.Update: save", err, "device_id", id)
	}
	d.Version++
	return d, nil
}

// Rename changes the name only.
func (s *Service) Rename(ctx context.Context, id uuid.UUID, name string, ifVersion int) (*Device, error) {
	return s.Update(ctx, id, Update{Name: &name}, ifVersion)
}

// UpdateRules replaces the inbound rules only.
func (s *Service) UpdateRules(ctx context.Context, id uuid.UUID, rules Rules, ifVersion int) (*Device, error) {
	return s.Update(ctx, id, Update{Rules: &rules}, ifVersion)
}

// Delete forgets the WhatsApp session outside any transaction, then deletes the device; a Forget failure keeps the device.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	ctx, span := startWithID(ctx, "device.Delete", id)
	defer span.End()

	d, err := s.load(ctx, span, "device.Delete", id)
	if err != nil {
		return err
	}
	if err := s.sessions.Forget(ctx, id); err != nil {
		return record(span, publicErr(err, d.PublicID))
	}
	var inner error
	err = s.uow(ctx, func(ctx context.Context) error {
		inner = s.store.Delete(ctx, id)
		return inner
	})
	if err == nil {
		return nil
	}
	if IsNotFoundError(inner) {
		return record(span, publicErr(inner, d.PublicID))
	}
	span.RecordError(err)
	return s.unexpected(ctx, "device.Delete: delete", err, "device_id", id)
}

// StartLink starts a QR pairing attempt.
func (s *Service) StartLink(ctx context.Context, id uuid.UUID) (LinkState, error) {
	return s.linkVerb(ctx, "device.StartLink", id, func(ctx context.Context) (LinkState, error) { return s.sessions.Link(ctx, id) })
}

// LinkWithPhone starts or reuses a pairing attempt and returns its pairing code.
func (s *Service) LinkWithPhone(ctx context.Context, id uuid.UUID, phone string) (LinkState, error) {
	return s.linkVerb(ctx, "device.LinkWithPhone", id, func(ctx context.Context) (LinkState, error) {
		return s.sessions.LinkWithPhone(ctx, id, phone)
	})
}

// LinkState returns the current pairing attempt.
func (s *Service) LinkState(ctx context.Context, id uuid.UUID) (LinkState, error) {
	return s.linkVerb(ctx, "device.LinkState", id, func(ctx context.Context) (LinkState, error) { return s.sessions.LinkState(ctx, id) })
}

// Unlink logs the device's WhatsApp account out and keeps the device.
func (s *Service) Unlink(ctx context.Context, id uuid.UUID) error {
	ctx, span := startWithID(ctx, "device.Unlink", id)
	defer span.End()

	d, err := s.load(ctx, span, "device.Unlink", id)
	if err != nil {
		return err
	}
	if err := s.sessions.Unlink(ctx, id); err != nil {
		return record(span, publicErr(err, d.PublicID))
	}
	return nil
}

func (s *Service) linkVerb(ctx context.Context, op string, id uuid.UUID, fn func(context.Context) (LinkState, error)) (LinkState, error) {
	ctx, span := startWithID(ctx, op, id)
	defer span.End()

	d, err := s.load(ctx, span, op, id)
	if err != nil {
		return LinkState{}, err
	}
	st, err := fn(ctx)
	if err != nil {
		return LinkState{}, record(span, publicErr(err, d.PublicID))
	}
	return st, nil
}

// SECURITY: the store filters by org only, so the project check here is what keeps a sibling project's device out of reach.
func (s *Service) load(ctx context.Context, span trace.Span, op string, id uuid.UUID) (*Device, error) {
	tc, d, err := s.locate(ctx, span, op, id)
	if err != nil {
		return nil, err
	}
	if d.ProjectID != tc.ProjectID {
		return nil, record(span, &NotFoundError{})
	}
	return d, nil
}

func (s *Service) locate(ctx context.Context, span trace.Span, op string, id uuid.UUID) (tenant.Context, *Device, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return tenant.Context{}, nil, record(span, err)
	}
	d, err := s.store.ByID(ctx, id)
	if err != nil {
		if IsNotFoundError(err) {
			return tenant.Context{}, nil, record(span, &NotFoundError{})
		}
		span.RecordError(err)
		return tenant.Context{}, nil, s.unexpected(ctx, op+": load", err, "device_id", id)
	}
	if d.OrgID != tc.OrgID {
		return tenant.Context{}, nil, record(span, &NotFoundError{})
	}
	return tc, d, nil
}

func startWithID(ctx context.Context, name string, id uuid.UUID) (context.Context, trace.Span) {
	return tracer.Start(ctx, name, trace.WithAttributes(attribute.String("device.id", id.String())))
}

func record(span trace.Span, err error) error {
	span.RecordError(err)
	return err
}
