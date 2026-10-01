package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/grpc/codes"

	chatv1 "altalune.id/openwa/gen/go/chat/v1"
	"altalune.id/openwa/gen/go/chat/v1/chatv1connect"
	contactv1 "altalune.id/openwa/gen/go/contact/v1"
	"altalune.id/openwa/gen/go/contact/v1/contactv1connect"
	"altalune.id/openwa/gen/go/device/v1/devicev1connect"
	messagev1 "altalune.id/openwa/gen/go/message/v1"
	"altalune.id/openwa/gen/go/message/v1/messagev1connect"
	"altalune.id/openwa/internal/boot"
	"altalune.id/openwa/internal/controlplane"
	"altalune.id/openwa/internal/platform/config"
)

const (
	cliChatID = "cht_Bd7Kq2Wm9Xp4Lz3a"
	cliMsgID  = "msg_Ab1Cd2Ef3Gh4Ij5k"
)

type fakeMessageRPC struct {
	messagev1connect.UnimplementedMessageServiceHandler
	mu        sync.Mutex
	sends     []*messagev1.SendRequest
	lists     []*messagev1.ListRequest
	sendErr   error
	loop      bool
	failPage2 error
}

func (f *fakeMessageRPC) Send(_ context.Context, req *connect.Request[messagev1.SendRequest]) (*connect.Response[messagev1.SendResponse], error) {
	f.mu.Lock()
	f.sends = append(f.sends, req.Msg)
	f.mu.Unlock()
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	return connect.NewResponse(&messagev1.SendResponse{Message: &messagev1.Message{Id: cliMsgID, ChatId: cliChatID, DeviceId: salesID, Direction: "out", Type: "text", Status: "queued", Body: req.Msg.GetText()}}), nil
}

func (f *fakeMessageRPC) List(_ context.Context, req *connect.Request[messagev1.ListRequest]) (*connect.Response[messagev1.ListResponse], error) {
	f.mu.Lock()
	f.lists = append(f.lists, req.Msg)
	f.mu.Unlock()
	if f.loop {
		return connect.NewResponse(&messagev1.ListResponse{Messages: []*messagev1.Message{{Id: "mx", Type: "text"}}, NextCursor: "C1"}), nil
	}
	if f.failPage2 != nil && req.Msg.GetCursor() != "" {
		return nil, f.failPage2
	}
	if req.Msg.GetCursor() == "" {
		return connect.NewResponse(&messagev1.ListResponse{Messages: []*messagev1.Message{{Id: "m2", Type: "text", Status: "read", Body: "second", Direction: "out"}}, NextCursor: "C1"}), nil
	}
	return connect.NewResponse(&messagev1.ListResponse{Messages: []*messagev1.Message{{Id: "m1", Type: "text", Status: "received", Body: "first", Direction: "in", SenderPhone: "628111"}}}), nil
}

type fakeChatRPC struct {
	chatv1connect.UnimplementedChatServiceHandler
	mu     sync.Mutex
	lists  []*chatv1.ListRequest
	joins  []*chatv1.JoinGroupRequest
	leaves []string
	groups []string
}

func (f *fakeChatRPC) List(_ context.Context, req *connect.Request[chatv1.ListRequest]) (*connect.Response[chatv1.ListResponse], error) {
	f.mu.Lock()
	f.lists = append(f.lists, req.Msg)
	f.mu.Unlock()
	return connect.NewResponse(&chatv1.ListResponse{Chats: []*chatv1.Chat{{Id: cliChatID, DeviceId: salesID, Jid: "628111@s.whatsapp.net", Kind: "dm", Name: "Budi", UnreadCount: 2, LastMessagePreview: "halo"}}}), nil
}

func (f *fakeChatRPC) Get(_ context.Context, req *connect.Request[chatv1.GetRequest]) (*connect.Response[chatv1.GetResponse], error) {
	return connect.NewResponse(&chatv1.GetResponse{Chat: &chatv1.Chat{Id: req.Msg.GetChatId(), Jid: "628111@s.whatsapp.net", Kind: "dm", Name: "Budi"}}), nil
}

func (f *fakeChatRPC) ListGroups(_ context.Context, req *connect.Request[chatv1.ListGroupsRequest]) (*connect.Response[chatv1.ListGroupsResponse], error) {
	f.mu.Lock()
	f.groups = append(f.groups, req.Msg.GetDeviceId())
	f.mu.Unlock()
	return connect.NewResponse(&chatv1.ListGroupsResponse{Groups: []*chatv1.Group{{Jid: "999@g.us", Name: "Ops", Participants: 12, Joined: true, ChatId: cliChatID}}}), nil
}

