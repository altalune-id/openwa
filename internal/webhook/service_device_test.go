package webhook_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/webhook"
)

type devicesStub map[string]uuid.UUID

func (d devicesStub) ResolveID(_ context.Context, publicID string) (uuid.UUID, error) {
	id, ok := d[publicID]
	if !ok {
		return uuid.Nil, &webhook.DeviceNotFoundError{ID: publicID}
	}
	return id, nil
}

func (h *harness) enqueueFor(ctx context.Context, typ events.Type, deviceID uuid.UUID, data any) error {
	return fakes.UnitOfWork(ctx, func(ctx context.Context) error {
		return h.svc.EnqueueForDevice(ctx, typ, deviceID, data)
	})
}

func (h *harness) createFor(ctx context.Context, t *testing.T, publicID string, types ...events.Type) *webhook.Endpoint {
	t.Helper()
	e, _, err := h.svc.Create(ctx, validURL, "", types, publicID)
	require.NoError(t, err)
	return e
}

func deviceBoundTypes() []events.Type {
	var out []events.Type
	for _, s := range events.All() {
		if s.DeviceBound {
			out = append(out, s.Type)
		}
	}
	return out
}

func TestService_Enqueue_RefusesEveryDeviceBoundType(t *testing.T) {
	h := newHarness(t)
	ctx, _ := svcCtx(t)
	h.create(ctx, t, events.MessageMatched)

	bound := deviceBoundTypes()
	require.NotEmpty(t, bound)
	for _, typ := range bound {
		err := h.enqueue(ctx, typ, events.MessageEventV1{})
		assert.True(t, webhook.IsDeviceRequiredError(err), "%s: got %T: %v", typ, err, err)
	}
	assert.Empty(t, h.outbox.Entries())
	assert.Zero(t, *h.unexpected)
}

func TestService_EnqueueForDevice_RefusesAProjectWideType(t *testing.T) {
	h := newHarness(t)
	ctx, _ := svcCtx(t)
	h.create(ctx, t, events.PostPublished)

	err := h.enqueueFor(ctx, events.PostPublished, uuid.New(), postPublished())
	assert.True(t, webhook.IsDeviceNotBoundError(err), "got %T: %v", err, err)
	assert.Empty(t, h.outbox.Entries())
}

func TestService_EnqueueForDevice_ScopedEndpointsGetOnlyTheirDevice(t *testing.T) {
	h := newHarness(t)
	a, b := uuid.New(), uuid.New()
	h.devices["dev_a"], h.devices["dev_b"] = a, b
	ctx, _ := svcCtx(t)
	scopedA := h.createFor(ctx, t, "dev_a", events.MessageMatched)
	h.createFor(ctx, t, "dev_b", events.MessageMatched)
	wide := h.createFor(ctx, t, "", events.MessageMatched)

	require.NoError(t, h.enqueueFor(ctx, events.MessageMatched, a, events.MessageEventV1{}))

	entries := h.outbox.Entries()
	require.Len(t, entries, 2)
	assert.ElementsMatch(t, []string{scopedA.ID.String(), wide.ID.String()}, []string{entries[0].Target, entries[1].Target})
}

func TestService_SendTest_ReachesAScopedEndpoint(t *testing.T) {
	h := newHarness(t)
	h.devices["dev_a"] = uuid.New()
	ctx, _ := svcCtx(t)
	e := h.createFor(ctx, t, "dev_a", events.MessageMatched)

	require.NoError(t, h.svc.SendTest(ctx, e.ID))
	entries := h.outbox.Entries()
	require.Len(t, entries, 1)
	assert.Equal(t, e.ID.String(), entries[0].Target)
}

func TestService_Create_Device(t *testing.T) {
	t.Run("stores the resolved device", func(t *testing.T) {
		h := newHarness(t)
		a := uuid.New()
		h.devices["dev_a"] = a
		ctx, _ := svcCtx(t)

		e := h.createFor(ctx, t, "dev_a", events.MessageMatched)
		stored, err := h.store.ByID(ctx, e.ID)
		require.NoError(t, err)
		require.NotNil(t, stored.DeviceID)
		assert.Equal(t, a, *stored.DeviceID)
	})

	t.Run("empty means every number", func(t *testing.T) {
		h := newHarness(t)
		ctx, _ := svcCtx(t)
		e := h.createFor(ctx, t, "", events.MessageMatched)
		assert.Nil(t, e.DeviceID)
	})

	t.Run("a device the project cannot resolve is refused and nothing is created", func(t *testing.T) {
		h := newHarness(t)
		ctx, _ := svcCtx(t)

		_, _, err := h.svc.Create(ctx, validURL, "", []events.Type{events.MessageMatched}, "dev_sibling")
		assert.True(t, webhook.IsDeviceNotFoundError(err), "got %T: %v", err, err)
		list, err := h.svc.List(ctx)
		require.NoError(t, err)
		assert.Empty(t, list)
		assert.Zero(t, *h.unexpected)
	})

	t.Run("a scoped endpoint cannot subscribe to a project-wide type", func(t *testing.T) {
		h := newHarness(t)
		h.devices["dev_a"] = uuid.New()
		ctx, _ := svcCtx(t)

		_, _, err := h.svc.Create(ctx, validURL, "", []events.Type{events.MessageMatched, events.PostPublished}, "dev_a")
		assert.True(t, webhook.IsInvalidEventTypesError(err), "got %T: %v", err, err)
	})
}

func TestService_Update_KeepsTheDevice(t *testing.T) {
	h := newHarness(t)
	a := uuid.New()
	h.devices["dev_a"] = a
	ctx, _ := svcCtx(t)
	e := h.createFor(ctx, t, "dev_a", events.MessageMatched)

	got, err := h.svc.Update(ctx, e.ID, "https://other.example.com/hook", "x", []events.Type{events.MessageReceived}, true)
	require.NoError(t, err)
	require.NotNil(t, got.DeviceID)
	assert.Equal(t, a, *got.DeviceID)

	_, err = h.svc.Update(ctx, e.ID, e.URL, "", []events.Type{events.PostPublished}, true)
	assert.True(t, webhook.IsInvalidEventTypesError(err), "a scoped endpoint stays device-bound on update: got %T: %v", err, err)
}

func TestService_Create_DuplicateIsARefusal(t *testing.T) {
	h := newHarness(t)
	h.devices["dev_a"] = uuid.New()
	ctx, _ := svcCtx(t)
	h.store.SaveErr = func(e *webhook.Endpoint) error { return &webhook.DuplicateEndpointError{URL: e.URL} }

	_, secret, err := h.svc.Create(ctx, validURL, "", []events.Type{events.MessageMatched}, "dev_a")
	assert.True(t, webhook.IsDuplicateEndpointError(err), "got %T: %v", err, err)
	assert.Empty(t, secret)
	assert.Zero(t, *h.unexpected, "a duplicate is the caller's refusal, not our failure")
}
