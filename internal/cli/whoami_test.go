package cli

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"altalune.id/openwa/internal/boot"
	"altalune.id/openwa/internal/controlplane"
	"altalune.id/openwa/internal/platform/config"
)

func runCLI(t *testing.T, bootClient ClientBootFn, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd(stubServerBoot, bootClient)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return buf.String(), err
}

func TestWhoami_NotSignedIn(t *testing.T) {
	setSelfhostedEnv(t)
	clearCLIEnv(t)
	_, err := runCLI(t, stubClientBoot, "auth", "whoami")
	if !IsNotSignedInError(err) || ExitCodeFor(err) != ExitUnauth {
		t.Fatalf("err = %v (exit %d), want NotSignedInError exiting 2", err, ExitCodeFor(err))
	}
}

func TestWhoami_AfterLogoutWithNoServerURLIsNotSignedIn(t *testing.T) {
	sessPath := setSelfhostedEnv(t)
	clearCLIEnv(t)
	t.Setenv("OPENWA_HTTP_BASEURL", "")
	t.Setenv("OPENWA_DATAPLANE_ENABLED", "false")
	writeRemoteProfile(t, sessPath, "prod", profile{URL: "https://app.example.com"})
	if _, err := runCLI(t, stubClientBoot, "--profile", "prod", "auth", "logout"); err != nil {
		t.Fatalf("logout: %v", err)
	}

	_, err := runCLI(t, stubClientBoot, "--profile", "prod", "auth", "whoami")
	if !IsNotSignedInError(err) || ExitCodeFor(err) != ExitUnauth {
		t.Fatalf("err = %v (exit %d), want NotSignedInError exiting 2", err, ExitCodeFor(err))
	}
	if !strings.Contains(err.Error(), "run `openwa auth login --url <server> --profile prod`") {
		t.Fatalf("err = %v, want the remote sign-in hint", err)
	}
}

func TestWhoami_LocalProfilePrintsTheStoredPrincipal(t *testing.T) {
	sessPath := setSelfhostedEnv(t)
	clearCLIEnv(t)
	writeRemoteProfile(t, sessPath, defaultProfile, profile{Principal: principalView{Source: "genesis", Email: "who@here"}})

	out, err := runCLI(t, stubClientBoot, "--output", "json", "auth", "whoami")
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if !strings.Contains(out, `"data"`) || !strings.Contains(out, "who@here") || !strings.Contains(out, `"source": "genesis"`) {
		t.Fatalf("output = %q", out)
	}
}

func TestWhoami_RemoteProfileAsksTheServerAndRefreshesThePrincipal(t *testing.T) {
	sessPath := setSelfhostedEnv(t)
	clearCLIEnv(t)
	f := newRemoteFixture(t, testCLIClient)
	f.store = profileStore{path: sessPath}
	p := f.signedIn(t, "prod")
	p.Principal = principalView{Email: "stale@example.com"}
	writeRemoteProfile(t, sessPath, "prod", p)

	out, err := runCLI(t, boot.BootClient, "--profile", "prod", "--output", "text", "auth", "whoami")
	if err != nil {
		t.Fatalf("whoami: %v (%s)", err, out)
	}
	if !strings.Contains(out, testEmail) || !strings.Contains(out, f.app.projectID) || !strings.Contains(out, "prod") {
		t.Fatalf("output = %q", out)
	}
	if !strings.Contains(out, "source:     token") {
		t.Fatalf("output = %q, want the server's principal source", out)
	}
	if got := f.profile(t, "prod").Principal.Email; got != testEmail {
		t.Fatalf("stored principal email = %q, want whoami's", got)
	}
}

func TestWhoami_ExplicitTokenLeavesProfilesAlone(t *testing.T) {
	sessPath := setSelfhostedEnv(t)
	clearCLIEnv(t)
	f := newRemoteFixture(t, testCLIClient)
	f.store = profileStore{path: sessPath}
	p := f.signedIn(t, defaultProfile)
	p.Principal = principalView{Email: "stale@example.com"}
	writeRemoteProfile(t, sessPath, defaultProfile, p)

	out, err := runCLI(t, boot.BootClient, "--url", f.app.URL, "--token", p.Token.Access, "--output", "json", "auth", "whoami")
	if err != nil {
		t.Fatalf("whoami: %v (%s)", err, out)
	}
	if !strings.Contains(out, testEmail) {
		t.Fatalf("output = %q", out)
	}
	if got := f.profile(t, defaultProfile).Principal.Email; got != "stale@example.com" {
		t.Fatalf("an explicit token rewrote the profile: %q", got)
	}

	_, err = runCLI(t, boot.BootClient, "--url", f.app.URL, "--token", "not-a-token", "auth", "whoami")
	if ExitCodeFor(err) != ExitUnauth || !strings.Contains(err.Error(), "auth login") {
		t.Fatalf("a rejected token: err = %v (exit %d), want exit 2 with a sign-in hint", err, ExitCodeFor(err))
	}
}