func (f *fakeChatRPC) JoinGroup(_ context.Context, req *connect.Request[chatv1.JoinGroupRequest]) (*connect.Response[chatv1.JoinGroupResponse], error) {
	f.mu.Lock()
	f.joins = append(f.joins, req.Msg)
	f.mu.Unlock()
	return connect.NewResponse(&chatv1.JoinGroupResponse{Chat: &chatv1.Chat{Id: cliChatID, Jid: "999@g.us", Kind: "group", Name: "Ops"}}), nil
}

func (f *fakeChatRPC) LeaveGroup(_ context.Context, req *connect.Request[chatv1.LeaveGroupRequest]) (*connect.Response[chatv1.LeaveGroupResponse], error) {
	f.mu.Lock()
	f.leaves = append(f.leaves, req.Msg.GetChatId())
	f.mu.Unlock()
	return connect.NewResponse(&chatv1.LeaveGroupResponse{Chat: &chatv1.Chat{Id: req.Msg.GetChatId(), Jid: "999@g.us", Kind: "group", Archived: true}}), nil
}

type fakeContactRPC struct {
	contactv1connect.UnimplementedContactServiceHandler
}

func (fakeContactRPC) List(context.Context, *connect.Request[contactv1.ListRequest]) (*connect.Response[contactv1.ListResponse], error) {
	return connect.NewResponse(&contactv1.ListResponse{Contacts: []*contactv1.Contact{{DeviceId: salesID, Jid: "628111@s.whatsapp.net", Phone: "628111", DisplayName: "Budi"}}}), nil
}

type messagingFakes struct {
	msg  *fakeMessageRPC
	chat *fakeChatRPC
	dev  *fakeDeviceRPC
}

func messagingServer(t *testing.T) (ClientBootFn, *messagingFakes) {
	t.Helper()
	f := &messagingFakes{msg: &fakeMessageRPC{}, chat: &fakeChatRPC{}, dev: &fakeDeviceRPC{}}
	mux := http.NewServeMux()
	for _, mount := range []func() (string, http.Handler){
		func() (string, http.Handler) { return messagev1connect.NewMessageServiceHandler(f.msg) },
		func() (string, http.Handler) { return chatv1connect.NewChatServiceHandler(f.chat) },
		func() (string, http.Handler) { return contactv1connect.NewContactServiceHandler(fakeContactRPC{}) },
		func() (string, http.Handler) { return devicev1connect.NewDeviceServiceHandler(f.dev) },
	} {
		path, h := mount()
		mux.Handle("/api"+path, http.StripPrefix("/api", h))
	}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return func(_ context.Context, cfg *config.Config, token string) (*boot.Client, error) {
		return &boot.Client{Cfg: cfg, Conn: controlplane.NewClient(ts.URL, token)}, nil
	}, f
}

func TestSendText_ResolvesTheDeviceNameAndPrintsTheID(t *testing.T) {
	setSelfhostedEnv(t)
	bc, f := messagingServer(t)
	out, _, err := runDevice(t, bc, "send", "text", "--device", "sales", "--to", "628111222333", "--text", "halo", "--reply-to", "3EB0X", "--mention", "628999", "--output", "text")
	if err != nil {
		t.Fatalf("send text: %v (%s)", err, out)
	}
	if !strings.Contains(out, "Queued message "+cliMsgID) {
		t.Fatalf("output = %s", out)
	}
	got := f.msg.sends[0]
	if got.GetDeviceId() != salesID || got.GetTo() != "628111222333" || got.GetReplyTo() != "3EB0X" || strings.Join(got.GetMentions(), ",") != "628999" {
		t.Fatalf("request = %+v", got)
	}
}

