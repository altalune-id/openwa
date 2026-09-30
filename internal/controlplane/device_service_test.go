package controlplane_test

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	devicev1 "altalune.id/openwa/gen/go/device/v1"
	devicev1connect "altalune.id/openwa/gen/go/device/v1/devicev1connect"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/platform/authn"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/project"
	"altalune.id/openwa/internal/testutil/fakes"
	"altalune.id/openwa/internal/whatsapp"
)

type deviceCP struct {
	h       *harness
	orgID   uuid.UUID
	project *project.Project
}

func newDeviceCP(t *testing.T) *deviceCP {
	t.Helper()
	orgID := uuid.New()
	proj, err := project.New(orgID, "p1", "Project 1")
	require.NoError(t, err)
	h := newHarness(t, session.Principal{UserID: uuid.New(), Email: "a@b", ActiveOrgID: orgID, ActiveProjectID: proj.ID})
	require.NoError(t, h.projs.Save(context.Background(), proj))
	return &deviceCP{h: h, orgID: orgID, project: proj}
}

func newKeyDeviceCP(t *testing.T) (*deviceCP, string) {
	t.Helper()
	f := newDeviceCP(t)
	plain := f.h.mintKey(f.orgID, f.project.ID, []string{authn.ScopeDevicesRead, authn.ScopeDevicesWrite}, nil)
	return f, plain
}

func devKeyReq[Req any](plain string, req *Req) *connect.Request[Req] {
	r := connect.NewRequest(req)
	withKey(plain)(r.Header())
	return r
}

func (f *deviceCP) seed(t *testing.T, name string) *device.Device {
	t.Helper()
	d, err := device.New(f.orgID, f.project.ID, fakes.DevicePublicID(), name)
	require.NoError(t, err)
	f.h.devices.Seed(d)
	return d
}

func devReq[Req any](req *Req) *connect.Request[Req] {
	r := connect.NewRequest(req)
	withBearer(r.Header())
	return r
}

func TestDevice_CreateThenListInTheActiveProject(t *testing.T) {
	f := newDeviceCP(t)
	c := f.h.deviceClient()
	created, err := c.CreateDevice(context.Background(), devReq(&devicev1.CreateDeviceRequest{Name: "Sales"}))
	require.NoError(t, err)
	require.Equal(t, "Sales", created.Msg.GetDevice().GetName())
	require.Equal(t, "unlinked", created.Msg.GetDevice().GetState())
	require.Equal(t, "mention", created.Msg.GetDevice().GetRules().GetGroupMode())

	list, err := c.ListDevices(context.Background(), devReq(&devicev1.ListDevicesRequest{}))
	require.NoError(t, err)
	require.Len(t, list.Msg.GetDevices(), 1)

	_, err = c.CreateDevice(context.Background(), devReq(&devicev1.CreateDeviceRequest{Name: "sales"}))
	require.Equal(t, connect.CodeAlreadyExists, connectCode(err))
}

func TestDevice_GetCarriesTheStatusAndThePendingLink(t *testing.T) {
	f := newDeviceCP(t)
	d := f.seed(t, "Sales")
	f.h.sessions.Statuses[d.ID] = device.SessionStatus{State: device.SessionConnected, Phone: "628111", Live: true}
	got, err := f.h.deviceClient().GetDevice(context.Background(), devReq(&devicev1.GetDeviceRequest{DeviceId: d.PublicID}))
	require.NoError(t, err)
	msg := got.Msg.GetDevice()
	require.Equal(t, "connected", msg.GetState())
	require.Equal(t, "628111", msg.GetPhone())
	require.True(t, msg.GetLive())
	require.Equal(t, "pending", msg.GetLink().GetOutcome())
	require.NotEmpty(t, msg.GetLink().GetPng())

	list, err := f.h.deviceClient().ListDevices(context.Background(), devReq(&devicev1.ListDevicesRequest{}))
	require.NoError(t, err)
	require.Len(t, list.Msg.GetDevices(), 1)
	require.Nil(t, list.Msg.GetDevices()[0].GetLink(), "ListDevices never carries a pairing attempt or its PNG")
}

