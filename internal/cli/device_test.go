package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/openwa/gen/go/apperror/v1"
	devicev1 "altalune.id/openwa/gen/go/device/v1"
	"altalune.id/openwa/gen/go/device/v1/devicev1connect"
	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/boot"
	"altalune.id/openwa/internal/controlplane"
	"altalune.id/openwa/internal/platform/config"
)

const (
	salesID   = "dev_SalesDevice00001"
	supportID = "dev_SupportDevice001"
)

type fakeDeviceRPC struct {
	devicev1connect.UnimplementedDeviceServiceHandler
	mu        sync.Mutex
	outcome   string
	startErr  error
	qrs       []string
	gets      []string
	starts    []string
	phones    []string
	unlinks   []string
	deletes   []string
	linkPolls int
}

type deviceCalls struct {
	gets, starts, phones, unlinks, deletes []string
	linkPolls                              int
}

func (f *fakeDeviceRPC) calls() deviceCalls {
	f.mu.Lock()
	defer f.mu.Unlock()
	return deviceCalls{
		gets:      slices.Clone(f.gets),
		starts:    slices.Clone(f.starts),
		phones:    slices.Clone(f.phones),
		unlinks:   slices.Clone(f.unlinks),
		deletes:   slices.Clone(f.deletes),
		linkPolls: f.linkPolls,
	}
}

func (f *fakeDeviceRPC) record(list *[]string, v string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	*list = append(*list, v)
}

func (f *fakeDeviceRPC) ListDevices(context.Context, *connect.Request[devicev1.ListDevicesRequest]) (*connect.Response[devicev1.ListDevicesResponse], error) {
	return connect.NewResponse(&devicev1.ListDevicesResponse{Devices: []*devicev1.Device{
		{Id: salesID, Name: "Sales", State: "connected", Phone: "628123456789", Version: 3},
		{Id: supportID, Name: "Support", State: "unlinked", Version: 1},
	}}), nil
}

func (f *fakeDeviceRPC) GetDevice(_ context.Context, req *connect.Request[devicev1.GetDeviceRequest]) (*connect.Response[devicev1.GetDeviceResponse], error) {
	f.record(&f.gets, req.Msg.GetDeviceId())
	return connect.NewResponse(&devicev1.GetDeviceResponse{Device: &devicev1.Device{Id: req.Msg.GetDeviceId(), Name: "Sales", State: "connected", Phone: "628123456789", Version: 3}}), nil
}

func (f *fakeDeviceRPC) CreateDevice(_ context.Context, req *connect.Request[devicev1.CreateDeviceRequest]) (*connect.Response[devicev1.CreateDeviceResponse], error) {
	return connect.NewResponse(&devicev1.CreateDeviceResponse{Device: &devicev1.Device{Id: supportID, Name: req.Msg.GetName(), State: "unlinked", Version: 1}}), nil
}

func (f *fakeDeviceRPC) DeleteDevice(_ context.Context, req *connect.Request[devicev1.DeleteDeviceRequest]) (*connect.Response[devicev1.DeleteDeviceResponse], error) {
	f.record(&f.deletes, req.Msg.GetDeviceId())
	return connect.NewResponse(&devicev1.DeleteDeviceResponse{}), nil
}

func (f *fakeDeviceRPC) StartLink(_ context.Context, req *connect.Request[devicev1.StartLinkRequest]) (*connect.Response[devicev1.StartLinkResponse], error) {
	f.record(&f.starts, req.Msg.GetDeviceId())
	if f.startErr != nil {
		return nil, f.startErr
	}
	return connect.NewResponse(&devicev1.StartLinkResponse{DeviceId: supportID, Link: &devicev1.LinkState{Method: "qr", Outcome: "pending", Qr: "2@abc,def"}}), nil
}

func (f *fakeDeviceRPC) LinkWithPhone(_ context.Context, req *connect.Request[devicev1.LinkWithPhoneRequest]) (*connect.Response[devicev1.LinkWithPhoneResponse], error) {
	f.record(&f.phones, req.Msg.GetPhone())
	return connect.NewResponse(&devicev1.LinkWithPhoneResponse{DeviceId: supportID, Link: &devicev1.LinkState{Method: "phone", Outcome: "pending", PairingCode: "ABCD-EFGH"}}), nil
}

func (f *fakeDeviceRPC) GetLinkState(context.Context, *connect.Request[devicev1.GetLinkStateRequest]) (*connect.Response[devicev1.GetLinkStateResponse], error) {
	f.mu.Lock()
	f.linkPolls++
	outcome := f.outcome
	var qr string
	if len(f.qrs) > 0 {
		qr, f.qrs = f.qrs[0], f.qrs[1:]
		outcome = "pending"
	}
	f.mu.Unlock()
	if outcome == "" {
		outcome = "connected"
	}
	return connect.NewResponse(&devicev1.GetLinkStateResponse{DeviceId: supportID, Link: &devicev1.LinkState{Method: "qr", Outcome: outcome, Qr: qr}}), nil
}

