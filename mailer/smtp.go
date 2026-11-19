package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"time"
)

// SMTPTimeout bounds one SMTP transaction when ctx carries no earlier deadline.
const SMTPTimeout = 10 * time.Second

// SMTP sends via net/smtp, honouring cfg.SMTP for auth/host.
type SMTP struct {
	Cfg Config
}

// Send delivers m via SMTP as one transaction bounded by ctx and SMTPTimeout; STARTTLS is used when the server offers it.
func (s *SMTP) Send(ctx context.Context, m Message) error {
	if m.From == "" {
		m.From = s.Cfg.From
	}
	// SECURITY: reject CR/LF in every header field (RFC 5322 sec 2.2.3) so callers cannot inject extra headers or split the body. Validates m itself, so a field added to Message is never silently left unchecked.
	if err := m.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, SMTPTimeout)
	defer cancel()

	addr := net.JoinHostPort(s.Cfg.SMTP.Host, strconv.Itoa(s.Cfg.SMTP.Port))
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return smtpErr(ctx, "dial", err)
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	return s.deliver(ctx, conn, m)
}

func (s *SMTP) deliver(ctx context.Context, conn net.Conn, m Message) error {
	c, err := smtp.NewClient(conn, s.Cfg.SMTP.Host)
	if err != nil {
		return smtpErr(ctx, "greeting", err)
	}
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: s.Cfg.SMTP.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return smtpErr(ctx, "starttls", err)
		}
	}
	if s.Cfg.SMTP.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.Cfg.SMTP.User, s.Cfg.SMTP.Pass, s.Cfg.SMTP.Host)); err != nil {
			return smtpErr(ctx, "auth", err)
		}
	}
	if err := c.Mail(m.From); err != nil {
		return smtpErr(ctx, "mail from", err)
	}
	if err := c.Rcpt(m.To); err != nil {
		return smtpErr(ctx, "rcpt to", err)
	}
	w, err := c.Data()
	if err != nil {
		return smtpErr(ctx, "data", err)
	}
	if _, err := w.Write([]byte(buildMIME(m.From, m))); err != nil {
		return smtpErr(ctx, "data", err)
	}
	if err := w.Close(); err != nil {
		return smtpErr(ctx, "data", err)
	}
	// NOTE: the server accepted the message at the end of DATA, so a failed QUIT must not trigger a resend.
	_ = c.Quit()
	return nil
}

func smtpErr(ctx context.Context, step string, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return fmt.Errorf("mailer: smtp %s: %w", step, errors.Join(cerr, err))
	}
	return fmt.Errorf("mailer: smtp %s: %w", step, err)
}

// SECURITY: strips CR/LF so a header value cannot inject headers or split the body (RFC 5322 2.2.3).
func stripCRLF(v string) string {
	if !strings.ContainsAny(v, "\r\n") {
		return v
	}
	return strings.NewReplacer("\r", "", "\n", "").Replace(v)
}

func buildMIME(from string, m Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", stripCRLF(from))
	fmt.Fprintf(&b, "To: %s\r\n", stripCRLF(m.To))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", stripCRLF(m.Subject)))
	for _, name := range slices.Sorted(maps.Keys(m.Headers)) {
		fmt.Fprintf(&b, "%s: %s\r\n", stripCRLF(name), stripCRLF(m.Headers[name]))
	}
	b.WriteString("MIME-Version: 1.0\r\n")
	if m.TextBody == "" || m.HTMLBody == "" {
		ct, body := "text/plain", m.TextBody
		if m.HTMLBody != "" {
			ct, body = "text/html", m.HTMLBody
		}
		fmt.Fprintf(&b, "Content-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", ct)
		writeQP(&b, body)
		return b.String()
	}
	mw := multipart.NewWriter(&b)
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", mw.Boundary())
	for _, p := range []struct{ ct, body string }{{"text/plain", m.TextBody}, {"text/html", m.HTMLBody}} {
		pw, _ := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {p.ct + "; charset=utf-8"}, "Content-Transfer-Encoding": {"quoted-printable"}})
		writeQP(pw, p.body)
	}
	_ = mw.Close()
	return b.String()
}

func writeQP(w io.Writer, body string) {
	qw := quotedprintable.NewWriter(w)
	_, _ = qw.Write([]byte(body))
	_ = qw.Close()
}
