package device

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Rules limits.
const (
	MaxRules          = 50
	MaxConditions     = 20
	MaxValues         = 100
	MaxTextValueRunes = 256
	MaxRuleNameRunes  = 64
	MaxRuleIDLength   = 64
	MaxEncodedRules   = 64 << 10
)

// Legacy rule ids, the only ids allowed to start with "legacy-".
const (
	LegacyDMRuleID    = "legacy-dm"
	LegacyGroupRuleID = "legacy-group"
)

// Field is what a condition looks at.
type Field string

// Field values.
const (
	FieldChatKind Field = "chat_kind"
	FieldChat     Field = "chat"
	FieldSender   Field = "sender"
	FieldText     Field = "text"
	FieldMention  Field = "mention"
)

// Op is how a condition compares.
type Op string

// Op values.
const (
	OpIs         Op = "is"
	OpIsNot      Op = "is_not"
	OpContains   Op = "contains"
	OpStartsWith Op = "starts_with"
	OpEquals     Op = "equals"
)

// Action is what a deciding rule does.
type Action string

// Action values.
const (
	ActionMatch  Action = "match"
	ActionIgnore Action = "ignore"
)

// Outcome is the result of evaluating the rules on one message.
type Outcome string

// Outcome values.
const (
	OutcomeMatched Outcome = "matched"
	OutcomeIgnored Outcome = "ignored"
	OutcomeNone    Outcome = "none"
)

// PhoneSenderPrefix marks a sender value compared with the sender's phone digits.
const PhoneSenderPrefix = "phone:"

// Rules is the ordered inbound-message filter attached to a device.
type Rules struct {
	IgnoreFromMe bool
	Items        []Rule
}

// Rule is one entry; the first enabled rule whose conditions all hold decides.
type Rule struct {
	ID         string
	Name       string
	Enabled    bool
	Conditions []Condition
	Action     Action
}

// Condition holds when any of Values compares true; mention takes no Op or Values.
type Condition struct {
	Field  Field
	Op     Op
	Values []string
}

// MatchResult is the deciding rule and why it held; RuleID is empty for OutcomeNone.
type MatchResult struct {
	Outcome  Outcome
	RuleID   string
	RuleName string
	Reasons  []string
}

// MatchInput is the normalised inbound message Rules.Match evaluates.
type MatchInput struct {
	IsGroup     bool
	ChatJID     string
	SenderPhone string
	SenderJID   string
	SenderLID   string
	FromMe      bool
	Body        string
	MentionedMe bool
	RepliedToMe bool
}

// Match evaluates the rules in order and returns the deciding rule, or OutcomeNone.
func (r Rules) Match(in MatchInput) MatchResult {
	if in.FromMe && r.IgnoreFromMe {
		return MatchResult{Outcome: OutcomeNone}
	}
	for _, rule := range r.Items {
		if !rule.Enabled {
			continue
		}
		reasons, ok := rule.holds(in)
		if !ok {
			continue
		}
		out := MatchResult{Outcome: OutcomeMatched, RuleID: rule.ID, RuleName: rule.Name, Reasons: reasons}
		if rule.Action == ActionIgnore {
			out.Outcome = OutcomeIgnored
		}
		return out
	}
	return MatchResult{Outcome: OutcomeNone}
}

type reasonSet struct {
	senderAllowed, senderExcluded, chat, mention, text, prefix bool
}

func (r Rule) holds(in MatchInput) ([]string, bool) {
	var rs reasonSet
	dmAdmitted := false
	for _, c := range r.Conditions {
		ok := c.holds(in)
		if !ok {
			return nil, false
		}
		switch c.Field {
		case FieldChatKind:
			dmAdmitted = dmAdmitted || !in.IsGroup
		case FieldChat:
			rs.chat = true
		case FieldSender:
			rs.senderAllowed = rs.senderAllowed || c.Op == OpIs
			rs.senderExcluded = rs.senderExcluded || c.Op == OpIsNot
		case FieldText:
			rs.prefix = rs.prefix || c.Op == OpStartsWith
			rs.text = rs.text || c.Op != OpStartsWith
		case FieldMention:
			rs.mention = true
		}
	}
	if !in.IsGroup && !dmAdmitted {
		return nil, false
	}
	return rs.tokens(in), true
}

