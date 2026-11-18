// Package alert emails a project's managers when one of its WhatsApp numbers stays disconnected.
package alert

import (
	"net/mail"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

const (
	// MaxRecipients caps a project's saved alert addresses.
	MaxRecipients = 10
	// TestsPerHour caps how many test emails one project can send in an hour.
	TestsPerHour = 3
)

// Settings is a project's alert configuration; a project with no saved row is enabled with no addresses.
type Settings struct {
	OrgID      uuid.UUID
	ProjectID  uuid.UUID
	Enabled    bool
	Recipients []string
	UpdatedAt  time.Time
}

// Recipient is one address an alert goes to and the locale it is written in.
type Recipient struct {
	Email  string
	Locale string
}

// Member is one org member as alerts see it.
type Member struct {
	Email   string
	Locale  string
	Manager bool
}

// Outage is one number whose disconnect an alert reports.
type Outage struct {
	DeviceID       string
	Name           string
	Phone          string
	DisconnectedAt time.Time
}

// Project names a project for an alert's text and links.
type Project struct {
	Name    string
	Slug    string
	OrgSlug string
}

// ParseRecipients splits raw on commas, semicolons and spaces into bare, lowercased, distinct addresses.
func ParseRecipients(raw string) ([]string, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		a, err := mail.ParseAddress(f)
		if err != nil || a.Name != "" || a.Address != f {
			return nil, &InvalidRecipientsError{Address: f}
		}
		addr := strings.ToLower(a.Address)
		if slices.Contains(out, addr) {
			continue
		}
		if len(out) == MaxRecipients {
			return nil, &TooManyRecipientsError{Max: MaxRecipients}
		}
		out = append(out, addr)
	}
	return out, nil
}

func maskPhone(phone string) string {
	d := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	switch {
	case d == "":
		return ""
	case len(d) < 9:
		return "•••• " + d[max(len(d)-4, 0):]
	default:
		return "+" + d[:2] + " " + d[2:5] + " •••• " + d[len(d)-4:]
	}
}
