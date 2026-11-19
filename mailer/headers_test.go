package mailer

import (
	"context"
	"net/http"
	"testing"
)

func TestMessage_ValidateChecksHeaders(t *testing.T) {
	ok := Message{From: "f@x", To: "t@x", Subject: "s", Headers: map[string]string{"List-Unsubscribe": "<https://x.test/u>"}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid header refused: %v", err)
	}
	cases := []struct {
		name    string
		headers map[string]string
	}{
		{"crlf in value", map[string]string{"List-Unsubscribe": "<https://x>\r\nBcc: a@evil.test"}},
		{"lf in name", map[string]string{"X-A\nBcc": "v"}},
		{"colon in name", map[string]string{"X-A:B": "v"}},
		{"space in name", map[string]string{"X A": "v"}},
		{"empty name", map[string]string{"": "v"}},
		{"driver-owned name", map[string]string{"bcc": "a@evil.test"}},
		{"driver-owned content type", map[string]string{"Content-Type": "text/plain"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := ok
			m.Headers = tc.headers
			if err := m.Validate(); !IsHeaderInjectionError(err) {
				t.Fatalf("want HeaderInjectionError, got %v", err)
			}
		})
	}
}

func TestIdempotencyKey_RoundTrip(t *testing.T) {
	if _, ok := IdempotencyKey(context.Background()); ok {
		t.Fatal("empty ctx reports a key")
	}
	if _, ok := IdempotencyKey(WithIdempotencyKey(context.Background(), "")); ok {
		t.Fatal("empty key counts as a key")
	}
	key, ok := IdempotencyKey(WithIdempotencyKey(context.Background(), "entry-1"))
	if !ok || key != "entry-1" {
		t.Fatalf("key = %q, %v", key, ok)
	}
}

func TestResend_Send_UsesIdempotencyKeyFromContext(t *testing.T) {
	rs := newResendServer(t, `{}`, http.StatusOK)
	r := newResendAt(t, rs, ResendConfig{APIKey: "re_x"})
	ctx := WithIdempotencyKey(t.Context(), "entry-42")
	for range 2 {
		if err := r.Send(ctx, validMessage()); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	for i, c := range rs.calls {
		if got := c.headers.Get("Idempotency-Key"); got != "entry-42" {
			t.Errorf("call %d Idempotency-Key = %q, want entry-42", i, got)
		}
	}
}

func TestResend_Send_FreshKeyWithoutContextKey(t *testing.T) {
	rs := newResendServer(t, `{}`, http.StatusOK)
	r := newResendAt(t, rs, ResendConfig{APIKey: "re_x"})
	for range 2 {
		if err := r.Send(t.Context(), validMessage()); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	a, b := rs.calls[0].headers.Get("Idempotency-Key"), rs.calls[1].headers.Get("Idempotency-Key")
	if a == "" || a == b {
		t.Fatalf("keys = %q, %q; want two distinct generated keys", a, b)
	}
}

func TestResend_Send_PostsHeaders(t *testing.T) {
	rs := newResendServer(t, `{}`, http.StatusOK)
	r := newResendAt(t, rs, ResendConfig{APIKey: "re_x"})
	m := validMessage()
	m.Headers = map[string]string{"List-Unsubscribe": "<https://x.test/u>", "List-Unsubscribe-Post": "List-Unsubscribe=One-Click"}
	if err := r.Send(t.Context(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := rs.last().body.Headers
	if got["List-Unsubscribe"] != "<https://x.test/u>" || got["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Fatalf("headers = %v", got)
	}
}

func TestResend_Send_RejectsBadHeaderBeforeCall(t *testing.T) {
	rs := newResendServer(t, `{}`, http.StatusOK)
	r := newResendAt(t, rs, ResendConfig{APIKey: "re_x"})
	m := validMessage()
	m.Headers = map[string]string{"X-A": "v\r\nBcc: a@evil.test"}
	if err := r.Send(t.Context(), m); !IsHeaderInjectionError(err) {
		t.Fatalf("err = %v, want HeaderInjectionError", err)
	}
	if len(rs.calls) != 0 {
		t.Fatalf("calls = %d, want 0", len(rs.calls))
	}
}

func TestResendAPIError_ExposesStatusAndRetryDelay(t *testing.T) {
	e := &ResendAPIError{StatusCode: 429, RetryAfter: 7}
	if e.HTTPStatus() != 429 || e.RetryDelay() != 7 {
		t.Fatalf("HTTPStatus=%d RetryDelay=%v", e.HTTPStatus(), e.RetryDelay())
	}
}