func (rs reasonSet) tokens(in MatchInput) []string {
	out := make([]string, 0, 4)
	add := func(ok bool, token string) {
		if ok {
			out = append(out, token)
		}
	}
	add(rs.senderAllowed, "sender_allowed")
	add(rs.senderExcluded, "sender_excluded")
	add(rs.chat, "chat")
	if in.IsGroup {
		switch {
		case !rs.mention:
			out = append(out, "group_open")
		case in.MentionedMe:
			out = append(out, "group_mention")
		default:
			out = append(out, "group_reply")
		}
	}
	if !in.IsGroup && rs.mention {
		add(in.MentionedMe, "mention")
		add(!in.MentionedMe, "reply")
	}
	add(rs.text, "text")
	add(rs.prefix, "prefix")
	add(!in.IsGroup, "dm")
	return out
}

func (c Condition) holds(in MatchInput) bool {
	if c.Field == FieldMention {
		return in.MentionedMe || in.RepliedToMe
	}
	hit := slices.ContainsFunc(c.Values, func(v string) bool { return c.valueHolds(v, in) })
	if c.Op == OpIsNot {
		return !hit
	}
	return hit
}

func (c Condition) valueHolds(v string, in MatchInput) bool {
	switch c.Field {
	case FieldChatKind:
		return (v == "group") == in.IsGroup
	case FieldChat:
		return in.IsGroup && v == in.ChatJID
	case FieldSender:
		if digits, ok := strings.CutPrefix(v, PhoneSenderPrefix); ok {
			return in.SenderPhone != "" && digits == in.SenderPhone
		}
		return (in.SenderJID != "" && v == in.SenderJID) || (in.SenderLID != "" && v == in.SenderLID)
	case FieldText:
		body := strings.ToLower(strings.TrimSpace(in.Body))
		v = strings.ToLower(v)
		switch c.Op {
		case OpContains:
			return strings.Contains(body, v)
		case OpStartsWith:
			return strings.HasPrefix(body, v)
		case OpEquals:
			return body == v
		}
	}
	return false
}

// FromLegacy converts the pre-builder shape: a DM rule and, unless groups are ignored, a group rule.
func FromLegacy(l Legacy) Rules {
	shared := func() []Condition {
		var out []Condition
		if len(l.AllowedSenders) > 0 {
			out = append(out, Condition{Field: FieldSender, Op: OpIs, Values: phoneValues(l.AllowedSenders)})
		}
		if l.TriggerPrefix != "" {
			out = append(out, Condition{Field: FieldText, Op: OpStartsWith, Values: []string{l.TriggerPrefix}})
		}
		return out
	}
	dm := Rule{
		ID: LegacyDMRuleID, Name: "Direct messages", Enabled: true, Action: ActionMatch,
		Conditions: append([]Condition{{Field: FieldChatKind, Op: OpIs, Values: []string{"dm"}}}, shared()...),
	}
	out := Rules{IgnoreFromMe: l.IgnoreFromMe, Items: []Rule{dm}}
	if l.GroupMode != GroupOpen && l.GroupMode != GroupMention {
		return out
	}
	conds := []Condition{{Field: FieldChatKind, Op: OpIs, Values: []string{"group"}}}
	if len(l.AllowedGroups) > 0 {
		conds = append(conds, Condition{Field: FieldChat, Op: OpIs, Values: slices.Clone(l.AllowedGroups)})
	}
	if l.GroupMode == GroupMention {
		conds = append(conds, Condition{Field: FieldMention})
	}
	conds = append(conds, shared()...)
	out.Items = append(out.Items, Rule{ID: LegacyGroupRuleID, Name: "Groups", Enabled: true, Action: ActionMatch, Conditions: conds})
	return out
}

