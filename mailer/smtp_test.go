package mailer

import (
	"bufio"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type smtpCapture struct {
	addr string
	data chan string
}

func newSMTPServer(t *testing.T) *smtpCapture {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	c := &smtpCapture{addr: ln.Addr().String(), data: make(chan string, 1)}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		serveSMTP(conn, c.data)
	}()
	return c
}

func serveSMTP(conn net.Conn, out chan<- string) {
	r := bufio.NewReader(conn)
	reply := func(s string) { _, _ = io.WriteString(conn, s+"\r\n") }
	reply("220 test ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			reply("250 test")
		case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
			reply("250 ok")
		case cmd == "DATA":
			reply("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			out <- b.String()
			reply("250 queued")
		case cmd == "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

func smtpAt(t *testing.T, addr string) *SMTP {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return &SMTP{Cfg: Config{From: "alerts@example.com", SMTP: SMTPConfig{Host: host, Port: p}}}
}

func TestSMTP_Send_WritesHeadersAndBothBodies(t *testing.T) {
	srv := newSMTPServer(t)
	s := smtpAt(t, srv.addr)
	m := Message{
		To: "t@example.com", Subject: "hi", TextBody: "plain body", HTMLBody: "<b>rich body</b>",
		Headers: map[string]string{"List-Unsubscribe": "<https://x.test/u?t=1>", "List-Unsubscribe-Post": "List-Unsubscribe=One-Click"},
	}
	if err := s.Send(t.Context(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}
	raw := <-srv.data
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, raw)
	}
	if got := msg.Header.Get("List-Unsubscribe"); got != "<https://x.test/u?t=1>" {
		t.Errorf("List-Unsubscribe = %q", got)
	}
	if got := msg.Header.Get("List-Unsubscribe-Post"); got != "List-Unsubscribe=One-Click" {
		t.Errorf("List-Unsubscribe-Post = %q", got)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/alternative" {
		t.Fatalf("Content-Type = %q (%v), want multipart/alternative", msg.Header.Get("Content-Type"), err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var types, bodies []string
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("part: %v", err)
		}
		b, _ := io.ReadAll(p)
		types = append(types, p.Header.Get("Content-Type"))
		bodies = append(bodies, string(b))
	}
	if len(types) != 2 || !strings.HasPrefix(types[0], "text/plain") || !strings.HasPrefix(types[1], "text/html") {
		t.Fatalf("parts = %v, want text/plain then text/html", types)
	}
	if bodies[0] != "plain body" || bodies[1] != "<b>rich body</b>" {
		t.Errorf("bodies = %q", bodies)
	}
}

func TestSMTP_Send_ReturnsWhenContextCanceled(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	s := smtpAt(t, ln.Addr().String())

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Send(ctx, Message{To: "t@example.com", Subject: "s", TextBody: "b"}) }()
	conn := <-accepted
	t.Cleanup(func() { _ = conn.Close() })
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want it to wrap context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send did not return after cancel; a silent server would hang the worker")
	}
}

func TestSMTP_Send_ReturnsAtContextDeadline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	s := smtpAt(t, ln.Addr().String())
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = s.Send(ctx, Message{To: "t@example.com", Subject: "s", TextBody: "b"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("Send took %s past a 200ms deadline", time.Since(start))
	}
}

func TestBuildMIME_WritesSingleBodyWhenOnlyOneSet(t *testing.T) {
	txt := buildMIME("f@x", Message{To: "t@x", Subject: "s", TextBody: "hi"})
	if strings.Contains(txt, "multipart") || !strings.Contains(txt, "text/plain") {
		t.Errorf("text-only MIME: %s", txt)
	}
	html := buildMIME("f@x", Message{To: "t@x", Subject: "s", HTMLBody: "<b>hi</b>"})
	if strings.Contains(html, "multipart") || !strings.Contains(html, "text/html") {
		t.Errorf("html-only MIME: %s", html)
	}
}

func TestSMTP_Send_EncodesNonASCIISubjectAndBodies(t *testing.T) {
	srv := newSMTPServer(t)
	s := smtpAt(t, srv.addr)
	long := strings.Repeat("انقطع اتصال ", 120)
	m := Message{To: "t@example.com", Subject: "انقطع اتصال Sales", TextBody: long, HTMLBody: "<p>" + long + "</p>"}
	require.NoError(t, s.Send(t.Context(), m))
	raw := <-srv.data

	for _, line := range strings.Split(raw, "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line of %d bytes breaks RFC 5322 section 2.1.1", len(line))
		}
	}
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(msg.Header.Get("Subject"), "=?utf-8?q?"), "a non-ASCII subject is RFC 2047 encoded: %q", msg.Header.Get("Subject"))
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	require.NoError(t, err)
	assert.Equal(t, m.Subject, subject)

	_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	require.NoError(t, err)
	mr := multipart.NewReader(msg.Body, params["boundary"])
	p, err := mr.NextPart()
	require.NoError(t, err)
	body, err := io.ReadAll(p)
	require.NoError(t, err)
	assert.Equal(t, long, string(body), "multipart.Reader decodes quoted-printable")
}
