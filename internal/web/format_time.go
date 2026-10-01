package web

import (
	"time"

	"altalune.id/openwa/internal/i18n"
)

const (
	absoluteLayout = "2 Jan 2006 15:04"
	dateLayout     = "2 Jan 2006"
)

// FormatTime returns a translated relative label and the absolute UTC form of t.
func FormatTime(t time.Time, tr *i18n.Translator, now time.Time) (relative, absolute string) {
	//i18n:use time.*
	t = t.UTC()
	absolute = t.Format(absoluteLayout) + " UTC"
	delta := now.Sub(t)
	switch {
	case delta < 0:
		return tr.T("time.on_date", "Date", t.Format(dateLayout)), absolute
	case delta < time.Minute:
		return tr.T("time.just_now"), absolute
	case delta < time.Hour:
		return tr.Tn("time.minutes_ago", int(delta/time.Minute)), absolute
	case delta < 24*time.Hour:
		return tr.Tn("time.hours_ago", int(delta/time.Hour)), absolute
	case delta < 7*24*time.Hour:
		return tr.Tn("time.days_ago", int(delta/(24*time.Hour))), absolute
	default:
		return tr.T("time.on_date", "Date", t.Format(dateLayout)), absolute
	}
}
