package legal_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/legal"
)

const disclaimer = "OpenWA is an unofficial WhatsApp client"

func termsSections() []string {
	return []string{
		"1. Who we are", "2. The service", "3. Eligibility and accounts", "4. Your responsibilities",
		"5. Acceptable use", "6. Your data and content", "7. Fees", "8. Availability and support",
		"9. Suspension and termination", "10. Disclaimers and limitation of liability", "11. Indemnity",
		"12. Changes to these terms", "13. Governing law and disputes", "14. Contact",
	}
}

func privacySections() []string {
	return []string{
		"1. Controller and contact", "2. What we collect", "3. Why and on what basis", "4. Processing on your behalf",
		"5. Sharing", "6. International transfers", "7. Retention", "8. Your rights and how to exercise them",
		"9. Security measures", "10. Children", "11. Changes", "12. Contact",
	}
}

func TestTerms_ParsesWithDisclaimerAndEverySection(t *testing.T) {
	t.Parallel()
	d, err := legal.Terms()
	require.NoError(t, err)
	require.Equal(t, legal.TermsSlug, d.Slug)
	require.Equal(t, "Terms of Service", d.Title)
	require.False(t, d.UpdatedAt.IsZero())
	require.NotContains(t, d.HTML, "<h1>", "the page header renders the only h1")
	require.Contains(t, d.HTML, disclaimer)
	require.Contains(t, d.HTML, `class="legal-callout"`)
	require.Equal(t, termsSections(), d.Headings)
	for _, s := range termsSections() {
		require.Contains(t, d.HTML, "<h2>"+s+"</h2>")
	}
	for _, fact := range []string{"legal@altalune.id", "Republic of Indonesia", "Jakarta", "18 years", "own risk", "30 days"} {
		require.Contains(t, d.HTML, fact)
	}
}

func TestPrivacy_ParsesWithDisclaimerAndEverySection(t *testing.T) {
	t.Parallel()
	d, err := legal.Privacy()
	require.NoError(t, err)
	require.Equal(t, "Privacy Policy", d.Title)
	require.False(t, d.UpdatedAt.IsZero())
	require.Contains(t, d.HTML, disclaimer)
	require.Equal(t, privacySections(), d.Headings)
	for _, s := range privacySections() {
		require.Contains(t, d.HTML, "<h2>"+s+"</h2>")
	}
	for _, fact := range []string{"legal@altalune.id", "90 days", "30 days", "advertising", "model training"} {
		require.Contains(t, d.HTML, fact)
	}
}

func TestLegal_BothDocumentsSayEnglishOnly(t *testing.T) {
	t.Parallel()
	for _, slug := range []string{legal.TermsSlug, legal.PrivacySlug} {
		d, err := legal.BySlug(slug)
		require.NoError(t, err)
		require.True(t, strings.Contains(d.HTML, "published in English only"), slug)
	}
}

func TestBySlug(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		slug    string
		wantErr bool
	}{
		{legal.TermsSlug, false},
		{legal.PrivacySlug, false},
		{"unknown", true},
	} {
		t.Run(tc.slug, func(t *testing.T) {
			t.Parallel()
			_, err := legal.BySlug(tc.slug)
			require.Equal(t, tc.wantErr, err != nil)
		})
	}
}
