package publicid_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/publicid"
)

func TestNew_IsPrefixUnderscoreAndSixteenNanoidCharacters(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{"dev", "lnk"} {
		id, err := publicid.New(prefix)
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(id, prefix+"_"), id)
		require.Len(t, id, len(prefix)+1+publicid.Length)
		require.True(t, publicid.Valid(prefix, id), id)
	}
	a, err := publicid.New("dev")
	require.NoError(t, err)
	b, err := publicid.New("dev")
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}

func TestNew_RefusesAnEmptyPrefix(t *testing.T) {
	t.Parallel()
	_, err := publicid.New("")
	require.Error(t, err)
	require.False(t, publicid.Valid("", "_V1StGXR8Z5jdHi6B"))
}

func TestValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"device id", "dev_V1StGXR8Z5jdHi6B", true},
		{"dash and underscore are in the alphabet", "dev_V1St-XR8Z5jd_i6B", true},
		{"wrong prefix", "cht_V1StGXR8Z5jdHi6B", false},
		{"no separator", "devV1StGXR8Z5jdHi6B", false},
		{"15 characters", "dev_V1StGXR8Z5jdHi6", false},
		{"17 characters", "dev_V1StGXR8Z5jdHi6BB", false},
		{"outside the alphabet", "dev_V1StGXR8Z5jdHi6.", false},
		{"a uuid", "018f9c3e-5555-7000-8000-000000000005", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.ok, publicid.Valid("dev", tc.in))
		})
	}
}
