package meow

import (
	"errors"

	"github.com/google/uuid"
	"go.mau.fi/whatsmeow"

	"altalune.id/openwa/internal/whatsapp"
)

// NotUpgradedError reports that whatsmeow's schema is absent from the runtime database.
type NotUpgradedError struct{}

func (*NotUpgradedError) Error() string {
	return "whatsmeow: schema not present — run `openwa migrate` (or enable db.autoMigrate) before serving"
}

// IsNotUpgradedError reports whether err is a NotUpgradedError.
func IsNotUpgradedError(err error) bool {
	_, ok := errors.AsType[*NotUpgradedError](err)
	return ok
}

func translate(deviceID uuid.UUID, op string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, whatsmeow.ErrNotConnected), errors.Is(err, whatsmeow.ErrNotLoggedIn):
		return &whatsapp.NotConnectedError{ID: deviceID.String()}
	case errors.Is(err, whatsmeow.ErrPhoneNumberTooShort), errors.Is(err, whatsmeow.ErrPhoneNumberIsNotInternational):
		return &whatsapp.InvalidPhoneError{Reason: err.Error()}
	case errors.Is(err, whatsmeow.ErrQRStoreContainsID), errors.Is(err, whatsmeow.ErrQRAlreadyConnected):
		return &whatsapp.AlreadyLinkedError{ID: deviceID.String()}
	}
	return &whatsapp.EngineError{Op: op, Err: err}
}