func TestLogin_RefusesATokenAndLocalFlagsWithURL(t *testing.T) {
	setSelfhostedEnv(t)
	clearCLIEnv(t)
	for _, args := range [][]string{
		{"auth", "login", "--token", "tok"},
		{"auth", "login", "--url", "https://app.example.com", "--admin"},
		{"auth", "login", "--url", "https://app.example.com", "--email", "a@b.co"},
	} {
		if _, err := runCLI(t, stubClientBoot, args...); ExitCodeFor(err) != ExitInvalidArg {
			t.Fatalf("%v: err = %v, want exit %d", args, err, ExitInvalidArg)
		}
	}
}

func TestLogout_CommandRemovesALocalProfile(t *testing.T) {
	sessPath := setSelfhostedEnv(t)
	clearCLIEnv(t)
	writeRemoteProfile(t, sessPath, defaultProfile, profile{Principal: principalView{Email: "z@z"}})

	out, err := runCLI(t, stubClientBoot, "auth", "logout")
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !strings.Contains(out, "Signed out (profile default)") {
		t.Fatalf("output = %q", out)
	}
	if p, _ := (profileStore{path: sessPath}).load(defaultProfile); p != nil {
		t.Fatal("the profile survived logout")
	}
}

func TestLogin_LocalSavesALocalProfileTheInProcessCommandsUse(t *testing.T) {
	sessPath := setSelfhostedEnv(t)
	clearCLIEnv(t)
	usePostgres(t)
	bootFn := func(ctx context.Context, cfg *config.Config, _ ...boot.Option) (*boot.Server, error) {
		return boot.BootServer(ctx, cfg)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("hunter2\n"); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	stdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = stdin })

	root := NewRootCmd(bootFn, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"--profile", "ops", "auth", "login", "--email", "root@example.com", "--password-stdin"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("login: %v (%s)", err, buf.String())
	}
	if !strings.Contains(buf.String(), "profile ops") {
		t.Fatalf("output = %q", buf.String())
	}
	p, err := (profileStore{path: sessPath}).load("ops")
	if err != nil || p == nil || p.URL != "" || p.Token != nil || p.Principal.Email != "root@example.com" || p.Principal.UserID == "" {
		t.Fatalf("local profile = %+v, %v", p, err)
	}

	root = NewRootCmd(bootFn, stubClientBoot)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"--profile", "ops", "--output", "json", "org", "list"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("org list as the local profile: %v (%s)", err, buf.String())
	}
}

func TestRemoteCommands_LoadWithoutServerConfig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HOME", dir)
	clearCLIEnv(t)
	for _, key := range []string{"db.dsn", "http.baseURL", "security.encryptionKey", "genesis.email", "genesis.password", "mode"} {
		t.Setenv(config.EnvVar(key), "")
	}
	t.Setenv(config.EnvVar("session.path"), filepath.Join(dir, "session.json"))
	devices := serveDevice(t, &fakeDeviceRPC{})
	blog := newRecordingServer(t, http.StatusOK, `{"posts":[]}`)
	bootClient := func(_ context.Context, cfg *config.Config, _ string, token string) (*boot.Client, error) {
		return &boot.Client{Cfg: cfg, Conn: controlplane.NewClient(devices, token)}, nil
	}

	for _, args := range [][]string{
		{"auth", "logout"},
		{"--url", devices, "--token", "tok", "device", "list", "--output", "json"},
	} {
		if _, err := runCLI(t, bootClient, args...); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
	if _, err := runBlog(t, "--url", blog.URL, "--token", "key_secret", "--org", "acme", "--project", "main", "blog", "list", "--output", "json"); err != nil {
		t.Errorf("blog list: %v", err)
	}
	if _, err := runCLI(t, bootClient, "auth", "whoami"); !IsNotSignedInError(err) {
		t.Errorf("auth whoami: err = %v, want NotSignedInError rather than a config error", err)
	}
	for _, args := range [][]string{{"auth", "login", "--email", "a@b.co", "--password-stdin"}, {"org", "list"}} {
		if _, err := runCLI(t, bootClient, args...); err == nil || !strings.Contains(err.Error(), "DSN") {
			t.Errorf("%v: err = %v, want the server config validated", args, err)
		}
	}
}

func TestNotSignedInError_HintMatchesTheProfile(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  NotSignedInError
		want string
	}{
		{"a known server", NotSignedInError{Profile: "prod", URL: "https://app.example.com"}, "run `openwa auth login --url https://app.example.com --profile prod`"},
		{"an unknown server", NotSignedInError{Profile: "prod"}, "run `openwa auth login --url <server> --profile prod`"},
		{"a local command", NotSignedInError{Profile: "ops", Local: true}, "run `openwa auth login --profile ops` without --url"},
		{"a remote profile on a local command", NotSignedInError{Profile: "prod", RemoteURL: "https://app.example.com"},
			"profile prod is signed in to https://app.example.com, but this command runs against the local database; run it on that server, signed in there"},
	} {
		if got := tc.err.Error(); !strings.Contains(got, tc.want) {
			t.Errorf("%s: %q does not contain %q", tc.name, got, tc.want)
		}
	}
}
