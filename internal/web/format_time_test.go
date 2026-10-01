package web_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"altalune.id/openwa/internal/i18n"
	"altalune.id/openwa/internal/web"
)

func TestFormatTime_RelativeBoundariesInEnglish(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	tr := i18n.NewEmbeddedBundle(i18n.EnUS).For(i18n.EnUS)
	cases := []struct {
		name string
		ago  time.Duration
		want string
	}{
		{"59s", 59 * time.Second, "just now"},
		{"60s", 60 * time.Second, "1 minute ago"},
		{"59m", 59 * time.Minute, "59 minutes ago"},
		{"60m", 60 * time.Minute, "1 hour ago"},
		{"23h59m", 23*time.Hour + 59*time.Minute, "23 hours ago"},
		{"24h", 24 * time.Hour, "1 day ago"},
		{"6d23h", 6*24*time.Hour + 23*time.Hour, "6 days ago"},
		{"7d", 7 * 24 * time.Hour, "on 21 Sep 2026"},
		{"future", -3 * 24 * time.Hour, "on 1 Oct 2026"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rel, abs := web.FormatTime(now.Add(-tc.ago), tr, now)
			assert.Equal(t, tc.want, rel)
			assert.Equal(t, now.Add(-tc.ago).Format("2 Jan 2006 15:04")+" UTC", abs)
		})
	}
}

func TestFormatTime_EveryLocaleTranslates(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	for _, loc := range b.All() {
		tr := b.For(loc)
		for _, ago := range []time.Duration{10 * time.Second, 5 * time.Minute, 3 * time.Hour, 2 * 24 * time.Hour, 30 * 24 * time.Hour} {
			rel, _ := web.FormatTime(now.Add(-ago), tr, now)
			assert.NotContains(t, rel, "time.", "%s left a key untranslated for %s", loc, ago)
			assert.NotEmpty(t, rel)
		}
	}
}

func TestFormatTime_NilTranslatorReturnsKeys(t *testing.T) {
	t.Parallel()
	now := time.Now()
	rel, _ := web.FormatTime(now.Add(-5*time.Minute), nil, now)
	assert.Equal(t, "time.minutes_ago", rel)
}
