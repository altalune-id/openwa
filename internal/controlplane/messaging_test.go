package controlplane_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	chatv1 "altalune.id/openwa/gen/go/chat/v1"
	"altalune.id/openwa/gen/go/chat/v1/chatv1connect"
	contactv1 "altalune.id/openwa/gen/go/contact/v1"
	"altalune.id/openwa/gen/go/contact/v1/contactv1connect"
	messagev1 "altalune.id/openwa/gen/go/message/v1"
	"altalune.id/openwa/gen/go/message/v1/messagev1connect"
	"altalune.id/openwa/internal/apikey"
	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/contact"
	"altalune.id/openwa/internal/controlplane"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/org"
	"altalune.id/openwa/internal/platform"
	"altalune.id/openwa/internal/platform/authn"
	"altalune.id/openwa/internal/platform/capabilities"
	"altalune.id/openwa/internal/platform/session"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/internal/platform/tokens"
	"altalune.id/openwa/internal/project"
	"altalune.id/openwa/internal/testutil/fakes"
)

type msgHarness struct {
	server   *httptest.Server
	tc       tenant.Context
	sibling  *device.Device
	devices  []*device.Device
	groups   *fakes.Groups
	store    *fakes.Message
	keyPlain string
	other    uuid.UUID
}

type cpChats struct{ svc *chat.Service }

func (p cpChats) EnsureForJID(ctx context.Context, d uuid.UUID, jid, lid, kind, name string) (message.ChatRef, error) {
	c, err := p.svc.EnsureForJID(ctx, d, jid, lid, chat.Kind(kind), name)
	if err != nil {
		return message.ChatRef{}, err
	}
	return message.ChatRef{ID: c.ID, PublicID: c.PublicID, DeviceID: c.DeviceID, JID: c.JID, Kind: string(c.Kind)}, nil
}

func (p cpChats) Get(ctx context.Context, id uuid.UUID) (message.ChatRef, error) {
	c, err := p.svc.Get(ctx, id)
	if err != nil {
		return message.ChatRef{}, err
	}
	return message.ChatRef{ID: c.ID, PublicID: c.PublicID, DeviceID: c.DeviceID, JID: c.JID, Kind: string(c.Kind)}, nil
}

func (p cpChats) Touch(ctx context.Context, id uuid.UUID, at time.Time, preview string, inbound bool) error {
	return p.svc.Touch(ctx, id, at, preview, inbound)
}

func (p cpChats) MarkRead(ctx context.Context, id uuid.UUID) error { return p.svc.MarkRead(ctx, id) }

func (p cpChats) Repair(ctx context.Context, id uuid.UUID, last *time.Time, preview string, unread int) error {
	return p.svc.Repair(ctx, id, last, preview, unread)
}

func newMsgHarness(t *testing.T, deviceNames ...string) *msgHarness {
	t.Helper()
	return newMsgHarnessAs(t, nil, deviceNames...)
}

type apiKeyAs struct{ bound bool }

