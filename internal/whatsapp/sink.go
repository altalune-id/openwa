package whatsapp

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/platform/tenant"
)

const sinkTimeout = 10 * time.Second

var _ EventSink = (*Service)(nil)

// OnState applies an engine-reported state; a transition the row cannot take is a debug-logged no-op.
func (s *Service) OnState(ref SessionRef, st State, reason string) {
	s.apply(ref, "whatsapp.OnState", false, func(sess *Session) (Outcome, error) {
		return transitionTo(sess, ref, st, reason)
	})
}

// OnDegraded records a keepalive failure on a connected session.
func (s *Service) OnDegraded(ref SessionRef, err error) {
	s.apply(ref, "whatsapp.OnDegraded", false, func(sess *Session) (Outcome, error) {
		return sess.Degraded(err)
	})
}

// OnLinked saves the paired identity; the runtime has already written the lease.
func (s *Service) OnLinked(ref SessionRef, id Identity) {
	s.apply(ref, "whatsapp.OnLinked", true, func(sess *Session) (Outcome, error) {
		return sess.Linked(id)
	})
}

// OnMessage forwards to the Inbound port, when one is wired.
func (s *Service) OnMessage(ref SessionRef, m InboundMessage) {
	s.forward(ref, "whatsapp.OnMessage", func(ctx context.Context, in Inbound) error { return in.RecordInbound(ctx, ref, m) })
}

// OnReceipt forwards to the Inbound port, when one is wired.
func (s *Service) OnReceipt(ref SessionRef, r Receipt) {
	s.forward(ref, "whatsapp.OnReceipt", func(ctx context.Context, in Inbound) error { return in.RecordReceipt(ctx, ref, r) })
}

// OnContact forwards to the Inbound port, when one is wired.
func (s *Service) OnContact(ref SessionRef, c ContactUpdate) {
	s.forward(ref, "whatsapp.OnContact", func(ctx context.Context, in Inbound) error { return in.UpsertContact(ctx, ref, c) })
}

// OnHistory is dropped in v1; history import is out of scope.
func (s *Service) OnHistory(ref SessionRef, batch []InboundMessage) {
	ctx, cancel := sinkContext(ref)
	defer cancel()
	s.log.DebugContext(ctx, "whatsapp: history batch dropped", "device_id", ref.DeviceID, "messages", len(batch))
}

func (s *Service) forward(ref SessionRef, op string, fn func(context.Context, Inbound) error) {
	ctx, cancel := sinkContext(ref)
	defer cancel()
	if s.inbound == nil {
		s.log.DebugContext(ctx, "whatsapp: no inbound port wired", "op", op, "device_id", ref.DeviceID)
		return
	}
	if err := fn(ctx, s.inbound); err != nil {
		s.log.ErrorContext(ctx, "whatsapp: inbound port failed", "op", op, "device_id", ref.DeviceID, "err", err)
	}
}

func (s *Service) apply(ref SessionRef, op string, strict bool, fn func(*Session) (Outcome, error)) {
	ctx, cancel := sinkContext(ref)
	defer cancel()
	ctx, span := tracer.Start(ctx, op, trace.WithAttributes(attribute.String("device.id", ref.DeviceID.String())))
	defer span.End()

	nameErr, err := s.transact(ctx, ref, fn)
	if IsStaleVersionError(err) {
		nameErr, err = s.transact(ctx, ref, fn)
	}
	if nameErr != nil {
		span.RecordError(nameErr)
		_ = s.unexpected(ctx, op+": describe device", nameErr, "device_id", ref.DeviceID)
	}
	switch {
	case err == nil:
	case !strict && (IsInvalidTransitionError(err) || IsSessionNotFoundError(err)):
		s.log.DebugContext(ctx, "whatsapp: engine event ignored", "op", op, "device_id", ref.DeviceID, "err", err)
	default:
		span.RecordError(err)
		_ = s.unexpected(ctx, op+": apply", err, "device_id", ref.DeviceID)
	}
}

// NOTE: a failed device lookup drops the event but never the state write; it is returned as nameErr for the caller to report once the unit of work has committed.
func (s *Service) transact(ctx context.Context, ref SessionRef, fn func(*Session) (Outcome, error)) (nameErr, err error) {
	err = s.uow(ctx, func(ctx context.Context) error {
		nameErr = nil
		sess, err := s.store.ByDevice(ctx, ref.DeviceID)
		if err != nil {
			return err
		}
		phone := sess.Phone
		out, err := fn(sess)
		if err != nil {
			return err
		}
		if !out.Changed {
			return nil
		}
		if err := s.store.Save(ctx, sess, sess.Version); err != nil {
			return err
		}
		if out.Emit == "" {
			return nil
		}
		publicID, name, err := s.devices.Describe(ctx, ref.DeviceID)
		if err != nil {
			nameErr = fmt.Errorf("device name: %w", err)
			return nil
		}
		ev := events.DeviceEventV1{
			Device: events.DeviceRefV1{ID: publicID, Name: name, Phone: cmp.Or(sess.Phone, phone)},
			State:  string(sess.State),
			Reason: sess.Reason,
			At:     s.clock().UTC().Truncate(time.Second),
		}
		return s.webhooks.Enqueue(ctx, out.Emit, devicePayload(out.Emit, ev))
	})
	if err != nil {
		nameErr = nil
	}
	return nameErr, err
}

// NOTE: the sink carries no ctx, so each call gets its own bounded one; a zero UserID is a valid system scope.
func sinkContext(ref SessionRef) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), sinkTimeout)
	return tenant.Into(ctx, tenant.Context{OrgID: ref.OrgID, ProjectID: ref.ProjectID}), cancel
}

func transitionTo(sess *Session, ref SessionRef, st State, reason string) (Outcome, error) {
	switch st {
	case StateConnected:
		repaired := repairIdentity(sess, ref)
		out, err := sess.Connected()
		if err != nil {
			return out, err
		}
		out.Changed = out.Changed || repaired
		return out, nil
	case StateDisconnected:
		return sess.Disconnected(reason)
	case StateLoggedOut:
		return sess.LoggedOut(reason)
	case StateUnlinked:
		return sess.Unlinked()
	case StateLinking:
		return sess.Linking()
	}
	return Outcome{}, &InvalidTransitionError{From: sess.State, To: st}
}

// NOTE: the runtime writes the lease before the identity; when the identity save failed the lease's JID heals the row.
func repairIdentity(sess *Session, ref SessionRef) bool {
	if sess.JID != "" || ref.JID == "" {
		return false
	}
	if sess.State == StateLinking {
		_, _ = sess.Linked(Identity{JID: ref.JID})
		return true
	}
	sess.JID = ref.JID
	sess.Phone = PhoneFromJID(ref.JID)
	return true
}

func devicePayload(t events.Type, ev events.DeviceEventV1) any {
	switch t {
	case events.DeviceConnected:
		return events.DeviceConnectedV1(ev)
	case events.DeviceDisconnected:
		return events.DeviceDisconnectedV1(ev)
	case events.DeviceLoggedOut:
		return events.DeviceLoggedOutV1(ev)
	}
	return ev
}
