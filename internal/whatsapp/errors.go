package whatsapp

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/openwa/gen/go/apperror/v1"
	"altalune.id/openwa/internal/apperror"
)

// SessionNotFoundError reports that the device has no session row in scope.
type SessionNotFoundError struct{ ID string }

func (e *SessionNotFoundError) Error() string {
	return "whatsapp: session not found: device_id=" + e.ID
}

// ToAppError converts the typed error into the wire envelope.
func (e *SessionNotFoundError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeWhatsAppSessionNotFound, "WhatsApp session not found", codes.NotFound,
		&apperrorv1.ErrorDetail{Code: apperror.CodeWhatsAppSessionNotFound, Meta: map[string]string{"device_id": e.ID}})
}

// WithDeviceID returns a copy naming the device by id, so a caller can swap the internal UUID for the public id.
func (e *SessionNotFoundError) WithDeviceID(id string) error { return &SessionNotFoundError{ID: id} }

// IsSessionNotFoundError reports whether err's tree contains a *SessionNotFoundError.
func IsSessionNotFoundError(err error) bool {
	_, ok := errors.AsType[*SessionNotFoundError](err)
	return ok
}

// NotOwnedError reports that this process holds no live engine session for the device.
type NotOwnedError struct{ ID string }

func (e *NotOwnedError) Error() string {
	return "whatsapp: session not owned by this process: device_id=" + e.ID
}

// ToAppError converts the typed error into the wire envelope.
func (e *NotOwnedError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeWhatsAppNotOwned, "This device is not running on this server right now; retry shortly", codes.Unavailable,
		&apperrorv1.ErrorDetail{Code: apperror.CodeWhatsAppNotOwned, Meta: map[string]string{"device_id": e.ID}})
}

// WithDeviceID returns a copy naming the device by id, so a caller can swap the internal UUID for the public id.
func (e *NotOwnedError) WithDeviceID(id string) error { return &NotOwnedError{ID: id} }

// IsNotOwnedError reports whether err's tree contains a *NotOwnedError.
func IsNotOwnedError(err error) bool {
	_, ok := errors.AsType[*NotOwnedError](err)
	return ok
}

// AlreadyLinkedError reports a link attempt on a device that already holds a lease or a live session.
type AlreadyLinkedError struct{ ID string }

func (e *AlreadyLinkedError) Error() string { return "whatsapp: already linked: device_id=" + e.ID }

// ToAppError converts the typed error into the wire envelope.
func (e *AlreadyLinkedError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeWhatsAppAlreadyLinked, "This device is already linked; log it out first", codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeWhatsAppAlreadyLinked, Meta: map[string]string{"device_id": e.ID}})
}

// WithDeviceID returns a copy naming the device by id, so a caller can swap the internal UUID for the public id.
func (e *AlreadyLinkedError) WithDeviceID(id string) error { return &AlreadyLinkedError{ID: id} }

// IsAlreadyLinkedError reports whether err's tree contains an *AlreadyLinkedError.
func IsAlreadyLinkedError(err error) bool {
	_, ok := errors.AsType[*AlreadyLinkedError](err)
	return ok
}

// LinkTimeoutError reports a link attempt that ran out of QR codes or time.
type LinkTimeoutError struct{ ID string }

func (e *LinkTimeoutError) Error() string { return "whatsapp: link timed out: device_id=" + e.ID }

// ToAppError converts the typed error into the wire envelope.
func (e *LinkTimeoutError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeWhatsAppLinkTimeout, "The pairing attempt expired; start a new one", codes.DeadlineExceeded,
		&apperrorv1.ErrorDetail{Code: apperror.CodeWhatsAppLinkTimeout, Meta: map[string]string{"device_id": e.ID}})
}

// WithDeviceID returns a copy naming the device by id, so a caller can swap the internal UUID for the public id.
func (e *LinkTimeoutError) WithDeviceID(id string) error { return &LinkTimeoutError{ID: id} }

