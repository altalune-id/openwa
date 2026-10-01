package meow

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"go.mau.fi/whatsmeow"

	"altalune.id/openwa/internal/whatsapp"
)

// NOTE: 463 is the server code WhatsApp answers when it throttles messages to new contacts.
const reachoutTimelock = 463

func mapError(op, deviceID string, err error) error {
	switch {
	case errors.Is(err, whatsmeow.ErrNotLoggedIn), errors.Is(err, whatsmeow.ErrNotConnected):
		return &whatsapp.NotConnectedError{ID: deviceID}
	case errors.Is(err, whatsmeow.ErrMessageTimedOut):
		return &whatsapp.EngineError{Op: op, Reason: "ack_timeout", Retryable: true, Err: err}
	case errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith403), errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith404), errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith410):
		return &whatsapp.MediaUnavailableError{ID: deviceID}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return &whatsapp.EngineError{Op: op, Reason: "cancelled", Retryable: true, Err: err}
	}
	if code, ok := serverCode(err); ok {
		if code == reachoutTimelock {
			return &whatsapp.EngineError{Op: op, Reason: "reachout_timelock", Err: err}
		}
		return &whatsapp.EngineError{Op: op, Reason: fmt.Sprintf("server_error_%d", code), Retryable: code >= 500, Err: err}
	}
	return &whatsapp.EngineError{Op: op, Retryable: true, Err: err}
}

// NOTE: whatsmeow reports a send refusal as fmt.Errorf("%w %d", ErrServerReturnedError, code), so the code is the error's last field.
func serverCode(err error) (int, bool) {
	if !errors.Is(err, whatsmeow.ErrServerReturnedError) {
		return 0, false
	}
	fields := strings.Fields(err.Error())
	code, convErr := strconv.Atoi(fields[len(fields)-1])
	return code, convErr == nil
}
