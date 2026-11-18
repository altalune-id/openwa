package device_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/testutil/fakes"
)

func (f *svcFixture) seedNamed(t *testing.T, name string, created time.Time, st device.SessionStatus) *device.Device {
	t.Helper()
	d, err := device.New(f.tc.OrgID, f.tc.ProjectID, fakes.DevicePublicID(), name)
	require.NoError(t, err)
	d.CreatedAt = created
	f.store.Seed(d)
	f.sessions.Statuses[d.ID] = st
	return d
}

func TestService_CreateDefaultNamesTheFirstFreeDefault(t *testing.T) {
	f := newSvcFixture(t)
	d, reused, err := f.svc.CreateDefault(f.ctx(t))
	require.NoError(t, err)
	require.False(t, reused)
	require.Equal(t, device.DefaultName, d.Name)

	paired := device.SessionStatus{State: device.SessionConnected, Phone: "628111"}
	f.sessions.Statuses[d.ID] = paired
	now := time.Now()
	f.seedNamed(t, "whatsapp 2", now, paired)
	f.seedNamed(t, "WhatsApp 4", now, paired)

	d, reused, err = f.svc.CreateDefault(f.ctx(t))
	require.NoError(t, err)
	require.False(t, reused)
	require.Equal(t, "WhatsApp 3", d.Name, "the free-name check ignores case like the name index")
}

func TestService_CreateDefaultReusesTheOldestNeverPairedDefault(t *testing.T) {
	for _, state := range []device.SessionState{device.SessionUnlinked, device.SessionLinking} {
		t.Run(string(state), func(t *testing.T) {
			f := newSvcFixture(t)
			now := time.Now()
			old := f.seedNamed(t, "WhatsApp 2", now.Add(-time.Hour), device.SessionStatus{State: state})
			f.seedNamed(t, "WhatsApp", now, device.SessionStatus{State: state})

			d, reused, err := f.svc.CreateDefault(f.ctx(t))
			require.NoError(t, err)
			require.True(t, reused)
			require.Equal(t, old.ID, d.ID)
			require.Equal(t, 2, f.store.Len(), "a reuse creates nothing")
		})
	}
}

// SECURITY: a number that was ever paired keeps its history and must never be handed to a new pairing.
func TestService_CreateDefaultNeverReusesAPairedOrRenamedNumber(t *testing.T) {
	seen := time.Now()
	cases := map[string]struct {
		name string
		st   device.SessionStatus
	}{
		"has a phone":      {"WhatsApp", device.SessionStatus{State: device.SessionUnlinked, Phone: "628111"}},
		"was seen":         {"WhatsApp", device.SessionStatus{State: device.SessionLinking, LastSeen: &seen}},
		"logged out":       {"WhatsApp", device.SessionStatus{State: device.SessionLoggedOut}},
		"connected":        {"WhatsApp", device.SessionStatus{State: device.SessionConnected}},
		"renamed":          {"Sales", device.SessionStatus{State: device.SessionUnlinked}},
		"default-ish name": {"WhatsApp Sales", device.SessionStatus{State: device.SessionUnlinked}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSvcFixture(t)
			existing := f.seedNamed(t, c.name, time.Now(), c.st)

			d, reused, err := f.svc.CreateDefault(f.ctx(t))
			require.NoError(t, err)
			require.False(t, reused)
			require.NotEqual(t, existing.ID, d.ID)
			require.Equal(t, 2, f.store.Len())
		})
	}
}

func TestService_CreateDefaultIgnoresASiblingProjectsNumber(t *testing.T) {
	f := newSvcFixture(t)
	sibling, err := device.New(f.tc.OrgID, uuid.New(), fakes.DevicePublicID(), device.DefaultName)
	require.NoError(t, err)
	f.store.Seed(sibling)

	d, reused, err := f.svc.CreateDefault(f.ctx(t))
	require.NoError(t, err)
	require.False(t, reused)
	require.NotEqual(t, sibling.ID, d.ID)
	require.Equal(t, f.tc.ProjectID, d.ProjectID)
	require.Equal(t, device.DefaultName, d.Name, "a sibling project's names do not count as taken")
}

func TestService_CreateDefaultRetriesANameRace(t *testing.T) {
	f := newSvcFixture(t)
	races := 1
	var tried []string
	f.store.SaveFn = func(ctx context.Context, d *device.Device, ifVersion int) error {
		tried = append(tried, d.Name)
		if races > 0 {
			races--
			other, err := device.New(f.tc.OrgID, f.tc.ProjectID, fakes.DevicePublicID(), d.Name)
			require.NoError(t, err)
			f.store.Seed(other)
			f.sessions.Statuses[other.ID] = device.SessionStatus{State: device.SessionConnected, Phone: "628111"}
			return &device.NameTakenError{Name: d.Name}
		}
		f.store.SaveFn = nil
		return f.store.Save(ctx, d, ifVersion)
	}

	d, reused, err := f.svc.CreateDefault(f.ctx(t))
	require.NoError(t, err)
	require.False(t, reused)
	require.Equal(t, []string{"WhatsApp", "WhatsApp 2"}, tried, "the retry re-lists and picks the next free name")
	require.Equal(t, "WhatsApp 2", d.Name)
}

func TestService_CreateDefaultGivesUpAfterThreeNameRaces(t *testing.T) {
	f := newSvcFixture(t)
	saves := 0
	f.store.SaveFn = func(_ context.Context, d *device.Device, _ int) error {
		saves++
		return &device.NameTakenError{Name: d.Name}
	}

	_, _, err := f.svc.CreateDefault(f.ctx(t))
	require.True(t, device.IsNameTakenError(err), "got %v", err)
	require.Equal(t, 3, saves)
}

func TestService_CreateDefaultNeedsATenant(t *testing.T) {
	f := newSvcFixture(t)
	_, _, err := f.svc.CreateDefault(context.Background())
	require.Error(t, err)
	require.Zero(t, f.store.Len())
}
