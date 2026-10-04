package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/db"
)

func TestLoad_DefaultsPopulate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	cfg, err := Load("", withCwdOverride(t, dir), withGenesisFallback(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Mode != ModeSelfhosted {
		t.Fatalf("mode: want %q, got %q", ModeSelfhosted, cfg.Mode)
	}
	if cfg.HTTP.Addr != ":5150" {
		t.Fatalf("http.addr: want :5150, got %q", cfg.HTTP.Addr)
	}
	if cfg.API.Enabled != true {
		t.Fatalf("api.enabled: want true, got %v", cfg.API.Enabled)
	}
	if cfg.Log.Level != "info" || cfg.Log.Format != "json" {
		t.Fatalf("log defaults wrong: %+v", cfg.Log)
	}
	if !cfg.DB.AutoMigrate {
		t.Fatalf("db.autoMigrate: want true, got false")
	}
}

func TestLoad_YAMLThenEnvOverride(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("HOME", t.TempDir())
	yaml := `
mode: selfhosted
http:
  addr: ":6000"
  baseURL: "http://localhost:6000"
genesis:
  email: "root@example.com"
  password: "correct-horse"
`
	path := writeTempYAML(t, "cfg.yaml", yaml)

	t.Setenv("OPENWA_HTTP_ADDR", ":8080")
	t.Setenv("OPENWA_HTTP_BASE_URL", "")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.Addr != ":8080" {
		t.Fatalf("env should win: got %q", cfg.HTTP.Addr)
	}
	if cfg.HTTP.BaseURL != "http://localhost:6000" {
		t.Fatalf("yaml value lost: %q", cfg.HTTP.BaseURL)
	}
	if cfg.Genesis.Email != "root@example.com" {
		t.Fatalf("genesis.email: %q", cfg.Genesis.Email)
	}
}

func TestLoad_ValidateCascades(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("HOME", t.TempDir())
	yaml := `
mode: selfhosted
genesis:
  email: "root@example.com"
  password: "x"
log:
  level: "shout"
`
	path := writeTempYAML(t, "cfg.yaml", yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "logger:") {
		t.Fatalf("expected logger validation error, got %v", err)
	}
}

func TestValidate_ModeInvariants(t *testing.T) {
	base := func() *Config {
		c := &Config{
			Mode:      ModeSelfhosted,
			DB:        validDB(),
			Genesis:   GenesisConfig{Email: "root@example.com", Password: "x"},
			Security:  validSecurity(),
			WhatsApp:  validWhatsApp(),
			Retention: Defaults().Retention,
			Media:     Defaults().Media,
		}
		c.Tenant.SingletonOrg.Slug = "default"
		c.Tenant.SingletonOrg.Name = "Default Organization"
		return c
	}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantSub string
	}{
		{
			name:    "unset mode fails",
			mutate:  func(c *Config) { c.Mode = "" },
			wantSub: "Config.Mode",
		},
		{
			name:    "unknown mode fails",
			mutate:  func(c *Config) { c.Mode = "hybrid" },
			wantSub: "oneof",
		},
		{
			name: "selfhosted without genesis or oidc is allowed (onboarding-first)",
			mutate: func(c *Config) {
				c.Genesis = GenesisConfig{}
				c.OIDC = OIDCConfig{}
			},
			wantSub: "",
		},
		{
			name: "cloud without oidc fails",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.Genesis = GenesisConfig{}
				c.OIDC = OIDCConfig{}
			},
			wantSub: "cloud requires oidc.issuer",
		},
		{
			name: "cloud without oidc clientID fails",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.Genesis = GenesisConfig{}
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com"}
			},
			wantSub: "cloud requires oidc.clientID",
		},
		{
			name: "postgres without an encryption key fails",
			mutate: func(c *Config) {
				c.Security.EncryptionKey = ""
			},
			wantSub: "OPENWA_SECURITY_ENCRYPTION_KEY",
		},
		{
			name: "cloud without an encryption key fails",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com", ClientID: "c", ClientSecret: "s"}
				c.Genesis = GenesisConfig{Email: "root@example.com"}
				c.Security.EncryptionKey = ""
			},
			wantSub: "OPENWA_SECURITY_ENCRYPTION_KEY",
		},
		{
			name: "cloud with full oidc + postgres + genesis + singleton org (no break-glass) is allowed",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com", ClientID: "c", ClientSecret: "s"}
				c.Genesis = GenesisConfig{Email: "root@example.com", Password: "s3cret", BreakGlass: true}
			},
			wantSub: "",
		},
		{
			name: "cloud with break-glass and genesis password is allowed",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com", ClientID: "c", ClientSecret: "s"}
				c.Genesis = GenesisConfig{Email: "root@example.com", Password: "s3cret", BreakGlass: true}
			},
			wantSub: "",
		},
		{
			name: "cloud with email only (no password, no break-glass) is allowed",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com", ClientID: "c", ClientSecret: "s"}
				c.Genesis = GenesisConfig{Email: "root@example.com"}
			},
			wantSub: "",
		},
		{
			name: "cloud with password but no break-glass fails",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com", ClientID: "c", ClientSecret: "s"}
				c.Genesis = GenesisConfig{Email: "root@example.com", Password: "s3cret"}
			},
			wantSub: "requires genesis.breakGlass",
		},
		{
			name: "cloud without genesis email fails",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com", ClientID: "c", ClientSecret: "s"}
				c.Genesis = GenesisConfig{}
			},
			wantSub: "requires genesis.email",
		},
		{
			name: "cloud without singleton org slug is allowed: the slug is prefill only",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com", ClientID: "c", ClientSecret: "s"}
				c.Genesis = GenesisConfig{Email: "root@example.com", Password: "s3cret", BreakGlass: true}
				c.Tenant.SingletonOrg.Slug = ""
			},
			wantSub: "",
		},
		{
			name: "cloud without singleton org name fails",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com", ClientID: "c", ClientSecret: "s"}
				c.Genesis = GenesisConfig{Email: "root@example.com", Password: "s3cret", BreakGlass: true}
				c.Tenant.SingletonOrg.Name = ""
			},
			wantSub: "singletonOrg.name",
		},
		{
			name: "selfhosted with genesis email alone is allowed",
			mutate: func(c *Config) {
				c.Genesis = GenesisConfig{Email: "root@example.com"}
			},
			wantSub: "",
		},
		{
			name: "selfhosted with genesis password alone fails",
			mutate: func(c *Config) {
				c.Genesis = GenesisConfig{Password: "x"}
			},
			wantSub: "genesis.password without genesis.email",
		},
		{
			name: "queue enabled without url fails",
			mutate: func(c *Config) {
				c.Queue.Enabled = true
				c.Queue.URL = ""
			},
			wantSub: "queue.url",
		},
		{
			name: "queue enabled with url is allowed",
			mutate: func(c *Config) {
				c.Queue.Enabled = true
				c.Queue.URL = "nats://127.0.0.1:4222"
			},
			wantSub: "",
		},
		{
			name: "queue user with password is allowed",
			mutate: func(c *Config) {
				c.Queue = QueueConfig{Enabled: true, URL: "nats://127.0.0.1:4222", User: "openwa", Password: "pw"}
			},
			wantSub: "",
		},
		{
			name: "queue user without password fails",
			mutate: func(c *Config) {
				c.Queue = QueueConfig{Enabled: true, URL: "nats://127.0.0.1:4222", User: "openwa"}
			},
			wantSub: "OPENWA_QUEUE_PASSWORD",
		},
		{
			name: "queue password without user fails",
			mutate: func(c *Config) {
				c.Queue = QueueConfig{Enabled: true, URL: "nats://127.0.0.1:4222", Password: "pw"}
			},
			wantSub: "OPENWA_QUEUE_USER",
		},
		{
			name: "queue token with user fails",
			mutate: func(c *Config) {
				c.Queue = QueueConfig{Enabled: true, URL: "nats://127.0.0.1:4222", Token: "t", User: "openwa", Password: "pw"}
			},
			wantSub: "mutually exclusive",
		},
		{
			name: "cloud with genesis password alone fails",
			mutate: func(c *Config) {
				c.Mode = ModeCloud
				c.OIDC = OIDCConfig{Issuer: "https://iss.example.com", ClientID: "c", ClientSecret: "s"}
				c.Genesis = GenesisConfig{Password: "x", BreakGlass: true}
			},
			wantSub: "genesis.password without genesis.email",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.mutate(c)
			err := c.Validate()
			if tc.wantSub == "" {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("want error containing %q, got %v", tc.wantSub, err)
			}
		})
	}
}

