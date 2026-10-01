package chat

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/platform/db"
	"altalune.id/openwa/internal/platform/keyset"
	"altalune.id/openwa/internal/platform/publicid"
	"altalune.id/openwa/internal/platform/tenant"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer = otel.Tracer("altalune.id/openwa/internal/chat")

const maxSaveAttempts = 3

var errRaceLost = errors.New("attempts exhausted")

// Groups is the port group verbs reach the WhatsApp engine through; boot adapts whatsapp.Service to it.
type Groups interface {
	List(ctx context.Context, deviceID uuid.UUID) ([]GroupInfo, error)
	Info(ctx context.Context, deviceID uuid.UUID, jid string) (GroupInfo, error)
	Join(ctx context.Context, deviceID uuid.UUID, inviteLink string) (string, error)
	Leave(ctx context.Context, deviceID uuid.UUID, jid string) error
}

// Service is the chat driving port.
type Service struct {
	store      Store
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
	uow        tenant.UnitOfWork
	groups     Groups
}

// NewService binds the service to its dependencies.
func NewService(store Store, log *slog.Logger, unexpected apperror.UnexpectedFunc, uow tenant.UnitOfWork, groups Groups) *Service {
	return &Service{store: store, log: log.With("module", "chat"), unexpected: unexpected, uow: uow, groups: groups}
}

// List pages the caller's project, newest activity first.
func (s *Service) List(ctx context.Context, opts ListOpts) ([]*Chat, string, error) {
	ctx, span := tracer.Start(ctx, "chat.List")
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
		return nil, "", s.unexpected(ctx, "chat.List: list", err)
	}
	return out, next, nil
}

// Get returns one chat of the caller's project.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Chat, error) {
	ctx, span := tracer.Start(ctx, "chat.Get")
	defer span.End()
	span.SetAttributes(attribute.String("chat.id", id.String()))
	return s.load(ctx, span, "chat.Get", id)
}

// Resolve returns the caller's project chat whose public id is publicID; a malformed id is not found without a query.
func (s *Service) Resolve(ctx context.Context, publicID string) (*Chat, error) {
	ctx, span := tracer.Start(ctx, "chat.Resolve")
	defer span.End()
	if !publicid.Valid(PublicIDPrefix, publicID) {
		return nil, &NotFoundError{ID: publicID}
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	c, err := s.store.ByPublicID(ctx, publicID)
	if err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, "chat.Resolve", err, "chat_public_id", publicID)
	}
	if c.OrgID != tc.OrgID || c.ProjectID != tc.ProjectID {
		return nil, &NotFoundError{ID: publicID}
	}
	return c, nil
}

// PublicIDs maps chat ids to their public ids in one store query, for list views.
func (s *Service) PublicIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out, err := s.store.PublicIDs(ctx, ids)
	if err != nil {
		return nil, s.unexpected(ctx, "chat.PublicIDs", err)
	}
	return out, nil
}

// EnsureForJID returns the device's chat keyed by jid or its lid alias, creating it and filling a newly learnt identifier; a public id collision on insert is a lost race the loop retries with a fresh id.
func (s *Service) EnsureForJID(ctx context.Context, deviceID uuid.UUID, jid, lid string, kind Kind, name string) (*Chat, error) {
	ctx, span := tracer.Start(ctx, "chat.EnsureForJID")
	defer span.End()
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	var out *Chat
	err = s.inTx(ctx, func(ctx context.Context) error {
		for range maxSaveAttempts {
			c, found, findErr := s.findEither(ctx, deviceID, jid, lid)
			if findErr != nil {
				return findErr
			}
			if !found {
				pid, mintErr := publicid.New(PublicIDPrefix)
				if mintErr != nil {
					return s.unexpected(ctx, "chat.EnsureForJID: public id", mintErr)
				}
				fresh, newErr := New(tc.OrgID, tc.ProjectID, deviceID, pid, jid, lid, kind)
				if newErr != nil {
					return newErr
				}
				fresh.Rename(name)
				inserted, insErr := s.store.Insert(ctx, fresh)
				if IsNotFoundError(insErr) {
					return insErr
				}
				if insErr != nil {
					return s.unexpected(ctx, "chat.EnsureForJID: insert", insErr, "device_id", deviceID)
				}
				if inserted {
					out = fresh
					return nil
				}
				continue
			}
			if c.ProjectID != tc.ProjectID {
				return &NotFoundError{ID: c.ID.String()}
			}
			alias, aliasErr := s.freeLID(ctx, deviceID, c, lid)
			if aliasErr != nil {
				return aliasErr
			}
			changed := c.Identify(jid, alias)
			if name != "" && c.Name == "" {
				c.Rename(name)
				changed = true
			}
			if !changed {
				out = c
				return nil
			}
			saveErr := s.store.Save(ctx, c, c.Version)
			if IsVersionMismatchError(saveErr) {
				continue
			}
			if saveErr != nil {
				return s.unexpected(ctx, "chat.EnsureForJID: save", saveErr, "chat_id", c.ID)
			}
			c.Version++
			out = c
			return nil
		}
		return s.unexpected(ctx, "chat.EnsureForJID: lost insert race repeatedly", errRaceLost, "device_id", deviceID)
	})
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	return out, nil
}