func phoneValues(phones []string) []string {
	out := make([]string, 0, len(phones))
	for _, p := range phones {
		out = append(out, PhoneSenderPrefix+p)
	}
	return out
}

// Legacy returns the pre-builder shape when the items say exactly what it can say; names are ignored.
func (r Rules) Legacy() (Legacy, bool) {
	if len(r.Items) == 0 || len(r.Items) > 2 || r.Items[0].ID != LegacyDMRuleID {
		return Legacy{}, false
	}
	l := Legacy{GroupMode: GroupIgnore, AllowedSenders: []string{}, AllowedGroups: []string{}, IgnoreFromMe: r.IgnoreFromMe}
	for _, c := range r.Items[0].Conditions {
		switch {
		case c.Field == FieldSender && c.Op == OpIs:
			for _, v := range c.Values {
				l.AllowedSenders = append(l.AllowedSenders, strings.TrimPrefix(v, PhoneSenderPrefix))
			}
		case c.Field == FieldText && c.Op == OpStartsWith && len(c.Values) == 1:
			l.TriggerPrefix = c.Values[0]
		}
	}
	if len(r.Items) == 2 {
		l.GroupMode = GroupOpen
		for _, c := range r.Items[1].Conditions {
			switch c.Field {
			case FieldChat:
				l.AllowedGroups = slices.Clone(c.Values)
			case FieldMention:
				l.GroupMode = GroupMention
			}
		}
	}
	if !sameBehavior(FromLegacy(l).Items, r.Items) {
		return Legacy{}, false
	}
	return l, true
}

// LegacyView is Legacy for reads: rules the legacy shape cannot express read as GroupCustom with no lists.
func (r Rules) LegacyView() Legacy {
	if l, ok := r.Legacy(); ok {
		return l
	}
	return Legacy{GroupMode: GroupCustom, IgnoreFromMe: r.IgnoreFromMe}
}

func sameBehavior(a, b []Rule) bool {
	return slices.EqualFunc(a, b, func(x, y Rule) bool {
		return x.ID == y.ID && x.Enabled == y.Enabled && x.Action == y.Action &&
			slices.EqualFunc(x.Conditions, y.Conditions, func(p, q Condition) bool {
				return p.Field == q.Field && p.Op == q.Op && slices.Equal(p.Values, q.Values)
			})
	})
}

func normalizeRules(r Rules) (Rules, error) {
	if len(r.Items) > MaxRules {
		return Rules{}, &InvalidRulesError{Field: "items", Reason: fmt.Sprintf("more than %d rules", MaxRules)}
	}
	out := Rules{IgnoreFromMe: r.IgnoreFromMe, Items: make([]Rule, 0, len(r.Items))}
	seen := make(map[string]struct{}, len(r.Items))
	for i, rule := range r.Items {
		n, err := normalizeRule(rule, fmt.Sprintf("items[%d]", i))
		if err != nil {
			return Rules{}, err
		}
		if _, dup := seen[n.ID]; dup {
			return Rules{}, &InvalidRulesError{Field: fmt.Sprintf("items[%d].id", i), Reason: "duplicate id"}
		}
		seen[n.ID] = struct{}{}
		out.Items = append(out.Items, n)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return Rules{}, fmt.Errorf("device: rules: measure: %w", err)
	}
	if len(b) > MaxEncodedRules {
		return Rules{}, &InvalidRulesError{Field: "items", Reason: "over 64 KiB"}
	}
	return out, nil
}

