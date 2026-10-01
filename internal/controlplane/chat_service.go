package controlplane

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	chatv1 "altalune.id/openwa/gen/go/chat/v1"
	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/project"
)

// ChatService implements chat.v1.ChatService.
type ChatService struct {
	chats    *chat.Service
	messages *message.Service
	scope    messagingScope
}

// NewChatService binds the handler to its collaborators.
func NewChatService(chats *chat.Service, messages *message.Service, devices *device.Service, projects *project.Service) *ChatService {
	return &ChatService{chats: chats, messages: messages, scope: newMessagingScope(projects, devices)}
}

func chatToProto(c *chat.Chat, devicePub string) *chatv1.Chat {
	out := &chatv1.Chat{
		Id: c.PublicID, DeviceId: devicePub, Jid: c.JID, Kind: string(c.Kind), Name: c.Name,
		LastMessagePreview: c.LastMessagePreview, UnreadCount: int32(c.UnreadCount), Archived: c.Archived, //nolint:gosec // G115: unread counts stay far below int32 max.
	}
	if c.LastMessageAt != nil {
		out.LastMessageAt = timestamppb.New(*c.LastMessageAt)
	}
	return out
}

func groupToProto(g chat.GroupInfo, chatPublicID string, joined bool) *chatv1.Group {
	return &chatv1.Group{
		Jid: g.JID, Name: g.Name, Topic: g.Topic, Participants: int32(g.Participants), //nolint:gosec // G115: group sizes are bounded by WhatsApp.
		Announce: g.Announce, Locked: g.Locked, InviteLink: g.InviteLink, ChatId: chatPublicID, Joined: joined,
	}
}

// NOTE: a page costs one device public-id read, whatever its length.
func (s *ChatService) protos(ctx context.Context, items []*chat.Chat) ([]*chatv1.Chat, error) {
	ids := make([]uuid.UUID, 0, len(items))
	for _, c := range items {
		ids = append(ids, c.DeviceID)
	}
	pubs, err := s.scope.devicePublicIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]*chatv1.Chat, 0, len(items))
	for _, c := range items {
		out = append(out, chatToProto(c, pubs[c.DeviceID]))
	}
	return out, nil
}

func (s *ChatService) one(ctx context.Context, c *chat.Chat) (*chatv1.Chat, error) {
	out, err := s.protos(ctx, []*chat.Chat{c})
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

// List pages the project's chats.
func (s *ChatService) List(ctx context.Context, req *connect.Request[chatv1.ListRequest]) (*connect.Response[chatv1.ListResponse], error) {
	tctx, err := s.scope.project(ctx, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	opts := chat.ListOpts{Search: req.Msg.GetQ(), Cursor: req.Msg.GetCursor(), Limit: int(req.Msg.GetLimit())}
	if opts.DeviceID, err = s.scope.optionalDevice(tctx, req.Msg.GetDeviceId()); err != nil {
		return nil, err
	}
	if k := req.Msg.GetKind(); k != "" {
		if k != string(chat.KindDM) && k != string(chat.KindGroup) {
			return nil, validationErr("kind", "kind is dm or group")
		}
		kind := chat.Kind(k)
		opts.Kind = &kind
	}
	items, next, err := s.chats.List(tctx, opts)
	if err != nil {
		return nil, err
	}
	protos, err := s.protos(tctx, items)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&chatv1.ListResponse{Chats: protos, NextCursor: next}), nil
}

// Get returns one chat.
func (s *ChatService) Get(ctx context.Context, req *connect.Request[chatv1.GetRequest]) (*connect.Response[chatv1.GetResponse], error) {
	tctx, c, err := s.load(ctx, req.Msg.GetProjectId(), req.Msg.GetChatId())
	if err != nil {
		return nil, err
	}
	out, err := s.one(tctx, c)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&chatv1.GetResponse{Chat: out}), nil
}

