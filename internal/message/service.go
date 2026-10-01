package message

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/platform/events"
	"altalune.id/openwa/internal/platform/keyset"
	"altalune.id/openwa/internal/platform/publicid"
	"altalune.id/openwa/internal/platform/tenant"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer = otel.Tracer("altalune.id/openwa/internal/message")

// RetentionBatch is how many rows one retention delete removes.
const RetentionBatch = 5000

// QueuedExpiry is how long a row may wait for a live device before retention fails it.
const QueuedExpiry = 24 * time.Hour

// ReasonExpired is the failure class of a row QueuedExpiry retired.
const ReasonExpired = "expired"

const maxErrorRunes = 500

const receiptChunk = 500

// DeviceRef is the device a message travels through.
type DeviceRef struct {
	ID       uuid.UUID
	PublicID string
	Name     string
	Phone    string
	Linked   bool
}

// MatchInput is what the device's inbound rules see; the devices shim derives MentionedMe and RepliedToMe from Mentions and QuotedSender.
type MatchInput struct {
	IsGroup      bool
	ChatJID      string
	SenderPhone  string
	FromMe       bool
	Body         string
	Mentions     []string
	QuotedSender string
}

// Devices resolves devices and evaluates their inbound rules.
type Devices interface {
	Ref(ctx context.Context, id uuid.UUID) (DeviceRef, error)
	Match(ctx context.Context, id uuid.UUID, in MatchInput) (bool, []string, error)
}

// ChatRef is the chat a message belongs to.
type ChatRef struct {
	ID       uuid.UUID
	PublicID string
	DeviceID uuid.UUID
	JID      string
	LID      string
	Kind     string
	Name     string
}

// Chats is the chat module seen from here; kind is "dm" or "group".
type Chats interface {
	EnsureForJID(ctx context.Context, deviceID uuid.UUID, jid, lid, kind, name string) (ChatRef, error)
	Get(ctx context.Context, id uuid.UUID) (ChatRef, error)
	Touch(ctx context.Context, id uuid.UUID, at time.Time, preview string, inbound bool) error
	MarkRead(ctx context.Context, id uuid.UUID) error
	Repair(ctx context.Context, id uuid.UUID, last *time.Time, preview string, unread int) error
}

// SenderInput is what an inbound message says about its sender.
type SenderInput struct {
	JID      string
	LID      string
	Phone    string
	PushName string
}

// Contacts records senders.
type Contacts interface {
	UpsertFromMessage(ctx context.Context, deviceID uuid.UUID, in SenderInput) error
}

// Tenants resolves slugs for media URLs and enumerates the org's projects for retention.
type Tenants interface {
	Slugs(ctx context.Context, orgID, projectID uuid.UUID) (string, string, error)
	ProjectIDs(ctx context.Context) ([]uuid.UUID, error)
}

// Transport reaches a live WhatsApp session.
type Transport interface {
	MarkRead(ctx context.Context, deviceID uuid.UUID, chatJID, senderJID string, waIDs []string, played bool) error
	FetchMedia(ctx context.Context, deviceID uuid.UUID, keys MediaKeys, kind string) (*os.File, error)
	IsOnWhatsApp(ctx context.Context, deviceID uuid.UUID, phones []string) (map[string]string, error)
}

// MediaStore opens stored media; the returned file is already unlinked, so closing it frees it.
type MediaStore interface {
	Open(ctx context.Context, m *Message) (*os.File, string, error)
	Put(ctx context.Context, m *Message, r io.Reader) (string, error)
}

// MediaFetcher downloads media from a public URL.
type MediaFetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, string, error)
}

// Webhooks enqueues an outbound event inside the caller's unit of work.
type Webhooks interface {
	Enqueue(ctx context.Context, t events.Type, data any) error
}

// Waker nudges a device's sender goroutine.
type Waker interface {
	Wake(deviceID uuid.UUID)
}

// Deps are the ports the service and its recorder reach other modules through.
type Deps struct {
	Devices   Devices
	Chats     Chats
	Contacts  Contacts
	Transport Transport
	Media     MediaStore
	Fetcher   MediaFetcher
	Waker     Waker
	Webhooks  Webhooks
	Tenants   Tenants
}

