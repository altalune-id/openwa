package cli

import "time"

func rfc3339UTC(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func tableDay(t time.Time) string { return t.UTC().Format(time.DateOnly) }

func tableDateTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") }

func tableDateTimeSeconds(t time.Time) string { return t.UTC().Format(time.DateTime) }