func TestSchedulerConfig_GetTimezone(t *testing.T) {
	tests := []struct {
		name    string
		tz      string
		want    string
		wantErr bool
	}{
		{"empty defaults to UTC", "", "UTC", false},
		{"named zone", "Asia/Jakarta", "Asia/Jakarta", false},
		{"invalid zone errors", "Mars/Olympus", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &SchedulerConfig{Timezone: tt.tz}
			loc, err := c.GetTimezone()
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetTimezone() err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if loc.String() != tt.want {
				t.Errorf("loc = %q, want %q", loc.String(), tt.want)
			}
		})
	}
}

func TestDefaults_SchedulerAndDBKeys(t *testing.T) {
	cfg := Defaults()
	if !cfg.Scheduler.Enabled {
		t.Error("scheduler.enabled must default true")
	}
	if cfg.Scheduler.Timezone != "UTC" {
		t.Errorf("scheduler.timezone = %q, want UTC", cfg.Scheduler.Timezone)
	}
	if cfg.Scheduler.ShutdownGrace != 30*time.Second {
		t.Errorf("scheduler.shutdownGrace = %s, want 30s", cfg.Scheduler.ShutdownGrace)
	}
	if cfg.DB.ConnectTimeout != 30*time.Second {
		t.Errorf("db.connectTimeout = %s, want 30s", cfg.DB.ConnectTimeout)
	}
	if cfg.DB.ConnectBackoff != 250*time.Millisecond {
		t.Errorf("db.connectBackoff = %s, want 250ms", cfg.DB.ConnectBackoff)
	}
	if cfg.DB.Health.Interval != 30*time.Second {
		t.Errorf("db.health.interval = %s, want 30s", cfg.DB.Health.Interval)
	}
	if cfg.DB.Health.Timeout != 2*time.Second {
		t.Errorf("db.health.timeout = %s, want 2s", cfg.DB.Health.Timeout)
	}
	if cfg.Queue.Enabled {
		t.Error("queue.enabled must default false")
	}
	if cfg.Queue.ConnectTimeout != 10*time.Second {
		t.Errorf("queue.connectTimeout = %s, want 10s", cfg.Queue.ConnectTimeout)
	}
}