// Options are the service's configuration.
type Options struct {
	BaseURL       string
	MaxMediaBytes int64
	RetentionDays int
	StaleAfter    time.Duration
	Clock         func() time.Time
}

// ReceiptInput is a delivery receipt as the engine reported it.
type ReceiptInput struct {
	ChatJID   string
	SenderJID string
	WAIDs     []string
	Type      string
	At        time.Time
}

// ClaimedMedia is the attachment of a claimed row.
type ClaimedMedia struct {
	Bytes    []byte
	Mime     string
	Filename string
	Caption  string
	Voice    bool
}

// Claimed is one outbound row handed to the sender, with the quote and target context the engine needs.
type Claimed struct {
	ID           uuid.UUID
	DeviceID     uuid.UUID
	Kind         Type
	WAID         string
	ChatJID      string
	Text         string
	Media        *ClaimedMedia
	Location     *Location
	QuotedWAID   string
	QuotedSender string
	QuotedBody   string
	QuotedRaw    []byte
	Mentions     []string
	TargetWAID   string
	TargetSender string
	TargetFromMe bool
	Attempts     int
}

// Service is the message driving port.
type Service struct {
	store         Store
	log           *slog.Logger
	unexpected    apperror.UnexpectedFunc
	uow           tenant.UnitOfWork
	devices       Devices
	chats         Chats
	transport     Transport
	media         MediaStore
	fetcher       MediaFetcher
	wake          Waker
	webhooks      Webhooks
	tenants       Tenants
	recorder      *Recorder
	baseURL       string
	maxMedia      int64
	retentionDays int
	staleAfter    time.Duration
	clock         func() time.Time
}

// NewService binds the service and its recorder to their dependencies.
func NewService(store Store, log *slog.Logger, unexpected apperror.UnexpectedFunc, uow tenant.UnitOfWork, deps Deps, opts Options) *Service {
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	days := opts.RetentionDays
	if days == 0 {
		days = DefaultRetentionDays
	}
	log = log.With("module", "message")
	s := &Service{
		store: store, log: log, unexpected: unexpected, uow: uow,
		devices: deps.Devices, chats: deps.Chats, transport: deps.Transport, media: deps.Media, fetcher: deps.Fetcher,
		wake: deps.Waker, webhooks: deps.Webhooks, tenants: deps.Tenants,
		baseURL: opts.BaseURL, maxMedia: opts.MaxMediaBytes, retentionDays: days, staleAfter: opts.StaleAfter, clock: clock,
	}
	s.recorder = &Recorder{
		store: store, log: log, unexpected: unexpected, uow: uow, devices: deps.Devices, chats: deps.Chats,
		contacts: deps.Contacts, webhooks: deps.Webhooks, tenants: deps.Tenants, baseURL: opts.BaseURL, clock: clock,
	}
	return s
}

// Recorder returns the inbound recorder the WhatsApp sink forwards to.
func (s *Service) Recorder() *Recorder { return s.recorder }

// Send validates and queues one outbound message, then wakes the device's sender.
func (s *Service) Send(ctx context.Context, deviceID uuid.UUID, in SendInput) (*Message, error) {
	ctx, span := tracer.Start(ctx, "message.Send")
	defer span.End()
	span.SetAttributes(attribute.String("device.id", deviceID.String()))
	const op = "message.Send"
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	dev, err := s.devices.Ref(ctx, deviceID)
	if err != nil {
		return nil, s.fail(ctx, op, err)
	}
	if !dev.Linked {
		return nil, &DeviceNotLinkedError{DeviceID: deviceID.String()}
	}
	jid, kind, err := recipient(in.To)
	if err != nil {
		return nil, err
	}
	if in.Media != nil && len(in.Media.Bytes) == 0 && in.Media.URL != "" {
		fetched, mimeType, fetchErr := s.fetcher.Fetch(ctx, in.Media.URL)
		if fetchErr != nil {
			if IsMediaFetchError(fetchErr) || IsMediaTooLargeError(fetchErr) {
				return nil, fetchErr
			}
			return nil, &MediaFetchError{URL: in.Media.URL, Reason: fetchErr.Error()}
		}
		media := *in.Media
		media.Bytes = fetched
		if media.Mime == "" {
			media.Mime = mimeType
		}
		in.Media = &media
	}
	if in.ReplyTo, err = s.resolveReply(ctx, deviceID, in.ReplyTo); err != nil {
		return nil, err
	}
	var out *Message
	err = retryOnce(func() error {
		return s.atomic(ctx, op, func(ctx context.Context) error {
			chat, chatErr := s.chats.EnsureForJID(ctx, deviceID, jid, "", kind, "")
			if chatErr != nil {
				return chatErr
			}
			pid, mintErr := s.mint(ctx)
			if mintErr != nil {
				return mintErr
			}
			m, newErr := NewOutbound(tc.OrgID, tc.ProjectID, deviceID, chat.ID, pid, in, s.maxMedia)
			if newErr != nil {
				return newErr
			}
			m.stamp(s.clock())
			if saveErr := s.store.Save(ctx, m, 0); saveErr != nil {
				return saveErr
			}
			out = m
			return s.chats.Touch(ctx, chat.ID, m.WATimestamp, PreviewFor(m), false)
		})
	})
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	if in.MarkReadFirst {
		if readErr := s.MarkRead(ctx, out.ChatID); readErr != nil {
			s.log.WarnContext(ctx, "message.Send: mark read first", slog.String("chat_id", out.ChatID.String()), slog.Any("error", readErr))
		}
	}
	s.wake.Wake(deviceID)
	return out, nil
}