// MarkRead sends read receipts for the chat's unread messages.
func (s *ChatService) MarkRead(ctx context.Context, req *connect.Request[chatv1.MarkReadRequest]) (*connect.Response[chatv1.MarkReadResponse], error) {
	tctx, c, err := s.load(ctx, req.Msg.GetProjectId(), req.Msg.GetChatId())
	if err != nil {
		return nil, err
	}
	if err := s.messages.MarkRead(tctx, c.ID); err != nil {
		return nil, err
	}
	return connect.NewResponse(&chatv1.MarkReadResponse{}), nil
}

// ListGroups returns a device's groups.
func (s *ChatService) ListGroups(ctx context.Context, req *connect.Request[chatv1.ListGroupsRequest]) (*connect.Response[chatv1.ListGroupsResponse], error) {
	tctx, err := s.scope.project(ctx, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	deviceID, err := s.scope.device(tctx, req.Msg.GetDeviceId())
	if err != nil {
		return nil, err
	}
	groups, err := s.chats.ListGroups(tctx, deviceID)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(groups))
	for _, g := range groups {
		if g.ChatID != uuid.Nil {
			ids = append(ids, g.ChatID)
		}
	}
	pubs := map[uuid.UUID]string{}
	if len(ids) > 0 {
		if pubs, err = s.chats.PublicIDs(tctx, ids); err != nil {
			return nil, err
		}
	}
	out := &chatv1.ListGroupsResponse{Groups: make([]*chatv1.Group, 0, len(groups))}
	for _, g := range groups {
		out.Groups = append(out.Groups, groupToProto(g.GroupInfo, pubs[g.ChatID], g.Joined))
	}
	return connect.NewResponse(out), nil
}

// GroupInfo returns a group chat's live details. NOTE: in v1 joined is derived from the chat's archived flag, not from live group membership.
func (s *ChatService) GroupInfo(ctx context.Context, req *connect.Request[chatv1.GroupInfoRequest]) (*connect.Response[chatv1.GroupInfoResponse], error) {
	tctx, c, err := s.load(ctx, req.Msg.GetProjectId(), req.Msg.GetChatId())
	if err != nil {
		return nil, err
	}
	g, err := s.chats.GroupInfo(tctx, c.ID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&chatv1.GroupInfoResponse{Group: groupToProto(g, c.PublicID, !c.Archived)}), nil
}

// JoinGroup joins by invite link.
func (s *ChatService) JoinGroup(ctx context.Context, req *connect.Request[chatv1.JoinGroupRequest]) (*connect.Response[chatv1.JoinGroupResponse], error) {
	tctx, err := s.scope.project(ctx, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	deviceID, err := s.scope.device(tctx, req.Msg.GetDeviceId())
	if err != nil {
		return nil, err
	}
	if req.Msg.GetInviteLink() == "" {
		return nil, validationErr("invite_link", "invite_link is required")
	}
	c, err := s.chats.JoinGroup(tctx, deviceID, req.Msg.GetInviteLink())
	if err != nil {
		return nil, err
	}
	out, err := s.one(tctx, c)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&chatv1.JoinGroupResponse{Chat: out}), nil
}

// LeaveGroup leaves a group and archives its chat.
func (s *ChatService) LeaveGroup(ctx context.Context, req *connect.Request[chatv1.LeaveGroupRequest]) (*connect.Response[chatv1.LeaveGroupResponse], error) {
	tctx, c, err := s.load(ctx, req.Msg.GetProjectId(), req.Msg.GetChatId())
	if err != nil {
		return nil, err
	}
	left, err := s.chats.LeaveGroup(tctx, c.ID)
	if err != nil {
		return nil, err
	}
	out, err := s.one(tctx, left)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&chatv1.LeaveGroupResponse{Chat: out}), nil
}

// NOTE: Resolve is project-scoped and answers NotFound for a malformed id without a query.
func (s *ChatService) load(ctx context.Context, projectRaw, idRaw string) (context.Context, *chat.Chat, error) {
	tctx, err := s.scope.project(ctx, projectRaw)
	if err != nil {
		return nil, nil, err
	}
	c, err := s.chats.Resolve(tctx, idRaw)
	if err != nil {
		return nil, nil, err
	}
	return tctx, c, nil
}