func (f *fakeDeviceRPC) Unlink(_ context.Context, req *connect.Request[devicev1.UnlinkRequest]) (*connect.Response[devicev1.UnlinkResponse], error) {
	f.record(&f.unlinks, req.Msg.GetDeviceId())
	return connect.NewResponse(&devicev1.UnlinkResponse{DeviceId: supportID}), nil
}

func serveDevice(t *testing.T, f *fakeDeviceRPC) string {
	t.Helper()
	path, h := devicev1connect.NewDeviceServiceHandler(f)
	mux := http.NewServeMux()
	mux.Handle("/api"+path, http.StripPrefix("/api", h))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts.URL
}

func deviceServer(t *testing.T, f *fakeDeviceRPC) ClientBootFn {
	t.Helper()
	url := serveDevice(t, f)
	return func(_ context.Context, cfg *config.Config, token string) (*boot.Client, error) {
		return &boot.Client{Cfg: cfg, Conn: controlplane.NewClient(url, token)}, nil
	}
}

func runDevice(t *testing.T, bootClient ClientBootFn, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := NewRootCmd(stubServerBoot, bootClient)
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

func TestDeviceList_TableAndJSON(t *testing.T) {
	setSelfhostedEnv(t)
	boot := deviceServer(t, &fakeDeviceRPC{})
	out, _, err := runDevice(t, boot, "device", "list", "--output", "text")
	if err != nil {
		t.Fatalf("list: %v (%s)", err, out)
	}
	for _, want := range []string{"NAME", "STATE", "Sales", "connected", "+628123456789"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table missing %q: %s", want, out)
		}
	}
	out, _, err = runDevice(t, boot, "device", "list", "--output", "json")
	if err != nil {
		t.Fatalf("list json: %v", err)
	}
	if !strings.Contains(out, `"data"`) || !strings.Contains(out, `"push_name"`) {
		t.Fatalf("json output lacks the data envelope or fields: %s", out)
	}
}

func TestDeviceGet_ResolvesANameThroughListDevices(t *testing.T) {
	setSelfhostedEnv(t)
	f := &fakeDeviceRPC{}
	boot := deviceServer(t, f)
	if _, _, err := runDevice(t, boot, "device", "get", "--device", "sales", "--output", "json"); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got := f.calls().gets; len(got) != 1 || got[0] != salesID {
		t.Fatalf("GetDevice ids = %v, want [%s]", got, salesID)
	}
	if _, _, err := runDevice(t, boot, "device", "get", "--device", "nobody"); err == nil {
		t.Fatal("an unknown device name must fail")
	}
	if _, _, err := runDevice(t, boot, "device", "get"); err == nil {
		t.Fatal("get without --device must fail")
	}
}

func TestDevicePair_PrintsTheQRAndReturnsOnceConnected(t *testing.T) {
	setSelfhostedEnv(t)
	f := &fakeDeviceRPC{}
	out, errOut, err := runDevice(t, deviceServer(t, f), "device", "pair", "--output", "text")
	if err != nil {
		t.Fatalf("pair: %v (%s)", err, out)
	}
	c := f.calls()
	if len(c.starts) != 1 || c.starts[0] != "" {
		t.Fatalf("StartLink device_id = %v, want the server to auto-resolve", c.starts)
	}
	if !strings.ContainsAny(errOut, "█▀▄") {
		t.Fatalf("no terminal QR on stderr: %s", errOut)
	}
	if !strings.Contains(out, "connected") || c.linkPolls != 1 {
		t.Fatalf("pair did not stop at the connected outcome (polls=%d): %s", c.linkPolls, out)
	}
}

func TestDevicePair_JSONKeepsStdoutParseableAndFailsAnEndedAttempt(t *testing.T) {
	setSelfhostedEnv(t)
	f := &fakeDeviceRPC{outcome: "timeout"}
	out, errOut, err := runDevice(t, deviceServer(t, f), "device", "pair", "--output", "json")
	if err == nil {
		t.Fatal("a timed-out attempt must exit non-zero in json mode too")
	}
	if strings.ContainsAny(out, "█▀▄") || !strings.Contains(out, `"outcome"`) || !strings.Contains(out, "timeout") {
		t.Fatalf("stdout must carry only the JSON envelope: %s", out)
	}
	if !strings.ContainsAny(errOut, "█▀▄") {
		t.Fatalf("the QR must still reach the human on stderr: %s", errOut)
	}
}

func TestDevicePair_PhonePrintsTheCode(t *testing.T) {
	setSelfhostedEnv(t)
	f := &fakeDeviceRPC{}
	_, errOut, err := runDevice(t, deviceServer(t, f), "device", "pair", "--phone", "+62 812 3456", "--output", "text")
	if err != nil {
		t.Fatalf("pair --phone: %v", err)
	}
	if !strings.Contains(errOut, "ABCD-EFGH") || len(f.calls().phones) != 1 {
		t.Fatalf("code not printed on stderr: %s", errOut)
	}
}