// Get returns one message of the caller's project.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Message, error) {
	ctx, span := tracer.Start(ctx, "message.Get")
	defer span.End()
	return s.load(ctx, "message.Get", id)
}

// List pages the caller's project, or returns rows after opts.SinceID.
func (s *Service) List(ctx context.Context, opts ListOpts) ([]*Message, string, error) {
	ctx, span := tracer.Start(ctx, "message.List")
	defer span.End()
	if _, err := tenant.From(ctx); err != nil {
		return nil, "", err
	}
	out, next, err := s.store.List(ctx, opts.WithDefaults())
	if err != nil {
		span.RecordError(err)
		if keyset.IsInvalidCursorError(err) {
			return nil, "", err
		}
		return nil, "", s.unexpected(ctx, "message.List: list", err)
	}
	return out, next, nil
}

// React queues a reaction to message id; an empty emoji removes it.
func (s *Service) React(ctx context.Context, id uuid.UUID, emoji string) (*Message, error) {
	return s.child(ctx, "message.React", id, func(tc tenant.Context, t *Message, pid string) (*Message, error) {
		return NewReaction(tc.OrgID, tc.ProjectID, t.DeviceID, t.ChatID, pid, t.WAMessageID, emoji)
	})
}

// Revoke queues a delete-for-everyone of one of the account's own messages.
func (s *Service) Revoke(ctx context.Context, id uuid.UUID) (*Message, error) {
	return s.child(ctx, "message.Revoke", id, func(tc tenant.Context, t *Message, pid string) (*Message, error) {
		if err := t.CheckRevocable(); err != nil {
			return nil, err
		}
		return NewRevoke(tc.OrgID, tc.ProjectID, t.DeviceID, t.ChatID, pid, t.WAMessageID)
	})
}

// Edit queues a new text for one of the account's own text messages, within EditWindow.
func (s *Service) Edit(ctx context.Context, id uuid.UUID, text string) (*Message, error) {
	return s.child(ctx, "message.Edit", id, func(tc tenant.Context, t *Message, pid string) (*Message, error) {
		if err := t.CheckEditable(s.clock()); err != nil {
			return nil, err
		}
		return NewEdit(tc.OrgID, tc.ProjectID, t.DeviceID, t.ChatID, pid, t.WAMessageID, text)
	})
}

func (s *Service) child(ctx context.Context, op string, id uuid.UUID, build func(tenant.Context, *Message, string) (*Message, error)) (*Message, error) {
	ctx, span := tracer.Start(ctx, op)
	defer span.End()
	span.SetAttributes(attribute.String("message.id", id.String()))
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	target, err := s.load(ctx, op, id)
	if err != nil {
		return nil, err
	}
	dev, err := s.devices.Ref(ctx, target.DeviceID)
	if err != nil {
		return nil, s.fail(ctx, op, err)
	}
	if !dev.Linked {
		return nil, &DeviceNotLinkedError{DeviceID: target.DeviceID.String()}
	}
	var m *Message
	err = retryOnce(func() error {
		pid, mintErr := s.mint(ctx)
		if mintErr != nil {
			return mintErr
		}
		built, buildErr := build(tc, target, pid)
		if buildErr != nil {
			return buildErr
		}
		built.stamp(s.clock())
		m = built
		return s.atomic(ctx, op, func(ctx context.Context) error { return s.store.Save(ctx, m, 0) })
	})
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	s.wake.Wake(target.DeviceID)
	return m, nil
}

