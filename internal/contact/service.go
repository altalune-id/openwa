package contact

import (
	"context"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/platform/keyset"
	"altalune.id/openwa/internal/platform/tenant"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer = otel.Tracer("altalune.id/openwa/internal/contact")

// ContactInput is a contact update reported by the engine.
type ContactInput struct {
	DeviceID     uuid.UUID
	JID          string
	LID          string
	Phone        string
	Name         string
	PushName     string
	BusinessName string
}

// SenderInput is what an inbound message says about its sender.
type SenderInput struct {
	JID      string
	LID      string
	Phone    string
	PushName string
}

// Service is the contact driving port.
type Service struct {
	store      Store
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
}

// NewService binds the service to its dependencies.
func NewService(store Store, log *slog.Logger, unexpected apperror.UnexpectedFunc) *Service {
	return &Service{store: store, log: log.With("module", "contact"), unexpected: unexpected}
}

// List pages the caller's project.
func (s *Service) List(ctx context.Context, opts ListOpts) ([]*Contact, string, error) {
	ctx, span := tracer.Start(ctx, "contact.List")
	defer span.End()
	if _, err := tenant.From(ctx); err != nil {
		return nil, "", err
	}
	out, next, err := s.store.List(ctx, opts.WithDefaults())
	if err != nil {
		span.RecordError(err)
		if keyset.IsInvalidCursorError(err) {
			return nil, "", err
		}
		return nil, "", s.unexpected(ctx, "contact.List: list", err)
	}
	return out, next, nil
}

// Get returns the caller's project contact that jid names on deviceID.
func (s *Service) Get(ctx context.Context, deviceID uuid.UUID, jid string) (*Contact, error) {
	ctx, span := tracer.Start(ctx, "contact.Get")
	defer span.End()
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	c, err := s.store.ByJID(ctx, deviceID, jid)
	if err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) {
			return nil, &NotFoundError{ID: jid}
		}
		return nil, s.unexpected(ctx, "contact.Get: load", err, "device_id", deviceID)
	}
	if c.OrgID != tc.OrgID || c.ProjectID != tc.ProjectID {
		return nil, &NotFoundError{ID: jid}
	}
	return c, nil
}

// UpsertFromEngine merges an engine contact update.
func (s *Service) UpsertFromEngine(ctx context.Context, in ContactInput) error {
	return s.upsert(ctx, "contact.UpsertFromEngine", in.DeviceID, Contact{
		JID: in.JID, LID: in.LID, Phone: in.Phone, Name: in.Name, PushName: in.PushName, BusinessName: in.BusinessName,
	})
}

// UpsertFromMessage records the sender of an inbound message, merging its push name only.
func (s *Service) UpsertFromMessage(ctx context.Context, deviceID uuid.UUID, in SenderInput) error {
	return s.upsert(ctx, "contact.UpsertFromMessage", deviceID, Contact{JID: in.JID, LID: in.LID, Phone: in.Phone, PushName: in.PushName})
}

func (s *Service) upsert(ctx context.Context, op string, deviceID uuid.UUID, in Contact) error {
	ctx, span := tracer.Start(ctx, op)
	defer span.End()
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(in.JID)
	if in.Phone != "" {
		key = in.Phone + "@s.whatsapp.net"
	}
	if key == "" {
		return nil
	}
	c := New(tc.OrgID, tc.ProjectID, deviceID, key)
	c.LID, c.Phone, c.Name, c.PushName, c.BusinessName = in.LID, in.Phone, in.Name, in.PushName, in.BusinessName
	if err := s.store.Upsert(ctx, c); err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) {
			return err
		}
		return s.unexpected(ctx, op+": upsert", err, "device_id", deviceID)
	}
	return nil
}
