package boot

import (
	"context"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/webhook"
)

type webhookDevices struct{ svc *device.Service }

var _ webhook.Devices = (*webhookDevices)(nil)

func (s *webhookDevices) ResolveID(ctx context.Context, publicID string) (uuid.UUID, error) {
	d, err := s.svc.Resolve(ctx, publicID)
	if device.IsNotFoundError(err) {
		return uuid.Nil, &webhook.DeviceNotFoundError{ID: publicID}
	}
	if err != nil {
		return uuid.Nil, err
	}
	return d.ID, nil
}