func TestDevice_UpdateIsConditional(t *testing.T) {
	f := newDeviceCP(t)
	d := f.seed(t, "Sales")
	c := f.h.deviceClient()
	resp, err := c.UpdateDevice(context.Background(), devReq(&devicev1.UpdateDeviceRequest{
		DeviceId: d.PublicID,
		Name:     proto.String("Support"),
		Rules:    &devicev1.Rules{GroupMode: "open", AllowedSenders: []string{"628111111111"}, IgnoreFromMe: true},
		Version:  1,
	}))
	require.NoError(t, err)
	require.Equal(t, "Support", resp.Msg.GetDevice().GetName())
	require.Equal(t, int32(2), resp.Msg.GetDevice().GetVersion())

	_, err = c.UpdateDevice(context.Background(), devReq(&devicev1.UpdateDeviceRequest{DeviceId: d.PublicID, Name: proto.String("X"), Version: 1}))
	require.Equal(t, connect.CodeAborted, connectCode(err))
	_, err = c.UpdateDevice(context.Background(), devReq(&devicev1.UpdateDeviceRequest{DeviceId: d.PublicID, Rules: &devicev1.Rules{GroupMode: "loud"}}))
	require.Equal(t, connect.CodeInvalidArgument, connectCode(err))
}

func TestDevice_LinkVerbsResolveTheOnlyDevice(t *testing.T) {
	f := newDeviceCP(t)
	c := f.h.deviceClient()

	_, err := c.StartLink(context.Background(), devReq(&devicev1.StartLinkRequest{}))
	require.Equal(t, connect.CodeInvalidArgument, connectCode(err), "no device to resolve")

	only := f.seed(t, "Sales")
	resp, err := c.StartLink(context.Background(), devReq(&devicev1.StartLinkRequest{}))
	require.NoError(t, err)
	require.Equal(t, only.PublicID, resp.Msg.GetDeviceId())
	require.Equal(t, "pending", resp.Msg.GetLink().GetOutcome())

	phone, err := c.LinkWithPhone(context.Background(), devReq(&devicev1.LinkWithPhoneRequest{Phone: "+62 812"}))
	require.NoError(t, err)
	require.Equal(t, "ABCD-EFGH", phone.Msg.GetLink().GetPairingCode())

	st, err := c.GetLinkState(context.Background(), devReq(&devicev1.GetLinkStateRequest{}))
	require.NoError(t, err)
	require.Equal(t, only.PublicID, st.Msg.GetDeviceId())

	f.seed(t, "Support")
	_, err = c.Unlink(context.Background(), devReq(&devicev1.UnlinkRequest{}))
	require.Equal(t, connect.CodeInvalidArgument, connectCode(err))
	require.True(t, strings.Contains(err.Error(), "Sales") && strings.Contains(err.Error(), "Support"),
		"the refusal lists the candidates: %v", err)

	_, err = c.Unlink(context.Background(), devReq(&devicev1.UnlinkRequest{DeviceId: only.PublicID}))
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{only.ID}, f.h.sessions.Unlinked)
}

func TestDevice_AnotherOrgsDeviceIsNotFound(t *testing.T) {
	f := newDeviceCP(t)
	other, err := device.New(uuid.New(), uuid.New(), fakes.DevicePublicID(), "Elsewhere")
	require.NoError(t, err)
	f.h.devices.Seed(other)
	_, err = f.h.deviceClient().GetDevice(context.Background(), devReq(&devicev1.GetDeviceRequest{DeviceId: other.PublicID}))
	require.Equal(t, connect.CodeNotFound, connectCode(err))
	_, err = f.h.deviceClient().StartLink(context.Background(), devReq(&devicev1.StartLinkRequest{DeviceId: other.PublicID}))
	require.Equal(t, connect.CodeNotFound, connectCode(err))
	require.Empty(t, f.h.sessions.Linked)
}