// IsLinkTimeoutError reports whether err's tree contains a *LinkTimeoutError.
func IsLinkTimeoutError(err error) bool {
	_, ok := errors.AsType[*LinkTimeoutError](err)
	return ok
}

// UnsupportedError reports a feature the engine does not implement.
type UnsupportedError struct{ Feature string }

func (e *UnsupportedError) Error() string { return "whatsapp: unsupported by the engine: " + e.Feature }

// ToAppError converts the typed error into the wire envelope.
func (e *UnsupportedError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeWhatsAppUnsupported, "The WhatsApp engine does not support "+e.Feature, codes.Unimplemented,
		&apperrorv1.ErrorDetail{Code: apperror.CodeWhatsAppUnsupported, Meta: map[string]string{"feature": e.Feature}})
}

// IsUnsupportedError reports whether err's tree contains an *UnsupportedError.
func IsUnsupportedError(err error) bool {
	_, ok := errors.AsType[*UnsupportedError](err)
	return ok
}

// NotConnectedError reports a call on a closed or never-connected engine session.
type NotConnectedError struct{ ID string }

func (e *NotConnectedError) Error() string {
	return "whatsapp: session not connected: device_id=" + e.ID
}

// ToAppError converts the typed error into the wire envelope.
func (e *NotConnectedError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeWhatsAppNotConnected, "This device is not connected", codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeWhatsAppNotConnected, Meta: map[string]string{"device_id": e.ID}})
}

// WithDeviceID returns a copy naming the device by id, so a caller can swap the internal UUID for the public id.
func (e *NotConnectedError) WithDeviceID(id string) error { return &NotConnectedError{ID: id} }

// IsNotConnectedError reports whether err's tree contains a *NotConnectedError.
func IsNotConnectedError(err error) bool {
	_, ok := errors.AsType[*NotConnectedError](err)
	return ok
}

// InvalidPhoneError reports a phone number that is not international E.164.
type InvalidPhoneError struct{ Reason string }

func (e *InvalidPhoneError) Error() string { return "whatsapp: invalid phone: " + e.Reason }

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidPhoneError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeWhatsAppInvalidPhone, "Invalid phone number: "+e.Reason, codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeWhatsAppInvalidPhone, Meta: map[string]string{"reason": e.Reason}})
}

// IsInvalidPhoneError reports whether err's tree contains an *InvalidPhoneError.
func IsInvalidPhoneError(err error) bool {
	_, ok := errors.AsType[*InvalidPhoneError](err)
	return ok
}

// SessionGoneError reports that the engine no longer holds the device the session row names.
type SessionGoneError struct{ ID string }

func (e *SessionGoneError) Error() string {
	return "whatsapp: session gone from the engine: device_id=" + e.ID
}

// ToAppError converts the typed error into the wire envelope.
func (e *SessionGoneError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeWhatsAppSessionGone, "The WhatsApp engine no longer holds this device; pair it again", codes.NotFound,
		&apperrorv1.ErrorDetail{Code: apperror.CodeWhatsAppSessionGone, Meta: map[string]string{"device_id": e.ID}})
}

// WithDeviceID returns a copy naming the device by id, so a caller can swap the internal UUID for the public id.
func (e *SessionGoneError) WithDeviceID(id string) error { return &SessionGoneError{ID: id} }

// IsSessionGoneError reports whether err's tree contains a *SessionGoneError.
func IsSessionGoneError(err error) bool {
	_, ok := errors.AsType[*SessionGoneError](err)
	return ok
}

// EngineError wraps an engine failure; Retryable says whether the sender should requeue the row.
type EngineError struct {
	Op        string
	Reason    string
	Retryable bool
	Err       error
}

func (e *EngineError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("whatsapp: engine %s: %s: %v", e.Op, e.Reason, e.Err)
	}
	return fmt.Sprintf("whatsapp: engine %s: %v", e.Op, e.Err)
}