// Touch records a message on the chat.
func (s *Service) Touch(ctx context.Context, id uuid.UUID, at time.Time, preview string, inbound bool) error {
	return s.mutate(ctx, "chat.Touch", id, func(c *Chat) bool {
		c.Touch(at, preview, inbound)
		return true
	})
}

// MarkRead clears the chat's unread counter.
func (s *Service) MarkRead(ctx context.Context, id uuid.UUID) error {
	return s.mutate(ctx, "chat.MarkRead", id, func(c *Chat) bool {
		if c.UnreadCount == 0 {
			return false
		}
		c.MarkRead()
		return true
	})
}

// Archive hides the chat from the default inbox.
func (s *Service) Archive(ctx context.Context, id uuid.UUID) error {
	return s.mutate(ctx, "chat.Archive", id, func(c *Chat) bool {
		if c.Archived {
			return false
		}
		c.Archive()
		return true
	})
}

// Repair replaces the derived fields after retention; a nil last clears them.
func (s *Service) Repair(ctx context.Context, id uuid.UUID, last *time.Time, preview string, unread int) error {
	return s.mutate(ctx, "chat.Repair", id, func(c *Chat) bool {
		c.Repair(last, preview, unread)
		return true
	})
}

func (s *Service) mutate(ctx context.Context, op string, id uuid.UUID, fn func(*Chat) bool) error {
	ctx, span := tracer.Start(ctx, op)
	defer span.End()
	span.SetAttributes(attribute.String("chat.id", id.String()))
	err := s.inTx(ctx, func(ctx context.Context) error {
		for range maxSaveAttempts {
			c, err := s.load(ctx, span, op, id)
			if err != nil {
				return err
			}
			if !fn(c) {
				return nil
			}
			saveErr := s.store.Save(ctx, c, c.Version)
			if IsVersionMismatchError(saveErr) {
				continue
			}
			if saveErr != nil {
				if IsNotFoundError(saveErr) {
					return saveErr
				}
				return s.unexpected(ctx, op+": save", saveErr, "chat_id", id)
			}
			return nil
		}
		return &VersionMismatchError{}
	})
	if err != nil {
		span.RecordError(err)
	}
	return err
}

func (s *Service) load(ctx context.Context, span trace.Span, op string, id uuid.UUID) (*Chat, error) {
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	c, err := s.store.ByID(ctx, id)
	if err != nil {
		span.RecordError(err)
		if IsNotFoundError(err) {
			return nil, err
		}
		return nil, s.unexpected(ctx, op+": load", err, "chat_id", id)
	}
	if c.OrgID != tc.OrgID || c.ProjectID != tc.ProjectID {
		return nil, &NotFoundError{ID: id.String()}
	}
	return c, nil
}

// NOTE: a LID another chat already holds is not written, so chats_device_lid_key never aborts the transaction (D5).
func (s *Service) freeLID(ctx context.Context, deviceID uuid.UUID, c *Chat, lid string) (string, error) {
	if lid == "" || c.LID != "" {
		return lid, nil
	}
	other, err := s.store.ByJID(ctx, deviceID, lid)
	if IsNotFoundError(err) {
		return lid, nil
	}
	if err != nil {
		return "", s.unexpected(ctx, "chat.EnsureForJID: alias lookup", err, "device_id", deviceID)
	}
	if other.ID != c.ID {
		return "", nil
	}
	return lid, nil
}

func (s *Service) findEither(ctx context.Context, deviceID uuid.UUID, jid, lid string) (*Chat, bool, error) {
	for _, key := range []string{jid, lid} {
		if key == "" {
			continue
		}
		c, err := s.store.ByJID(ctx, deviceID, key)
		if err == nil {
			return c, true, nil
		}
		if !IsNotFoundError(err) {
			return nil, false, s.unexpected(ctx, "chat.EnsureForJID: lookup", err, "device_id", deviceID)
		}
	}
	return nil, false, nil
}

