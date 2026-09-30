package device_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/publicid"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/testutil/fakes"
)

type svcFixture struct {
	svc      *device.Service
	store    *fakes.Device
	sessions *fakes.DeviceSessions
	tc       tenant.Context
}

func newSvcFixture(t *testing.T) *svcFixture {
	t.Helper()
	f := &svcFixture{store: fakes.NewDevice(), sessions: fakes.NewDeviceSessions(), tc: tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}}
	unexpected := func(_ context.Context, msg string, cause error, _ ...any) *apperror.AppError {
		return apperror.New("GEN900", msg, codes.Internal).WithCause(cause)
	}
	f.svc = device.NewService(f.store, slog.New(slog.NewTextHandler(io.Discard, nil)), unexpected, fakes.UnitOfWork, f.sessions)
	return f
}

func (f *svcFixture) ctx(t *testing.T) context.Context {
	t.Helper()
	return tenant.Into(t.Context(), f.tc)
}

func TestService_CreateAndNameTaken(t *testing.T) {
	f := newSvcFixture(t)
	d, err := f.svc.Create(f.ctx(t), "Sales")
	require.NoError(t, err)
	require.Equal(t, f.tc.OrgID, d.OrgID)
	require.Equal(t, f.tc.ProjectID, d.ProjectID)
	require.True(t, publicid.Valid(device.PublicIDPrefix, d.PublicID), "got %q", d.PublicID)
	_, err = f.svc.Create(f.ctx(t), "sales")
	require.True(t, device.IsNameTakenError(err))
	_, err = f.svc.Create(f.ctx(t), " ")
	require.True(t, device.IsInvalidNameError(err))
}

func TestService_CreateRetriesAPublicIDCollisionOnce(t *testing.T) {
	f := newSvcFixture(t)
	collisions := 1
	var tried []string
	f.store.SaveFn = func(ctx context.Context, d *device.Device, ifVersion int) error {
		tried = append(tried, d.PublicID)
		if collisions > 0 {
			collisions--
			return &device.PublicIDTakenError{PublicID: d.PublicID}
		}
		f.store.SaveFn = nil
		return f.store.Save(ctx, d, ifVersion)
	}
	d, err := f.svc.Create(f.ctx(t), "Sales")
	require.NoError(t, err)
	require.Len(t, tried, 2)
	require.NotEqual(t, tried[0], tried[1], "the retry mints a fresh id")
	require.Equal(t, tried[1], d.PublicID)

	f.store.SaveFn = func(_ context.Context, d *device.Device, _ int) error {
		return &device.PublicIDTakenError{PublicID: d.PublicID}
	}
	_, err = f.svc.Create(f.ctx(t), "Support")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr, "a second collision is reported through unexpected, never as a user-facing error")
}

func TestService_ResolveAndLocateTakeThePublicID(t *testing.T) {
	f := newSvcFixture(t)
	d, err := f.svc.Create(f.ctx(t), "A")
	require.NoError(t, err)

	got, err := f.svc.Resolve(f.ctx(t), d.PublicID)
	require.NoError(t, err)
	require.Equal(t, d.ID, got.ID)

	sibling := tenant.Into(t.Context(), tenant.Context{OrgID: f.tc.OrgID, ProjectID: uuid.New()})
	_, err = f.svc.Resolve(sibling, d.PublicID)
	require.True(t, device.IsNotFoundError(err), "Resolve is project-scoped")
	located, err := f.svc.Locate(sibling, d.PublicID)
	require.NoError(t, err, "Locate is org-wide for callers that re-scope to the row's project")
	require.Equal(t, d.ProjectID, located.ProjectID)

	for _, bad := range []string{d.ID.String(), "dev_short", "cht_V1StGXR8Z5jdHi6B", ""} {
		_, err = f.svc.Resolve(f.ctx(t), bad)
		require.True(t, device.IsNotFoundError(err), "%q must be not-found", bad)
		_, err = f.svc.Locate(f.ctx(t), bad)
		require.True(t, device.IsNotFoundError(err), "%q must be not-found", bad)
	}
}

type idNamingErr struct{ ID string }

func (e *idNamingErr) Error() string                { return "port failure: device_id=" + e.ID }
func (e *idNamingErr) WithDeviceID(id string) error { return &idNamingErr{ID: id} }

