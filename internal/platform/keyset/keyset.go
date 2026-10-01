// Package keyset encodes the opaque (timestamp, id) cursors that keyset-paginated lists hand to their callers.
package keyset

import (
	"encoding/base64"
	"encoding/binary"
	"time"

	"github.com/google/uuid"
)

// Cursor is an opaque, URL-safe page position. SECURITY: unsigned on purpose; a forged cursor only moves within the caller's own org_id predicate.
type Cursor string

const encodedLen = 8 + 16

// Encode packs the last row's timestamp and id into a cursor.
func Encode(ts time.Time, id uuid.UUID) Cursor {
	var b [encodedLen]byte
	binary.BigEndian.PutUint64(b[:8], uint64(ts.UTC().UnixNano())) //nolint:gosec // G115: a round trip through uint64 preserves every int64 bit pattern.
	copy(b[8:], id[:])
	return Cursor(base64.RawURLEncoding.EncodeToString(b[:]))
}

// Decode unpacks a cursor produced by Encode, returning *InvalidCursorError for anything else.
func Decode(c Cursor) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(string(c))
	if err != nil || len(raw) != encodedLen {
		return time.Time{}, uuid.Nil, &InvalidCursorError{Cursor: string(c)}
	}
	ns := int64(binary.BigEndian.Uint64(raw[:8])) //nolint:gosec // G115: inverse of Encode.
	var id uuid.UUID
	copy(id[:], raw[8:])
	return time.Unix(0, ns).UTC(), id, nil
}
