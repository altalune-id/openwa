// Package mailer sends transactional email. Console writes to a writer, SMTP uses STARTTLS, Resend posts to the Resend HTTP API.
package mailer

import (
	"context"
	"errors"
	"fmt"
	"net/textproto"
	"strconv"
	"strings"
)

// Config is the input mailer needs to construct a driver.
type Config struct {
	Driver string
	From   string
	SMTP   SMTPConfig
	Resend ResendConfig
}

// SMTPConfig configures the SMTP driver used by Config.
type SMTPConfig struct {
	Host string
	Port int
	User string
	Pass string
	TLS  bool
}

// ResendConfig configures the Resend driver used by Config.
type ResendConfig struct {
	APIKey      string
	Endpoint    string
	MaxAttempts int
}

// Message is one email being sent.
type Message struct {
	To       string
	From     string
	Subject  string
	TextBody string
	HTMLBody string
	// Headers are extra header fields, such as List-Unsubscribe; names the driver writes are refused.
	Headers map[string]string
}

// Validate rejects CR or LF in any header field (RFC 5322 section 2.2.3), a malformed extra header name, and an extra header the driver writes itself.
func (m Message) Validate() error {
	for _, f := range []struct {
		name, val string
	}{
		{"From", m.From},
		{"To", m.To},
		{"Subject", m.Subject},
	} {
		if strings.ContainsAny(f.val, "\r\n") {
			return &HeaderInjectionError{Field: f.name}
		}
	}
	for name, val := range m.Headers {
		if !validHeaderName(name) || strings.ContainsAny(val, "\r\n") || driverHeaders[textproto.CanonicalMIMEHeaderKey(name)] {
			return &HeaderInjectionError{Field: "header " + strconv.Quote(name)}
		}
	}
	return nil
}

//nolint:gochecknoglobals // Immutable lookup table.
var driverHeaders = map[string]bool{
	"From": true, "To": true, "Cc": true, "Bcc": true, "Subject": true, "Date": true, "Message-Id": true,
	"Mime-Version": true, "Content-Type": true, "Content-Transfer-Encoding": true,
}

// RFC 5322 section 3.6.8
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := range len(name) {
		if c := name[i]; c < 33 || c > 126 || c == ':' {
			return false
		}
	}
	return true
}

// HeaderInjectionError is returned when a Message field that becomes an SMTP header contains CR or LF.
type HeaderInjectionError struct {
	Field string
}

func (e *HeaderInjectionError) Error() string {
	return fmt.Sprintf("mailer: header injection in %s: CR/LF not allowed", e.Field)
}

// IsHeaderInjectionError reports whether err (or anything it wraps) is a HeaderInjectionError.
func IsHeaderInjectionError(err error) bool {
	var target *HeaderInjectionError
	return errors.As(err, &target)
}

// MissingConfigError is returned when a required mailer config field is empty.
type MissingConfigError struct {
	Field string
}

func (e *MissingConfigError) Error() string {
	return fmt.Sprintf("mailer: %s required", e.Field)
}

// IsMissingConfigError reports whether err (or anything it wraps) is a MissingConfigError.
func IsMissingConfigError(err error) bool {
	var target *MissingConfigError
	return errors.As(err, &target)
}

// InvalidConfigError is returned when a mailer config field holds an unusable value.
type InvalidConfigError struct {
	Field  string
	Value  string
	Reason string
}

func (e *InvalidConfigError) Error() string {
	return fmt.Sprintf("mailer: %s %q: %s", e.Field, e.Value, e.Reason)
}

// IsInvalidConfigError reports whether err (or anything it wraps) is an InvalidConfigError.
func IsInvalidConfigError(err error) bool {
	var target *InvalidConfigError
	return errors.As(err, &target)
}

type idempotencyKeyCtx struct{}

// WithIdempotencyKey returns ctx carrying key, which drivers that support it send so a retried Send delivers once.
func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempotencyKeyCtx{}, key)
}

// IdempotencyKey returns the key WithIdempotencyKey put on ctx; an empty key counts as none.
func IdempotencyKey(ctx context.Context) (string, bool) {
	key, _ := ctx.Value(idempotencyKeyCtx{}).(string)
	return key, key != ""
}

// Mailer sends a Message via whichever driver Config selected.
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// New builds a Mailer from cfg. Empty driver falls back to console.
func New(cfg Config) (Mailer, error) {
	if cfg.From == "" {
		return nil, &MissingConfigError{Field: "from"}
	}
	switch cfg.Driver {
	case "", "console":
		return &Console{From: cfg.From}, nil
	case "smtp":
		if cfg.SMTP.Host == "" {
			return nil, &MissingConfigError{Field: "smtp.host"}
		}
		return &SMTP{Cfg: cfg}, nil
	case "resend":
		r, err := NewResend(cfg)
		if err != nil {
			// NOTE: returning NewResend directly would hand back a non-nil Mailer holding a nil *Resend.
			return nil, err
		}
		return r, nil
	default:
		return nil, &InvalidConfigError{Field: "driver", Value: cfg.Driver, Reason: "unknown driver"}
	}
}