// SECURITY: an error that leaves the service must never name the device by its internal UUID.
func TestService_ErrorsNameTheDeviceByItsPublicID(t *testing.T) {
	f := newSvcFixture(t)
	d, err := f.svc.Create(f.ctx(t), "Sales")
	require.NoError(t, err)

	f.sessions.LinkErr = &idNamingErr{ID: d.ID.String()}
	_, err = f.svc.StartLink(f.ctx(t), d.ID)
	got, ok := errors.AsType[*idNamingErr](err)
	require.True(t, ok, "the port's error type survives, got %v", err)
	require.Equal(t, d.PublicID, got.ID)
	require.NotContains(t, err.Error(), d.ID.String())

	f.sessions.UnlinkErr = &idNamingErr{ID: d.ID.String()}
	err = f.svc.Unlink(f.ctx(t), d.ID)
	require.NotContains(t, err.Error(), d.ID.String())

	f.store.ByIDFn = func(_ context.Context, id uuid.UUID) (*device.Device, error) {
		return nil, &device.NotFoundError{ID: id.String()}
	}
	_, err = f.svc.Get(f.ctx(t), d.ID)
	require.True(t, device.IsNotFoundError(err))
	require.NotContains(t, err.Error(), d.ID.String(), "a lookup by UUID reports not-found without naming the UUID")
}

func TestService_BatchIDReadsSkipTheSessionPort(t *testing.T) {
	f := newSvcFixture(t)
	a, err := f.svc.Create(f.ctx(t), "A")
	require.NoError(t, err)
	pubs, err := f.svc.PublicIDs(f.ctx(t), []uuid.UUID{a.ID, uuid.New()})
	require.NoError(t, err)
	require.Equal(t, map[uuid.UUID]string{a.ID: a.PublicID}, pubs)
	ids, err := f.svc.IDs(f.ctx(t), []string{a.PublicID, "dev_short"})
	require.NoError(t, err)
	require.Equal(t, map[string]uuid.UUID{a.PublicID: a.ID}, ids)
	require.Equal(t, 2, f.store.BatchReads(), "one store read per call")
	require.Zero(t, f.sessions.StatusCalls, "no session status is loaded for an id mapping")
}

func TestService_ListBatchesOneStatusLookup(t *testing.T) {
	f := newSvcFixture(t)
	a, err := f.svc.Create(f.ctx(t), "A")
	require.NoError(t, err)
	_, err = f.svc.Create(f.ctx(t), "B")
	require.NoError(t, err)
	f.sessions.Statuses[a.ID] = device.SessionStatus{State: device.SessionConnected, Phone: "628111", Live: true}

	views, err := f.svc.List(f.ctx(t))
	require.NoError(t, err)
	require.Len(t, views, 2)
	require.Equal(t, 1, f.sessions.StatusCalls)
	byName := map[string]device.SessionStatus{}
	for _, v := range views {
		byName[v.Device.Name] = v.Status
	}
	require.Equal(t, device.SessionConnected, byName["A"].State)
	require.Equal(t, device.SessionUnlinked, byName["B"].State)
}

func TestService_GetChecksTheProject(t *testing.T) {
	f := newSvcFixture(t)
	d, err := f.svc.Create(f.ctx(t), "A")
	require.NoError(t, err)
	v, err := f.svc.Get(f.ctx(t), d.ID)
	require.NoError(t, err)
	require.Equal(t, d.ID, v.Device.ID)

	sibling := tenant.Into(t.Context(), tenant.Context{OrgID: f.tc.OrgID, ProjectID: uuid.New()})
	_, err = f.svc.Get(sibling, d.ID)
	require.True(t, device.IsNotFoundError(err), "a sibling project's device must be not-found, got %v", err)
	_, err = f.svc.StartLink(sibling, d.ID)
	require.True(t, device.IsNotFoundError(err))
	require.Empty(t, f.sessions.Linked, "the port is never reached for an out-of-scope device")
}

func TestService_UpdateIsOneConditionalWrite(t *testing.T) {
	f := newSvcFixture(t)
	d, err := f.svc.Create(f.ctx(t), "A")
	require.NoError(t, err)

	counting := &countingDeviceStore{Device: f.store}
	svc := device.NewService(counting, slog.New(slog.NewTextHandler(io.Discard, nil)), func(_ context.Context, msg string, cause error, _ ...any) *apperror.AppError {
		return apperror.New("GEN900", msg, codes.Internal).WithCause(cause)
	}, fakes.UnitOfWork, f.sessions)

	name := "B"
	rules := device.Rules{GroupMode: device.GroupOpen, IgnoreFromMe: true}
	got, err := svc.Update(f.ctx(t), d.ID, device.Update{Name: &name, Rules: &rules}, 1)
	require.NoError(t, err)
	require.Equal(t, 1, counting.saves)
	require.Equal(t, "B", got.Name)
	require.Equal(t, device.GroupOpen, got.Rules.GroupMode)
	require.Equal(t, 2, got.Version)

	_, err = svc.Rename(f.ctx(t), d.ID, "C", 1)
	require.True(t, device.IsStaleVersionError(err))
	_, err = svc.UpdateRules(f.ctx(t), d.ID, device.Rules{GroupMode: "loud"}, 0)
	require.True(t, device.IsInvalidRulesError(err))
	renamed, err := svc.Rename(f.ctx(t), d.ID, "C", 0)
	require.NoError(t, err, "ifVersion 0 guards on the loaded version")
	require.Equal(t, 3, renamed.Version)
}

