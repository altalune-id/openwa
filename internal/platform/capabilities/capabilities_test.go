package capabilities

import (
	"testing"

	"altalune.id/openwa/internal/platform/config"
)

func TestFrom_SelfhostedGenesis(t *testing.T) {
	c := config.Defaults()
	c.Genesis.Email, c.Genesis.Password = "a@b", "p"
	caps := From(c)
	if caps.PublicSignup {
		t.Error("selfhosted must not allow public signup")
	}
	if !caps.LocalIdentity {
		t.Error("expected LocalIdentity")
	}
	if caps.ExternalIdentity {
		t.Error("unexpected ExternalIdentity")
	}
	if caps.OrgCreation {
		t.Error("selfhosted must not allow org creation")
	}
}

func TestFrom_CloudOIDC(t *testing.T) {
	c := config.Defaults()
	c.Mode = config.ModeCloud
	c.OIDC.Issuer, c.OIDC.ClientID = "https://x", "id"
	caps := From(c)
	if !caps.PublicSignup {
		t.Error("cloud must allow public signup")
	}
	if !caps.ExternalIdentity {
		t.Error("expected ExternalIdentity")
	}
	if caps.LocalIdentity {
		t.Error("cloud must not have LocalIdentity")
	}
	if !caps.OrgCreation {
		t.Error("cloud must allow org creation")
	}
}

func TestFrom_TokenAuth(t *testing.T) {
	c := config.Defaults()
	c.Genesis.Email, c.Genesis.Password = "a@b", "p"
	c.Tokens.Issuer = "https://idp"
	caps := From(c)
	if !caps.TokenAuth {
		t.Error("expected TokenAuth when tokens.issuer set")
	}
}

func TestFrom_MailEnabled_ConsoleWithoutSMTP(t *testing.T) {
	c := config.Defaults()
	c.Genesis.Email, c.Genesis.Password = "a@b", "p"
	caps := From(c)
	if caps.MailEnabled {
		t.Error("console-only should not count as MailEnabled")
	}
}

func TestFrom_MailEnabled_SMTP(t *testing.T) {
	c := config.Defaults()
	c.Mail.Driver = "smtp"
	c.Mail.SMTP.Host = "smtp.example.com"
	caps := From(c)
	if !caps.MailEnabled {
		t.Error("SMTP with host should be enabled")
	}
}

func TestFrom_MailEnabled_OnlyForADeliveringDriver(t *testing.T) {
	for _, tc := range []struct {
		driver, host string
		want         bool
	}{
		{"", "", false},
		{"console", "", false},
		{"console", "smtp.example.com", false},
		{"smtp", "smtp.example.com", true},
		{"resend", "", true},
	} {
		c := config.Defaults()
		c.Mail.Driver, c.Mail.SMTP.Host = tc.driver, tc.host
		if got := From(c).MailEnabled; got != tc.want {
			t.Errorf("driver %q host %q: MailEnabled = %v, want %v", tc.driver, tc.host, got, tc.want)
		}
	}
}

func TestFrom_APIEnabled(t *testing.T) {
	c := config.Defaults()
	if !From(c).APIEnabled {
		t.Error("API should be enabled by default")
	}
	c.API.Enabled = false
	if From(c).APIEnabled {
		t.Error("APIEnabled should reflect config")
	}
}