// NOTE: joins a caller's unit of work (the recorder's) instead of nesting one.
func (s *Service) inTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := db.CurrentTx(ctx); ok {
		return fn(ctx)
	}
	var inner error
	err := s.uow(ctx, func(ctx context.Context) error {
		inner = fn(ctx)
		return inner
	})
	if err == nil || inner != nil {
		return inner
	}
	return s.unexpected(ctx, "chat: unit of work", err)
}

// ListGroups returns the device's live groups, with the chat row where one exists, followed by stored groups it has left; it writes nothing.
func (s *Service) ListGroups(ctx context.Context, deviceID uuid.UUID) ([]GroupView, error) {
	ctx, span := tracer.Start(ctx, "chat.ListGroups")
	defer span.End()
	live, err := s.groups.List(ctx, deviceID)
	if err != nil {
		span.RecordError(err)
		return nil, s.pass(ctx, "chat.ListGroups: engine", err)
	}
	stored, err := s.storedGroups(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	byJID := make(map[string]*Chat, len(stored))
	for _, c := range stored {
		byJID[c.JID] = c
	}
	out := make([]GroupView, 0, len(live)+len(stored))
	seen := make(map[string]struct{}, len(live))
	for _, g := range live {
		seen[g.JID] = struct{}{}
		v := GroupView{GroupInfo: g, Joined: true}
		if c, ok := byJID[g.JID]; ok {
			v.ChatID = c.ID
		}
		out = append(out, v)
	}
	for _, c := range stored {
		if _, ok := seen[c.JID]; !ok {
			out = append(out, GroupView{GroupInfo: GroupInfo{JID: c.JID, Name: c.Name}, ChatID: c.ID})
		}
	}
	return out, nil
}

func (s *Service) storedGroups(ctx context.Context, deviceID uuid.UUID) ([]*Chat, error) {
	kind := KindGroup
	var out []*Chat
	cursor := ""
	for {
		page, next, err := s.List(ctx, ListOpts{DeviceID: &deviceID, Kind: &kind, Limit: MaxListLimit, Cursor: cursor})
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

// GroupInfo asks the engine for the details of a group chat.
func (s *Service) GroupInfo(ctx context.Context, chatID uuid.UUID) (GroupInfo, error) {
	ctx, span := tracer.Start(ctx, "chat.GroupInfo")
	defer span.End()
	c, err := s.groupChat(ctx, span, "chat.GroupInfo", chatID)
	if err != nil {
		return GroupInfo{}, err
	}
	info, err := s.groups.Info(ctx, c.DeviceID, c.JID)
	if err != nil {
		span.RecordError(err)
		return GroupInfo{}, s.pass(ctx, "chat.GroupInfo: engine", err)
	}
	return info, nil
}

// JoinGroup joins by invite link and records the group's chat once the engine returns its JID.
func (s *Service) JoinGroup(ctx context.Context, deviceID uuid.UUID, inviteLink string) (*Chat, error) {
	ctx, span := tracer.Start(ctx, "chat.JoinGroup")
	defer span.End()
	jid, err := s.groups.Join(ctx, deviceID, inviteLink)
	if err != nil {
		span.RecordError(err)
		return nil, s.pass(ctx, "chat.JoinGroup: engine", err)
	}
	name := ""
	if info, infoErr := s.groups.Info(ctx, deviceID, jid); infoErr == nil {
		name = info.Name
	}
	return s.EnsureForJID(ctx, deviceID, jid, "", KindGroup, name)
}

// LeaveGroup leaves through the engine first, then archives the chat.
func (s *Service) LeaveGroup(ctx context.Context, chatID uuid.UUID) (*Chat, error) {
	ctx, span := tracer.Start(ctx, "chat.LeaveGroup")
	defer span.End()
	c, err := s.groupChat(ctx, span, "chat.LeaveGroup", chatID)
	if err != nil {
		return nil, err
	}
	if err := s.groups.Leave(ctx, c.DeviceID, c.JID); err != nil {
		span.RecordError(err)
		return nil, s.pass(ctx, "chat.LeaveGroup: engine", err)
	}
	if err := s.Archive(ctx, chatID); err != nil {
		return nil, err
	}
	return s.Get(ctx, chatID)
}

func (s *Service) groupChat(ctx context.Context, span trace.Span, op string, id uuid.UUID) (*Chat, error) {
	c, err := s.load(ctx, span, op, id)
	if err != nil {
		return nil, err
	}
	if c.Kind != KindGroup {
		return nil, &NotAGroupError{ID: c.PublicID}
	}
	return c, nil
}

// NOTE: an engine error with a wire code (a WhatsApp session error) passes through; anything else is unexpected.
func (s *Service) pass(ctx context.Context, op string, err error) error {
	if _, ok := apperror.AsAppError(err); ok {
		return err
	}
	return s.unexpected(ctx, op, err)
}
