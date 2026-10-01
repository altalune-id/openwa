package controlplane

import (
	"context"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	messagev1 "altalune.id/openwa/gen/go/message/v1"
	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/project"
)

// MessageService implements message.v1.MessageService.
type MessageService struct {
	log      *slog.Logger
	messages *message.Service
	chats    *chat.Service
	scope    messagingScope
	projects *project.Service
	orgs     *org.Service
	baseURL  string
}

// NewMessageService binds the handler to its collaborators.
func NewMessageService(messages *message.Service, chats *chat.Service, devices *device.Service, projects *project.Service, orgs *org.Service, baseURL string, log *slog.Logger) *MessageService {
	return &MessageService{log: log, messages: messages, chats: chats, scope: newMessagingScope(projects, devices), projects: projects, orgs: orgs, baseURL: baseURL}
}

// Send queues one message through the request's device, or the project's only device.
func (s *MessageService) Send(ctx context.Context, req *connect.Request[messagev1.SendRequest]) (*connect.Response[messagev1.SendResponse], error) {
	tctx, err := s.scope.project(ctx, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	deviceID, err := s.scope.device(tctx, req.Msg.GetDeviceId())
	if err != nil {
		return nil, err
	}
	in := message.SendInput{To: req.Msg.GetTo(), Text: req.Msg.GetText(), ReplyTo: req.Msg.GetReplyTo(), Mentions: req.Msg.GetMentions(), MarkReadFirst: req.Msg.GetMarkReadFirst()}
	if md := req.Msg.GetMedia(); md != nil {
		in.Media = &message.MediaInput{Bytes: md.GetData(), URL: md.GetUrl(), Mime: md.GetMime(), Filename: md.GetFilename(), Caption: md.GetCaption(), Voice: md.GetVoice()}
	}
	if loc := req.Msg.GetLocation(); loc != nil {
		in.Location = &message.Location{Lat: loc.GetLat(), Lng: loc.GetLng(), Name: loc.GetName(), Address: loc.GetAddress()}
	}
	m, err := s.messages.Send(tctx, deviceID, in)
	if err != nil {
		return nil, err
	}
	out, err := s.one(tctx, m)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&messagev1.SendResponse{Message: out}), nil
}

// Get returns one message of the project.
func (s *MessageService) Get(ctx context.Context, req *connect.Request[messagev1.GetRequest]) (*connect.Response[messagev1.GetResponse], error) {
	tctx, m, err := s.load(ctx, req.Msg.GetProjectId(), req.Msg.GetMessageId())
	if err != nil {
		return nil, err
	}
	out, err := s.one(tctx, m)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&messagev1.GetResponse{Message: out}), nil
}

// List pages a chat's or a device's messages; every id in the request and the answer is a public id.
func (s *MessageService) List(ctx context.Context, req *connect.Request[messagev1.ListRequest]) (*connect.Response[messagev1.ListResponse], error) {
	tctx, err := s.scope.project(ctx, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	opts := message.ListOpts{Cursor: req.Msg.GetCursor(), Limit: int(req.Msg.GetLimit())}
	if opts.DeviceID, err = s.scope.optionalDevice(tctx, req.Msg.GetDeviceId()); err != nil {
		return nil, err
	}
	if raw := req.Msg.GetChatId(); raw != "" {
		c, resolveErr := s.chats.Resolve(tctx, raw)
		if resolveErr != nil {
			return nil, resolveErr
		}
		opts.ChatID = &c.ID
	}
	if raw := req.Msg.GetSinceId(); raw != "" {
		since, resolveErr := s.messages.Resolve(tctx, raw)
		if resolveErr != nil {
			return nil, resolveErr
		}
		opts.SinceID = &since.ID
	}
	items, next, err := s.messages.List(tctx, opts)
	if err != nil {
		return nil, err
	}
	protos, err := s.protos(tctx, items)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&messagev1.ListResponse{Messages: protos, NextCursor: next}), nil
}

// React queues a reaction.
func (s *MessageService) React(ctx context.Context, req *connect.Request[messagev1.ReactRequest]) (*connect.Response[messagev1.ReactResponse], error) {
	tctx, target, err := s.load(ctx, req.Msg.GetProjectId(), req.Msg.GetMessageId())
	if err != nil {
		return nil, err
	}
	m, err := s.messages.React(tctx, target.ID, req.Msg.GetEmoji())
	if err != nil {
		return nil, err
	}
	out, err := s.one(tctx, m)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&messagev1.ReactResponse{Message: out}), nil
}

// Revoke queues a delete-for-everyone.
func (s *MessageService) Revoke(ctx context.Context, req *connect.Request[messagev1.RevokeRequest]) (*connect.Response[messagev1.RevokeResponse], error) {
	tctx, target, err := s.load(ctx, req.Msg.GetProjectId(), req.Msg.GetMessageId())
	if err != nil {
		return nil, err
	}
	m, err := s.messages.Revoke(tctx, target.ID)
	if err != nil {
		return nil, err
	}
	out, err := s.one(tctx, m)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&messagev1.RevokeResponse{Message: out}), nil
}