func normalizeRule(r Rule, at string) (Rule, error) {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if !validRuleID(r.ID) {
		return Rule{}, &InvalidRulesError{Field: at + ".id", Reason: "must be 1 to 64 letters, digits, - or _, and not start with legacy- unless it is legacy-dm or legacy-group"}
	}
	r.Name = strings.TrimSpace(r.Name)
	if utf8.RuneCountInString(r.Name) > MaxRuleNameRunes {
		return Rule{}, &InvalidRulesError{Field: at + ".name", Reason: "over 64 characters"}
	}
	if r.Action == "" {
		r.Action = ActionMatch
	}
	if r.Action != ActionMatch && r.Action != ActionIgnore {
		return Rule{}, &InvalidRulesError{Field: at + ".action", Reason: "must be match or ignore"}
	}
	if len(r.Conditions) > MaxConditions {
		return Rule{}, &InvalidRulesError{Field: at + ".conditions", Reason: fmt.Sprintf("more than %d conditions", MaxConditions)}
	}
	conds := make([]Condition, 0, len(r.Conditions))
	for j, c := range r.Conditions {
		n, err := normalizeCondition(c, fmt.Sprintf("%s.conditions[%d]", at, j))
		if err != nil {
			return Rule{}, err
		}
		conds = append(conds, n)
	}
	r.Conditions = conds
	return r, nil
}

func validRuleID(id string) bool {
	if id == "" || len(id) > MaxRuleIDLength {
		return false
	}
	if strings.HasPrefix(id, "legacy-") && id != LegacyDMRuleID && id != LegacyGroupRuleID {
		return false
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

func normalizeCondition(c Condition, at string) (Condition, error) {
	if c.Field == FieldMention {
		if c.Op != "" || len(c.Values) > 0 {
			return Condition{}, &InvalidRulesError{Field: at, Reason: "mention takes no op or values"}
		}
		return Condition{Field: FieldMention}, nil
	}
	ops, valid, reason := fieldSpec(c.Field)
	if ops == nil {
		return Condition{}, &InvalidRulesError{Field: at + ".field", Reason: "must be chat_kind, chat, sender, text or mention"}
	}
	if !slices.Contains(ops, c.Op) {
		return Condition{}, &InvalidRulesError{Field: at + ".op", Reason: "not allowed for " + string(c.Field)}
	}
	if len(c.Values) > MaxValues {
		return Condition{}, &InvalidRulesError{Field: at + ".values", Reason: fmt.Sprintf("more than %d values", MaxValues)}
	}
	values, err := normalizeList(c.Values, at+".values", valid, reason)
	if err != nil {
		return Condition{}, err
	}
	if len(values) == 0 {
		return Condition{}, &InvalidRulesError{Field: at + ".values", Reason: "empty"}
	}
	return Condition{Field: c.Field, Op: c.Op, Values: values}, nil
}

func fieldSpec(f Field) (ops []Op, valid func(string) bool, reason string) {
	switch f {
	case FieldChatKind:
		return []Op{OpIs, OpIsNot}, func(v string) bool { return v == "dm" || v == "group" }, "must be dm or group"
	case FieldChat:
		return []Op{OpIs, OpIsNot}, isGroupJID, "must be a group JID ending in @g.us"
	case FieldSender:
		return []Op{OpIs, OpIsNot}, isSenderValue, "must be phone:<8 to 15 digits> or a JID ending in @s.whatsapp.net or @lid"
	case FieldText:
		return []Op{OpContains, OpStartsWith, OpEquals}, func(v string) bool { return utf8.RuneCountInString(v) <= MaxTextValueRunes }, "over 256 characters"
	}
	return nil, nil, ""
}

func isSenderValue(v string) bool {
	if digits, ok := strings.CutPrefix(v, PhoneSenderPrefix); ok {
		return isPhoneDigits(digits)
	}
	for _, server := range []string{"@s.whatsapp.net", "@lid"} {
		if user, ok := strings.CutSuffix(v, server); ok {
			return user != "" && !strings.ContainsAny(user, "@ :")
		}
	}
	return false
}