func TestDeviceCreateLogoutDelete(t *testing.T) {
	setSelfhostedEnv(t)
	f := &fakeDeviceRPC{}
	boot := deviceServer(t, f)
	out, _, err := runDevice(t, boot, "device", "create", "--name", "Support", "--output", "text")
	if err != nil || !strings.Contains(out, "Created device Support ("+supportID+")") {
		t.Fatalf("create: %v %s", err, out)
	}
	if out, _, err := runDevice(t, boot, "device", "logout", "--device", "Support", "--output", "text"); err != nil || !strings.Contains(out, "Logged out device "+supportID) {
		t.Fatalf("logout: %v %s", err, out)
	}
	if got := f.calls().unlinks; len(got) != 1 || got[0] != supportID {
		t.Fatalf("Unlink ids = %v", got)
	}
	if out, _, err := runDevice(t, boot, "device", "delete", "--device", supportID, "--output", "text"); err != nil || !strings.Contains(out, "Deleted device "+supportID) {
		t.Fatalf("delete: %v %s", err, out)
	}
}

func TestDeviceLogoutAndDeleteEmitTheJSONEnvelope(t *testing.T) {
	setSelfhostedEnv(t)
	boot := deviceServer(t, &fakeDeviceRPC{})
	for _, args := range [][]string{
		{"device", "logout", "--device", supportID, "--output", "json"},
		{"device", "delete", "--device", supportID, "--output", "json"},
	} {
		out, _, err := runDevice(t, boot, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(out, `"data"`) || !strings.Contains(out, supportID) {
			t.Fatalf("%v printed no envelope: %s", args, out)
		}
	}
}

func appErr(code string, grpc codes.Code, msg string) error {
	ae := apperror.New(code, msg, grpc, &apperrorv1.ErrorDetail{Code: code})
	cerr := connect.NewError(connect.Code(grpc), ae)
	if det, err := connect.NewErrorDetail(&apperrorv1.ErrorDetail{Code: code}); err == nil {
		cerr.AddDetail(det)
	}
	return cerr
}

func TestDevicePair_MultiDeviceRefusalExitsInvalidArgument(t *testing.T) {
	setSelfhostedEnv(t)
	f := &fakeDeviceRPC{startErr: appErr("GEN004", codes.InvalidArgument, "This project has several devices; pass device_id")}
	_, _, err := runDevice(t, deviceServer(t, f), "device", "pair", "--output", "text")
	if err == nil {
		t.Fatal("the refusal must fail")
	}
	if got := ExitCodeFor(err); got != ExitInvalidArg {
		t.Fatalf("exit = %d, want %d", got, ExitInvalidArg)
	}
	if ae, ok := apperror.AsAppError(err); !ok || ae.Code() != "GEN004" {
		t.Fatalf("server code lost: %v", err)
	}
}

func TestDevicePair_NoActiveProjectExitsOnboardingRequired(t *testing.T) {
	setSelfhostedEnv(t)
	f := &fakeDeviceRPC{startErr: appErr("PRJ005", codes.FailedPrecondition, "No project was given")}
	_, _, err := runDevice(t, deviceServer(t, f), "device", "pair", "--output", "text")
	if got := ExitCodeFor(err); got != ExitOnboardingRequired {
		t.Fatalf("exit = %d, want %d (err %v)", got, ExitOnboardingRequired, err)
	}
}

func TestDevicePair_InterruptedExitsNonZero(t *testing.T) {
	setSelfhostedEnv(t)
	f := &fakeDeviceRPC{outcome: "pending"}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		for f.calls().linkPolls == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
	}()
	root := NewRootCmd(stubServerBoot, deviceServer(t, f))
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"device", "pair", "--output", "text"})
	err := root.ExecuteContext(ctx)
	if !IsPairInterruptedError(err) {
		t.Fatalf("err = %v, want a PairInterruptedError", err)
	}
	if ExitCodeFor(err) == ExitOK {
		t.Fatal("an interrupted pair must exit non-zero")
	}
}

func TestWaitForLink_ReprintsARotatedQRAndStopsOnCancel(t *testing.T) {
	f := &fakeDeviceRPC{qrs: []string{"2@one", "2@two", "2@two", "2@three"}, outcome: "pending"}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	conn := controlplane.NewClient(serveDevice(t, f), "tok")
	cmd := NewRootCmd(stubServerBoot, nil)
	errOut := &bytes.Buffer{}
	cmd.SetErr(errOut)
	go func() {
		for f.calls().linkPolls < 6 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	_, err := waitForLink(ctx, cmd, conn, supportID, "2@zero", time.Millisecond)
	if !IsPairInterruptedError(err) || errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want only a PairInterruptedError", err)
	}
	if got := strings.Count(errOut.String(), "Scan with WhatsApp"); got != 3 {
		t.Fatalf("QR printed %d times, want 3 (one, two, three; the repeat is skipped)", got)
	}
}