func newMsgHarnessAs(t *testing.T, key *apiKeyAs, deviceNames ...string) *msgHarness {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := apperror.NewReporter(log, false)
	orgID := uuid.New()
	projs := fakes.NewProject()
	proj, err := project.New(orgID, "p1", "Project 1")
	require.NoError(t, err)
	require.NoError(t, projs.Save(context.Background(), proj))
	otherProj, err := project.New(orgID, "p2", "Project 2")
	require.NoError(t, err)
	require.NoError(t, projs.Save(context.Background(), otherProj))
	tc := tenant.Context{OrgID: orgID, ProjectID: proj.ID}
	ctx := tenant.Into(context.Background(), tc)

	devStore := fakes.NewDevice()
	refs := map[uuid.UUID]message.DeviceRef{}
	var devices []*device.Device
	for _, n := range deviceNames {
		d, err := device.New(orgID, proj.ID, fakes.DevicePublicID(), n)
		require.NoError(t, err)
		require.NoError(t, devStore.Save(ctx, d, 0))
		refs[d.ID] = message.DeviceRef{ID: d.ID, PublicID: d.PublicID, Name: n, Linked: true}
		devices = append(devices, d)
	}
	sibling, err := device.New(orgID, uuid.New(), fakes.DevicePublicID(), "sibling-01")
	require.NoError(t, err)
	devStore.Seed(sibling)
	groups := &fakes.Groups{JoinJID: "999@g.us"}
	chats := chat.NewService(fakes.NewChat(), log, reporter.Unexpected, fakes.UnitOfWork, groups)
	store := fakes.NewMessage()
	msgs := message.NewService(store, log, reporter.Unexpected, fakes.UnitOfWork, message.Deps{
		Devices: &fakes.MessageDevices{Refs: refs}, Chats: cpChats{svc: chats}, Contacts: &fakes.MessageContacts{},
		Transport: &fakes.Transport{}, Media: fakes.MediaStore{Transport: &fakes.Transport{}}, Fetcher: &fakes.MediaFetcher{},
		Waker: &fakes.Waker{}, Webhooks: &fakes.Webhooks{}, Tenants: &fakes.MessageTenants{Org: "acme", Project: "p1"},
	}, message.Options{BaseURL: "https://wa.example.com", MaxMediaBytes: 1 << 20, StaleAfter: time.Minute})
	contacts := contact.NewService(fakes.NewContact(), log, reporter.Unexpected)
	require.NoError(t, contacts.UpsertFromEngine(ctx, contact.ContactInput{DeviceID: devices[0].ID, JID: "628111@s.whatsapp.net", Phone: "628111", Name: "Budi"}))

	orgSvc := org.NewService(fakes.NewOrg(), capabilities.Capabilities{OrgCreation: true}, log, reporter.Unexpected)
	projectSvc := project.NewService(projs, log, reporter.Unexpected)
	p := session.Principal{UserID: uuid.New(), Email: "a@b", ActiveOrgID: orgID, ActiveProjectID: proj.ID}
	keyStore := fakes.NewAPIKey()
	var keyPlain string
	if key != nil {
		// SECURITY: mint a real key so its principal carries the reach fields (ProjectIDs, ResourceIDs) the reach helpers read; a hand-built SourceAPIKey principal reaches nothing under PR #36.
		scopes := []string{authn.ScopeMessagesRead, authn.ScopeMessagesWrite, authn.ScopeChatsRead, authn.ScopeChatsWrite, authn.ScopeContactsRead}
		var resourceIDs []uuid.UUID
		if key.bound {
			resourceIDs = []uuid.UUID{devices[0].ID}
		}
		k, plain, err := apikey.Scheme{}.Mint(orgID, proj.ID, "msg-test", scopes, resourceIDs, nil, time.Now().UTC())
		require.NoError(t, err)
		keyStore.Seed(k)
		keyPlain = plain
	}
	kernel := &platform.Kernel{Log: log, Reporter: reporter, Verifier: stubVerifier{principal: p}}
	deviceSvc := device.NewService(devStore, log, reporter.Unexpected, fakes.UnitOfWork, fakes.NewDeviceSessions())
	srv := controlplane.New(nil, kernel, nil, nil, orgSvc, projectSvc, nil, nil, nil, nil, nil, nil, deviceSvc)
	srv.Authn = authn.Chain{apikey.NewAuthenticator(keyStore, nil, apikey.Scheme{}, fakes.NewMembers()), tokens.NewAuthenticator(kernel.Verifier)}
	srv.KeyPrefix = apikey.DefaultPrefix
	srv.Messages, srv.Chats, srv.Contacts = msgs, chats, contacts
	ts := httptest.NewServer(srv.Handler(""))
	t.Cleanup(ts.Close)
	return &msgHarness{server: ts, tc: tc, sibling: sibling, devices: devices, groups: groups, store: store, keyPlain: keyPlain, other: otherProj.ID}
}