func TestMode_IsProduction(t *testing.T) {
	if !ModeCloud.IsProduction() {
		t.Fatal("cloud must be production")
	}
	if ModeSelfhosted.IsProduction() {
		t.Fatal("selfhosted must not be production")
	}
}

func TestLoad_RequireFile_Missing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	_, err := Load(missing, WithRequireFile())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "required") && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected required-file error, got %v", err)
	}
}

func writeTempYAML(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	return p
}

func validDB() db.DBConfig {
	return db.DBConfig{DSN: "postgres://openwa:openwa@localhost:5432/openwa?sslmode=disable", Schema: "public", TablePrefix: "openwa_"}
}

func validSecurity() SecurityConfig {
	return SecurityConfig{EncryptionKey: strings.Repeat("ab", 32)}
}

func withCwdOverride(t *testing.T, dir string) Option {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return func(*loadOptions) {}
}

func withGenesisFallback(t *testing.T) Option {
	t.Helper()
	t.Setenv("OPENWA_GENESIS_EMAIL", "root@example.com")
	t.Setenv("OPENWA_GENESIS_PASSWORD", "x")
	withRequiredEnv(t)
	return func(*loadOptions) {}
}

func withRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("OPENWA_DB_DSN", validDB().DSN)
	t.Setenv("OPENWA_SECURITY_ENCRYPTION_KEY", validSecurity().EncryptionKey)
	t.Setenv("OPENWA_HTTP_BASE_URL", "http://127.0.0.1:5150")
}