// SECURITY: a device-bound key reaches only its device — served on verbs that name that device, refused on project-wide verbs — through Principal.ReachesResource/ReachesWholeProject; no extra DB read.
func TestDevice_ADeviceBoundKeyReachesOnlyItsDevice(t *testing.T) {
	f := newDeviceCP(t)
	d := f.seed(t, "Sales")
	other := f.seed(t, "Support")
	ctx := context.Background()
	c := f.h.deviceClient()
	bound := f.h.mintKey(f.orgID, f.project.ID, []string{authn.ScopeDevicesRead, authn.ScopeDevicesWrite}, []uuid.UUID{d.ID})

	_, err := c.GetDevice(ctx, devKeyReq(bound, &devicev1.GetDeviceRequest{DeviceId: d.PublicID}))
	require.NoError(t, err, "the bound key reaches its own device")

	_, err = c.GetDevice(ctx, devKeyReq(bound, &devicev1.GetDeviceRequest{DeviceId: other.PublicID}))
	require.Equal(t, connect.CodeNotFound, connectCode(err), "another device reads as absent")

	_, err = c.Unlink(ctx, devKeyReq(bound, &devicev1.UnlinkRequest{DeviceId: d.PublicID}))
	require.NoError(t, err, "Unlink names the bound device")

	projectWide := map[string]func() error{
		"ListDevices": func() error {
			_, err := c.ListDevices(ctx, devKeyReq(bound, &devicev1.ListDevicesRequest{}))
			return err
		},
		"CreateDevice": func() error {
			_, err := c.CreateDevice(ctx, devKeyReq(bound, &devicev1.CreateDeviceRequest{Name: "Ops"}))
			return err
		},
		"StartLink": func() error {
			_, err := c.StartLink(ctx, devKeyReq(bound, &devicev1.StartLinkRequest{}))
			return err
		},
	}
	for name, call := range projectWide {
		err := call()
		require.Equal(t, connect.CodePermissionDenied, connectCode(err), "%s enters through ReachesWholeProject: %v", name, err)
	}
}

func TestDevice_AKeyCannotReachASiblingProjectsDevice(t *testing.T) {
	f, plain := newKeyDeviceCP(t)
	sibling, err := project.New(f.orgID, "p2", "Project 2")
	require.NoError(t, err)
	require.NoError(t, f.h.projs.Save(context.Background(), sibling))
	d, err := device.New(f.orgID, sibling.ID, fakes.DevicePublicID(), "Elsewhere")
	require.NoError(t, err)
	f.h.devices.Seed(d)
	c := f.h.deviceClient()

	_, err = c.GetDevice(context.Background(), devKeyReq(plain, &devicev1.GetDeviceRequest{DeviceId: d.PublicID}))
	require.Equal(t, connect.CodeNotFound, connectCode(err), "device_id of a sibling project reads as absent")
	_, err = c.ListDevices(context.Background(), devKeyReq(plain, &devicev1.ListDevicesRequest{ProjectId: sibling.ID.String()}))
	require.Equal(t, connect.CodePermissionDenied, connectCode(err), "project_id of a sibling project is refused")
}

func TestDevice_TheInternalUUIDIsNeverAnID(t *testing.T) {
	f := newDeviceCP(t)
	d := f.seed(t, "Sales")
	c := f.h.deviceClient()

	got, err := c.GetDevice(context.Background(), devReq(&devicev1.GetDeviceRequest{DeviceId: d.PublicID}))
	require.NoError(t, err)
	require.Equal(t, d.PublicID, got.Msg.GetDevice().GetId())
	require.NotContains(t, got.Msg.String(), d.ID.String(), "the internal UUID never leaves the server")

	_, err = c.GetDevice(context.Background(), devReq(&devicev1.GetDeviceRequest{DeviceId: d.ID.String()}))
	require.Equal(t, connect.CodeNotFound, connectCode(err), "a UUID is not a device id on any surface")

	link, err := c.StartLink(context.Background(), devReq(&devicev1.StartLinkRequest{DeviceId: d.PublicID}))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(link.Msg.GetLink().GetId(), "lnk_"), "got %q", link.Msg.GetLink().GetId())
}

func requireNoUUIDInError(t *testing.T, err error, id uuid.UUID) {
	t.Helper()
	require.Error(t, err)
	require.NotContains(t, err.Error(), id.String())
	var ce *connect.Error
	require.ErrorAs(t, err, &ce)
	for _, det := range ce.Details() {
		require.NotContains(t, string(det.Bytes()), id.String(), "error detail %s names the internal UUID", det.Type())
	}
}

