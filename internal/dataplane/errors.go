package dataplane

import (
	"cmp"
	"encoding/json"
	"errors"
	"net/http"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/blog"
	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/contact"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/platform/keyset"
	"altalune.id/openwa/internal/whatsapp"
)

// NotFoundError is the single opaque "does not exist" outcome for an unresolvable scope, a missing resource or a denied authorization.
type NotFoundError struct{}

func (*NotFoundError) Error() string { return "dataplane: not found" }

// IsNotFoundError reports whether err is a *NotFoundError.
func IsNotFoundError(err error) bool {
	var target *NotFoundError
	return errors.As(err, &target)
}

// UnauthorizedError is the single opaque authentication failure.
type UnauthorizedError struct{}

func (*UnauthorizedError) Error() string { return "dataplane: unauthorized" }

// IsUnauthorizedError reports whether err is an *UnauthorizedError.
func IsUnauthorizedError(err error) bool {
	var target *UnauthorizedError
	return errors.As(err, &target)
}

// MethodNotAllowedError reports a request whose path is routed but whose method is not.
type MethodNotAllowedError struct{}

func (*MethodNotAllowedError) Error() string { return "dataplane: method not allowed" }

// IsMethodNotAllowedError reports whether err is a *MethodNotAllowedError.
func IsMethodNotAllowedError(err error) bool {
	var target *MethodNotAllowedError
	return errors.As(err, &target)
}

// PreconditionFailedError reports a conditional write whose If-Match no longer holds.
type PreconditionFailedError struct{}

func (*PreconditionFailedError) Error() string { return "dataplane: precondition failed" }

// IsPreconditionFailedError reports whether err is a *PreconditionFailedError.
func IsPreconditionFailedError(err error) bool {
	var target *PreconditionFailedError
	return errors.As(err, &target)
}

// PreconditionRequiredError reports a mutating request that carried no If-Match at all.
type PreconditionRequiredError struct{}

func (*PreconditionRequiredError) Error() string { return "dataplane: precondition required" }

// IsPreconditionRequiredError reports whether err is a *PreconditionRequiredError.
func IsPreconditionRequiredError(err error) bool {
	var target *PreconditionRequiredError
	return errors.As(err, &target)
}

// BadRequestError reports a malformed request body, header or identifier.
type BadRequestError struct{}

func (*BadRequestError) Error() string { return "dataplane: bad request" }

// IsBadRequestError reports whether err is a *BadRequestError.
func IsBadRequestError(err error) bool {
	var target *BadRequestError
	return errors.As(err, &target)
}

// ConflictError reports a slug already in use, or an Idempotency-Key replayed with a different body.
type ConflictError struct{}

func (*ConflictError) Error() string { return "dataplane: conflict" }

// IsConflictError reports whether err is a *ConflictError.
func IsConflictError(err error) bool {
	var target *ConflictError
	return errors.As(err, &target)
}

// InProgressError reports an Idempotency-Key whose first request has not answered yet.
type InProgressError struct{}

func (*InProgressError) Error() string { return "dataplane: request in progress" }

// IsInProgressError reports whether err is an *InProgressError.
func IsInProgressError(err error) bool {
	var target *InProgressError
	return errors.As(err, &target)
}

// PayloadTooLargeError reports a request body over this surface's limit.
type PayloadTooLargeError struct{}

func (*PayloadTooLargeError) Error() string { return "dataplane: payload too large" }

// IsPayloadTooLargeError reports whether err is a *PayloadTooLargeError.
func IsPayloadTooLargeError(err error) bool {
	var target *PayloadTooLargeError
	return errors.As(err, &target)
}

// UnavailableError reports a request abandoned while waiting for capacity.
type UnavailableError struct{}

func (*UnavailableError) Error() string { return "dataplane: unavailable" }

