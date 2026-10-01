package controlplane

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	contactv1 "altalune.id/openwa/gen/go/contact/v1"
	"altalune.id/openwa/internal/contact"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/project"
)

// ContactService implements contact.v1.ContactService.
type ContactService struct {
	contacts *contact.Service
	scope    messagingScope
}

// NewContactService binds the handler to its collaborators.
func NewContactService(contacts *contact.Service, devices *device.Service, projects *project.Service) *ContactService {
	return &ContactService{contacts: contacts, scope: newMessagingScope(projects, devices)}
}

func contactToProto(c *contact.Contact, devicePub string) *contactv1.Contact {
	return &contactv1.Contact{
		DeviceId: devicePub, Jid: c.JID, Phone: c.Phone, Name: c.Name,
		PushName: c.PushName, BusinessName: c.BusinessName, DisplayName: c.DisplayName(), UpdatedAt: timestamppb.New(c.UpdatedAt),
	}
}

// List pages the project's contacts; a page costs one device public-id read.
func (s *ContactService) List(ctx context.Context, req *connect.Request[contactv1.ListRequest]) (*connect.Response[contactv1.ListResponse], error) {
	tctx, err := s.scope.project(ctx, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	opts := contact.ListOpts{Search: req.Msg.GetQ(), Cursor: req.Msg.GetCursor(), Limit: int(req.Msg.GetLimit())}
	if opts.DeviceID, err = s.scope.optionalDevice(tctx, req.Msg.GetDeviceId()); err != nil {
		return nil, err
	}
	items, next, err := s.contacts.List(tctx, opts)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(items))
	for _, c := range items {
		ids = append(ids, c.DeviceID)
	}
	pubs, err := s.scope.devicePublicIDs(tctx, ids)
	if err != nil {
		return nil, err
	}
	out := &contactv1.ListResponse{Contacts: make([]*contactv1.Contact, 0, len(items)), NextCursor: next}
	for _, c := range items {
		out.Contacts = append(out.Contacts, contactToProto(c, pubs[c.DeviceID]))
	}
	return connect.NewResponse(out), nil
}

// Get returns the contact jid names on a device.
func (s *ContactService) Get(ctx context.Context, req *connect.Request[contactv1.GetRequest]) (*connect.Response[contactv1.GetResponse], error) {
	tctx, err := s.scope.project(ctx, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	deviceID, err := s.scope.device(tctx, req.Msg.GetDeviceId())
	if err != nil {
		return nil, err
	}
	if req.Msg.GetJid() == "" {
		return nil, validationErr("jid", "jid is required")
	}
	c, err := s.contacts.Get(tctx, deviceID, req.Msg.GetJid())
	if err != nil {
		return nil, err
	}
	pub, err := s.scope.devicePublicIDs(tctx, []uuid.UUID{c.DeviceID})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&contactv1.GetResponse{Contact: contactToProto(c, pub[c.DeviceID])}), nil
}