func (s *Service) mint(ctx context.Context) (string, error) {
	id, err := publicid.New(PublicIDPrefix)
	if err != nil {
		return "", s.unexpected(ctx, "message: public id", err)
	}
	return id, nil
}

// NOTE: a public id collision aborts the transaction, so the whole unit of work runs once more with a fresh id.
func retryOnce(fn func() error) error {
	if err := fn(); !IsPublicIDTakenError(err) {
		return err
	}
	return fn()
}

// Resolve returns the caller's project message whose public id is publicID; a malformed id is not found without a query.
func (s *Service) Resolve(ctx context.Context, publicID string) (*Message, error) {
	ctx, span := tracer.Start(ctx, "message.Resolve")
	defer span.End()
	if !publicid.Valid(PublicIDPrefix, publicID) {
		return nil, &NotFoundError{ID: publicID}
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	m, err := s.store.ByPublicID(ctx, publicID)
	if err != nil {
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "message.Resolve", err, "message_public_id", publicID)
	}
	if m.OrgID != tc.OrgID || m.ProjectID != tc.ProjectID {
		return nil, &NotFoundError{ID: publicID}
	}
	return m, nil
}

// MarkRead sends read receipts for the chat's unread inbound messages, grouped by sender, then clears the chat's counter.
func (s *Service) MarkRead(ctx context.Context, chatID uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "message.MarkRead")
	defer span.End()
	const op = "message.MarkRead"
	chat, err := s.chats.Get(ctx, chatID)
	if err != nil {
		return s.fail(ctx, op, err)
	}
	unread, err := s.allUnread(ctx, chatID)
	if err != nil {
		return s.unexpected(ctx, op+": list", err, "chat_id", chatID)
	}
	senders := []string{}
	bySender := map[string][]string{}
	for _, m := range unread {
		if _, seen := bySender[m.SenderJID]; !seen {
			senders = append(senders, m.SenderJID)
		}
		bySender[m.SenderJID] = append(bySender[m.SenderJID], m.WAMessageID)
	}
	for _, sender := range senders {
		for ids := range slices.Chunk(bySender[sender], receiptChunk) {
			if err := s.transport.MarkRead(ctx, chat.DeviceID, chat.JID, sender, ids, false); err != nil {
				return s.fail(ctx, op, err)
			}
		}
	}
	now := s.clock()
	return s.atomic(ctx, op, func(ctx context.Context) error {
		// NOTE: bounded by the newest row the receipts covered, so a message that arrived meanwhile stays unread.
		if len(unread) > 0 {
			newest := slices.MaxFunc(unread, func(a, b *Message) int { return strings.Compare(a.ID.String(), b.ID.String()) })
			if _, err := s.store.MarkChatRead(ctx, chatID, newest.ID, now); err != nil {
				return err
			}
		}
		return s.chats.MarkRead(ctx, chatID)
	})
}

func (s *Service) allUnread(ctx context.Context, chatID uuid.UUID) ([]*Message, error) {
	var out []*Message
	cursor := ""
	for {
		page, next, err := s.store.List(ctx, ListOpts{ChatID: &chatID, Unread: true, Limit: MaxListLimit, Cursor: cursor})
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if next == "" {
			return out, nil
		}
		cursor = next
	}
}

