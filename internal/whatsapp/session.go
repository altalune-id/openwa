package whatsapp

import (
	"time"

	"altalune.id/openwa/internal/platform/events"
)

// Outcome reports what a transition did: whether the row changed and which event, if any, it emits.
type Outcome struct {
	Changed bool
	Emit    events.Type
}

// ReasonPaired is the reason Linked records.
const ReasonPaired = "paired"

// Linking moves an unlinked, disconnected or logged-out session into linking; a linking session is a no-op.
func (s *Session) Linking() (Outcome, error) {
	switch s.State {
	case StateLinking:
		return Outcome{}, nil
	case StateUnlinked, StateDisconnected, StateLoggedOut:
		s.State = StateLinking
		s.Reason = ""
		s.touch()
		return Outcome{Changed: true}, nil
	default:
		return Outcome{}, &InvalidTransitionError{From: s.State, To: StateLinking}
	}
}

// Linked records the paired identity and parks the session in disconnected until the engine reports Connected.
func (s *Session) Linked(id Identity) (Outcome, error) {
	if s.State != StateLinking {
		return Outcome{}, &InvalidTransitionError{From: s.State, To: StateDisconnected}
	}
	s.JID = id.JID
	s.LID = id.LID
	s.Phone = id.Phone
	if s.Phone == "" {
		s.Phone = PhoneFromJID(id.JID)
	}
	s.PushName = id.PushName
	s.Platform = id.Platform
	s.State = StateDisconnected
	s.Reason = ReasonPaired
	s.touch()
	return Outcome{Changed: true}, nil
}

// Connected marks the session live; it emits device.connected only when the previous state was not connected.
func (s *Session) Connected() (Outcome, error) {
	switch s.State {
	case StateConnected:
		if s.LastError == "" {
			return Outcome{}, nil
		}
		s.LastError = ""
		s.touch()
		return Outcome{Changed: true}, nil
	case StateLinking, StateDisconnected:
		now := time.Now().UTC()
		s.State = StateConnected
		s.Reason = ""
		s.LastError = ""
		s.LastConnectedAt = &now
		s.LastSeenAt = &now
		s.UpdatedAt = now
		return Outcome{Changed: true, Emit: events.DeviceConnected}, nil
	default:
		return Outcome{}, &InvalidTransitionError{From: s.State, To: StateConnected}
	}
}

// Disconnected records a lost connection; it emits device.disconnected only when the previous state was connected.
func (s *Session) Disconnected(reason string) (Outcome, error) {
	switch s.State {
	case StateDisconnected:
		return Outcome{}, nil
	case StateConnected:
		now := time.Now().UTC()
		s.State = StateDisconnected
		s.Reason = reason
		s.LastSeenAt = &now
		s.UpdatedAt = now
		return Outcome{Changed: true, Emit: events.DeviceDisconnected}, nil
	case StateLinking:
		s.State = StateDisconnected
		s.Reason = reason
		s.touch()
		return Outcome{Changed: true}, nil
	default:
		return Outcome{}, &InvalidTransitionError{From: s.State, To: StateDisconnected}
	}
}

// LoggedOut clears the identity and emits device.logged_out; a logged-out session is a no-op.
func (s *Session) LoggedOut(reason string) (Outcome, error) {
	if s.State == StateLoggedOut {
		return Outcome{}, nil
	}
	s.State = StateLoggedOut
	s.Reason = reason
	s.JID, s.LID, s.Phone, s.PushName = "", "", "", ""
	s.touch()
	return Outcome{Changed: true, Emit: events.DeviceLoggedOut}, nil
}

// Unlinked returns the session to unlinked from any state; an unlinked session is a no-op.
func (s *Session) Unlinked() (Outcome, error) {
	if s.State == StateUnlinked {
		return Outcome{}, nil
	}
	s.State = StateUnlinked
	s.Reason = ""
	s.touch()
	return Outcome{Changed: true}, nil
}

// Degraded records a keepalive failure on a connected session without changing its state.
func (s *Session) Degraded(err error) (Outcome, error) {
	if s.State != StateConnected {
		return Outcome{}, &InvalidTransitionError{From: s.State, To: StateConnected}
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if s.LastError == msg {
		return Outcome{}, nil
	}
	s.LastError = msg
	s.touch()
	return Outcome{Changed: true}, nil
}

func (s *Session) touch() { s.UpdatedAt = time.Now().UTC() }
