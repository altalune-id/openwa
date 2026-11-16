package phone_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/phone"
)

func TestNormalize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		cc   string
		want string
		bad  bool
	}{
		{"plus spaces dashes", "+62 812-3456", "", "628123456", false},
		{"parentheses and dots", "62 (812) 3456.789", "", "628123456789", false},
		{"already canonical", "628123456789", "62", "628123456789", false},
		{"leading zero with default", "0812 3456 789", "62", "628123456789", false},
		{"leading zero without default", "0812 3456 789", "", "", true},
		{"plus then zero is not national", "+0812345678", "62", "", true},
		{"too short", "+1", "", "", true},
		{"too long", "+12345678901234567", "", "", true},
		{"too long after default", "0123456789012345", "62", "", true},
		{"letters", "abc", "", "", true},
		{"digits with letters", "62812abc", "", "", true},
		{"empty", "", "62", "", true},
		{"only separators", " - ", "62", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := phone.Normalize(tc.in, tc.cc)
			if tc.bad {
				require.True(t, phone.IsInvalidError(err), "got %v", err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