// OpenMedia returns the decrypted attachment of message id; the caller closes the file.
func (s *Service) OpenMedia(ctx context.Context, id uuid.UUID) (file *os.File, mimeType, filename string, err error) {
	ctx, span := tracer.Start(ctx, "message.OpenMedia")
	defer span.End()
	const op = "message.OpenMedia"
	m, err := s.load(ctx, op, id)
	if err != nil {
		return nil, "", "", err
	}
	if m.Media == nil {
		return nil, "", "", &NotFoundError{ID: id.String()}
	}
	if m.Media.Keys.DirectPath == "" {
		return nil, "", "", &MediaUnavailableError{ID: id.String()}
	}
	if s.maxMedia > 0 && m.Media.Size > s.maxMedia {
		return nil, "", "", &MediaTooLargeError{Size: m.Media.Size, Max: s.maxMedia}
	}
	f, mimeType, err := s.media.Open(ctx, m)
	if err != nil {
		span.RecordError(err)
		return nil, "", "", s.fail(ctx, op, err)
	}
	return f, mimeType, MediaFilename(m), nil
}

// IsOnWhatsApp maps each phone registered on WhatsApp to its JID.
func (s *Service) IsOnWhatsApp(ctx context.Context, deviceID uuid.UUID, phones []string) (map[string]string, error) {
	ctx, span := tracer.Start(ctx, "message.IsOnWhatsApp")
	defer span.End()
	if _, err := s.devices.Ref(ctx, deviceID); err != nil {
		return nil, s.fail(ctx, "message.IsOnWhatsApp", err)
	}
	out, err := s.transport.IsOnWhatsApp(ctx, deviceID, phones)
	if err != nil {
		return nil, s.fail(ctx, "message.IsOnWhatsApp", err)
	}
	return out, nil
}

// ClaimNext hands the sender the device's next outbound row, or nil when the queue is empty.
//
//nolint:nilnil // an empty queue is not an error.
func (s *Service) ClaimNext(ctx context.Context, deviceID uuid.UUID) (*Claimed, error) {
	ctx, span := tracer.Start(ctx, "message.ClaimNext")
	defer span.End()
	var out *Claimed
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		exhausted := false
		err := s.atomic(ctx, "message.ClaimNext", func(ctx context.Context) error {
			m, err := s.store.ClaimNext(ctx, deviceID, s.staleAfter)
			if err != nil || m == nil {
				return err
			}
			if m.Status == StatusFailed {
				exhausted = true
				return s.emitStatus(ctx, m, string(StatusFailed), m.Error, s.clock())
			}
			c, err := s.claimed(ctx, deviceID, m)
			out = c
			return err
		})
		if err != nil || !exhausted {
			return out, err
		}
	}
}

func (s *Service) claimed(ctx context.Context, deviceID uuid.UUID, m *Message) (*Claimed, error) {
	chat, err := s.chats.Get(ctx, m.ChatID)
	if err != nil {
		return nil, err
	}
	c := &Claimed{
		ID: m.ID, DeviceID: m.DeviceID, Kind: m.Type, WAID: m.WAMessageID, ChatJID: chat.JID,
		Location: m.Location, Mentions: m.Mentions, TargetWAID: m.TargetWAMessageID, Attempts: m.Attempts,
	}
	switch m.Type {
	case TypeImage, TypeVideo, TypeAudio, TypeDocument:
		c.Media = &ClaimedMedia{Bytes: m.Raw, Mime: m.Media.Mime, Filename: m.Media.Filename, Caption: m.Body, Voice: m.Media.Voice}
	default:
		c.Text = m.Body
	}
	if m.QuotedWAMessageID != "" {
		c.QuotedWAID = m.QuotedWAMessageID
		q, qErr := s.store.QuotedByWAID(ctx, deviceID, m.QuotedWAMessageID)
		switch {
		case qErr == nil:
			c.QuotedBody, c.QuotedRaw = q.Body, q.Raw
			if !q.FromMe {
				c.QuotedSender = q.SenderJID
			}
		case !IsNotFoundError(qErr):
			return nil, qErr
		}
	}
	if m.TargetWAMessageID != "" {
		c.TargetFromMe = true
		t, tErr := s.store.ByWAID(ctx, deviceID, m.TargetWAMessageID)
		switch {
		case tErr == nil:
			c.TargetFromMe, c.TargetSender = t.FromMe, t.SenderJID
		case !IsNotFoundError(tErr):
			return nil, tErr
		}
	}
	return c, nil
}

