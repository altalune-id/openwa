package controlplane

import (
	"context"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	devicev1 "altalune.id/openwa/gen/go/device/v1"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/project"
)

// DeviceService implements device.v1.DeviceService.
type DeviceService struct {
	devices  *device.Service
	projects *project.Service
}

// NewDeviceService binds the handler to its collaborators.
func NewDeviceService(devices *device.Service, projects *project.Service) *DeviceService {
	return &DeviceService{devices: devices, projects: projects}
}

// ListDevices returns the devices of the request's project, or of the principal's active project.
func (s *DeviceService) ListDevices(ctx context.Context, req *connect.Request[devicev1.ListDevicesRequest]) (*connect.Response[devicev1.ListDevicesResponse], error) {
	tctx, err := scopeToActiveProject(ctx, s.projects, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	views, err := s.devices.List(tctx)
	if err != nil {
		return nil, err
	}
	out := make([]*devicev1.Device, 0, len(views))
	for _, v := range views {
		out = append(out, deviceToProto(v, device.LinkState{Outcome: device.LinkNone}))
	}
	return connect.NewResponse(&devicev1.ListDevicesResponse{Devices: out}), nil
}

// GetDevice returns one device with its status and any pending pairing attempt. SECURITY: see GetLinkState for why devices:read may see the pairing payload.
func (s *DeviceService) GetDevice(ctx context.Context, req *connect.Request[devicev1.GetDeviceRequest]) (*connect.Response[devicev1.GetDeviceResponse], error) {
	tctx, v, err := s.scopeToDevice(ctx, req.Msg.GetDeviceId())
	if err != nil {
		return nil, err
	}
	st, err := s.devices.LinkState(tctx, v.Device.ID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&devicev1.GetDeviceResponse{Device: deviceToProto(v, st)}), nil
}

// CreateDevice adds a device to the request's project.
func (s *DeviceService) CreateDevice(ctx context.Context, req *connect.Request[devicev1.CreateDeviceRequest]) (*connect.Response[devicev1.CreateDeviceResponse], error) {
	tctx, err := scopeToActiveProject(ctx, s.projects, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	d, err := s.devices.Create(tctx, req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	v := device.DeviceView{Device: d, Status: device.SessionStatus{State: device.SessionUnlinked}}
	return connect.NewResponse(&devicev1.CreateDeviceResponse{Device: deviceToProto(v, device.LinkState{Outcome: device.LinkNone})}), nil
}

// UpdateDevice changes the name and rules in one conditional write.
func (s *DeviceService) UpdateDevice(ctx context.Context, req *connect.Request[devicev1.UpdateDeviceRequest]) (*connect.Response[devicev1.UpdateDeviceResponse], error) {
	tctx, v, err := s.scopeToDevice(ctx, req.Msg.GetDeviceId())
	if err != nil {
		return nil, err
	}
	if req.Msg.GetVersion() < 0 {
		return nil, validationErr("version", "version must be the device version returned by the API, or 0 for an unconditional write")
	}
	var u device.Update
	if req.Msg.Name != nil {
		name := req.Msg.GetName()
		u.Name = &name
	}
	if req.Msg.GetRules() != nil {
		rules := rulesFromProto(req.Msg.GetRules())
		u.Rules = &rules
	}
	d, err := s.devices.Update(tctx, v.Device.ID, u, int(req.Msg.GetVersion()))
	if err != nil {
		return nil, err
	}
	v.Device = d
	return connect.NewResponse(&devicev1.UpdateDeviceResponse{Device: deviceToProto(v, device.LinkState{Outcome: device.LinkNone})}), nil
}

// DeleteDevice logs the account out and removes the device.
func (s *DeviceService) DeleteDevice(ctx context.Context, req *connect.Request[devicev1.DeleteDeviceRequest]) (*connect.Response[devicev1.DeleteDeviceResponse], error) {
	tctx, v, err := s.scopeToDevice(ctx, req.Msg.GetDeviceId())
	if err != nil {
		return nil, err
	}
	if err := s.devices.Delete(tctx, v.Device.ID); err != nil {
		return nil, err
	}
	return connect.NewResponse(&devicev1.DeleteDeviceResponse{}), nil
}

// StartLink starts a QR pairing attempt; an empty device_id resolves the project's only device.
func (s *DeviceService) StartLink(ctx context.Context, req *connect.Request[devicev1.StartLinkRequest]) (*connect.Response[devicev1.StartLinkResponse], error) {
	tctx, d, err := s.resolveDevice(ctx, req.Msg.GetProjectId(), req.Msg.GetDeviceId())
	if err != nil {
		return nil, err
	}
	st, err := s.devices.StartLink(tctx, d.ID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&devicev1.StartLinkResponse{DeviceId: d.PublicID, Link: linkToProto(st)}), nil
}

// GetLinkState returns the current pairing attempt. SECURITY: devices:read may see a QR or pairing code by design; it cannot start a pairing, so it only sees a link a devices:write credential began.
func (s *DeviceService) GetLinkState(ctx context.Context, req *connect.Request[devicev1.GetLinkStateRequest]) (*connect.Response[devicev1.GetLinkStateResponse], error) {
	tctx, d, err := s.resolveDevice(ctx, req.Msg.GetProjectId(), req.Msg.GetDeviceId())
	if err != nil {
		return nil, err
	}
	st, err := s.devices.LinkState(tctx, d.ID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&devicev1.GetLinkStateResponse{DeviceId: d.PublicID, Link: linkToProto(st)}), nil
}

// LinkWithPhone returns an 8-character pairing code for the device.
func (s *DeviceService) LinkWithPhone(ctx context.Context, req *connect.Request[devicev1.LinkWithPhoneRequest]) (*connect.Response[devicev1.LinkWithPhoneResponse], error) {
	tctx, d, err := s.resolveDevice(ctx, req.Msg.GetProjectId(), req.Msg.GetDeviceId())
	if err != nil {
		return nil, err
	}
	st, err := s.devices.LinkWithPhone(tctx, d.ID, req.Msg.GetPhone())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&devicev1.LinkWithPhoneResponse{DeviceId: d.PublicID, Link: linkToProto(st)}), nil
}

// Unlink logs the device's WhatsApp account out.
func (s *DeviceService) Unlink(ctx context.Context, req *connect.Request[devicev1.UnlinkRequest]) (*connect.Response[devicev1.UnlinkResponse], error) {
	tctx, d, err := s.resolveDevice(ctx, req.Msg.GetProjectId(), req.Msg.GetDeviceId())
	if err != nil {
		return nil, err
	}
	if err := s.devices.Unlink(tctx, d.ID); err != nil {
		return nil, err
	}
	return connect.NewResponse(&devicev1.UnlinkResponse{DeviceId: d.PublicID}), nil
}

// SECURITY: the device is located org-wide, then scopeToResource checks ReachesResource — a project-reaching key is served, a device-bound key only for its own device, and any other device (or project) reads as absent.
func (s *DeviceService) scopeToDevice(ctx context.Context, raw string) (context.Context, device.DeviceView, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, device.DeviceView{}, err
	}
	d, err := s.devices.Locate(tenant.Into(ctx, tenant.Context{OrgID: p.ActiveOrgID, UserID: p.UserID}), strings.TrimSpace(raw))
	if err != nil {
		return nil, device.DeviceView{}, err
	}
	tctx, err := scopeToResource(ctx, d.OrgID, d.ProjectID, d.ID, func() error {
		return &device.NotFoundError{ID: d.PublicID}
	})
	if err != nil {
		return nil, device.DeviceView{}, err
	}
	v, err := s.devices.Get(tctx, d.ID)
	if err != nil {
		return nil, device.DeviceView{}, err
	}
	return tctx, v, nil
}

func (s *DeviceService) resolveDevice(ctx context.Context, projectRaw, deviceRaw string) (context.Context, *device.Device, error) {
	if strings.TrimSpace(deviceRaw) != "" {
		tctx, v, err := s.scopeToDevice(ctx, deviceRaw)
		if err != nil {
			return nil, nil, err
		}
		return tctx, v.Device, nil
	}
	tctx, err := scopeToActiveProject(ctx, s.projects, projectRaw)
	if err != nil {
		return nil, nil, err
	}
	views, err := s.devices.List(tctx)
	if err != nil {
		return nil, nil, err
	}
	if len(views) == 1 {
		return tctx, views[0].Device, nil
	}
	names := make([]string, 0, len(views))
	for _, v := range views {
		names = append(names, v.Device.Name+" ("+v.Device.PublicID+")")
	}
	return nil, nil, &DeviceUnresolvedError{Candidates: names}
}

func deviceToProto(v device.DeviceView, st device.LinkState) *devicev1.Device {
	d := v.Device
	msg := &devicev1.Device{
		Id:        d.PublicID,
		ProjectId: d.ProjectID.String(),
		Name:      d.Name,
		Rules:     rulesToProto(d.Rules),
		Version:   versionToProto(d.Version),
		State:     string(v.Status.State),
		Phone:     v.Status.Phone,
		PushName:  v.Status.PushName,
		Reason:    v.Status.Reason,
		Live:      v.Status.Live,
		CreatedAt: timestamppb.New(d.CreatedAt),
		UpdatedAt: timestamppb.New(d.UpdatedAt),
	}
	if msg.State == "" {
		msg.State = string(device.SessionUnlinked)
	}
	if v.Status.LastSeen != nil {
		msg.LastSeenAt = timestamppb.New(*v.Status.LastSeen)
	}
	if st.Outcome != device.LinkNone && st.Outcome != "" {
		msg.Link = linkToProto(st)
	}
	return msg
}

func linkToProto(st device.LinkState) *devicev1.LinkState {
	msg := &devicev1.LinkState{
		Id:          st.ID,
		Method:      string(st.Method),
		Outcome:     string(st.Outcome),
		Qr:          st.QR,
		Png:         st.PNG,
		PairingCode: st.PairingCode,
		Error:       st.Error,
	}
	if !st.ExpiresAt.IsZero() {
		msg.ExpiresAt = timestamppb.New(st.ExpiresAt)
	}
	if !st.StartedAt.IsZero() {
		msg.StartedAt = timestamppb.New(st.StartedAt)
	}
	return msg
}

func rulesToProto(r device.Rules) *devicev1.Rules {
	return &devicev1.Rules{
		GroupMode:      string(r.GroupMode),
		AllowedSenders: append([]string{}, r.AllowedSenders...),
		AllowedGroups:  append([]string{}, r.AllowedGroups...),
		TriggerPrefix:  r.TriggerPrefix,
		IgnoreFromMe:   r.IgnoreFromMe,
	}
}

func rulesFromProto(r *devicev1.Rules) device.Rules {
	return device.Rules{
		GroupMode:      device.GroupMode(r.GetGroupMode()),
		AllowedSenders: r.GetAllowedSenders(),
		AllowedGroups:  r.GetAllowedGroups(),
		TriggerPrefix:  r.GetTriggerPrefix(),
		IgnoreFromMe:   r.GetIgnoreFromMe(),
	}
}
