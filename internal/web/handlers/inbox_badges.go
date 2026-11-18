package handlers

import (
	"strings"

	"github.com/google/uuid"

	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/web/templates"
)

//i18n:use inbox.match.*
func (h *InboxHandler) badger(sc ProjectScope, deviceID uuid.UUID) func(*message.Message) *templates.MatchBadge {
	tr := h.ProjectFragmentBase(sc).Tr
	dv, err := h.Devices.Get(sc.req.Context(), deviceID)
	if err != nil {
		h.LogErr("web inbox: rules for badges", err)
		return func(*message.Message) *templates.MatchBadge { return nil }
	}
	names := map[string]string{}
	for _, r := range dv.Device.Rules.Items {
		names[r.ID] = r.Name
	}
	rules := h.ProjectURL(sc, "/devices/"+dv.Device.PublicID+"/rules")
	ruleHref := func(id string) string {
		if base, ok := strings.CutSuffix(id, dmRuleSuffix); ok {
			if _, paired := names[base]; paired {
				id = base
			}
		}
		return rules + "/" + id
	}
	return func(m *message.Message) *templates.MatchBadge {
		if m.Direction != message.DirectionIn || m.FromMe {
			return nil
		}
		b := badgeFor(m, names, rules, ruleHref, tr)
		b.Label = tr("inbox.match." + b.Kind)
		return b
	}
}

func badgeFor(m *message.Message, names map[string]string, rules string, ruleHref func(string) string, tr func(string, ...any) string) *templates.MatchBadge {
	name, known := names[m.MatchedRuleID]
	switch {
	case m.MatchOutcome == message.MatchNone:
		return &templates.MatchBadge{Kind: "not_sent", Reason: tr("inbox.match.no_rule"), Href: rules}
	case m.MatchOutcome == message.MatchIgnored && !known:
		return &templates.MatchBadge{Kind: "not_sent", Reason: tr("inbox.match.ignored_rule_gone"), Href: rules}
	case m.MatchOutcome == message.MatchIgnored:
		return &templates.MatchBadge{Kind: "not_sent", Reason: tr("inbox.match.ignored_rule", "Name", ruleName(tr, name)), Href: ruleHref(m.MatchedRuleID)}
	case m.MatchOutcome == message.MatchMatched && !known:
		return &templates.MatchBadge{Kind: "sent", Reason: tr("inbox.match.sent_rule_gone"), Href: rules}
	case m.MatchOutcome == message.MatchMatched:
		return &templates.MatchBadge{Kind: "sent", Reason: tr("inbox.match.sent_rule", "Name", ruleName(tr, name)), Href: ruleHref(m.MatchedRuleID)}
	}
	return &templates.MatchBadge{Kind: "not_evaluated", Reason: tr("inbox.match.not_evaluated_body")}
}