func bearer[T any](msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	withBearer(req.Header())
	return req
}

func keyed[T any](h *msgHarness, msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	withKey(h.keyPlain)(req.Header())
	return req
}

func TestMessageService_SendAutoResolvesTheOnlyDevice(t *testing.T) {
	h := newMsgHarness(t, "sales-01")
	c := messagev1connect.NewMessageServiceClient(http.DefaultClient, h.server.URL+"/api")
	resp, err := c.Send(context.Background(), bearer(&messagev1.SendRequest{To: "628111222333", Text: "halo"}))
	require.NoError(t, err)
	require.Equal(t, "queued", resp.Msg.GetMessage().GetStatus())
	require.Equal(t, h.devices[0].PublicID, resp.Msg.GetMessage().GetDeviceId())
	require.Regexp(t, `^msg_[A-Za-z0-9_-]{16}$`, resp.Msg.GetMessage().GetId(), "the RPC answers with public ids only")
	require.Regexp(t, `^cht_[A-Za-z0-9_-]{16}$`, resp.Msg.GetMessage().GetChatId())
}

func TestMessageService_SendWithTwoDevicesNamesTheCandidates(t *testing.T) {
	h := newMsgHarness(t, "sales-01", "support-02")
	c := messagev1connect.NewMessageServiceClient(http.DefaultClient, h.server.URL+"/api")
	_, err := c.Send(context.Background(), bearer(&messagev1.SendRequest{To: "628111222333", Text: "x"}))
	require.Equal(t, connect.CodeInvalidArgument, connectCode(err))
	require.ErrorContains(t, err, "several devices")
	require.ErrorContains(t, err, "sales-01 ("+h.devices[0].PublicID+")")
	require.ErrorContains(t, err, "support-02 ("+h.devices[1].PublicID+")")
	require.NotContains(t, err.Error(), h.devices[0].ID.String(), "SECURITY: error details name public ids, never UUIDs")
}

func TestMessageService_InlineMediaFollowsTheServiceLimitNotAFixedCap(t *testing.T) {
	h := newMsgHarness(t, "sales-01")
	c := messagev1connect.NewMessageServiceClient(http.DefaultClient, h.server.URL+"/api")
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 900<<10)...)
	_, err := c.Send(context.Background(), bearer(&messagev1.SendRequest{To: "628111222333", Media: &messagev1.MediaInput{Data: png, Mime: "image/png"}}))
	require.NoError(t, err, "the RPC has no 4 MiB rule of its own; the harness's 1 MiB service limit applies")
	_, err = c.Send(context.Background(), bearer(&messagev1.SendRequest{To: "628111222333", Media: &messagev1.MediaInput{Data: make([]byte, 1<<20+1), Mime: "image/png"}}))
	require.Equal(t, connect.CodeInvalidArgument, connectCode(err), "MSG004 from the service")
}

func TestControlPlane_RefusesABodyOverTheRPCLimit(t *testing.T) {
	h := newMsgHarness(t, "sales-01")
	c := messagev1connect.NewMessageServiceClient(http.DefaultClient, h.server.URL+"/api")
	_, err := c.Send(context.Background(), bearer(&messagev1.SendRequest{To: "628111222333", Media: &messagev1.MediaInput{Data: make([]byte, 9<<20), Mime: "image/png"}}))
	require.Equal(t, connect.CodeResourceExhausted, connectCode(err), "with no config the RPC limit is 8 MiB")
}

