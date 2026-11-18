package mailqueue

import "errors"

// PayloadError reports a queued payload that cannot be opened; it never carries the payload.
type PayloadError struct {
	ID     string
	Reason string
}

func (e *PayloadError) Error() string { return "mailqueue: entry " + e.ID + " payload: " + e.Reason }

// IsPayloadError reports whether err's tree contains a *PayloadError.
func IsPayloadError(err error) bool {
	var target *PayloadError
	return errors.As(err, &target)
}

// SettleRefusedError reports a settle refused because the entry was re-claimed or already settled.
type SettleRefusedError struct{ ID string }

func (e *SettleRefusedError) Error() string {
	return "mailqueue: entry " + e.ID + " is no longer held by this claim"
}

// IsSettleRefusedError reports whether err's tree contains a *SettleRefusedError.
func IsSettleRefusedError(err error) bool {
	var target *SettleRefusedError
	return errors.As(err, &target)
}