type countingDeviceStore struct {
	*fakes.Device
	saves int
}

func (c *countingDeviceStore) Save(ctx context.Context, d *device.Device, ifVersion int) error {
	c.saves++
	return c.Device.Save(ctx, d, ifVersion)
}

func TestService_DeleteForgetsBeforeDeletingAndKeepsTheDeviceOnFailure(t *testing.T) {
	f := newSvcFixture(t)
	d, err := f.svc.Create(f.ctx(t), "A")
	require.NoError(t, err)

	f.sessions.ForgetErr = errors.New("engine down")
	require.Error(t, f.svc.Delete(f.ctx(t), d.ID))
	require.Equal(t, 1, f.store.Len(), "a failed Forget keeps the device")

	f.sessions.ForgetErr = nil
	f.sessions.ForgetFn = func(context.Context, uuid.UUID) error {
		require.Equal(t, 1, f.store.Len(), "Forget runs before the row is deleted")
		return nil
	}
	require.NoError(t, f.svc.Delete(f.ctx(t), d.ID))
	require.Zero(t, f.store.Len())
}

func TestService_LinkVerbsDelegate(t *testing.T) {
	f := newSvcFixture(t)
	d, err := f.svc.Create(f.ctx(t), "A")
	require.NoError(t, err)
	st, err := f.svc.StartLink(f.ctx(t), d.ID)
	require.NoError(t, err)
	require.Equal(t, device.LinkPending, st.Outcome)
	st, err = f.svc.LinkWithPhone(f.ctx(t), d.ID, "+62 812")
	require.NoError(t, err)
	require.Equal(t, "ABCD-EFGH", st.PairingCode)
	_, err = f.svc.LinkState(f.ctx(t), d.ID)
	require.NoError(t, err)
	require.NoError(t, f.svc.Unlink(f.ctx(t), d.ID))
	require.Equal(t, []uuid.UUID{d.ID}, f.sessions.Unlinked)
}

// SECURITY: the whatsapp session store filters by org only, so a same-org device of a sibling project must never reach the Sessions port.
func TestService_SiblingProjectDeviceNeverReachesTheSessionPort(t *testing.T) {
	f := newSvcFixture(t)
	sibling := tenant.Context{OrgID: f.tc.OrgID, ProjectID: uuid.New(), UserID: f.tc.UserID}
	other, err := f.svc.Create(tenant.Into(t.Context(), sibling), "Other")
	require.NoError(t, err)
	f.sessions.Statuses[other.ID] = device.SessionStatus{State: device.SessionConnected, Phone: "628999", Live: true}
	f.sessions.State = device.LinkState{ID: "lnk_SiblingSecret0001", Method: device.LinkPhone, Outcome: device.LinkPending, QR: "2@secret", PairingCode: "SECR-ETCD"}

	ctx := f.ctx(t)
	calls := map[string]func() (device.LinkState, error){
		"Get": func() (device.LinkState, error) {
			v, err := f.svc.Get(ctx, other.ID)
			require.Empty(t, v.Status.Phone, "status must not leak")
			return device.LinkState{}, err
		},
		"StartLink":     func() (device.LinkState, error) { return f.svc.StartLink(ctx, other.ID) },
		"LinkWithPhone": func() (device.LinkState, error) { return f.svc.LinkWithPhone(ctx, other.ID, "+62 812") },
		"LinkState":     func() (device.LinkState, error) { return f.svc.LinkState(ctx, other.ID) },
		"Unlink":        func() (device.LinkState, error) { return device.LinkState{}, f.svc.Unlink(ctx, other.ID) },
		"Delete":        func() (device.LinkState, error) { return device.LinkState{}, f.svc.Delete(ctx, other.ID) },
		"Update": func() (device.LinkState, error) {
			name := "hijack"
			_, err := f.svc.Update(ctx, other.ID, device.Update{Name: &name}, 0)
			return device.LinkState{}, err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			st, err := call()
			require.True(t, device.IsNotFoundError(err), "want not-found, got %v", err)
			require.Empty(t, st.QR)
			require.Empty(t, st.PairingCode)
			require.Empty(t, st.ID)
		})
	}

	require.Zero(t, f.sessions.StatusCalls, "status is never read for a sibling device")
	require.Empty(t, f.sessions.Linked)
	require.Empty(t, f.sessions.Phones)
	require.Empty(t, f.sessions.Unlinked)
	require.Empty(t, f.sessions.Forgot)
	require.Equal(t, 1, f.store.Len(), "the sibling device survives")

	views, err := f.svc.List(ctx)
	require.NoError(t, err)
	require.Empty(t, views, "List is project-scoped")
}