func TestMessageService_ListGetReactAndMarkRead(t *testing.T) {
	h := newMsgHarness(t, "sales-01")
	c := messagev1connect.NewMessageServiceClient(http.DefaultClient, h.server.URL+"/api")
	sent, err := c.Send(context.Background(), bearer(&messagev1.SendRequest{To: "628111222333", Text: "halo"}))
	require.NoError(t, err)
	chatID := sent.Msg.GetMessage().GetChatId()
	list, err := c.List(context.Background(), bearer(&messagev1.ListRequest{ChatId: chatID}))
	require.NoError(t, err)
	require.Len(t, list.Msg.GetMessages(), 1)
	got, err := c.Get(context.Background(), bearer(&messagev1.GetRequest{MessageId: sent.Msg.GetMessage().GetId()}))
	require.NoError(t, err)
	require.Equal(t, "halo", got.Msg.GetMessage().GetBody())
	_, err = c.React(context.Background(), bearer(&messagev1.ReactRequest{MessageId: sent.Msg.GetMessage().GetId(), Emoji: "👍"}))
	require.NoError(t, err)
	_, err = c.MarkRead(context.Background(), bearer(&messagev1.MarkReadRequest{ChatId: chatID}))
	require.NoError(t, err)
	since, err := c.List(context.Background(), bearer(&messagev1.ListRequest{ChatId: chatID, SinceId: sent.Msg.GetMessage().GetId()}))
	require.NoError(t, err)
	require.Len(t, since.Msg.GetMessages(), 1, "the reaction is newer than the sent message")
	for _, bad := range []string{uuid.NewString(), "msg_Unknown000000001", chatID} {
		_, err = c.Get(context.Background(), bearer(&messagev1.GetRequest{MessageId: bad}))
		require.Equal(t, connect.CodeNotFound, connectCode(err), bad)
	}
}

// SECURITY: S2 group and send verbs never reach a device of a sibling project in the same org.
func TestMessagingRPCsRefuseASiblingProjectsDevice(t *testing.T) {
	h := newMsgHarness(t, "sales-01")
	foreign := h.sibling.PublicID
	c := chatv1connect.NewChatServiceClient(http.DefaultClient, h.server.URL+"/api")
	_, err := c.ListGroups(context.Background(), bearer(&chatv1.ListGroupsRequest{DeviceId: foreign}))
	require.Equal(t, connect.CodeNotFound, connectCode(err))
	_, err = c.JoinGroup(context.Background(), bearer(&chatv1.JoinGroupRequest{DeviceId: foreign, InviteLink: "https://chat.whatsapp.com/AbC"}))
	require.Equal(t, connect.CodeNotFound, connectCode(err))
	require.Zero(t, h.groups.JoinCalls, "the engine was never asked to join")
	mc := messagev1connect.NewMessageServiceClient(http.DefaultClient, h.server.URL+"/api")
	_, err = mc.Send(context.Background(), bearer(&messagev1.SendRequest{DeviceId: foreign, To: "628111222333", Text: "x"}))
	require.Equal(t, connect.CodeNotFound, connectCode(err))
	require.Empty(t, h.store.All(), "nothing was queued")
}

func TestChatService_ListGroupsAndJoin(t *testing.T) {
	h := newMsgHarness(t, "sales-01")
	c := chatv1connect.NewChatServiceClient(http.DefaultClient, h.server.URL+"/api")
	joined, err := c.JoinGroup(context.Background(), bearer(&chatv1.JoinGroupRequest{InviteLink: "https://chat.whatsapp.com/AbC"}))
	require.NoError(t, err)
	require.Equal(t, "999@g.us", joined.Msg.GetChat().GetJid())
	groups, err := c.ListGroups(context.Background(), bearer(&chatv1.ListGroupsRequest{}))
	require.NoError(t, err)
	require.Len(t, groups.Msg.GetGroups(), 1, "the joined group is stored even when the engine lists nothing")
	list, err := c.List(context.Background(), bearer(&chatv1.ListRequest{Kind: "group"}))
	require.NoError(t, err)
	require.Len(t, list.Msg.GetChats(), 1)
}