// Edit queues a new text.
func (s *MessageService) Edit(ctx context.Context, req *connect.Request[messagev1.EditRequest]) (*connect.Response[messagev1.EditResponse], error) {
	tctx, target, err := s.load(ctx, req.Msg.GetProjectId(), req.Msg.GetMessageId())
	if err != nil {
		return nil, err
	}
	m, err := s.messages.Edit(tctx, target.ID, req.Msg.GetText())
	if err != nil {
		return nil, err
	}
	out, err := s.one(tctx, m)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&messagev1.EditResponse{Message: out}), nil
}

// MarkRead sends read receipts for a chat.
func (s *MessageService) MarkRead(ctx context.Context, req *connect.Request[messagev1.MarkReadRequest]) (*connect.Response[messagev1.MarkReadResponse], error) {
	tctx, err := s.scope.project(ctx, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	c, err := s.chats.Resolve(tctx, req.Msg.GetChatId())
	if err != nil {
		return nil, err
	}
	if err := s.messages.MarkRead(tctx, c.ID); err != nil {
		return nil, err
	}
	return connect.NewResponse(&messagev1.MarkReadResponse{}), nil
}

// NOTE: Resolve is project-scoped and answers NotFound for a malformed id without a query.
func (s *MessageService) load(ctx context.Context, projectRaw, idRaw string) (context.Context, *message.Message, error) {
	tctx, err := s.scope.project(ctx, projectRaw)
	if err != nil {
		return nil, nil, err
	}
	m, err := s.messages.Resolve(tctx, idRaw)
	if err != nil {
		return nil, nil, err
	}
	return tctx, m, nil
}

type mediaLinks struct {
	baseURL, org, project string
}

func (l mediaLinks) of(publicID string) string {
	if l.org == "" || l.project == "" {
		return ""
	}
	return message.MediaURL(l.baseURL, l.org, l.project, publicID)
}

// NOTE: once per request; a failed lookup is logged and leaves media.url empty rather than failing the call.
func (s *MessageService) links(ctx context.Context) mediaLinks {
	tc, err := tenant.From(ctx)
	if err != nil {
		return mediaLinks{}
	}
	o, err := s.orgs.ByID(ctx, tc.OrgID)
	if err != nil {
		s.log.WarnContext(ctx, "controlplane: media url: org slug", slog.Any("error", err))
		return mediaLinks{}
	}
	p, err := s.projects.ByID(ctx, tc.ProjectID)
	if err != nil {
		s.log.WarnContext(ctx, "controlplane: media url: project slug", slog.Any("error", err))
		return mediaLinks{}
	}
	return mediaLinks{baseURL: s.baseURL, org: o.Slug, project: p.Slug}
}

func (s *MessageService) one(ctx context.Context, m *message.Message) (*messagev1.Message, error) {
	out, err := s.protos(ctx, []*message.Message{m})
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

// NOTE: a page costs one device and one chat public-id read, whatever its length.
func (s *MessageService) protos(ctx context.Context, items []*message.Message) ([]*messagev1.Message, error) {
	deviceIDs := make([]uuid.UUID, 0, len(items))
	chatIDs := make([]uuid.UUID, 0, len(items))
	for _, m := range items {
		deviceIDs, chatIDs = append(deviceIDs, m.DeviceID), append(chatIDs, m.ChatID)
	}
	devicePubs, err := s.scope.devicePublicIDs(ctx, deviceIDs)
	if err != nil {
		return nil, err
	}
	chatPubs := map[uuid.UUID]string{}
	if len(chatIDs) > 0 {
		if chatPubs, err = s.chats.PublicIDs(ctx, chatIDs); err != nil {
			return nil, err
		}
	}
	links := s.links(ctx)
	out := make([]*messagev1.Message, 0, len(items))
	for _, m := range items {
		out = append(out, messageToProto(links, m, devicePubs[m.DeviceID], chatPubs[m.ChatID]))
	}
	return out, nil
}

func messageToProto(links mediaLinks, m *message.Message, devicePub, chatPub string) *messagev1.Message {
	out := &messagev1.Message{
		Id: m.PublicID, DeviceId: devicePub, ChatId: chatPub, Direction: string(m.Direction),
		WaId: m.WAMessageID, Type: string(m.Type), Status: string(m.Status), Body: m.Body, SenderJid: m.SenderJID,
		SenderPhone: m.SenderPhone, SenderName: m.SenderName, FromMe: m.FromMe, QuotedWaId: m.QuotedWAMessageID,
		TargetWaId: m.TargetWAMessageID, Mentions: m.Mentions, Error: m.Error, Attempts: int32(m.Attempts), //nolint:gosec // G115: attempts never exceed MaxAttempts.
		Timestamp: timestamppb.New(m.WATimestamp),
	}
	if m.SentAt != nil {
		out.SentAt = timestamppb.New(*m.SentAt)
	}
	if m.DeliveredAt != nil {
		out.DeliveredAt = timestamppb.New(*m.DeliveredAt)
	}
	if m.ReadAt != nil {
		out.ReadAt = timestamppb.New(*m.ReadAt)
	}
	if m.Media != nil {
		out.Media = &messagev1.MediaInfo{Mime: m.Media.Mime, Size: m.Media.Size, Filename: m.Media.Filename, Voice: m.Media.Voice, Url: links.of(m.PublicID)}
	}
	if m.Location != nil {
		out.Location = &messagev1.Location{Lat: m.Location.Lat, Lng: m.Location.Lng, Name: m.Location.Name, Address: m.Location.Address}
	}
	return out
}