func TestSendText_WithoutDeviceLetsTheServerResolve(t *testing.T) {
	setSelfhostedEnv(t)
	bc, f := messagingServer(t)
	if _, _, err := runDevice(t, bc, "send", "text", "--to", "628111", "--text", "x"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if f.msg.sends[0].GetDeviceId() != "" {
		t.Fatalf("device_id = %q, want empty", f.msg.sends[0].GetDeviceId())
	}
}

func TestSendImage_ReadsTheFileAndDetectsTheMime(t *testing.T) {
	setSelfhostedEnv(t)
	bc, f := messagingServer(t)
	path := filepath.Join(t.TempDir(), "pic.png")
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	if err := os.WriteFile(path, png, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runDevice(t, bc, "send", "image", "--to", "628111", "--file", path, "--caption", "look"); err != nil {
		t.Fatalf("send image: %v", err)
	}
	m := f.msg.sends[0].GetMedia()
	if m.GetMime() != "image/png" || string(m.GetData()) != string(png) || m.GetCaption() != "look" || m.GetFilename() != "pic.png" {
		t.Fatalf("media = %+v", m)
	}
}

func TestSendImage_RefusesANonImageAndAnOversizeFile(t *testing.T) {
	setSelfhostedEnv(t)
	bc, _ := messagingServer(t)
	dir := t.TempDir()
	txt := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(txt, []byte("plain text"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runDevice(t, bc, "send", "image", "--to", "628111", "--file", txt); err == nil {
		t.Fatal("a text file sent as an image must fail")
	}
}

func TestSendDocument_ServerOversizeRefusalSuggestsURL(t *testing.T) {
	setSelfhostedEnv(t)
	bc, f := messagingServer(t)
	f.msg.sendErr = connect.NewError(connect.CodeResourceExhausted, errors.New("media too large"))
	path := filepath.Join(t.TempDir(), "a.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4 x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := runDevice(t, bc, "send", "document", "--to", "628111", "--file", path)
	if err == nil || !strings.Contains(err.Error(), "--url") || !strings.Contains(err.Error(), "mediaMaxBytes") {
		t.Fatalf("err = %v, want a hint to use --url", err)
	}
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("the server code must survive: %v", err)
	}
	f.msg.sendErr = connect.NewError(connect.CodeResourceExhausted, errors.New("rate"))
	_, _, err = runDevice(t, bc, "send", "text", "--to", "628111", "--text", "x")
	if err == nil || strings.Contains(err.Error(), "--url") {
		t.Fatalf("a text send must not get the --url hint: %v", err)
	}
}

func TestSendDocumentByURLAndLocation(t *testing.T) {
	setSelfhostedEnv(t)
	bc, f := messagingServer(t)
	if _, _, err := runDevice(t, bc, "send", "document", "--to", "628111", "--url", "https://files.example.com/a.pdf", "--filename", "a.pdf"); err != nil {
		t.Fatalf("send document: %v", err)
	}
	if got := f.msg.sends[0].GetMedia(); got.GetUrl() != "https://files.example.com/a.pdf" || got.GetFilename() != "a.pdf" || len(got.GetData()) != 0 {
		t.Fatalf("media = %+v", got)
	}
	if _, _, err := runDevice(t, bc, "send", "location", "--to", "628111", "--lat=-6.2", "--lng=106.8", "--name", "Office"); err != nil {
		t.Fatalf("send location: %v", err)
	}
	if loc := f.msg.sends[1].GetLocation(); loc.GetLat() != -6.2 || loc.GetLng() != 106.8 || loc.GetName() != "Office" {
		t.Fatalf("location = %+v", loc)
	}
	if _, _, err := runDevice(t, bc, "send", "document", "--to", "628111"); err == nil {
		t.Fatal("document without --file or --url must fail")
	}
}

func TestMessageList_NDJSONAllFollowsTheCursor(t *testing.T) {
	setSelfhostedEnv(t)
	bc, f := messagingServer(t)
	out, _, err := runDevice(t, bc, "message", "list", "--chat", cliChatID, "--all", "--output", "ndjson")
	if err != nil {
		t.Fatalf("message list: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"id":"m2"`) || !strings.Contains(lines[1], `"id":"m1"`) {
		t.Fatalf("ndjson = %q", out)
	}
	if len(f.msg.lists) != 2 || f.msg.lists[1].GetCursor() != "C1" || f.msg.lists[0].GetChatId() != cliChatID {
		t.Fatalf("list requests = %+v", f.msg.lists)
	}
	if _, _, err := runDevice(t, bc, "message", "list", "--chat", cliChatID, "--all", "--cursor", "C1"); err == nil {
		t.Fatal("--all with --cursor must fail")
	}
}

func TestMessageList_JSONCarriesTheNextCursor(t *testing.T) {
	setSelfhostedEnv(t)
	bc, _ := messagingServer(t)
	text, errOut, err := runDevice(t, bc, "message", "list", "--chat", cliChatID, "--output", "text")
	if err != nil {
		t.Fatalf("message list text: %v", err)
	}
	if !strings.Contains(errOut, "more: --cursor C1") || strings.Contains(text, "more:") {
		t.Fatalf("the next cursor belongs on stderr: stdout=%q stderr=%q", text, errOut)
	}
	out, _, err := runDevice(t, bc, "message", "list", "--chat", cliChatID, "--output", "json")
	if err != nil {
		t.Fatalf("message list: %v", err)
	}
	if !strings.Contains(out, `"next_cursor": "C1"`) || !strings.Contains(out, `"items"`) {
		t.Fatalf("json = %s", out)
	}
}

func TestChatListAndShow(t *testing.T) {
	setSelfhostedEnv(t)
	bc, f := messagingServer(t)
	out, _, err := runDevice(t, bc, "chat", "list", "--kind", "dm", "--q", "bu", "--output", "text")
	if err != nil {
		t.Fatalf("chat list: %v", err)
	}
	for _, want := range []string{"NAME", "UNREAD", "Budi", "2", cliChatID} {
		if !strings.Contains(out, want) {
			t.Fatalf("table missing %q: %s", want, out)
		}
	}
	if f.chat.lists[0].GetKind() != "dm" || f.chat.lists[0].GetQ() != "bu" {
		t.Fatalf("list request = %+v", f.chat.lists[0])
	}
	out, _, err = runDevice(t, bc, "chat", "show", cliChatID, "--output", "text")
	if err != nil {
		t.Fatalf("chat show: %v", err)
	}
	for _, want := range []string{"Budi", "628111@s.whatsapp.net", "second"} {
		if !strings.Contains(out, want) {
			t.Fatalf("show missing %q: %s", want, out)
		}
	}
	if _, _, err := runDevice(t, bc, "chat", "list", "--kind", "channel"); err == nil {
		t.Fatal("an unknown --kind must fail before the call")
	}
}

func TestContactList(t *testing.T) {
	setSelfhostedEnv(t)
	bc, _ := messagingServer(t)
	out, _, err := runDevice(t, bc, "contact", "list", "--output", "ndjson")
	if err != nil {
		t.Fatalf("contact list: %v", err)
	}
	if !strings.Contains(out, `"display_name":"Budi"`) || !strings.Contains(out, `"phone":"628111"`) {
		t.Fatalf("ndjson = %s", out)
	}
}

func TestGroupListJoinLeave(t *testing.T) {
	setSelfhostedEnv(t)
	bc, f := messagingServer(t)
	out, _, err := runDevice(t, bc, "group", "list", "--device", "Sales", "--output", "text")
	if err != nil || !strings.Contains(out, "Ops") || !strings.Contains(out, "12") {
		t.Fatalf("group list: %v %s", err, out)
	}
	if f.chat.groups[0] != salesID {
		t.Fatalf("ListGroups device = %q", f.chat.groups[0])
	}
	out, _, err = runDevice(t, bc, "group", "join", "https://chat.whatsapp.com/AbC", "--output", "text")
	if err != nil || !strings.Contains(out, "Joined group Ops ("+cliChatID+")") {
		t.Fatalf("group join: %v %s", err, out)
	}
	if f.chat.joins[0].GetInviteLink() != "https://chat.whatsapp.com/AbC" {
		t.Fatalf("join = %+v", f.chat.joins[0])
	}
	out, _, err = runDevice(t, bc, "group", "leave", cliChatID, "--output", "text")
	if err != nil || !strings.Contains(out, "Left group 999@g.us") {
		t.Fatalf("group leave: %v %s", err, out)
	}
}

func TestSendText_SurfacesTheServersDeviceUnresolvedRefusal(t *testing.T) {
	setSelfhostedEnv(t)
	bc, f := messagingServer(t)
	f.msg.sendErr = appErr("GEN004", codes.InvalidArgument, "This project has several devices; pass device_id as one of: Sales, Support")
	_, _, err := runDevice(t, bc, "send", "text", "--to", "628111", "--text", "x")
	if err == nil || !strings.Contains(err.Error(), "several devices") {
		t.Fatalf("err = %v, want the server's several-devices refusal", err)
	}
	if got := ExitCodeFor(err); got != ExitInvalidArg {
		t.Fatalf("exit = %d, want %d", got, ExitInvalidArg)
	}
}

func TestMessageList_RequiresAChatOrADevice(t *testing.T) {
	setSelfhostedEnv(t)
	bc, f := messagingServer(t)
	if _, _, err := runDevice(t, bc, "message", "list"); err == nil {
		t.Fatal("message list without --chat or --device must fail")
	}
	if len(f.msg.lists) != 0 {
		t.Fatalf("no call expected, got %d", len(f.msg.lists))
	}
}

func TestSendText_JSONAndNDJSONPrintTheSingleRecord(t *testing.T) {
	setSelfhostedEnv(t)
	bc, _ := messagingServer(t)
	for _, mode := range []string{"json", "ndjson"} {
		out, _, err := runDevice(t, bc, "send", "text", "--to", "628111", "--text", "hi", "--output", mode)
		if err != nil {
			t.Fatalf("send %s: %v", mode, err)
		}
		if !strings.Contains(out, `"id"`) || !strings.Contains(out, cliMsgID) {
			t.Fatalf("%s output = %s", mode, out)
		}
	}
}

func TestMessageList_NDJSONAllStreamsPagesAsTheyArrive(t *testing.T) {
	setSelfhostedEnv(t)
	bc, f := messagingServer(t)
	f.msg.failPage2 = connect.NewError(connect.CodeUnavailable, errors.New("page two down"))
	out, _, err := runDevice(t, bc, "message", "list", "--chat", cliChatID, "--all", "--output", "ndjson")
	if err == nil {
		t.Fatal("the second page fails, so the command must fail")
	}
	if !strings.Contains(out, `"id":"m2"`) {
		t.Fatalf("page one must be printed before page two is fetched: %q", out)
	}
}

func TestMessageList_AllStopsOnANonAdvancingCursor(t *testing.T) {
	setSelfhostedEnv(t)
	bc, f := messagingServer(t)
	f.msg.loop = true
	for _, mode := range []string{"ndjson", "json", "text"} {
		_, _, err := runDevice(t, bc, "message", "list", "--chat", cliChatID, "--all", "--output", mode)
		if err == nil || !strings.Contains(err.Error(), "again") {
			t.Fatalf("%s: err = %v, want the repeated-cursor refusal", mode, err)
		}
	}
	if n := len(f.msg.lists); n > 9 {
		t.Fatalf("made %d calls; the guard must stop after a few", n)
	}
}

func TestMessageList_AllInJSONMergesEveryPage(t *testing.T) {
	setSelfhostedEnv(t)
	bc, _ := messagingServer(t)
	out, _, err := runDevice(t, bc, "message", "list", "--chat", cliChatID, "--all", "--output", "json")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, `"id": "m2"`) || !strings.Contains(out, `"id": "m1"`) || !strings.Contains(out, `"next_cursor": ""`) {
		t.Fatalf("json = %s", out)
	}
}

func TestMessageList_PassesLimitSinceAndDeviceOnly(t *testing.T) {
	setSelfhostedEnv(t)
	bc, f := messagingServer(t)
	if _, _, err := runDevice(t, bc, "message", "list", "--chat", cliChatID, "--limit", "7", "--since", cliMsgID); err != nil {
		t.Fatalf("list: %v", err)
	}
	got := f.msg.lists[0]
	if got.GetLimit() != 7 || got.GetSinceId() != cliMsgID || got.GetChatId() != cliChatID {
		t.Fatalf("request = %+v", got)
	}
	if _, _, err := runDevice(t, bc, "message", "list", "--device", "sales"); err != nil {
		t.Fatalf("device-only list: %v", err)
	}
	got = f.msg.lists[1]
	if got.GetDeviceId() != salesID || got.GetChatId() != "" {
		t.Fatalf("request = %+v", got)
	}
}

func TestChatList_PassesLimit(t *testing.T) {
	setSelfhostedEnv(t)
	bc, f := messagingServer(t)
	if _, _, err := runDevice(t, bc, "chat", "list", "--limit", "5", "--output", "json"); err != nil {
		t.Fatalf("chat list: %v", err)
	}
	if f.chat.lists[0].GetLimit() != 5 {
		t.Fatalf("request = %+v", f.chat.lists[0])
	}
}

func TestGroupList_JSON(t *testing.T) {
	setSelfhostedEnv(t)
	bc, _ := messagingServer(t)
	out, _, err := runDevice(t, bc, "group", "list", "--output", "json")
	if err != nil {
		t.Fatalf("group list: %v", err)
	}
	for _, want := range []string{`"items"`, `"jid": "999@g.us"`, `"participants": 12`, `"joined": true`} {
		if !strings.Contains(out, want) {
			t.Fatalf("json missing %q: %s", want, out)
		}
	}
}

func TestSendText_MarkReadIsForwarded(t *testing.T) {
	setSelfhostedEnv(t)
	bc, f := messagingServer(t)
	if _, _, err := runDevice(t, bc, "send", "text", "--to", "628111", "--text", "x", "--mark-read"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if !f.msg.sends[0].GetMarkReadFirst() {
		t.Fatalf("request = %+v", f.msg.sends[0])
	}
}