func TestContactService_List(t *testing.T) {
	h := newMsgHarness(t, "sales-01")
	c := contactv1connect.NewContactServiceClient(http.DefaultClient, h.server.URL+"/api")
	resp, err := c.List(context.Background(), bearer(&contactv1.ListRequest{Q: "bu"}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.GetContacts(), 1)
	require.Equal(t, "Budi", resp.Msg.GetContacts()[0].GetDisplayName())
	require.Equal(t, h.devices[0].PublicID, resp.Msg.GetContacts()[0].GetDeviceId())
	got, err := c.Get(context.Background(), bearer(&contactv1.GetRequest{Jid: "628111@s.whatsapp.net"}))
	require.NoError(t, err)
	require.Equal(t, "Budi", got.Msg.GetContact().GetName())
	_, err = c.Get(context.Background(), bearer(&contactv1.GetRequest{DeviceId: h.sibling.PublicID, Jid: "628111@s.whatsapp.net"}))
	require.Equal(t, connect.CodeNotFound, connectCode(err))
}

func TestMessagingRPCsServeAProjectWideKey(t *testing.T) {
	h := newMsgHarnessAs(t, &apiKeyAs{bound: false}, "sales-01")
	mc := messagev1connect.NewMessageServiceClient(http.DefaultClient, h.server.URL+"/api")
	_, err := mc.Send(context.Background(), keyed(h, &messagev1.SendRequest{To: "628111222333", Text: "x"}))
	require.NoError(t, err)
}

func TestMessagingRPCsRefuseADeviceBoundKey(t *testing.T) {
	h := newMsgHarnessAs(t, &apiKeyAs{bound: true}, "sales-01")
	mc := messagev1connect.NewMessageServiceClient(http.DefaultClient, h.server.URL+"/api")
	// SECURITY: every messaging RPC enters through the project scope, whose ReachesWholeProject refuses a device-bound key; there is no per-verb device exception on S2/S7.
	_, err := mc.Send(context.Background(), keyed(h, &messagev1.SendRequest{DeviceId: h.devices[0].PublicID, To: "628111222333", Text: "x"}))
	require.Equal(t, connect.CodePermissionDenied, connectCode(err))
	require.ErrorContains(t, err, "outside this credential's reach")
	cc := chatv1connect.NewChatServiceClient(http.DefaultClient, h.server.URL+"/api")
	_, err = cc.List(context.Background(), keyed(h, &chatv1.ListRequest{}))
	require.Equal(t, connect.CodePermissionDenied, connectCode(err))
	kc := contactv1connect.NewContactServiceClient(http.DefaultClient, h.server.URL+"/api")
	_, err = kc.List(context.Background(), keyed(h, &contactv1.ListRequest{}))
	require.Equal(t, connect.CodePermissionDenied, connectCode(err))
}

func TestMessagingRPCsRefuseADeviceBoundKeyOnEveryVerb(t *testing.T) {
	h := newMsgHarnessAs(t, &apiKeyAs{bound: true}, "sales-01")
	base := h.server.URL + "/api"
	mc := messagev1connect.NewMessageServiceClient(http.DefaultClient, base)
	cc := chatv1connect.NewChatServiceClient(http.DefaultClient, base)
	kc := contactv1connect.NewContactServiceClient(http.DefaultClient, base)
	ctx := context.Background()
	dev := h.devices[0].PublicID
	calls := map[string]func() error{
		"message.Send": func() error {
			_, err := mc.Send(ctx, keyed(h, &messagev1.SendRequest{DeviceId: dev, To: "628111222333", Text: "x"}))
			return err
		},
		"message.Get":  func() error { _, err := mc.Get(ctx, keyed(h, &messagev1.GetRequest{MessageId: "msg_x"})); return err },
		"message.List": func() error { _, err := mc.List(ctx, keyed(h, &messagev1.ListRequest{DeviceId: dev})); return err },
		"message.React": func() error {
			_, err := mc.React(ctx, keyed(h, &messagev1.ReactRequest{MessageId: "msg_x"}))
			return err
		},
		"message.Revoke": func() error {
			_, err := mc.Revoke(ctx, keyed(h, &messagev1.RevokeRequest{MessageId: "msg_x"}))
			return err
		},
		"message.Edit": func() error {
			_, err := mc.Edit(ctx, keyed(h, &messagev1.EditRequest{MessageId: "msg_x", Text: "y"}))
			return err
		},
		"message.MarkRead": func() error {
			_, err := mc.MarkRead(ctx, keyed(h, &messagev1.MarkReadRequest{ChatId: "cht_x"}))
			return err
		},
		"chat.List": func() error { _, err := cc.List(ctx, keyed(h, &chatv1.ListRequest{})); return err },
		"chat.Get":  func() error { _, err := cc.Get(ctx, keyed(h, &chatv1.GetRequest{ChatId: "cht_x"})); return err },
		"chat.MarkRead": func() error {
			_, err := cc.MarkRead(ctx, keyed(h, &chatv1.MarkReadRequest{ChatId: "cht_x"}))
			return err
		},
		"chat.ListGroups": func() error {
			_, err := cc.ListGroups(ctx, keyed(h, &chatv1.ListGroupsRequest{DeviceId: dev}))
			return err
		},
		"chat.GroupInfo": func() error {
			_, err := cc.GroupInfo(ctx, keyed(h, &chatv1.GroupInfoRequest{ChatId: "cht_x"}))
			return err
		},
		"chat.JoinGroup": func() error {
			_, err := cc.JoinGroup(ctx, keyed(h, &chatv1.JoinGroupRequest{DeviceId: dev, InviteLink: "https://chat.whatsapp.com/AbC"}))
			return err
		},
		"chat.LeaveGroup": func() error {
			_, err := cc.LeaveGroup(ctx, keyed(h, &chatv1.LeaveGroupRequest{ChatId: "cht_x"}))
			return err
		},
		"contact.List": func() error { _, err := kc.List(ctx, keyed(h, &contactv1.ListRequest{})); return err },
		"contact.Get": func() error {
			_, err := kc.Get(ctx, keyed(h, &contactv1.GetRequest{DeviceId: dev, Jid: "628111@s.whatsapp.net"}))
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, connect.CodePermissionDenied, connectCode(call()))
		})
	}
	require.Zero(t, h.groups.JoinCalls, "the engine was never asked to join")
	require.Empty(t, h.store.All(), "nothing was queued")
}

