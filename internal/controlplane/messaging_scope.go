package controlplane

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/project"
)

type messagingScope struct {
	projects *project.Service
	devices  *device.Service
}

func newMessagingScope(projects *project.Service, devices *device.Service) messagingScope {
	return messagingScope{projects: projects, devices: devices}
}

// SECURITY: the project goes through scopeToActiveProject, whose ReachesWholeProject refuses any device-bound key on this project-wide entry point, so an API key never leaves its own project and a bound key never runs a project-wide verb.
func (s messagingScope) project(ctx context.Context, projectIDRaw string) (context.Context, error) {
	return scopeToActiveProject(ctx, s.projects, projectIDRaw)
}

func (s messagingScope) device(ctx context.Context, raw string) (uuid.UUID, error) {
	if strings.TrimSpace(raw) != "" {
		// SECURITY: Resolve is project-scoped and answers NotFound for a malformed id without a query, so another tenant's device, or a UUID, is NotFound here, before any group or transport call.
		d, err := s.devices.Resolve(ctx, raw)
		if err != nil {
			return uuid.Nil, err
		}
		return d.ID, nil
	}
	views, err := s.devices.List(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if len(views) == 1 {
		return views[0].Device.ID, nil
	}
	names := make([]string, 0, len(views))
	for _, v := range views {
		names = append(names, v.Device.Name+" ("+v.Device.PublicID+")")
	}
	return uuid.Nil, &DeviceUnresolvedError{Candidates: names}
}

func (s messagingScope) optionalDevice(ctx context.Context, raw string) (*uuid.UUID, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil //nolint:nilnil // an absent optional id is not an error.
	}
	id, err := s.device(ctx, raw)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// NOTE: one batch read per page, never one per row; an empty page reads nothing.
func (s messagingScope) devicePublicIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]string{}, nil
	}
	return s.devices.PublicIDs(ctx, ids)
}