// MarkSent records the acknowledgement and the upload keys, and emits message.status "sent" only when the status rose.
func (s *Service) MarkSent(ctx context.Context, id uuid.UUID, at time.Time, keys *MediaKeys) error {
	ctx, span := tracer.Start(ctx, "message.MarkSent")
	defer span.End()
	return s.atomic(ctx, "message.MarkSent", func(ctx context.Context) error {
		m, err := s.load(ctx, "message.MarkSent", id)
		if err != nil {
			return err
		}
		rose := m.MarkSent(at, keys, s.clock())
		if err := s.store.Save(ctx, m, m.Version); err != nil {
			return err
		}
		if !rose {
			return nil
		}
		return s.emitStatus(ctx, m, string(StatusSent), "", at)
	})
}

// MarkFailed requeues or fails the row; reason is a short failure class, never raw engine text, and only a terminal failure emits message.status "failed".
func (s *Service) MarkFailed(ctx context.Context, id uuid.UUID, reason string, retryable bool) error {
	ctx, span := tracer.Start(ctx, "message.MarkFailed")
	defer span.End()
	if reason == "" {
		reason = "unknown"
	}
	reason = truncate(reason, maxErrorRunes)
	return s.atomic(ctx, "message.MarkFailed", func(ctx context.Context) error {
		m, err := s.load(ctx, "message.MarkFailed", id)
		if err != nil {
			return err
		}
		terminal := m.MarkFailed(reason, retryable, s.clock())
		if err := s.store.Save(ctx, m, m.Version); err != nil {
			return err
		}
		if !terminal {
			return nil
		}
		return s.emitStatus(ctx, m, string(StatusFailed), reason, s.clock())
	})
}

// Requeue hands a claimed row back to the queue without using up an attempt and without an event; the sender calls it when the send never reached the engine.
func (s *Service) Requeue(ctx context.Context, id uuid.UUID) error {
	ctx, span := tracer.Start(ctx, "message.Requeue")
	defer span.End()
	return s.atomic(ctx, "message.Requeue", func(ctx context.Context) error {
		m, err := s.load(ctx, "message.Requeue", id)
		if err != nil {
			return err
		}
		if !m.Release(s.clock()) {
			return nil
		}
		return s.store.Save(ctx, m, m.Version)
	})
}

// RecordReceipt applies a delivery receipt to every named message and emits message.status per change.
func (s *Service) RecordReceipt(ctx context.Context, ref Ref, in ReceiptInput) error {
	kind, ok := ReceiptKindOf(in.Type)
	if !ok {
		return nil
	}
	ctx = tenant.Into(ctx, tenant.Context{OrgID: ref.OrgID, ProjectID: ref.ProjectID})
	ctx, span := tracer.Start(ctx, "message.RecordReceipt")
	defer span.End()
	at := in.At
	if at.IsZero() {
		at = s.clock()
	}
	return s.atomic(ctx, "message.RecordReceipt", func(ctx context.Context) error {
		for _, waid := range in.WAIDs {
			m, err := s.store.ByWAID(ctx, ref.DeviceID, waid)
			if IsNotFoundError(err) {
				continue
			}
			if err != nil {
				return err
			}
			if !m.Receipt(kind, at, s.clock()) {
				continue
			}
			if err := s.store.Save(ctx, m, m.Version); err != nil {
				return err
			}
			if err := s.emitStatus(ctx, m, string(kind), "", at); err != nil {
				return err
			}
		}
		return nil
	})
}

// Retention returns the caller's project's retention in days.
func (s *Service) Retention(ctx context.Context) (int, error) {
	ctx, span := tracer.Start(ctx, "message.Retention")
	defer span.End()
	tc, err := tenant.From(ctx)
	if err != nil {
		return 0, err
	}
	return s.retentionOf(ctx, tc.ProjectID)
}

// SetRetention stores the caller's project's retention.
func (s *Service) SetRetention(ctx context.Context, days int) error {
	ctx, span := tracer.Start(ctx, "message.SetRetention")
	defer span.End()
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	p, err := NewRetentionPolicy(tc.OrgID, tc.ProjectID, days)
	if err != nil {
		return err
	}
	if err := s.store.SaveRetention(ctx, p); err != nil {
		return s.fail(ctx, "message.SetRetention", err)
	}
	return nil
}

