// Package publicid mints and checks the prefixed nanoid ids outside surfaces use in place of internal UUIDs.
package publicid

import (
	"errors"
	"strings"

	"altalune.id/openwa/nanoid"
)

// Length is the number of nanoid characters after the prefix and separator.
const Length = 16

var errEmptyPrefix = errors.New("publicid: empty prefix")

// New returns prefix + "_" + a fresh 16-character nanoid.
func New(prefix string) (string, error) {
	if prefix == "" {
		return "", errEmptyPrefix
	}
	id, err := nanoid.New(Length)
	if err != nil {
		return "", err
	}
	return prefix + "_" + id, nil
}

// Valid reports whether s is prefix + "_" + 16 characters of the nanoid alphabet.
func Valid(prefix, s string) bool {
	rest, ok := strings.CutPrefix(s, prefix+"_")
	return ok && prefix != "" && nanoid.Valid(rest, Length)
}
