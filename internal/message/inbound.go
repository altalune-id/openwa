package message

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/platform/publicid"
	"altalune.id/openwa/internal/platform/tenant"
)

// Recorder stores inbound messages once and emits their events in the same unit of work.
type Recorder struct {
	store      Store
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
	uow        tenant.UnitOfWork
	devices    Devices
	chats      Chats
	contacts   Contacts
	webhooks   Webhooks
	tenants    Tenants
	baseURL    string
	clock      func() time.Time
}

// RecordInbound stores one live message and emits message.received, plus message.matched when the rules pass.
func (r *Recorder) RecordInbound(ctx context.Context, ref Ref, in InboundInput) error {
	return retryOnce(func() error { return r.record(ctx, ref, []InboundInput{in}, true) })
}

// RecordBatch stores history messages without emitting events; it is the history-import entry point.
func (r *Recorder) RecordBatch(ctx context.Context, ref Ref, batch []InboundInput) error {
	return retryOnce(func() error { return r.record(ctx, ref, batch, false) })
}

func (r *Recorder) record(ctx context.Context, ref Ref, msgs []InboundInput, emit bool) error {
	ctx = tenant.Into(ctx, tenant.Context{OrgID: ref.OrgID, ProjectID: ref.ProjectID})
	var errs []error
	for _, in := range msgs {
		if err := r.recordOne(ctx, ref, in, emit); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *Recorder) recordOne(ctx context.Context, ref Ref, in InboundInput, emit bool) error {
	ctx, span := tracer.Start(ctx, "message.RecordInbound")
	defer span.End()
	span.SetAttributes(attribute.String("device.id", ref.DeviceID.String()), attribute.String("message.wa_id", in.WAID))
	var inner error
	err := r.uow(ctx, func(ctx context.Context) error {
		inner = r.store1(ctx, ref, in, emit)
		return inner
	})
	if inner != nil {
		span.RecordError(inner)
		if _, ok := apperror.AsAppError(inner); ok || IsPublicIDTakenError(inner) {
			return inner
		}
		return r.unexpected(ctx, "message.RecordInbound", inner, "device_id", ref.DeviceID, "wa_id", in.WAID)
	}
	if err != nil {
		return r.unexpected(ctx, "message.RecordInbound: unit of work", err, "device_id", ref.DeviceID)
	}
	return nil
}

func (r *Recorder) store1(ctx context.Context, ref Ref, in InboundInput, emit bool) error {
	jid, lid, kind := chatIdentity(in)
	name := ""
	if kind == "dm" && !in.FromMe {
		name = in.PushName
	}
	chat, err := r.chats.EnsureForJID(ctx, ref.DeviceID, jid, lid, kind, name)
	if err != nil {
		return err
	}
	pid, err := publicid.New(PublicIDPrefix)
	if err != nil {
		return err
	}
	m := NewInbound(ref, chat.ID, pid, in)
	m.stamp(r.clock())
	inserted, err := r.store.InsertInbound(ctx, m)
	if err != nil || !inserted {
		return err
	}
	if !in.FromMe {
		pn, senderLID := splitIdentity(in.SenderJID, in.SenderAlt)
		key := pn
		if key == "" {
			key = senderLID
		}
		if err := r.contacts.UpsertFromMessage(ctx, ref.DeviceID, SenderInput{JID: key, LID: senderLID, Phone: in.SenderPhone, PushName: in.PushName}); err != nil {
			return err
		}
	}
	if err := r.chats.Touch(ctx, chat.ID, m.WATimestamp, PreviewFor(m), !in.FromMe); err != nil {
		return err
	}
	if !emit || in.FromMe {
		return nil
	}
	return r.emit(ctx, ref, chat, m, in)
}

func (r *Recorder) emit(ctx context.Context, ref Ref, chat ChatRef, m *Message, in InboundInput) error {
	dev, err := r.devices.Ref(ctx, ref.DeviceID)
	if err != nil {
		return err
	}
	matched, reasons, err := r.devices.Match(ctx, ref.DeviceID, MatchInput{
		IsGroup: in.IsGroup, ChatJID: chat.JID, SenderPhone: in.SenderPhone, FromMe: in.FromMe,
		Body: m.Body, Mentions: in.Mentions, QuotedSender: in.QuotedSender,
	})
	if err != nil {
		// NOTE: WhatsApp already delivered the message, so a rules failure logs and leaves it received but unmatched.
		r.log.WarnContext(ctx, "message.RecordInbound: rules match", slog.String("device_id", ref.DeviceID.String()), slog.Any("error", err))
		matched, reasons = false, nil
	}
	quotedBody := ""
	if m.QuotedWAMessageID != "" {
		if q, qErr := r.store.ByWAID(ctx, ref.DeviceID, m.QuotedWAMessageID); qErr == nil {
			quotedBody = q.Body
		}
	}
	mediaURL := ""
	if m.Media != nil {
		org, project, slugErr := r.tenants.Slugs(ctx, ref.OrgID, ref.ProjectID)
		if slugErr != nil {
			return slugErr
		}
		mediaURL = MediaURL(r.baseURL, org, project, m.PublicID)
	}
	payload := eventPayload(dev, chat, m, quotedBody, mediaURL)
	if err := r.webhooks.Enqueue(ctx, events.MessageReceived, payload); err != nil {
		return err
	}
	if !matched {
		return nil
	}
	payload.Matched = &events.MatchedV1{Reasons: append([]string{}, reasons...)}
	return r.webhooks.Enqueue(ctx, events.MessageMatched, payload)
}

// NOTE: keys a DM by its phone-number JID when either address reveals one, keeping the LID beside it.
func chatIdentity(in InboundInput) (jid, lid, kind string) {
	if in.IsGroup {
		return in.ChatJID, "", "group"
	}
	pn, lid := splitIdentity(in.ChatJID, in.ChatAlt)
	if pn != "" {
		return pn, lid, "dm"
	}
	return lid, lid, "dm"
}

func splitIdentity(a, b string) (pn, lid string) {
	for _, j := range []string{a, b} {
		switch {
		case strings.HasSuffix(j, "@lid"):
			lid = j
		case strings.HasSuffix(j, "@s.whatsapp.net"):
			pn = j
		}
	}
	return pn, lid
}