// SECURITY: error details are part of the wire; a device UUID must not travel in them either.
func TestDevice_ErrorDetailsNeverCarryTheDeviceUUID(t *testing.T) {
	f := newDeviceCP(t)
	d := f.seed(t, "Sales")
	c := f.h.deviceClient()

	f.h.sessions.LinkErr = &whatsapp.AlreadyLinkedError{ID: d.ID.String()}
	_, err := c.StartLink(context.Background(), devReq(&devicev1.StartLinkRequest{DeviceId: d.PublicID}))
	require.Equal(t, connect.CodeFailedPrecondition, connectCode(err))
	requireNoUUIDInError(t, err, d.ID)
	var ce *connect.Error
	require.ErrorAs(t, err, &ce)
	require.NotEmpty(t, ce.Details())
	require.Contains(t, string(ce.Details()[0].Bytes()), d.PublicID, "the error detail names the device by the id the caller used")
	f.h.sessions.LinkErr = nil

	f.h.devices.ByIDFn = func(_ context.Context, id uuid.UUID) (*device.Device, error) {
		return nil, &device.NotFoundError{ID: id.String()}
	}
	_, err = c.GetDevice(context.Background(), devReq(&devicev1.GetDeviceRequest{DeviceId: d.PublicID}))
	require.Equal(t, connect.CodeNotFound, connectCode(err), "the device vanished between Locate and Get")
	requireNoUUIDInError(t, err, d.ID)
}

func TestDevice_Delete(t *testing.T) {
	f := newDeviceCP(t)
	d := f.seed(t, "Sales")
	_, err := f.h.deviceClient().DeleteDevice(context.Background(), devReq(&devicev1.DeleteDeviceRequest{DeviceId: d.PublicID}))
	require.NoError(t, err)
	require.Zero(t, f.h.devices.Len())
	require.Equal(t, []uuid.UUID{d.ID}, f.h.sessions.Forgot)
}

func TestDevice_ADeviceBoundKeyIsScopedOnEveryDeviceIDVerb(t *testing.T) {
	ctx := context.Background()
	verbs := []struct {
		name string
		call func(c devicev1connect.DeviceServiceClient, key, id string) error
	}{
		{"GetDevice", func(c devicev1connect.DeviceServiceClient, key, id string) error {
			_, err := c.GetDevice(ctx, devKeyReq(key, &devicev1.GetDeviceRequest{DeviceId: id}))
			return err
		}},
		{"UpdateDevice", func(c devicev1connect.DeviceServiceClient, key, id string) error {
			_, err := c.UpdateDevice(ctx, devKeyReq(key, &devicev1.UpdateDeviceRequest{DeviceId: id, Name: proto.String("Renamed")}))
			return err
		}},
		{"DeleteDevice", func(c devicev1connect.DeviceServiceClient, key, id string) error {
			_, err := c.DeleteDevice(ctx, devKeyReq(key, &devicev1.DeleteDeviceRequest{DeviceId: id}))
			return err
		}},
		{"StartLink", func(c devicev1connect.DeviceServiceClient, key, id string) error {
			_, err := c.StartLink(ctx, devKeyReq(key, &devicev1.StartLinkRequest{DeviceId: id}))
			return err
		}},
		{"LinkWithPhone", func(c devicev1connect.DeviceServiceClient, key, id string) error {
			_, err := c.LinkWithPhone(ctx, devKeyReq(key, &devicev1.LinkWithPhoneRequest{DeviceId: id, Phone: "+62 812"}))
			return err
		}},
		{"GetLinkState", func(c devicev1connect.DeviceServiceClient, key, id string) error {
			_, err := c.GetLinkState(ctx, devKeyReq(key, &devicev1.GetLinkStateRequest{DeviceId: id}))
			return err
		}},
		{"Unlink", func(c devicev1connect.DeviceServiceClient, key, id string) error {
			_, err := c.Unlink(ctx, devKeyReq(key, &devicev1.UnlinkRequest{DeviceId: id}))
			return err
		}},
	}
	for _, v := range verbs {
		t.Run(v.name, func(t *testing.T) {
			f := newDeviceCP(t)
			own := f.seed(t, "Sales")
			sibling := f.seed(t, "Support")
			c := f.h.deviceClient()
			bound := f.h.mintKey(f.orgID, f.project.ID, []string{authn.ScopeDevicesRead, authn.ScopeDevicesWrite}, []uuid.UUID{own.ID})

			err := v.call(c, bound, sibling.PublicID)
			require.Contains(t, []connect.Code{connect.CodeNotFound, connect.CodePermissionDenied}, connectCode(err), "a sibling device is out of reach: %v", err)
			require.Equal(t, 2, f.h.devices.Len(), "the sibling attempt changed nothing")
			require.Empty(t, f.h.sessions.Linked)
			require.Empty(t, f.h.sessions.Unlinked)

			require.NoError(t, v.call(c, bound, own.PublicID), "the bound key is served on its own device")
		})
	}
}
