// Package device is the devices bounded context: a named slot a WhatsApp account is paired into.
package device

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// MaxNameRunes bounds a device name.
const MaxNameRunes = 64

// PublicIDPrefix is the prefix of every device public id (dev_ + 16 nanoid characters).
const PublicIDPrefix = "dev"

// NOTE: publicIDLength must equal publicid.Length; a test pins them.
const publicIDLength = 16

// MaxTriggerPrefixRunes bounds Rules.TriggerPrefix.
const MaxTriggerPrefixRunes = 16

// GroupMode says which group messages a device treats as matched.
type GroupMode string

// GroupMode values.
const (
	GroupIgnore  GroupMode = "ignore"
	GroupMention GroupMode = "mention"
	GroupOpen    GroupMode = "open"
)

// Rules is the inbound-message filter attached to a device.
type Rules struct {
	GroupMode      GroupMode
	AllowedSenders []string
	AllowedGroups  []string
	TriggerPrefix  string
	IgnoreFromMe   bool
}

// Device is the aggregate root; ID never leaves the database, PublicID is the id every outside surface uses.
type Device struct {
	ID        uuid.UUID
	PublicID  string
	OrgID     uuid.UUID
	ProjectID uuid.UUID
	Name      string
	Rules     Rules
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ListOpts filters Store.List; it is empty in v1.
type ListOpts struct{}

// DefaultRules returns the rules a new device starts with.
func DefaultRules() Rules {
	return Rules{GroupMode: GroupMention, AllowedSenders: []string{}, AllowedGroups: []string{}, IgnoreFromMe: true}
}

// New enforces creation invariants and returns a device carrying DefaultRules; the caller mints publicID.
func New(orgID, projectID uuid.UUID, publicID, name string) (*Device, error) {
	name, err := validateName(name)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return &Device{
		ID:        uuid.Must(uuid.NewV7()),
		PublicID:  publicID,
		OrgID:     orgID,
		ProjectID: projectID,
		Name:      name,
		Rules:     DefaultRules(),
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// Rename replaces the name after validation.
func (d *Device) Rename(name string) error {
	name, err := validateName(name)
	if err != nil {
		return err
	}
	d.Name = name
	d.UpdatedAt = time.Now().UTC()
	return nil
}

// SetRules validates every field, normalises the lists and replaces the rules.
func (d *Device) SetRules(r Rules) error {
	normalised, err := normalizeRules(r)
	if err != nil {
		return err
	}
	d.Rules = normalised
	d.UpdatedAt = time.Now().UTC()
	return nil
}

func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", &InvalidNameError{Reason: "empty"}
	}
	if utf8.RuneCountInString(name) > MaxNameRunes {
		return "", &InvalidNameError{Reason: "over 64 characters"}
	}
	if looksLikePublicID(name) {
		return "", &InvalidNameError{Reason: "shaped like a device id"}
	}
	return name, nil
}

// NOTE: a name shaped like dev_ + 16 characters would make `--device <name|id>` ambiguous; the check is by shape so this file needs no import.
func looksLikePublicID(name string) bool {
	return strings.HasPrefix(name, PublicIDPrefix+"_") && len(name) == len(PublicIDPrefix)+1+publicIDLength
}

func normalizeRules(r Rules) (Rules, error) {
	switch r.GroupMode {
	case GroupIgnore, GroupMention, GroupOpen:
	default:
		return Rules{}, &InvalidRulesError{Field: "group_mode", Reason: "must be ignore, mention or open"}
	}
	senders, err := normalizeList(r.AllowedSenders, "allowed_senders", isPhoneDigits, "must be E.164 digits without a plus, 8 to 15 long")
	if err != nil {
		return Rules{}, err
	}
	groups, err := normalizeList(r.AllowedGroups, "allowed_groups", isGroupJID, "must be a group JID ending in @g.us")
	if err != nil {
		return Rules{}, err
	}
	prefix := strings.TrimSpace(r.TriggerPrefix)
	if utf8.RuneCountInString(prefix) > MaxTriggerPrefixRunes {
		return Rules{}, &InvalidRulesError{Field: "trigger_prefix", Reason: "over 16 characters"}
	}
	return Rules{
		GroupMode:      r.GroupMode,
		AllowedSenders: senders,
		AllowedGroups:  groups,
		TriggerPrefix:  prefix,
		IgnoreFromMe:   r.IgnoreFromMe,
	}, nil
}

func normalizeList(in []string, field string, valid func(string) bool, reason string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if !valid(v) {
			return nil, &InvalidRulesError{Field: field, Reason: reason}
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out, nil
}

func isPhoneDigits(s string) bool {
	if len(s) < 8 || len(s) > 15 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func isGroupJID(s string) bool {
	user, ok := strings.CutSuffix(s, "@g.us")
	return ok && user != "" && !strings.ContainsAny(user, "@ ")
}
