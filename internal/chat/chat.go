// Package chat is the conversation-thread bounded context: one row per device and peer or group.
package chat

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Kind separates one-to-one chats from groups.
type Kind string

// Kind values.
const (
	KindDM    Kind = "dm"
	KindGroup Kind = "group"
)

// Limits on chat fields and list pages.
const (
	MaxPreviewRunes  = 120
	MaxNameRunes     = 256
	DefaultListLimit = 50
	MaxListLimit     = 200
)

// Chat is the aggregate root: one conversation of one device.
type Chat struct {
	ID                 uuid.UUID
	PublicID           string
	OrgID              uuid.UUID
	ProjectID          uuid.UUID
	DeviceID           uuid.UUID
	JID                string
	LID                string
	Kind               Kind
	Name               string
	LastMessageAt      *time.Time
	LastMessagePreview string
	UnreadCount        int
	Archived           bool
	ActivityAt         time.Time
	Version            int
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// GroupInfo is what the engine reports about one group.
type GroupInfo struct {
	JID          string
	Name         string
	Topic        string
	Participants int
	Announce     bool
	Locked       bool
	InviteLink   string
}

// GroupView is a group as the device sees it now; Joined is false for a stored group the engine no longer lists.
type GroupView struct {
	GroupInfo
	ChatID uuid.UUID
	Joined bool
}

// ListOpts filters and pages Store.List; Cursor is the next_cursor a previous page returned.
type ListOpts struct {
	DeviceID  *uuid.UUID
	DeviceIDs []uuid.UUID
	Kind      *Kind
	Search    string
	Limit     int
	Cursor    string
}

// WithDefaults clamps Limit and trims Search.
func (o ListOpts) WithDefaults() ListOpts {
	if o.Limit <= 0 {
		o.Limit = DefaultListLimit
	}
	o.Limit = min(o.Limit, MaxListLimit)
	o.Search = strings.TrimSpace(o.Search)
	return o
}

// PublicIDPrefix is the prefix of a chat's public id.
const PublicIDPrefix = "cht"

// New builds a chat, enforcing a well-formed JID, a LID-shaped alias and a group JID for a group; the service mints publicID.
func New(orgID, projectID, deviceID uuid.UUID, publicID, jid, lid string, kind Kind) (*Chat, error) {
	jid = strings.TrimSpace(jid)
	lid = strings.TrimSpace(lid)
	switch {
	case !validJID(jid):
		return nil, &InvalidJIDError{JID: jid, Reason: "not a JID"}
	case lid != "" && !isLID(lid):
		return nil, &InvalidJIDError{JID: lid, Reason: "alias is not a LID"}
	case kind == KindGroup && !strings.HasSuffix(jid, "@g.us"):
		return nil, &InvalidJIDError{JID: jid, Reason: "a group JID ends in @g.us"}
	case kind != KindDM && kind != KindGroup:
		return nil, &InvalidJIDError{JID: jid, Reason: "unknown chat kind " + string(kind)}
	}
	now := time.Now().UTC()
	return &Chat{
		ID:         uuid.Must(uuid.NewV7()),
		PublicID:   publicID,
		OrgID:      orgID,
		ProjectID:  projectID,
		DeviceID:   deviceID,
		JID:        jid,
		LID:        lid,
		Kind:       kind,
		ActivityAt: now,
		Version:    1,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

// Touch records a message at at; only a newer message moves the preview, and inbound messages count as unread.
func (c *Chat) Touch(at time.Time, preview string, inbound bool) {
	at = at.UTC()
	if c.LastMessageAt == nil || !at.Before(*c.LastMessageAt) {
		c.LastMessageAt = &at
		c.LastMessagePreview = truncateRunes(preview, MaxPreviewRunes)
		c.ActivityAt = at
	}
	if inbound {
		c.UnreadCount++
		c.Archived = false
	}
	c.UpdatedAt = time.Now().UTC()
}

// MarkRead clears the unread counter.
func (c *Chat) MarkRead() {
	c.UnreadCount = 0
	c.UpdatedAt = time.Now().UTC()
}

// Rename sets the display name, trimmed and bounded.
func (c *Chat) Rename(name string) {
	c.Name = truncateRunes(strings.TrimSpace(name), MaxNameRunes)
	c.UpdatedAt = time.Now().UTC()
}

// SetLID records the chat's LID alias when lid is one.
func (c *Chat) SetLID(lid string) {
	if !isLID(lid) {
		return
	}
	c.LID = lid
	c.UpdatedAt = time.Now().UTC()
}

// Identify fills a missing LID alias and replaces a LID-keyed JID with a phone-number JID, reporting whether anything changed.
func (c *Chat) Identify(jid, lid string) bool {
	changed := false
	if lid != "" && c.LID == "" && isLID(lid) {
		c.SetLID(lid)
		changed = true
	}
	if validJID(jid) && !isLID(jid) && isLID(c.JID) {
		if c.LID == "" {
			c.LID = c.JID
		}
		c.JID = jid
		c.UpdatedAt = time.Now().UTC()
		changed = true
	}
	return changed
}

// Archive hides the chat from the default inbox view.
func (c *Chat) Archive() {
	c.Archived = true
	c.UpdatedAt = time.Now().UTC()
}

// Repair replaces the derived fields after retention deleted messages; a nil last clears the preview.
func (c *Chat) Repair(last *time.Time, preview string, unread int) {
	c.LastMessageAt = nil
	c.LastMessagePreview = ""
	c.ActivityAt = c.CreatedAt
	if last != nil {
		at := last.UTC()
		c.LastMessageAt = &at
		c.LastMessagePreview = truncateRunes(preview, MaxPreviewRunes)
		c.ActivityAt = at
	}
	c.UnreadCount = max(unread, 0)
	c.UpdatedAt = time.Now().UTC()
}

func validJID(s string) bool {
	user, server, ok := strings.Cut(s, "@")
	return ok && user != "" && server != "" && !strings.ContainsAny(s, " \t\r\n")
}

func isLID(s string) bool { return validJID(s) && strings.HasSuffix(s, "@lid") }

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
