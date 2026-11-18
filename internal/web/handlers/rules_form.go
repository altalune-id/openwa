package handlers

import (
	"net/url"
	"slices"
	"strconv"
	"strings"

	"altalune.id/openwa/internal/device"
)

const (
	groupsNone  = "none"
	groupsThese = "these"
	groupsAll   = "all"

	whoEveryone = "everyone"
	whoOnly     = "only"
	whoExcept   = "except"

	blockWhere = "where"
	blockWho   = "who"

	dmRuleSuffix = "-dm"
)

type textRow struct {
	Op     device.Op
	Values []string
}

type ruleForm struct {
	ID      string
	Name    string
	Action  device.Action
	Enabled bool
	DM      bool
	Groups  string
	Chats   []string
	Mention bool
	Who     string
	Senders []string
	Texts   []textRow
}

type ruleFormError struct {
	Block string
	Key   string
}

func parseRuleForm(id string, v url.Values) ruleForm {
	f := ruleForm{
		ID:      id,
		Name:    strings.TrimSpace(v.Get("name")),
		Action:  device.ActionMatch,
		Enabled: v.Get("enabled") != "0",
		DM:      v.Get("dm") == "1",
		Groups:  oneOf(v.Get("groups"), groupsNone, groupsThese, groupsAll),
		Chats:   nonEmpty(v["chat"]),
		Mention: v.Get("mention") == "1",
		Who:     oneOf(v.Get("who"), whoEveryone, whoOnly, whoExcept),
		Senders: nonEmpty(v["sender"]),
	}
	if v.Get("action") == string(device.ActionIgnore) {
		f.Action = device.ActionIgnore
	}
	for i := range device.MaxConditions {
		op := v.Get("text." + strconv.Itoa(i) + ".op")
		if op == "" {
			continue
		}
		f.Texts = append(f.Texts, textRow{Op: device.Op(op), Values: nonEmpty(v["text."+strconv.Itoa(i)+".value"])})
	}
	return f
}

func oneOf(v, fallback string, allowed ...string) string {
	if slices.Contains(allowed, v) {
		return v
	}
	return fallback
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

//i18n:use rules.error.*
func (f ruleForm) validate() *ruleFormError {
	if !f.DM && f.Groups == groupsNone {
		return &ruleFormError{Block: blockWhere, Key: "rules.error.where_nowhere"}
	}
	if f.Groups == groupsThese && len(f.Chats) == 0 {
		return &ruleFormError{Block: blockWhere, Key: "rules.error.where_groups"}
	}
	if f.Who != whoEveryone && len(f.Senders) == 0 {
		return &ruleFormError{Block: blockWho, Key: "rules.error.who_empty"}
	}
	for i, t := range f.Texts {
		if len(t.Values) == 0 {
			return &ruleFormError{Block: textBlock(i), Key: "rules.error.text_empty"}
		}
	}
	return nil
}

func (f ruleForm) pairable(fresh func() string) ruleForm {
	if !f.DM || f.Groups == groupsNone {
		return f
	}
	if strings.HasPrefix(f.ID, "legacy-") || len(f.ID) > device.MaxRuleIDLength-len(dmRuleSuffix) || strings.HasSuffix(f.ID, dmRuleSuffix) {
		f.ID = fresh()
	}
	return f
}

func textBlock(i int) string { return "t" + strconv.Itoa(i) }

func (f ruleForm) rules() ([]device.Rule, *ruleFormError) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	var shared []device.Condition
	switch f.Who {
	case whoOnly:
		shared = append(shared, device.Condition{Field: device.FieldSender, Op: device.OpIs, Values: slices.Clone(f.Senders)})
	case whoExcept:
		shared = append(shared, device.Condition{Field: device.FieldSender, Op: device.OpIsNot, Values: slices.Clone(f.Senders)})
	}
	for _, t := range f.Texts {
		shared = append(shared, device.Condition{Field: device.FieldText, Op: t.Op, Values: slices.Clone(t.Values)})
	}
	rule := func(id string, head ...device.Condition) device.Rule {
		return device.Rule{ID: id, Name: f.Name, Enabled: f.Enabled, Action: f.Action, Conditions: slices.Concat(head, shared)}
	}
	dm := device.Condition{Field: device.FieldChatKind, Op: device.OpIs, Values: []string{"dm"}}
	if f.Groups == groupsNone {
		return []device.Rule{rule(f.ID, dm)}, nil
	}
	group := []device.Condition{{Field: device.FieldChatKind, Op: device.OpIs, Values: []string{"group"}}}
	if f.Groups == groupsThese {
		group = append(group, device.Condition{Field: device.FieldChat, Op: device.OpIs, Values: slices.Clone(f.Chats)})
	}
	if f.Mention {
		group = append(group, device.Condition{Field: device.FieldMention})
	}
	if !f.DM {
		return []device.Rule{rule(f.ID, group...)}, nil
	}
	return []device.Rule{rule(f.ID, group...), rule(f.ID+dmRuleSuffix, dm)}, nil
}

// NOTE: n is how many items the form covers: 2 when items[i] and items[i+1] are a group rule and its "-dm" twin.
func formAt(items []device.Rule, i int) (f ruleForm, n int, ok bool) {
	f, ok = formFromRule(items[i])
	if !ok {
		return ruleForm{}, 1, false
	}
	if f.DM || f.Groups == groupsNone || i+1 >= len(items) || items[i+1].ID != items[i].ID+dmRuleSuffix {
		return f, 1, true
	}
	twin, twinOK := formFromRule(items[i+1])
	if !twinOK || !twin.DM || twin.Groups != groupsNone || twin.Action != f.Action || twin.Enabled != f.Enabled ||
		twin.Who != f.Who || !slices.Equal(twin.Senders, f.Senders) || !slices.EqualFunc(twin.Texts, f.Texts, sameTextRow) {
		return f, 1, true
	}
	f.DM = true
	return f, 2, true
}

func sameTextRow(a, b textRow) bool { return a.Op == b.Op && slices.Equal(a.Values, b.Values) }

func formFromRule(r device.Rule) (ruleForm, bool) {
	f := ruleForm{ID: r.ID, Name: r.Name, Action: r.Action, Enabled: r.Enabled, Groups: groupsNone, Who: whoEveryone}
	var kind, chat, sender bool
	for _, c := range r.Conditions {
		switch {
		case c.Field == device.FieldChatKind && c.Op == device.OpIs && !kind:
			kind = true
			f.DM = slices.Contains(c.Values, "dm")
			if slices.Contains(c.Values, "group") {
				f.Groups = groupsAll
			}
		case c.Field == device.FieldChat && c.Op == device.OpIs && !chat:
			chat = true
			f.Chats = slices.Clone(c.Values)
		case c.Field == device.FieldMention && !f.Mention:
			f.Mention = true
		case c.Field == device.FieldSender && (c.Op == device.OpIs || c.Op == device.OpIsNot) && !sender:
			sender = true
			f.Who, f.Senders = whoOnly, slices.Clone(c.Values)
			if c.Op == device.OpIsNot {
				f.Who = whoExcept
			}
		case c.Field == device.FieldText:
			f.Texts = append(f.Texts, textRow{Op: c.Op, Values: slices.Clone(c.Values)})
		default:
			return ruleForm{}, false
		}
	}
	if !kind || f.Groups == groupsNone && (chat || f.Mention) || f.DM && (chat || f.Mention) {
		return ruleForm{}, false
	}
	if chat {
		f.Groups = groupsThese
	}
	return f, true
}
