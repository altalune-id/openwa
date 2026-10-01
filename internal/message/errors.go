package message

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/openwa/gen/go/apperror/v1"
	"altalune.id/openwa/internal/apperror"
)

func appErr(code, msg string, c codes.Code, meta map[string]string) *apperror.AppError {
	return apperror.New(code, msg, c, &apperrorv1.ErrorDetail{Code: code, Meta: meta})
}

// PublicIDTakenError reports a save whose freshly minted public id collided with a stored one.
type PublicIDTakenError struct{ PublicID string }

func (e *PublicIDTakenError) Error() string {
	return "message: save: public id already taken: " + e.PublicID
}

// IsPublicIDTakenError reports whether err's tree contains a *PublicIDTakenError.
func IsPublicIDTakenError(err error) bool {
	_, ok := errors.AsType[*PublicIDTakenError](err)
	return ok
}

// NotFoundError reports that no message matched the lookup in the caller's scope.
type NotFoundError struct{ ID string }

func (e *NotFoundError) Error() string { return "message: not found: " + e.ID }

// ToAppError converts the typed error into the wire envelope.
func (e *NotFoundError) ToAppError() *apperror.AppError {
	return appErr(apperror.CodeMessageNotFound, "Message not found", codes.NotFound, nil)
}

// IsNotFoundError reports whether err's tree contains a *NotFoundError.
func IsNotFoundError(err error) bool { _, ok := errors.AsType[*NotFoundError](err); return ok }

// InvalidInputError reports a send, edit or reaction request that breaks an invariant.
type InvalidInputError struct{ Field, Reason string }

func (e *InvalidInputError) Error() string {
	return fmt.Sprintf("message: invalid %s: %s", e.Field, e.Reason)
}

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidInputError) ToAppError() *apperror.AppError {
	return appErr(apperror.CodeMessageInvalidInput, "Invalid "+e.Field+": "+e.Reason, codes.InvalidArgument, map[string]string{"field": e.Field})
}

// IsInvalidInputError reports whether err's tree contains an *InvalidInputError.
func IsInvalidInputError(err error) bool { _, ok := errors.AsType[*InvalidInputError](err); return ok }

// DeviceNotLinkedError reports a send through a device with no linked WhatsApp account.
type DeviceNotLinkedError struct{ DeviceID string }

func (e *DeviceNotLinkedError) Error() string { return "message: device not linked: " + e.DeviceID }

// ToAppError converts the typed error into the wire envelope.
func (e *DeviceNotLinkedError) ToAppError() *apperror.AppError {
	return appErr(apperror.CodeMessageDeviceNotLinked, "The device is not linked to a WhatsApp account; pair it first", codes.FailedPrecondition, nil)
}

// IsDeviceNotLinkedError reports whether err's tree contains a *DeviceNotLinkedError.
func IsDeviceNotLinkedError(err error) bool {
	_, ok := errors.AsType[*DeviceNotLinkedError](err)
	return ok
}

// MediaTooLargeError reports an attachment above whatsapp.mediaMaxBytes.
type MediaTooLargeError struct{ Size, Max int64 }

func (e *MediaTooLargeError) Error() string {
	return fmt.Sprintf("message: media %d bytes over %d", e.Size, e.Max)
}

// ToAppError converts the typed error into the wire envelope.
func (e *MediaTooLargeError) ToAppError() *apperror.AppError {
	return appErr(apperror.CodeMessageMediaTooLarge, fmt.Sprintf("The attachment is larger than %d bytes", e.Max), codes.InvalidArgument, nil)
}

// IsMediaTooLargeError reports whether err's tree contains a *MediaTooLargeError.
func IsMediaTooLargeError(err error) bool {
	_, ok := errors.AsType[*MediaTooLargeError](err)
	return ok
}

// UnsupportedMimeError reports an attachment without a usable mime type.
type UnsupportedMimeError struct{ Mime string }

func (e *UnsupportedMimeError) Error() string { return "message: unsupported mime " + e.Mime }

// ToAppError converts the typed error into the wire envelope.
func (e *UnsupportedMimeError) ToAppError() *apperror.AppError {
	return appErr(apperror.CodeMessageUnsupportedMime, "The attachment's type is not supported", codes.InvalidArgument, nil)
}

