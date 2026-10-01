package message_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/message"
)

type typedErr interface {
	error
	ToAppError() *apperror.AppError
}

func TestErrors_CodesAndPredicates(t *testing.T) {
	cases := []struct {
		name string
		err  typedErr
		code string
		is   func(error) bool
	}{
		{"not found", &message.NotFoundError{ID: "x"}, apperror.CodeMessageNotFound, message.IsNotFoundError},
		{"invalid input", &message.InvalidInputError{Field: "f", Reason: "r"}, apperror.CodeMessageInvalidInput, message.IsInvalidInputError},
		{"device not linked", &message.DeviceNotLinkedError{DeviceID: "d"}, apperror.CodeMessageDeviceNotLinked, message.IsDeviceNotLinkedError},
		{"too large", &message.MediaTooLargeError{Size: 2, Max: 1}, apperror.CodeMessageMediaTooLarge, message.IsMediaTooLargeError},
		{"mime", &message.UnsupportedMimeError{Mime: "x"}, apperror.CodeMessageUnsupportedMime, message.IsUnsupportedMimeError},
		{"edit window", &message.EditWindowClosedError{}, apperror.CodeMessageEditWindowClosed, message.IsEditWindowClosedError},
		{"not own", &message.NotOwnMessageError{}, apperror.CodeMessageNotOwn, message.IsNotOwnMessageError},
		{"version", &message.VersionMismatchError{Want: 1, Got: 2}, apperror.CodeMessageVersionMismatch, message.IsVersionMismatchError},
		{"media unavailable", &message.MediaUnavailableError{ID: "m"}, apperror.CodeMessageMediaUnavailable, message.IsMediaUnavailableError},
		{"retention", &message.InvalidRetentionError{Days: 0}, apperror.CodeMessageInvalidRetention, message.IsInvalidRetentionError},
		{"fetch", &message.MediaFetchError{URL: "u", Reason: "r"}, apperror.CodeMessageMediaFetch, message.IsMediaFetchError},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			require.NotEmpty(t, tt.err.Error())
			require.Equal(t, tt.code, tt.err.ToAppError().Code())
			require.True(t, tt.is(fmt.Errorf("wrapped: %w", tt.err)))
			require.False(t, tt.is(fmt.Errorf("other")))
		})
	}
	taken := &message.PublicIDTakenError{PublicID: "msg_x"}
	require.Contains(t, taken.Error(), "msg_x")
	require.True(t, message.IsPublicIDTakenError(fmt.Errorf("w: %w", taken)))
	require.False(t, message.IsPublicIDTakenError(nil))
}