func TestSchedulerConfig_Locations(t *testing.T) {
	tests := []struct {
		name     string
		cfg      SchedulerConfig
		job      string
		wantZone string
	}{
		{
			name:     "unset global falls back to UTC",
			cfg:      SchedulerConfig{},
			job:      "any-job",
			wantZone: "UTC",
		},
		{
			name:     "global applies to every job",
			cfg:      SchedulerConfig{Timezone: "Asia/Jakarta"},
			job:      "any-job",
			wantZone: "Asia/Jakarta",
		},
		{
			name: "per-job overrides global",
			cfg: SchedulerConfig{
				Timezone: "Asia/Jakarta",
				Jobs:     map[string]SchedulerJobConfig{"todo-autocomplete-stale": {Timezone: "Europe/Berlin"}},
			},
			job:      "todo-autocomplete-stale",
			wantZone: "Europe/Berlin",
		},
		{
			name: "unlisted job still gets the global",
			cfg: SchedulerConfig{
				Timezone: "Asia/Jakarta",
				Jobs:     map[string]SchedulerJobConfig{"other-job": {Timezone: "Europe/Berlin"}},
			},
			job:      "todo-autocomplete-stale",
			wantZone: "Asia/Jakarta",
		},
		{
			name: "empty per-job timezone falls back to global",
			cfg: SchedulerConfig{
				Timezone: "Asia/Jakarta",
				Jobs:     map[string]SchedulerJobConfig{"todo-autocomplete-stale": {}},
			},
			job:      "todo-autocomplete-stale",
			wantZone: "Asia/Jakarta",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, err := tt.cfg.Locations()
			require.NoError(t, err)
			require.Equal(t, tt.wantZone, loc(tt.job).String())
		})
	}
}

func TestSchedulerConfig_Locations_RejectsBadZones(t *testing.T) {
	tests := []struct {
		name string
		cfg  SchedulerConfig
	}{
		{"bad global", SchedulerConfig{Timezone: "Mars/Olympus"}},
		{"bad per-job", SchedulerConfig{Jobs: map[string]SchedulerJobConfig{"j": {Timezone: "Mars/Olympus"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.cfg.Locations()
			require.Error(t, err, "a bad IANA name must fail at boot, not silently fall back to UTC")
		})
	}
}

func TestSchedulerConfig_Locations_NilReceiverIsUTC(t *testing.T) {
	var cfg *SchedulerConfig
	loc, err := cfg.Locations()
	require.NoError(t, err)
	require.Equal(t, "UTC", loc("any").String())
}

func TestDefaults_FirstRunSlugsAreGenerated(t *testing.T) {
	cfg := Defaults()
	require.Empty(t, cfg.Tenant.SingletonOrg.Slug, "a blank tenant.singletonOrg.slug means generate one at first run")
	require.Empty(t, cfg.Tenant.PersonalProjectSlug, "a blank tenant.personalProjectSlug means generate one at first run")
}

func TestLoad_FirstRunSlugsHonourEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENWA_TENANT_SINGLETON_ORG_SLUG", "acme")
	t.Setenv("OPENWA_TENANT_PERSONAL_PROJECT_SLUG", "web")
	cfg, err := Load("", withCwdOverride(t, t.TempDir()), withGenesisFallback(t))
	require.NoError(t, err)
	require.Equal(t, "acme", cfg.Tenant.SingletonOrg.Slug, "a configured slug still wins over generation")
	require.Equal(t, "web", cfg.Tenant.PersonalProjectSlug)
}

func validSelfhosted() *Config {
	c := &Config{
		Mode:      ModeSelfhosted,
		DB:        validDB(),
		Genesis:   GenesisConfig{Email: "root@example.com", Password: "x"},
		Security:  validSecurity(),
		WhatsApp:  validWhatsApp(),
		Retention: Defaults().Retention,
		Media:     Defaults().Media,
	}
	c.Tenant.SingletonOrg.Slug = "default"
	c.Tenant.SingletonOrg.Name = "Default Organization"
	return c
}

func TestValidate_EncryptionKeyIsAlwaysRequired(t *testing.T) {
	t.Parallel()
	cfg := validSelfhosted()
	cfg.Security.EncryptionKey = ""
	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "OPENWA_SECURITY_ENCRYPTION_KEY")
}

