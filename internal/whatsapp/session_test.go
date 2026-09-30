package whatsapp_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/whatsapp"
)

func sessionIn(state whatsapp.State) *whatsapp.Session {
	s := whatsapp.NewSession(uuid.New(), uuid.New(), uuid.New(), whatsapp.EngineWhatsmeow)
	s.State = state
	if state != whatsapp.StateUnlinked && state != whatsapp.StateLinking && state != whatsapp.StateLoggedOut {
		s.JID = "628111:1@s.whatsapp.net"
		s.Phone = "628111"
	}
	return s
}

func TestSession_Transitions(t *testing.T) {
	t.Parallel()
	identity := whatsapp.Identity{JID: "628111:1@s.whatsapp.net", LID: "1@lid", PushName: "Ops", Platform: "android"}
	cases := []struct {
		name    string
		from    whatsapp.State
		apply   func(*whatsapp.Session) (whatsapp.Outcome, error)
		to      whatsapp.State
		changed bool
		emit    events.Type
		invalid bool
	}{
		{"linking from unlinked", whatsapp.StateUnlinked, (*whatsapp.Session).Linking, whatsapp.StateLinking, true, "", false},
		{"linking from disconnected", whatsapp.StateDisconnected, (*whatsapp.Session).Linking, whatsapp.StateLinking, true, "", false},
		{"linking from logged_out", whatsapp.StateLoggedOut, (*whatsapp.Session).Linking, whatsapp.StateLinking, true, "", false},
		{"linking same-state no-op", whatsapp.StateLinking, (*whatsapp.Session).Linking, whatsapp.StateLinking, false, "", false},
		{"linking from connected refused", whatsapp.StateConnected, (*whatsapp.Session).Linking, whatsapp.StateConnected, false, "", true},
		{"linked from linking", whatsapp.StateLinking, func(s *whatsapp.Session) (whatsapp.Outcome, error) { return s.Linked(identity) }, whatsapp.StateDisconnected, true, "", false},
		{"linked from unlinked refused", whatsapp.StateUnlinked, func(s *whatsapp.Session) (whatsapp.Outcome, error) { return s.Linked(identity) }, whatsapp.StateUnlinked, false, "", true},
		{"connected from linking emits", whatsapp.StateLinking, (*whatsapp.Session).Connected, whatsapp.StateConnected, true, events.DeviceConnected, false},
		{"connected from disconnected emits", whatsapp.StateDisconnected, (*whatsapp.Session).Connected, whatsapp.StateConnected, true, events.DeviceConnected, false},
		{"connected same-state no-op", whatsapp.StateConnected, (*whatsapp.Session).Connected, whatsapp.StateConnected, false, "", false},
		{"connected from logged_out refused", whatsapp.StateLoggedOut, (*whatsapp.Session).Connected, whatsapp.StateLoggedOut, false, "", true},
		{"disconnected from connected emits", whatsapp.StateConnected, func(s *whatsapp.Session) (whatsapp.Outcome, error) { return s.Disconnected("network") }, whatsapp.StateDisconnected, true, events.DeviceDisconnected, false},
		{"disconnected from linking silent", whatsapp.StateLinking, func(s *whatsapp.Session) (whatsapp.Outcome, error) { return s.Disconnected("lease_lost") }, whatsapp.StateDisconnected, true, "", false},
		{"disconnected same-state no-op", whatsapp.StateDisconnected, func(s *whatsapp.Session) (whatsapp.Outcome, error) { return s.Disconnected("network") }, whatsapp.StateDisconnected, false, "", false},
		{"disconnected from unlinked refused", whatsapp.StateUnlinked, func(s *whatsapp.Session) (whatsapp.Outcome, error) { return s.Disconnected("x") }, whatsapp.StateUnlinked, false, "", true},
		{"logged_out from connected emits", whatsapp.StateConnected, func(s *whatsapp.Session) (whatsapp.Outcome, error) { return s.LoggedOut("logged_out_by_phone") }, whatsapp.StateLoggedOut, true, events.DeviceLoggedOut, false},
		{"logged_out from disconnected emits", whatsapp.StateDisconnected, func(s *whatsapp.Session) (whatsapp.Outcome, error) { return s.LoggedOut("unlinked") }, whatsapp.StateLoggedOut, true, events.DeviceLoggedOut, false},
		{"logged_out same-state no-op", whatsapp.StateLoggedOut, func(s *whatsapp.Session) (whatsapp.Outcome, error) { return s.LoggedOut("x") }, whatsapp.StateLoggedOut, false, "", false},
		{"unlinked from linking", whatsapp.StateLinking, (*whatsapp.Session).Unlinked, whatsapp.StateUnlinked, true, "", false},
		{"unlinked same-state no-op", whatsapp.StateUnlinked, (*whatsapp.Session).Unlinked, whatsapp.StateUnlinked, false, "", false},
		{"degraded from connected", whatsapp.StateConnected, func(s *whatsapp.Session) (whatsapp.Outcome, error) { return s.Degraded(errors.New("keepalive")) }, whatsapp.StateConnected, true, "", false},
		{"degraded from disconnected refused", whatsapp.StateDisconnected, func(s *whatsapp.Session) (whatsapp.Outcome, error) { return s.Degraded(errors.New("keepalive")) }, whatsapp.StateDisconnected, false, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := sessionIn(tc.from)
			out, err := tc.apply(s)
			if tc.invalid {
				require.True(t, whatsapp.IsInvalidTransitionError(err), "got %v", err)
				require.Equal(t, tc.from, s.State)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.to, s.State)
			require.Equal(t, tc.changed, out.Changed)
			require.Equal(t, tc.emit, out.Emit)
		})
	}
}

func TestSession_LinkedRecordsIdentityAndPaired(t *testing.T) {
	t.Parallel()
	s := sessionIn(whatsapp.StateLinking)
	_, err := s.Linked(whatsapp.Identity{JID: "628111:3@s.whatsapp.net", LID: "9@lid", PushName: "Ops", Platform: "android"})
	require.NoError(t, err)
	require.Equal(t, "628111", s.Phone, "phone derives from the JID when Identity carries none")
	require.Equal(t, "9@lid", s.LID)
	require.Equal(t, "paired", s.Reason)
	require.Equal(t, whatsapp.StateDisconnected, s.State)
}

func TestSession_ConnectedClearsLastErrorAndDegradedSetsIt(t *testing.T) {
	t.Parallel()
	s := sessionIn(whatsapp.StateConnected)
	out, err := s.Degraded(errors.New("keepalive timeout"))
	require.NoError(t, err)
	require.True(t, out.Changed)
	require.Equal(t, "keepalive timeout", s.LastError)
	out, err = s.Degraded(errors.New("keepalive timeout"))
	require.NoError(t, err)
	require.False(t, out.Changed, "same error text is a no-op")
	out, err = s.Connected()
	require.NoError(t, err)
	require.True(t, out.Changed)
	require.Equal(t, events.Type(""), out.Emit, "clearing LastError on an already connected row emits nothing")
	require.Empty(t, s.LastError)
}

func TestSession_LoggedOutClearsIdentity(t *testing.T) {
	t.Parallel()
	s := sessionIn(whatsapp.StateConnected)
	s.LID, s.PushName = "9@lid", "Ops"
	_, err := s.LoggedOut("logged_out_by_phone")
	require.NoError(t, err)
	require.Empty(t, s.JID)
	require.Empty(t, s.LID)
	require.Empty(t, s.Phone)
	require.Empty(t, s.PushName)
	require.Equal(t, "logged_out_by_phone", s.Reason)
}
