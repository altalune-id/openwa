package device

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/openwa/gen/go/apperror/v1"
	"altalune.id/openwa/internal/apperror"
)

// NotFoundError reports that no device matched the lookup.
type NotFoundError struct{ ID string }

func (e *NotFoundError) Error() string {
	return "device: lookup: not found: id=" + e.ID
}

// ToAppError converts the typed error into the wire envelope; an empty ID (a lookup by internal UUID) carries no device_id at all.
func (e *NotFoundError) ToAppError() *apperror.AppError {
	detail := &apperrorv1.ErrorDetail{Code: apperror.CodeDeviceNotFound}
	if e.ID != "" {
		detail.Meta = map[string]string{"device_id": e.ID}
	}
	return apperror.New(apperror.CodeDeviceNotFound, "Device not found", codes.NotFound, detail)
}

// WithDeviceID returns a copy naming the device by id.
func (e *NotFoundError) WithDeviceID(id string) error { return &NotFoundError{ID: id} }

type deviceIDCarrier interface {
	error
	WithDeviceID(id string) error
}

// SECURITY: every error that leaves the service names the device by its public id; one built from the internal UUID (a store call by id, a whatsapp error through the Sessions port) is re-pointed here.
func publicErr(err error, publicID string) error {
	if err == nil {
		return nil
	}
	if c, ok := errors.AsType[deviceIDCarrier](err); ok {
		return c.WithDeviceID(publicID)
	}
	return err
}

// IsNotFoundError reports whether err's tree contains a *NotFoundError.
func IsNotFoundError(err error) bool {
	_, ok := errors.AsType[*NotFoundError](err)
	return ok
}

// NameTakenError reports that the project already has a device with this name.
type NameTakenError struct{ Name string }

func (e *NameTakenError) Error() string {
	return "device: save: name already taken in this project: name=" + e.Name
}

// ToAppError converts the typed error into the wire envelope.
func (e *NameTakenError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeDeviceNameTaken, "A device with this name already exists", codes.AlreadyExists,
		&apperrorv1.ErrorDetail{Code: apperror.CodeDeviceNameTaken, Meta: map[string]string{"name": e.Name}})
}

// IsNameTakenError reports whether err's tree contains a *NameTakenError.
func IsNameTakenError(err error) bool {
	_, ok := errors.AsType[*NameTakenError](err)
	return ok
}

// InvalidNameError reports a name that breaks the aggregate's invariants.
type InvalidNameError struct{ Reason string }

func (e *InvalidNameError) Error() string {
	return "device: name: invalid: " + e.Reason
}

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidNameError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeDeviceInvalidName, "Invalid device name: "+e.Reason, codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeDeviceInvalidName, Meta: map[string]string{"reason": e.Reason}})
}

// IsInvalidNameError reports whether err's tree contains an *InvalidNameError.
func IsInvalidNameError(err error) bool {
	_, ok := errors.AsType[*InvalidNameError](err)
	return ok
}

// InvalidRulesError reports one rules field that breaks its invariant.
type InvalidRulesError struct {
	Field  string
	Reason string
}

func (e *InvalidRulesError) Error() string {
	return fmt.Sprintf("device: rules: invalid %s: %s", e.Field, e.Reason)
}

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidRulesError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeDeviceInvalidRules, "Invalid device rules: "+e.Field+" "+e.Reason, codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeDeviceInvalidRules, Meta: map[string]string{"field": e.Field, "reason": e.Reason}})
}

// IsInvalidRulesError reports whether err's tree contains an *InvalidRulesError.
func IsInvalidRulesError(err error) bool {
	_, ok := errors.AsType[*InvalidRulesError](err)
	return ok
}

// StaleVersionError reports a conditional write whose expected version no longer matches.
type StaleVersionError struct{ Want, Got int }

func (e *StaleVersionError) Error() string {
	return fmt.Sprintf("device: stale version, have %d want %d", e.Got, e.Want)
}

// ToAppError converts the typed error into the wire envelope.
func (e *StaleVersionError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeDeviceStaleVersion, "The device changed since the version you sent", codes.Aborted,
		&apperrorv1.ErrorDetail{Code: apperror.CodeDeviceStaleVersion})
}

// IsStaleVersionError reports whether err's tree contains a *StaleVersionError.
func IsStaleVersionError(err error) bool {
	_, ok := errors.AsType[*StaleVersionError](err)
	return ok
}

// PublicIDTakenError reports an insert whose freshly minted public id collided with a stored one.
type PublicIDTakenError struct{ PublicID string }

func (e *PublicIDTakenError) Error() string {
	return "device: save: public id already taken: " + e.PublicID
}

// IsPublicIDTakenError reports whether err's tree contains a *PublicIDTakenError.
func IsPublicIDTakenError(err error) bool {
	_, ok := errors.AsType[*PublicIDTakenError](err)
	return ok
}
