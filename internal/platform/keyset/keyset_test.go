package keyset_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/apperror"
	"altalune.id/openwa/internal/platform/keyset"
)

func TestEncodeDecode_RoundTrips(t *testing.T) {
	cases := []time.Time{
		time.Date(2026, 9, 28, 10, 11, 12, 123456000, time.UTC),
		time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 28, 17, 0, 0, 0, time.FixedZone("WIB", 7*3600)),
	}
	for _, ts := range cases {
		id := uuid.Must(uuid.NewV7())
		gotTS, gotID, err := keyset.Decode(keyset.Encode(ts, id))
		require.NoError(t, err)
		require.True(t, ts.Equal(gotTS), "want %s got %s", ts, gotTS)
		require.Equal(t, time.UTC, gotTS.Location())
		require.Equal(t, id, gotID)
	}
}

func TestEncode_IsURLSafeAndOpaque(t *testing.T) {
	c := keyset.Encode(time.Now(), uuid.New())
	require.NotContains(t, string(c), "+")
	require.NotContains(t, string(c), "/")
	require.NotContains(t, string(c), "=")
}

func TestDecode_RejectsGarbage(t *testing.T) {
	for _, raw := range []string{"", "!!!", "YWJj", string(keyset.Encode(time.Now(), uuid.New())) + "AA"} {
		_, _, err := keyset.Decode(keyset.Cursor(raw))
		require.True(t, keyset.IsInvalidCursorError(err), "%q: got %v", raw, err)
		ae, ok := apperror.AsAppError(err)
		require.True(t, ok)
		require.Equal(t, apperror.CodeValidation, ae.Code())
	}
}