// Unwrap exposes the engine's error.
func (e *EngineError) Unwrap() error { return e.Err }

// ToAppError converts the typed error into the wire envelope.
func (e *EngineError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeWhatsAppEngine, "The WhatsApp engine failed during "+e.Op, codes.Internal,
		&apperrorv1.ErrorDetail{Code: apperror.CodeWhatsAppEngine, Meta: map[string]string{"op": e.Op}}).WithCause(e.Err)
}

// IsEngineError reports whether err's tree contains an *EngineError.
func IsEngineError(err error) bool {
	_, ok := errors.AsType[*EngineError](err)
	return ok
}

// InvalidTransitionError reports a session transition whose precondition failed; it never crosses a surface.
type InvalidTransitionError struct {
	From State
	To   State
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("whatsapp: invalid transition from %s to %s", e.From, e.To)
}

// IsInvalidTransitionError reports whether err's tree contains an *InvalidTransitionError.
func IsInvalidTransitionError(err error) bool {
	_, ok := errors.AsType[*InvalidTransitionError](err)
	return ok
}

// InvalidJIDError reports a string that is not a JID.
type InvalidJIDError struct{ Raw string }

func (e *InvalidJIDError) Error() string { return "whatsapp: invalid jid: " + e.Raw }

// IsInvalidJIDError reports whether err's tree contains an *InvalidJIDError.
func IsInvalidJIDError(err error) bool {
	_, ok := errors.AsType[*InvalidJIDError](err)
	return ok
}

// StaleVersionError reports a conditional session write whose expected version no longer matches; the sink reloads and retries.
type StaleVersionError struct{ Want, Got int }

func (e *StaleVersionError) Error() string {
	return fmt.Sprintf("whatsapp: stale session version, have %d want %d", e.Got, e.Want)
}

// IsStaleVersionError reports whether err's tree contains a *StaleVersionError.
func IsStaleVersionError(err error) bool {
	_, ok := errors.AsType[*StaleVersionError](err)
	return ok
}

// MediaUnavailableError reports media WhatsApp no longer serves (HTTP 404 or 410 from the media host).
type MediaUnavailableError struct{ ID string }

func (e *MediaUnavailableError) Error() string { return "whatsapp: media unavailable: " + e.ID }

// IsMediaUnavailableError reports whether err's tree contains a *MediaUnavailableError.
func IsMediaUnavailableError(err error) bool {
	_, ok := errors.AsType[*MediaUnavailableError](err)
	return ok
}

// SetupError reports a port wired twice, or after the runtime started.
type SetupError struct{ Reason string }

func (e *SetupError) Error() string { return "whatsapp: setup: " + e.Reason }

// IsSetupError reports whether err's tree contains a *SetupError.
func IsSetupError(err error) bool {
	_, ok := errors.AsType[*SetupError](err)
	return ok
}

// IsRetryable classifies a send failure: a lost connection, a cancelled or timed-out send and an engine error marked retryable requeue; malformed input and unsupported kinds do not.
func IsRetryable(err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), IsNotConnectedError(err):
		return true
	case IsUnsupportedError(err), IsInvalidJIDError(err), IsMediaUnavailableError(err):
		return false
	}
	if e, ok := errors.AsType[*EngineError](err); ok {
		return e.Retryable
	}
	return true
}

// FailureClass names a send failure for the message.status webhook; the full error text stays in the logs.
func FailureClass(err error) string {
	if e, ok := errors.AsType[*EngineError](err); ok && e.Reason != "" {
		return e.Reason
	}
	switch {
	case err == nil:
		return "unknown"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "cancelled"
	case IsNotConnectedError(err):
		return "not_connected"
	case IsNotOwnedError(err):
		return "not_owned"
	case IsUnsupportedError(err):
		return "unsupported"
	case IsInvalidJIDError(err):
		return "invalid_recipient"
	case IsMediaUnavailableError(err):
		return "media_unavailable"
	}
	return "engine_error"
}
