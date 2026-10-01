package whatsapp

import (
	"context"
	"os"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/platform/tenant"
)

// SECURITY: a held session answers only to the tenant its lease names; a device id from another org or project is not owned here, which every surface masks as not-found.
func (s *Service) messaging(ctx context.Context, deviceID uuid.UUID) (MessagingSession, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	ref, sess, ok := s.runtime.Held(deviceID)
	if !ok || ref.OrgID != tc.OrgID || ref.ProjectID != tc.ProjectID {
		return nil, &NotOwnedError{ID: deviceID.String()}
	}
	ms, ok := sess.(MessagingSession)
	if !ok {
		return nil, &UnsupportedError{Feature: "messaging"}
	}
	return ms, nil
}

// MarkRead sends read (or played) receipts from the device.
func (s *Service) MarkRead(ctx context.Context, deviceID uuid.UUID, chat, sender string, ids []string, played bool) error {
	ctx, span := tracer.Start(ctx, "whatsapp.MarkRead")
	defer span.End()
	ms, err := s.messaging(ctx, deviceID)
	if err != nil {
		return err
	}
	return ms.MarkRead(ctx, chat, sender, ids, played)
}

// FetchMedia downloads an attachment into an unlinked temp file the caller closes.
func (s *Service) FetchMedia(ctx context.Context, deviceID uuid.UUID, keys MediaKeys, kind string) (*os.File, error) {
	ctx, span := tracer.Start(ctx, "whatsapp.FetchMedia")
	defer span.End()
	ms, err := s.messaging(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	return ms.FetchMedia(ctx, keys, kind)
}

// IsOnWhatsApp maps each registered phone to its JID.
func (s *Service) IsOnWhatsApp(ctx context.Context, deviceID uuid.UUID, phones []string) (map[string]string, error) {
	ctx, span := tracer.Start(ctx, "whatsapp.IsOnWhatsApp")
	defer span.End()
	ms, err := s.messaging(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	return ms.IsOnWhatsApp(ctx, phones)
}

// GroupList returns the groups the device has joined.
func (s *Service) GroupList(ctx context.Context, deviceID uuid.UUID) ([]GroupInfo, error) {
	ctx, span := tracer.Start(ctx, "whatsapp.GroupList")
	defer span.End()
	ms, err := s.messaging(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	return ms.GroupList(ctx)
}

// GroupInfo returns one group's details.
func (s *Service) GroupInfo(ctx context.Context, deviceID uuid.UUID, jid string) (GroupInfo, error) {
	ctx, span := tracer.Start(ctx, "whatsapp.GroupInfo")
	defer span.End()
	ms, err := s.messaging(ctx, deviceID)
	if err != nil {
		return GroupInfo{}, err
	}
	return ms.GroupInfo(ctx, jid)
}

// GroupJoin joins a group by invite link and returns its JID.
func (s *Service) GroupJoin(ctx context.Context, deviceID uuid.UUID, link string) (string, error) {
	ctx, span := tracer.Start(ctx, "whatsapp.GroupJoin")
	defer span.End()
	ms, err := s.messaging(ctx, deviceID)
	if err != nil {
		return "", err
	}
	return ms.GroupJoin(ctx, link)
}

// GroupLeave leaves a group.
func (s *Service) GroupLeave(ctx context.Context, deviceID uuid.UUID, jid string) error {
	ctx, span := tracer.Start(ctx, "whatsapp.GroupLeave")
	defer span.End()
	ms, err := s.messaging(ctx, deviceID)
	if err != nil {
		return err
	}
	return ms.GroupLeave(ctx, jid)
}