// IsUnavailableError reports whether err is an *UnavailableError.
func IsUnavailableError(err error) bool {
	var target *UnavailableError
	return errors.As(err, &target)
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SECURITY: an unrecognized error type falls through to 500, never admitted as success.
func writeError(w http.ResponseWriter, err error) {
	f := statusFor(err)
	w.Header().Del("ETag")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(f.status)
	_ = json.NewEncoder(w).Encode(errorBody{Code: f.code, Message: cmp.Or(f.message, f.code)})
}

type failure struct {
	status  int
	code    string
	message string
}

//nolint:cyclop // a flat mapping table; splitting it hides the contract it states.
func statusFor(err error) failure {
	switch {
	case IsNotFoundError(err), blog.IsNotFoundError(err), device.IsNotFoundError(err),
		message.IsNotFoundError(err), chat.IsNotFoundError(err), contact.IsNotFoundError(err),
		whatsapp.IsSessionNotFoundError(err), whatsapp.IsSessionGoneError(err):
		return failure{status: http.StatusNotFound, code: "not_found"}
	case message.IsMediaUnavailableError(err):
		return failure{status: http.StatusGone, code: "gone"}
	case IsPayloadTooLargeError(err), message.IsMediaTooLargeError(err):
		return failure{status: http.StatusRequestEntityTooLarge, code: "payload_too_large"}
	case IsUnauthorizedError(err):
		return failure{status: http.StatusUnauthorized, code: "unauthorized"}
	case IsMethodNotAllowedError(err):
		return failure{status: http.StatusMethodNotAllowed, code: "method_not_allowed"}
	case IsPreconditionRequiredError(err):
		return failure{status: http.StatusPreconditionRequired, code: "precondition_required"}
	case IsPreconditionFailedError(err), blog.IsStaleVersionError(err), device.IsStaleVersionError(err):
		return failure{status: http.StatusPreconditionFailed, code: "precondition_failed"}
	case device.IsRulesShapeConflictError(err):
		return coded(http.StatusConflict, err)
	case device.IsInvalidRulesError(err):
		ir, _ := errors.AsType[*device.InvalidRulesError](err)
		return failure{status: http.StatusBadRequest, code: "bad_request", message: ir.Field + ": " + ir.Reason}
	case IsConflictError(err), blog.IsAlreadyExistsError(err), device.IsNameTakenError(err),
		message.IsDeviceNotLinkedError(err), message.IsEditWindowClosedError(err), message.IsNotOwnMessageError(err),
		chat.IsNotAGroupError(err), message.IsVersionMismatchError(err), chat.IsVersionMismatchError(err),
		whatsapp.IsAlreadyLinkedError(err), whatsapp.IsNotConnectedError(err), whatsapp.IsLinkTimeoutError(err):
		return failure{status: http.StatusConflict, code: "conflict"}
	case IsInProgressError(err):
		return failure{status: http.StatusConflict, code: "in_progress"}
	case IsBadRequestError(err), isInvalidPost(err), isInvalidDevice(err), isInvalidMessaging(err):
		return failure{status: http.StatusBadRequest, code: "bad_request"}
	case whatsapp.IsNotOwnedError(err), IsUnavailableError(err):
		return failure{status: http.StatusServiceUnavailable, code: "unavailable"}
	case whatsapp.IsUnsupportedError(err):
		return failure{status: http.StatusNotImplemented, code: "not_implemented"}
	}
	return failure{status: http.StatusInternalServerError, code: "internal"}
}

func coded(status int, err error) failure {
	ae, _ := apperror.AsAppError(err)
	return failure{status: status, code: ae.Code(), message: ae.Message()}
}

func isInvalidPost(err error) bool {
	return blog.IsInvalidTitleError(err) ||
		blog.IsInvalidSlugError(err) ||
		blog.IsInvalidBodyError(err) ||
		blog.IsCategoryRequiredError(err)
}

func isInvalidDevice(err error) bool {
	return device.IsInvalidNameError(err) || device.IsInvalidRulesError(err) || whatsapp.IsInvalidPhoneError(err)
}

func isInvalidMessaging(err error) bool {
	return message.IsInvalidInputError(err) || message.IsUnsupportedMimeError(err) || message.IsMediaFetchError(err) ||
		message.IsInvalidRetentionError(err) || chat.IsInvalidJIDError(err) || keyset.IsInvalidCursorError(err)
}
