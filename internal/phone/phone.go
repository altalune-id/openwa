// Package phone turns a typed phone number into canonical international digits.
package phone

import (
	"errors"
	"strings"
)

// InvalidError reports a phone number that cannot become 8 to 15 international digits.
type InvalidError struct{ Reason string }

func (e *InvalidError) Error() string { return "phone: invalid: " + e.Reason }

// IsInvalidError reports whether err's tree contains an *InvalidError.
func IsInvalidError(err error) bool {
	_, ok := errors.AsType[*InvalidError](err)
	return ok
}

// Normalize strips spaces, dashes, dots, parentheses and a leading plus; a national number (leading 0) takes defaultCC, or is refused when defaultCC is empty.
func Normalize(raw, defaultCC string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	international := strings.HasPrefix(trimmed, "+")
	trimmed = strings.TrimPrefix(trimmed, "+")
	var b strings.Builder
	for _, c := range trimmed {
		switch {
		case c >= '0' && c <= '9':
			b.WriteRune(c)
		case c == ' ' || c == '-' || c == '.' || c == '(' || c == ')':
		default:
			return "", &InvalidError{Reason: "contains a character that is not a digit"}
		}
	}
	digits := b.String()
	if digits == "" {
		return "", &InvalidError{Reason: "empty"}
	}
	if strings.HasPrefix(digits, "0") {
		if international || defaultCC == "" {
			return "", &InvalidError{Reason: "national number; use the international form with a country code"}
		}
		digits = defaultCC + digits[1:]
	}
	if len(digits) < 8 || len(digits) > 15 {
		return "", &InvalidError{Reason: "must be 8 to 15 digits"}
	}
	return digits, nil
}
