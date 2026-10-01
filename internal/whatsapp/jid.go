package whatsapp

import (
	"strconv"
	"strings"
)

// WhatsApp JID servers.
const (
	ServerUser  = "s.whatsapp.net"
	ServerGroup = "g.us"
	ServerLID   = "lid"
)

// JID is a parsed WhatsApp identifier: user[.agent][:device]@server.
type JID struct {
	User   string
	Agent  uint8
	Device uint16
	Server string
}

// ParseJID parses the textual form; it accepts user@server, user:device@server and user.agent:device@server.
func ParseJID(s string) (JID, error) {
	user, server, ok := strings.Cut(s, "@")
	if !ok || server == "" || user == "" {
		return JID{}, &InvalidJIDError{Raw: s}
	}
	j := JID{Server: server}
	if base, dev, found := strings.Cut(user, ":"); found {
		n, err := strconv.ParseUint(dev, 10, 16)
		if err != nil {
			return JID{}, &InvalidJIDError{Raw: s}
		}
		j.Device = uint16(n)
		user = base
	}
	if base, agent, found := strings.Cut(user, "."); found {
		n, err := strconv.ParseUint(agent, 10, 8)
		if err != nil {
			return JID{}, &InvalidJIDError{Raw: s}
		}
		j.Agent = uint8(n)
		user = base
	}
	if user == "" {
		return JID{}, &InvalidJIDError{Raw: s}
	}
	j.User = user
	return j, nil
}

// String renders the JID in the form ParseJID accepts.
func (j JID) String() string {
	var b strings.Builder
	b.WriteString(j.User)
	if j.Agent != 0 {
		b.WriteByte('.')
		b.WriteString(strconv.Itoa(int(j.Agent)))
	}
	if j.Device != 0 {
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(int(j.Device)))
	}
	b.WriteByte('@')
	b.WriteString(j.Server)
	return b.String()
}

// NonAD strips the agent and device parts, the form used as a chat or contact key.
func (j JID) NonAD() JID { return JID{User: j.User, Server: j.Server} }

// NonAD returns s without its agent and device parts; an unparsable s is returned as is.
func NonAD(s string) string {
	j, err := ParseJID(s)
	if err != nil {
		return s
	}
	return j.NonAD().String()
}

// IsGroup reports whether s names a group chat.
func IsGroup(s string) bool { return strings.HasSuffix(s, "@"+ServerGroup) }

// IsLID reports whether s is a hidden-user (LID) identifier.
func IsLID(s string) bool { return strings.HasSuffix(s, "@"+ServerLID) }

// PhoneFromJID returns the phone digits of a phone-number JID, or "" for any other JID.
func PhoneFromJID(s string) string {
	j, err := ParseJID(s)
	if err != nil || j.Server != ServerUser {
		return ""
	}
	return j.User
}

// NormalizePhone turns "+62 812-3456" into "628123456"; a national number (leading 0) or a non-number is an *InvalidPhoneError.
func NormalizePhone(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "+")
	var b strings.Builder
	for _, c := range trimmed {
		switch {
		case c >= '0' && c <= '9':
			b.WriteRune(c)
		case c == ' ' || c == '-' || c == '.' || c == '(' || c == ')':
		default:
			return "", &InvalidPhoneError{Reason: "contains a character that is not a digit"}
		}
	}
	digits := b.String()
	if digits == "" {
		return "", &InvalidPhoneError{Reason: "empty"}
	}
	if strings.HasPrefix(digits, "0") {
		return "", &InvalidPhoneError{Reason: "national number; use the international form with a country code"}
	}
	if len(digits) < 8 || len(digits) > 15 {
		return "", &InvalidPhoneError{Reason: "must be 8 to 15 digits"}
	}
	return digits, nil
}

// ResolvePhone applies the umbrella rule: a phone-number sender is its phone; a LID sender resolves through the alt JID, then lookup; otherwise "".
func ResolvePhone(sender, senderAlt string, lookup func(lid string) string) string {
	if !IsLID(sender) {
		return PhoneFromJID(sender)
	}
	if p := PhoneFromJID(senderAlt); p != "" {
		return p
	}
	if lookup == nil {
		return ""
	}
	return PhoneFromJID(lookup(NonAD(sender)))
}