func validWhatsApp() WhatsAppConfig {
	return Defaults().WhatsApp
}

func TestDefaults_BrandName(t *testing.T) {
	cfg := Defaults()
	require.Equal(t, "OpenWA", cfg.Brand.Name)
}

func TestEnvKeys_BrandNameIsNotBootstrap(t *testing.T) {
	var found bool
	for _, k := range WalkEnvKeys(EnvPrefix) {
		if k.YAML != "brand.name" {
			continue
		}
		found = true
		require.NotContains(t, k.Awareness, "bootstrap")
		require.NotContains(t, k.Awareness, "required")
	}
	require.True(t, found, "brand.name must be a known env key")
}

func TestValidateWhatsAppLease(t *testing.T) {
	t.Parallel()
	ok := WhatsAppConfig{Engine: "whatsmeow", ClientName: "OpenWA", LeaseTTL: 45 * time.Second, LeaseInterval: 15 * time.Second, LinkTimeout: 3 * time.Minute}
	cases := []struct {
		name string
		mut  func(*WhatsAppConfig)
		bad  bool
	}{
		{"defaults", func(*WhatsAppConfig) {}, false},
		{"ttl equals two intervals", func(w *WhatsAppConfig) { w.LeaseTTL = 30 * time.Second }, true},
		{"zero interval", func(w *WhatsAppConfig) { w.LeaseInterval = 0 }, true},
		{"zero link timeout", func(w *WhatsAppConfig) { w.LinkTimeout = 0 }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := ok
			tc.mut(&w)
			err := validateWhatsAppLease(&Config{WhatsApp: w})
			if tc.bad {
				require.Error(t, err)
				require.Contains(t, err.Error(), "whatsapp.")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestDefaults_WhatsAppRuntime(t *testing.T) {
	t.Parallel()
	c := Defaults()
	require.Equal(t, 45*time.Second, c.WhatsApp.LeaseTTL)
	require.Equal(t, 15*time.Second, c.WhatsApp.LeaseInterval)
	require.Equal(t, 3*time.Minute, c.WhatsApp.LinkTimeout)
	require.Zero(t, c.WhatsApp.ParkFor)
	require.Equal(t, 1024, c.WhatsApp.InboundQueueSize)
}

func TestValidate_QueueCredentialErrorsAreTyped(t *testing.T) {
	tests := []struct {
		name  string
		queue QueueConfig
		is    func(error) bool
	}{
		{"conflict", QueueConfig{Enabled: true, URL: "nats://x:4222", Token: "t", Password: "pw"}, IsQueueCredentialsConflictError},
		{"incomplete", QueueConfig{Enabled: true, URL: "nats://x:4222", User: "u"}, IsQueueCredentialsIncompleteError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validSelfhosted()
			c.Queue = tt.queue
			err := validateInvariants(c)
			if !tt.is(err) {
				t.Fatalf("want typed %s error, got %v", tt.name, err)
			}
		})
	}
	if IsQueueCredentialsConflictError(errors.New("other")) || IsQueueCredentialsIncompleteError(errors.New("other")) {
		t.Fatal("helpers must reject unrelated errors")
	}
}