// PendingDeletion counts the caller's project's messages older than its cut-off.
func (s *Service) PendingDeletion(ctx context.Context) (int64, error) {
	ctx, span := tracer.Start(ctx, "message.PendingDeletion")
	defer span.End()
	tc, err := tenant.From(ctx)
	if err != nil {
		return 0, err
	}
	days, err := s.retentionOf(ctx, tc.ProjectID)
	if err != nil {
		return 0, err
	}
	n, err := s.store.CountOlderThan(ctx, tc.ProjectID, RetentionPolicy{Days: days}.Cutoff(s.clock()))
	if err != nil {
		return 0, s.unexpected(ctx, "message.PendingDeletion: count", err)
	}
	return n, nil
}

// CountToday counts the caller's project's messages since midnight UTC.
func (s *Service) CountToday(ctx context.Context) (int64, error) {
	ctx, span := tracer.Start(ctx, "message.CountToday")
	defer span.End()
	if _, err := tenant.From(ctx); err != nil {
		return 0, err
	}
	n, err := s.store.CountToday(ctx, s.clock().UTC().Truncate(24*time.Hour))
	if err != nil {
		return 0, s.unexpected(ctx, "message.CountToday: count", err)
	}
	return n, nil
}

// RunRetention deletes, project by project, every message older than that project's retention, then repairs the touched chats.
func (s *Service) RunRetention(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "message.RunRetention")
	defer span.End()
	if _, err := tenant.From(ctx); err != nil {
		return err
	}
	projects, err := s.tenants.ProjectIDs(ctx)
	if err != nil {
		return s.unexpected(ctx, "message.RunRetention: projects", err)
	}
	var errs []error
	for _, pid := range projects {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		pctx := tenant.WithProject(ctx, pid)
		if expireErr := s.expireQueued(pctx, pid); expireErr != nil {
			errs = append(errs, expireErr)
		}
		deleted, chats, purgeErr := s.purge(pctx, pid)
		if purgeErr != nil {
			errs = append(errs, purgeErr)
			continue
		}
		for _, chatID := range chats {
			if repairErr := s.repair(pctx, chatID); repairErr != nil {
				errs = append(errs, repairErr)
			}
		}
		s.log.InfoContext(ctx, "message.retention", slog.String("project_id", pid.String()),
			slog.Int64("deleted", deleted), slog.Int("chats", len(chats)))
	}
	return errors.Join(errs...)
}

func (s *Service) purge(ctx context.Context, projectID uuid.UUID) (int64, []uuid.UUID, error) {
	days, err := s.retentionOf(ctx, projectID)
	if err != nil {
		return 0, nil, err
	}
	cutoff := RetentionPolicy{Days: days}.Cutoff(s.clock())
	var total int64
	seen := map[uuid.UUID]struct{}{}
	touched := []uuid.UUID{}
	for {
		n, chats, delErr := s.store.DeleteOlderThan(ctx, projectID, cutoff, RetentionBatch)
		if delErr != nil {
			return total, touched, s.unexpected(ctx, "message.RunRetention: delete", delErr, "project_id", projectID)
		}
		total += n
		for _, c := range chats {
			if _, dup := seen[c]; !dup {
				seen[c] = struct{}{}
				touched = append(touched, c)
			}
		}
		if n == 0 || ctx.Err() != nil {
			return total, touched, ctx.Err()
		}
	}
}

// NOTE: a row queued, or left sending, for QueuedExpiry fails and releases its bytes.
func (s *Service) expireQueued(ctx context.Context, projectID uuid.UUID) error {
	before := s.clock().Add(-QueuedExpiry)
	for {
		rows, err := s.store.UnsentBefore(ctx, projectID, before, RetentionBatch)
		if err != nil {
			return s.unexpected(ctx, "message.RunRetention: expire", err, "project_id", projectID)
		}
		if len(rows) == 0 || ctx.Err() != nil {
			return ctx.Err()
		}
		for _, m := range rows {
			if err := s.atomic(ctx, "message.RunRetention: expire", func(ctx context.Context) error {
				if !m.Expire(ReasonExpired, s.clock()) {
					return nil
				}
				if err := s.store.Save(ctx, m, m.Version); err != nil {
					return err
				}
				return s.emitStatus(ctx, m, string(StatusFailed), ReasonExpired, s.clock())
			}); err != nil && !IsVersionMismatchError(err) {
				return err
			}
		}
	}
}

