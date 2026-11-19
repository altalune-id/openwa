package alert

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/platform/sealer"
)

func TestParseRecipients(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      []string
	}{
		{"empty", "  ", []string{}},
		{"commas and spaces", "a@example.com, b@example.com  c@example.com", []string{"a@example.com", "b@example.com", "c@example.com"}},
		{"lowercased and deduped", "Ops@Example.com,ops@example.com", []string{"ops@example.com"}},
		{"newlines and semicolons", "a@example.com;\nb@example.com", []string{"a@example.com", "b@example.com"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRecipients(tc.raw)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseRecipients_Refuses(t *testing.T) {
	for _, tc := range []struct {
		name, raw, bad string
	}{
		{"not an address", "a@example.com, nope", "nope"},
		{"display name form", "Ops <ops@example.com>", "Ops"},
		{"angle brackets", "<ops@example.com>", "<ops@example.com>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseRecipients(tc.raw)
			var target *InvalidRecipientsError
			require.ErrorAs(t, err, &target)
			assert.Equal(t, tc.bad, target.Address)
		})
	}
}

func TestParseRecipients_RefusesAnEleventhAddress(t *testing.T) {
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = "u" + string(rune('a'+i)) + "@example.com"
	}
	_, err := ParseRecipients(strings.Join(eleven, ","))
	assert.True(t, IsTooManyRecipientsError(err), "got %v", err)
	assert.False(t, IsInvalidRecipientsError(err), "every address is valid; only the count is over")
}

func TestMaskPhone(t *testing.T) {
	assert.Equal(t, "+62 813 •••• 0198", maskPhone("6281322220198"))
	assert.Equal(t, "+62 813 •••• 0198", maskPhone("+62 813-2222-0198"))
	assert.Equal(t, "•••• 4567", maskPhone("1234567"))
	assert.Equal(t, "", maskPhone(""))
}

func TestUnsubscribeToken_RoundTripAndTamper(t *testing.T) {
	key, err := sealer.GenerateKey()
	require.NoError(t, err)
	sl, err := sealer.New(key)
	require.NoError(t, err)
	want := optOut{OrgID: uuid.New(), ProjectID: uuid.New(), Email: "ops@example.com"}

	tok, err := sealOptOut(sl, want)
	require.NoError(t, err)
	assert.NotContains(t, tok, "ops@example.com", "the address stays out of the URL")
	got, err := openOptOut(sl, tok)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	for name, bad := range map[string]string{
		"empty":    "",
		"garbage":  "not-a-token",
		"tampered": tok[:len(tok)-2] + "AA",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := openOptOut(sl, bad)
			assert.True(t, IsInvalidUnsubscribeTokenError(err), "got %v", err)
		})
	}

	other, err := sl.Seal([]byte(`{"o":"`+want.OrgID.String()+`","p":"`+want.ProjectID.String()+`","e":"x@example.com"}`), []byte("other-purpose"))
	require.NoError(t, err)
	_, err = openOptOut(sl, encodeToken(other))
	assert.True(t, IsInvalidUnsubscribeTokenError(err), "a token sealed for another purpose must not open")
}
