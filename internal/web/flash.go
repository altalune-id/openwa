package web

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// FlashCookieName carries one signed flash payload across a redirect.
const FlashCookieName = "openwa_flash"

// FlashMaxAge is the cookie lifetime in seconds.
const FlashMaxAge = 60

// FlashMaxBytes caps name, "=" and signed value together, the size browsers enforce per cookie.
const FlashMaxBytes = 4096

// FlashPayload is what SetFlash writes and the Flash middleware reads back.
type FlashPayload struct {
	Kind FlashKind `json:"k"`
	Key  string    `json:"key"`
	Args []string  `json:"args,omitempty"`
}

// FlashTooLargeError reports a signed flash cookie above FlashMaxBytes.
type FlashTooLargeError struct{ Size int }

func (e *FlashTooLargeError) Error() string {
	return fmt.Sprintf("web: flash cookie is %d bytes, max %d", e.Size, FlashMaxBytes)
}

// IsFlashTooLargeError reports whether err's tree contains a *FlashTooLargeError.
func IsFlashTooLargeError(err error) bool {
	var target *FlashTooLargeError
	return errors.As(err, &target)
}

// EncodeFlash serialises p as base64url JSON.
func EncodeFlash(p FlashPayload) (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// SignedFlashCookie encodes and signs p, refusing a cookie whose name and value exceed FlashMaxBytes.
func SignedFlashCookie(secret []byte, p FlashPayload) (string, error) {
	raw, err := EncodeFlash(p)
	if err != nil {
		return "", err
	}
	signed := SignCookie(secret, raw)
	if size := len(FlashCookieName) + 1 + len(signed); size > FlashMaxBytes {
		return "", &FlashTooLargeError{Size: size}
	}
	return signed, nil
}

// DecodeFlash parses what EncodeFlash produced.
func DecodeFlash(raw string) (FlashPayload, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return FlashPayload{}, fmt.Errorf("web: flash decode: %w", err)
	}
	var p FlashPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return FlashPayload{}, fmt.Errorf("web: flash parse: %w", err)
	}
	return p, nil
}