func TestMessagingRPCsRefuseAnotherProjectToAProjectKey(t *testing.T) {
	h := newMsgHarnessAs(t, &apiKeyAs{bound: false}, "sales-01")
	base := h.server.URL + "/api"
	other := h.other.String()
	ctx := context.Background()
	_, err := messagev1connect.NewMessageServiceClient(http.DefaultClient, base).Send(ctx, keyed(h, &messagev1.SendRequest{ProjectId: other, To: "628111222333", Text: "x"}))
	require.Equal(t, connect.CodePermissionDenied, connectCode(err))
	_, err = chatv1connect.NewChatServiceClient(http.DefaultClient, base).List(ctx, keyed(h, &chatv1.ListRequest{ProjectId: other}))
	require.Equal(t, connect.CodePermissionDenied, connectCode(err))
	_, err = contactv1connect.NewContactServiceClient(http.DefaultClient, base).List(ctx, keyed(h, &contactv1.ListRequest{ProjectId: other}))
	require.Equal(t, connect.CodePermissionDenied, connectCode(err))
	require.Empty(t, h.store.All(), "nothing was queued")
}

func TestMessagingRPCsServeAProjectWideKeyOnReadVerbs(t *testing.T) {
	h := newMsgHarnessAs(t, &apiKeyAs{bound: false}, "sales-01")
	base := h.server.URL + "/api"
	ctx := context.Background()
	_, err := chatv1connect.NewChatServiceClient(http.DefaultClient, base).List(ctx, keyed(h, &chatv1.ListRequest{}))
	require.NoError(t, err)
	_, err = contactv1connect.NewContactServiceClient(http.DefaultClient, base).List(ctx, keyed(h, &contactv1.ListRequest{}))
	require.NoError(t, err)
	_, err = messagev1connect.NewMessageServiceClient(http.DefaultClient, base).List(ctx, keyed(h, &messagev1.ListRequest{DeviceId: h.devices[0].PublicID}))
	require.NoError(t, err)
}