// IsUnsupportedMimeError reports whether err's tree contains an *UnsupportedMimeError.
func IsUnsupportedMimeError(err error) bool {
	_, ok := errors.AsType[*UnsupportedMimeError](err)
	return ok
}

// EditWindowClosedError reports an edit more than 20 minutes after sending.
type EditWindowClosedError struct{}

func (e *EditWindowClosedError) Error() string { return "message: edit window closed" }

// ToAppError converts the typed error into the wire envelope.
func (e *EditWindowClosedError) ToAppError() *apperror.AppError {
	return appErr(apperror.CodeMessageEditWindowClosed, "A message can only be edited within 20 minutes of sending", codes.FailedPrecondition, nil)
}

// IsEditWindowClosedError reports whether err's tree contains an *EditWindowClosedError.
func IsEditWindowClosedError(err error) bool {
	_, ok := errors.AsType[*EditWindowClosedError](err)
	return ok
}

// NotOwnMessageError reports a revoke or edit of a message the account did not send.
type NotOwnMessageError struct{}

func (e *NotOwnMessageError) Error() string { return "message: not sent by this account" }

// ToAppError converts the typed error into the wire envelope.
func (e *NotOwnMessageError) ToAppError() *apperror.AppError {
	return appErr(apperror.CodeMessageNotOwn, "Only messages this account sent can be changed", codes.FailedPrecondition, nil)
}

// IsNotOwnMessageError reports whether err's tree contains a *NotOwnMessageError.
func IsNotOwnMessageError(err error) bool {
	_, ok := errors.AsType[*NotOwnMessageError](err)
	return ok
}

// VersionMismatchError reports a conditional write whose expected version moved on.
type VersionMismatchError struct{ Want, Got int }

func (e *VersionMismatchError) Error() string {
	return fmt.Sprintf("message: version mismatch: want %d, stored %d", e.Want, e.Got)
}

// ToAppError converts the typed error into the wire envelope.
func (e *VersionMismatchError) ToAppError() *apperror.AppError {
	return appErr(apperror.CodeMessageVersionMismatch, "The message changed; retry", codes.Aborted, nil)
}

// IsVersionMismatchError reports whether err's tree contains a *VersionMismatchError.
func IsVersionMismatchError(err error) bool {
	_, ok := errors.AsType[*VersionMismatchError](err)
	return ok
}

// MediaUnavailableError reports media WhatsApp no longer holds, or media this row never had.
type MediaUnavailableError struct{ ID string }

func (e *MediaUnavailableError) Error() string { return "message: media unavailable: " + e.ID }

// ToAppError converts the typed error into the wire envelope.
func (e *MediaUnavailableError) ToAppError() *apperror.AppError {
	return appErr(apperror.CodeMessageMediaUnavailable, "The media is no longer available from WhatsApp", codes.FailedPrecondition, nil)
}

// IsMediaUnavailableError reports whether err's tree contains a *MediaUnavailableError.
func IsMediaUnavailableError(err error) bool {
	_, ok := errors.AsType[*MediaUnavailableError](err)
	return ok
}

// InvalidRetentionError reports a retention outside 1..365 days.
type InvalidRetentionError struct{ Days int }

func (e *InvalidRetentionError) Error() string {
	return fmt.Sprintf("message: invalid retention %d days", e.Days)
}

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidRetentionError) ToAppError() *apperror.AppError {
	return appErr(apperror.CodeMessageInvalidRetention, "Retention is 1 to 365 days", codes.InvalidArgument, map[string]string{"field": "days"})
}

// IsInvalidRetentionError reports whether err's tree contains an *InvalidRetentionError.
func IsInvalidRetentionError(err error) bool {
	_, ok := errors.AsType[*InvalidRetentionError](err)
	return ok
}

// MediaFetchError reports a media URL that was refused or could not be fetched.
type MediaFetchError struct{ URL, Reason string }

func (e *MediaFetchError) Error() string { return "message: media fetch failed: " + e.Reason }

// ToAppError converts the typed error into the wire envelope.
func (e *MediaFetchError) ToAppError() *apperror.AppError {
	return appErr(apperror.CodeMessageMediaFetch, "The media URL could not be fetched: "+e.Reason, codes.InvalidArgument, map[string]string{"field": "media.url"})
}

// IsMediaFetchError reports whether err's tree contains a *MediaFetchError.
func IsMediaFetchError(err error) bool { _, ok := errors.AsType[*MediaFetchError](err); return ok }