func (s *Service) repair(ctx context.Context, chatID uuid.UUID) error {
	latest, _, err := s.store.List(ctx, ListOpts{ChatID: &chatID, Limit: 1})
	if err != nil {
		return s.unexpected(ctx, "message.RunRetention: latest", err, "chat_id", chatID)
	}
	unread, err := s.store.CountUnreadInbound(ctx, chatID)
	if err != nil {
		return s.unexpected(ctx, "message.RunRetention: unread", err, "chat_id", chatID)
	}
	var last *time.Time
	preview := ""
	if len(latest) == 1 {
		at := latest[0].WATimestamp
		last, preview = &at, PreviewFor(latest[0])
	}
	return s.fail(ctx, "message.RunRetention: repair", s.chats.Repair(ctx, chatID, last, preview, unread))
}

func (s *Service) retentionOf(ctx context.Context, projectID uuid.UUID) (int, error) {
	p, err := s.store.RetentionByProject(ctx, projectID)
	if err != nil {
		return 0, s.unexpected(ctx, "message: retention", err, "project_id", projectID)
	}
	if p == nil {
		return s.retentionDays, nil
	}
	return p.Days, nil
}

func (s *Service) emitStatus(ctx context.Context, m *Message, status, errText string, at time.Time) error {
	dev, err := s.devices.Ref(ctx, m.DeviceID)
	if err != nil {
		return err
	}
	return s.webhooks.Enqueue(ctx, events.MessageStatus, statusPayload(dev, m, status, errText, at))
}

func (s *Service) resolveReply(ctx context.Context, deviceID uuid.UUID, replyTo string) (string, error) {
	replyTo = strings.TrimSpace(replyTo)
	if !publicid.Valid(PublicIDPrefix, replyTo) {
		return replyTo, nil
	}
	q, err := s.Resolve(ctx, replyTo)
	if err != nil {
		return "", err
	}
	if q.DeviceID != deviceID {
		return "", &InvalidInputError{Field: "reply_to", Reason: "the quoted message belongs to another device"}
	}
	return q.WAMessageID, nil
}

func (s *Service) load(ctx context.Context, op string, id uuid.UUID) (*Message, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	m, err := s.store.ByID(ctx, id)
	if err != nil {
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, op+": load", err, "message_id", id)
	}
	if m.OrgID != tc.OrgID || m.ProjectID != tc.ProjectID {
		return nil, &NotFoundError{ID: id.String()}
	}
	return m, nil
}

func (s *Service) atomic(ctx context.Context, op string, fn func(ctx context.Context) error) error {
	var inner error
	err := s.uow(ctx, func(ctx context.Context) error {
		inner = fn(ctx)
		return inner
	})
	if inner != nil {
		return s.fail(ctx, op, inner)
	}
	if err != nil {
		return s.unexpected(ctx, op+": unit of work", err)
	}
	return nil
}

// NOTE: a typed error from any module passes through; only an untyped one is reported as unexpected.
func (s *Service) fail(ctx context.Context, op string, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := apperror.AsAppError(err); ok || IsPublicIDTakenError(err) {
		return err
	}
	// NOTE: a caller that went away is not a server fault, so it is returned as is rather than reported.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return s.unexpected(ctx, op, err)
}

func recipient(to string) (jid, kind string, err error) {
	to = strings.TrimSpace(to)
	if user, server, ok := strings.Cut(to, "@"); ok {
		switch {
		case user == "":
		case server == "s.whatsapp.net", server == "lid":
			return to, "dm", nil
		case server == "g.us":
			return to, "group", nil
		}
		return "", "", &InvalidInputError{Field: "to", Reason: "a JID ends in @s.whatsapp.net, @lid or @g.us"}
	}
	if strings.HasPrefix(to, "0") {
		return "", "", &InvalidInputError{Field: "to", Reason: "use the international number with its country code, e.g. 62812…"}
	}
	digits := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsDigit(r):
			return r
		case strings.ContainsRune("+ -().", r):
			return -1
		}
		return 'x'
	}, to)
	if strings.ContainsRune(digits, 'x') || len(digits) < 8 || len(digits) > 15 {
		return "", "", &InvalidInputError{Field: "to", Reason: "a phone number of 8 to 15 digits, or a JID"}
	}
	return digits + "@s.whatsapp.net", "dm", nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