// SECURITY: a message or chat public id of one project is NotFound when the caller scopes to a sibling project of the same org.
func TestMessagingRPCsTreatAForeignProjectsIDsAsNotFound(t *testing.T) {
	h := newMsgHarness(t, "sales-01")
	base := h.server.URL + "/api"
	ctx := context.Background()
	mc := messagev1connect.NewMessageServiceClient(http.DefaultClient, base)
	cc := chatv1connect.NewChatServiceClient(http.DefaultClient, base)
	sent, err := mc.Send(ctx, bearer(&messagev1.SendRequest{To: "628111222333", Text: "halo"}))
	require.NoError(t, err)
	msgID, chatID := sent.Msg.GetMessage().GetId(), sent.Msg.GetMessage().GetChatId()
	other := h.other.String()

	_, err = mc.Get(ctx, bearer(&messagev1.GetRequest{MessageId: msgID}))
	require.NoError(t, err, "control: the owning project resolves it")
	_, err = cc.Get(ctx, bearer(&chatv1.GetRequest{ChatId: chatID}))
	require.NoError(t, err, "control: the owning project resolves it")

	calls := map[string]func() error{
		"message.Get": func() error {
			_, err := mc.Get(ctx, bearer(&messagev1.GetRequest{ProjectId: other, MessageId: msgID}))
			return err
		},
		"message.React": func() error {
			_, err := mc.React(ctx, bearer(&messagev1.ReactRequest{ProjectId: other, MessageId: msgID, Emoji: "👍"}))
			return err
		},
		"message.Revoke": func() error {
			_, err := mc.Revoke(ctx, bearer(&messagev1.RevokeRequest{ProjectId: other, MessageId: msgID}))
			return err
		},
		"message.Edit": func() error {
			_, err := mc.Edit(ctx, bearer(&messagev1.EditRequest{ProjectId: other, MessageId: msgID, Text: "y"}))
			return err
		},
		"message.List.chat": func() error {
			_, err := mc.List(ctx, bearer(&messagev1.ListRequest{ProjectId: other, ChatId: chatID}))
			return err
		},
		"message.List.since": func() error {
			_, err := mc.List(ctx, bearer(&messagev1.ListRequest{ProjectId: other, SinceId: msgID}))
			return err
		},
		"message.MarkRead": func() error {
			_, err := mc.MarkRead(ctx, bearer(&messagev1.MarkReadRequest{ProjectId: other, ChatId: chatID}))
			return err
		},
		"chat.Get": func() error {
			_, err := cc.Get(ctx, bearer(&chatv1.GetRequest{ProjectId: other, ChatId: chatID}))
			return err
		},
		"chat.MarkRead": func() error {
			_, err := cc.MarkRead(ctx, bearer(&chatv1.MarkReadRequest{ProjectId: other, ChatId: chatID}))
			return err
		},
		"chat.GroupInfo": func() error {
			_, err := cc.GroupInfo(ctx, bearer(&chatv1.GroupInfoRequest{ProjectId: other, ChatId: chatID}))
			return err
		},
		"chat.LeaveGroup": func() error {
			_, err := cc.LeaveGroup(ctx, bearer(&chatv1.LeaveGroupRequest{ProjectId: other, ChatId: chatID}))
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, connect.CodeNotFound, connectCode(call()))
		})
	}
}
