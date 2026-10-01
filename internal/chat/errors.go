package chat

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/openwa/gen/go/apperror/v1"
	"altalune.id/openwa/internal/apperror"
)

// PublicIDTakenError reports a save whose freshly minted public id collided with a stored one.
type PublicIDTakenError struct{ PublicID string }

func (e *PublicIDTakenError) Error() string {
	return "chat: save: public id already taken: " + e.PublicID
}

// IsPublicIDTakenError reports whether err's tree contains a *PublicIDTakenError.
func IsPublicIDTakenError(err error) bool {
	_, ok := errors.AsType[*PublicIDTakenError](err)
	return ok
}

// NotFoundError reports that no chat matched the lookup in the caller's scope.
type NotFoundError struct{ ID string }

func (e *NotFoundError) Error() string { return fmt.Sprintf("chat: not found: %s", e.ID) }

// ToAppError converts the typed error into the wire envelope.
func (e *NotFoundError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeChatNotFound, "Chat not found", codes.NotFound,
		&apperrorv1.ErrorDetail{Code: apperror.CodeChatNotFound})
}

// IsNotFoundError reports whether err's tree contains a *NotFoundError.
func IsNotFoundError(err error) bool {
	_, ok := errors.AsType[*NotFoundError](err)
	return ok
}

// VersionMismatchError reports a conditional write whose expected version moved on.
type VersionMismatchError struct{ Want, Got int }

func (e *VersionMismatchError) Error() string {
	return fmt.Sprintf("chat: version mismatch: want %d, stored %d", e.Want, e.Got)
}

// ToAppError converts the typed error into the wire envelope.
func (e *VersionMismatchError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeChatVersionMismatch, "The chat changed; retry", codes.Aborted,
		&apperrorv1.ErrorDetail{Code: apperror.CodeChatVersionMismatch})
}

// IsVersionMismatchError reports whether err's tree contains a *VersionMismatchError.
func IsVersionMismatchError(err error) bool {
	_, ok := errors.AsType[*VersionMismatchError](err)
	return ok
}

// InvalidJIDError reports a chat identifier that is not a usable WhatsApp JID.
type InvalidJIDError struct{ JID, Reason string }

func (e *InvalidJIDError) Error() string {
	return fmt.Sprintf("chat: invalid jid %q: %s", e.JID, e.Reason)
}

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidJIDError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeChatInvalidJID, "The chat identifier is not a valid WhatsApp JID", codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeChatInvalidJID})
}

// IsInvalidJIDError reports whether err's tree contains an *InvalidJIDError.
func IsInvalidJIDError(err error) bool {
	_, ok := errors.AsType[*InvalidJIDError](err)
	return ok
}

// NotAGroupError reports a group verb called on a one-to-one chat.
type NotAGroupError struct{ ID string }

func (e *NotAGroupError) Error() string { return fmt.Sprintf("chat: %s is not a group", e.ID) }

// ToAppError converts the typed error into the wire envelope.
func (e *NotAGroupError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeChatNotAGroup, "This chat is not a group", codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeChatNotAGroup})
}

// IsNotAGroupError reports whether err's tree contains a *NotAGroupError.
func IsNotAGroupError(err error) bool {
	_, ok := errors.AsType[*NotAGroupError](err)
	return ok
}
