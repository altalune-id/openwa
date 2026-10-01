package device

import (
	"slices"
	"strings"
)

// MatchInput is the normalised inbound message Rules.Match evaluates.
type MatchInput struct {
	IsGroup     bool
	ChatJID     string
	SenderPhone string
	FromMe      bool
	Body        string
	MentionedMe bool
	RepliedToMe bool
}

// Match reports whether in passes the rules and which rules it passed; reasons is nil on a miss.
func (r Rules) Match(in MatchInput) (matched bool, reasons []string) {
	reasons = make([]string, 0, 3)
	if in.FromMe && r.IgnoreFromMe {
		return false, nil
	}
	if len(r.AllowedSenders) > 0 {
		if in.SenderPhone == "" || !slices.Contains(r.AllowedSenders, in.SenderPhone) {
			return false, nil
		}
		reasons = append(reasons, "sender_allowed")
	}
	if !in.IsGroup {
		ok, prefixReason := r.matchPrefix(in.Body)
		if !ok {
			return false, nil
		}
		reasons = appendReason(reasons, prefixReason)
		return true, append(reasons, "dm")
	}
	if r.GroupMode == GroupIgnore {
		return false, nil
	}
	if len(r.AllowedGroups) > 0 && !slices.Contains(r.AllowedGroups, in.ChatJID) {
		return false, nil
	}
	switch r.GroupMode {
	case GroupMention:
		switch {
		case in.MentionedMe:
			reasons = append(reasons, "group_mention")
		case in.RepliedToMe:
			reasons = append(reasons, "group_reply")
		default:
			return false, nil
		}
	case GroupOpen:
		reasons = append(reasons, "group_open")
	default:
		return false, nil
	}
	ok, prefixReason := r.matchPrefix(in.Body)
	if !ok {
		return false, nil
	}
	return true, appendReason(reasons, prefixReason)
}

func (r Rules) matchPrefix(body string) (ok bool, reason string) {
	if r.TriggerPrefix == "" {
		return true, ""
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(body)), strings.ToLower(r.TriggerPrefix)) {
		return true, "prefix"
	}
	return false, ""
}

func appendReason(reasons []string, reason string) []string {
	if reason == "" {
		return reasons
	}
	return append(reasons, reason)
}
