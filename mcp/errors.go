package mcp

import (
	"errors"
	"fmt"
)

// DefaultUnmappedCode is the ErrorPayload.Code a tool failure no ErrorMapper claimed answers with, unless WithUnmappedCode overrides it.
const DefaultUnmappedCode = "GEN900"

const unmappedMessage = "unexpected error"

// ErrorPayload is the JSON body of a failed tool call, mirroring the apperror.v1.ErrorDetail envelope every other surface answers with.
type ErrorPayload struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Meta      map[string]string `json:"meta,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
	TraceID   string            `json:"trace_id,omitempty"`
}

// ScopeDeniedError reports a tool call whose caller did not carry the tool's required scope.
type ScopeDeniedError struct {
	Tool  string
	Scope string
}

func (e *ScopeDeniedError) Error() string {
	return fmt.Sprintf("mcp: tool %q requires scope %q", e.Tool, e.Scope)
}

// IsScopeDeniedError reports whether err is a ScopeDeniedError.
func IsScopeDeniedError(err error) bool {
	var target *ScopeDeniedError
	return errors.As(err, &target)
}

// ScopeUndeclaredError reports a tool registered without a Scope; such a tool is always denied.
type ScopeUndeclaredError struct {
	Tool string
}

func (e *ScopeUndeclaredError) Error() string {
	return fmt.Sprintf("mcp: tool %q declares no scope", e.Tool)
}

// IsScopeUndeclaredError reports whether err is a ScopeUndeclaredError.
func IsScopeUndeclaredError(err error) bool {
	var target *ScopeUndeclaredError
	return errors.As(err, &target)
}
