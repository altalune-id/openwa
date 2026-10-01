package keyset

import (
	"errors"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/openwa/gen/go/apperror/v1"
	"altalune.id/openwa/internal/apperror"
)

// InvalidCursorError reports a cursor this package did not produce.
type InvalidCursorError struct{ Cursor string }

func (e *InvalidCursorError) Error() string { return "keyset: invalid cursor" }

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidCursorError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeValidation, "The cursor is not valid; pass back the next_cursor a list returned", codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeValidation, Meta: map[string]string{"field": "cursor"}})
}

// IsInvalidCursorError reports whether err's tree contains an *InvalidCursorError.
func IsInvalidCursorError(err error) bool {
	_, ok := errors.AsType[*InvalidCursorError](err)
	return ok
}
