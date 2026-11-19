package alert

import (
	"errors"
	"strconv"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/openwa/gen/go/apperror/v1"
	"altalune.id/openwa/internal/apperror"
)

// InvalidRecipientsError reports a recipient that is not a bare email address.
type InvalidRecipientsError struct{ Address string }

func (e *InvalidRecipientsError) Error() string {
	return "alert: recipients: invalid or extra address " + strconv.Quote(e.Address)
}

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidRecipientsError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeAlertInvalidRecipients, "Use up to 10 valid email addresses", codes.InvalidArgument, &apperrorv1.ErrorDetail{Code: apperror.CodeAlertInvalidRecipients, Meta: map[string]string{"address": e.Address}})
}

// IsInvalidRecipientsError reports whether err's chain contains a *InvalidRecipientsError.
func IsInvalidRecipientsError(err error) bool {
	_, ok := errors.AsType[*InvalidRecipientsError](err)
	return ok
}

// TooManyRecipientsError reports a recipient list longer than MaxRecipients.
type TooManyRecipientsError struct{ Max int }

func (e *TooManyRecipientsError) Error() string {
	return "alert: recipients: more than " + strconv.Itoa(e.Max) + " addresses"
}

// ToAppError converts the typed error into the wire envelope.
func (e *TooManyRecipientsError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeAlertTooManyRecipients, "Use up to "+strconv.Itoa(e.Max)+" email addresses", codes.InvalidArgument, &apperrorv1.ErrorDetail{Code: apperror.CodeAlertTooManyRecipients, Meta: map[string]string{"max": strconv.Itoa(e.Max)}})
}

// IsTooManyRecipientsError reports whether err's chain contains a *TooManyRecipientsError.
func IsTooManyRecipientsError(err error) bool {
	_, ok := errors.AsType[*TooManyRecipientsError](err)
	return ok
}

// TestLimitError reports a test email refused by the hourly or the daily limit.
type TestLimitError struct{}

func (*TestLimitError) Error() string { return "alert: test email limit reached" }

// ToAppError converts the typed error into the wire envelope.
func (*TestLimitError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeAlertTestLimit, "Too many test emails; try again later", codes.ResourceExhausted, &apperrorv1.ErrorDetail{Code: apperror.CodeAlertTestLimit})
}

// IsTestLimitError reports whether err's chain contains a *TestLimitError.
func IsTestLimitError(err error) bool {
	_, ok := errors.AsType[*TestLimitError](err)
	return ok
}

// MailUnavailableError reports an alert refused because the server has no delivering mail driver.
type MailUnavailableError struct{}

func (*MailUnavailableError) Error() string { return "alert: no mail driver delivers on this server" }

// ToAppError converts the typed error into the wire envelope.
func (*MailUnavailableError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeAlertMailUnavailable, "Email is not set up on this server", codes.FailedPrecondition, &apperrorv1.ErrorDetail{Code: apperror.CodeAlertMailUnavailable})
}

// IsMailUnavailableError reports whether err's chain contains a *MailUnavailableError.
func IsMailUnavailableError(err error) bool {
	_, ok := errors.AsType[*MailUnavailableError](err)
	return ok
}

// DisabledError reports an alert refused because the server turned disconnect emails off.
type DisabledError struct{}

func (*DisabledError) Error() string { return "alert: disconnect emails are turned off on this server" }

// ToAppError converts the typed error into the wire envelope.
func (*DisabledError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeAlertDisabled, "Disconnect emails are turned off on this server", codes.FailedPrecondition, &apperrorv1.ErrorDetail{Code: apperror.CodeAlertDisabled})
}

// IsDisabledError reports whether err's chain contains a *DisabledError.
func IsDisabledError(err error) bool {
	_, ok := errors.AsType[*DisabledError](err)
	return ok
}

// NoRecipientsError reports an alert with no address left once opt-outs are removed.
type NoRecipientsError struct{}

func (*NoRecipientsError) Error() string { return "alert: no address left to email" }

// ToAppError converts the typed error into the wire envelope.
func (*NoRecipientsError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeAlertNoRecipients, "No address can get alerts for this project", codes.FailedPrecondition, &apperrorv1.ErrorDetail{Code: apperror.CodeAlertNoRecipients})
}

// IsNoRecipientsError reports whether err's chain contains a *NoRecipientsError.
func IsNoRecipientsError(err error) bool {
	_, ok := errors.AsType[*NoRecipientsError](err)
	return ok
}

// InvalidUnsubscribeTokenError reports an unsubscribe link whose token does not open.
type InvalidUnsubscribeTokenError struct{}

func (*InvalidUnsubscribeTokenError) Error() string {
	return "alert: unsubscribe token cannot be opened"
}

// ToAppError converts the typed error into the wire envelope.
func (*InvalidUnsubscribeTokenError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeAlertInvalidUnsubscribeToken, "This unsubscribe link is not valid", codes.InvalidArgument, &apperrorv1.ErrorDetail{Code: apperror.CodeAlertInvalidUnsubscribeToken})
}

// IsInvalidUnsubscribeTokenError reports whether err's chain contains a *InvalidUnsubscribeTokenError.
func IsInvalidUnsubscribeTokenError(err error) bool {
	_, ok := errors.AsType[*InvalidUnsubscribeTokenError](err)
	return ok
}
