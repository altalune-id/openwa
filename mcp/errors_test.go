package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"altalune.id/template/mcp"
)

// NOTE: mirrors the append-only registry shape in internal/apperror/codes.go.
var codePattern = regexp.MustCompile(`^[A-Z]{3}[0-9]{3}$`)

func unmappedPayload(t *testing.T, opts ...mcp.Option) mcp.ErrorPayload {
	t.Helper()

	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolSpec{
		Name:    "blog_list",
		Scope:   "posts:read",
		Handler: func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, errors.New("boom") },
	}, nil)

	opts = append(opts,
		mcp.WithRegistry(reg),
		mcp.WithScopes(scopesFromContext),
		mcp.WithLogger(slog.New(slog.DiscardHandler)),
	)
	res, err := connect(t, mcp.NewServer(opts...), "posts:read").
		CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "blog_list"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	var payload mcp.ErrorPayload
	if err := json.Unmarshal([]byte(textOf(t, res)), &payload); err != nil {
		t.Fatalf("tool error content is not an ErrorPayload: %v", err)
	}
	return payload
}

func TestUnmappedCodeIsARegistryCode(t *testing.T) {
	if !codePattern.MatchString(mcp.DefaultUnmappedCode) {
		t.Fatalf("DefaultUnmappedCode = %q, want a <DOM><NNN> registry code", mcp.DefaultUnmappedCode)
	}
	if got := unmappedPayload(t).Code; got != mcp.DefaultUnmappedCode {
		t.Errorf("unmapped payload Code = %q, want %q", got, mcp.DefaultUnmappedCode)
	}
}

// TestWithUnmappedCodeOverridesTheDefault pins the escape hatch a fork with its own code registry needs.
func TestWithUnmappedCodeOverridesTheDefault(t *testing.T) {
	tests := []struct {
		name string
		opt  mcp.Option
		want string
	}{
		{"fork code", mcp.WithUnmappedCode("YSK900"), "YSK900"},
		{"empty is ignored", mcp.WithUnmappedCode(""), mcp.DefaultUnmappedCode},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := unmappedPayload(t, tc.opt).Code; got != tc.want {
				t.Errorf("unmapped payload Code = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestErrorPayloadCarriesTheCanonicalEnvelope pins the apperror.v1.ErrorDetail field set every surface answers with.
func TestErrorPayloadCarriesTheCanonicalEnvelope(t *testing.T) {
	full := mcp.ErrorPayload{
		Code:      "BLG404",
		Message:   "post not found",
		Meta:      map[string]string{"post_id": "01H8"},
		RequestID: "V1StGXR8",
		TraceID:   "4bf92f3577b34da6a3ce929d0e0e4736",
	}

	raw, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for key, want := range map[string]any{
		"code":       "BLG404",
		"message":    "post not found",
		"request_id": "V1StGXR8",
		"trace_id":   "4bf92f3577b34da6a3ce929d0e0e4736",
	} {
		if wire[key] != want {
			t.Errorf("wire[%q] = %v, want %v", key, wire[key], want)
		}
	}
}

func TestErrorPayloadOmitsTheCorrelationFieldsWhenEmpty(t *testing.T) {
	raw, err := json.Marshal(mcp.ErrorPayload{Code: "BLG404", Message: "post not found"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"request_id", "trace_id", "meta"} {
		if _, present := wire[key]; present {
			t.Errorf("wire carries %q with no value; it must be omitempty", key)
		}
	}
}
