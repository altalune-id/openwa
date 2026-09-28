package apikey

import "errors"

// UnknownScopeError names a scope outside the authn catalog.
type UnknownScopeError struct{ Scope string }

func (e *UnknownScopeError) Error() string { return "apikey: unknown scope " + e.Scope }

// IsUnknownScopeError reports whether err is an *UnknownScopeError.
func IsUnknownScopeError(err error) bool {
	var target *UnknownScopeError
	return errors.As(err, &target)
}

// NotFoundError reports a key that does not exist, is revoked, or has expired.
type NotFoundError struct{}

func (*NotFoundError) Error() string { return "apikey: not found" }

// IsNotFoundError reports whether err is a *NotFoundError.
func IsNotFoundError(err error) bool {
	var target *NotFoundError
	return errors.As(err, &target)
}
