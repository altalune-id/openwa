package handlers

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"altalune.id/openwa/internal/chat"
	"altalune.id/openwa/internal/contact"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/message"
	"altalune.id/openwa/internal/web"
	"altalune.id/openwa/internal/web/templates"
	"altalune.id/openwa/internal/whatsapp"
)

const maxAutoNameRunes = 60

type builderInput struct {
	form    ruleForm
	isNew   bool
	custom  bool
	test    bool
	formErr *ruleFormError
	saveErr error
}

type ruleLabels struct {
	chats    map[string]string
	contacts []*contact.Contact
	groups   []*chat.Chat
	dms      []*chat.Chat
}

func (h *RulesHandler) labels(ctx context.Context, deviceID device.DeviceView) (ruleLabels, error) {
	id := deviceID.Device.ID
	group, dm := chat.KindGroup, chat.KindDM
	groups, _, err := h.Chats.List(ctx, chat.ListOpts{DeviceID: &id, Kind: &group, Limit: chat.MaxListLimit})
	if err != nil {
		return ruleLabels{}, err
	}
	dms, _, err := h.Chats.List(ctx, chat.ListOpts{DeviceID: &id, Kind: &dm, Limit: chat.MaxListLimit})
	if err != nil {
		return ruleLabels{}, err
	}
	contacts, _, err := h.Contacts.List(ctx, contact.ListOpts{DeviceID: &id, Limit: contact.MaxListLimit})
	if err != nil {
		return ruleLabels{}, err
	}
	l := ruleLabels{chats: map[string]string{}, contacts: contacts, groups: groups, dms: dms}
	for _, c := range groups {
		l.chats[c.JID] = c.DisplayName()
	}
	return l, nil
}

func (l ruleLabels) chat(jid string) string {
	if name, ok := l.chats[jid]; ok {
		return name
	}
	return (&chat.Chat{JID: jid}).DisplayName()
}

// NOTE: a hidden-number member is never shown by its raw @lid; tr labels it instead.
//
//i18n:use rules.hidden_number
func (l ruleLabels) sender(tr func(string, ...any) string, v string) string {
	for _, c := range l.contacts {
		if v == device.PhoneSenderPrefix+c.Phone && c.Phone != "" || v == c.JID || v == c.LID && c.LID != "" {
			return contactLabel(tr, c)
		}
	}
	if digits, ok := strings.CutPrefix(v, device.PhoneSenderPrefix); ok {
		return "+" + digits
	}
	if strings.HasSuffix(v, "@lid") {
		return tr("rules.hidden_number")
	}
	user, _, _ := strings.Cut(v, "@")
	return "+" + user
}

func (l ruleLabels) senderChips(tr func(string, ...any) string, values []string) []templates.RuleChip {
	out := make([]templates.RuleChip, 0, len(values))
	for _, v := range values {
		out = append(out, templates.RuleChip{Value: v, Label: l.sender(tr, v)})
	}
	return out
}

func (l ruleLabels) chatChips(values []string) []templates.RuleChip {
	out := make([]templates.RuleChip, 0, len(values))
	for _, v := range values {
		out = append(out, templates.RuleChip{Value: v, Label: l.chat(v)})
	}
	return out
}

const slot = "\x00"

func fill(tr func(string, ...any) string, key string, slots map[string][]templates.SentencePart) []templates.SentencePart {
	args := make([]any, 0, 2*len(slots))
	for name := range slots {
		args = append(args, name, slot+name+slot)
	}
	var out []templates.SentencePart
	for i, piece := range strings.Split(tr(key, args...), slot) {
		if i%2 == 1 {
			out = append(out, slots[piece]...)
			continue
		}
		if piece != "" {
			out = append(out, templates.SentencePart{Text: piece})
		}
	}
	return out
}

func text(s string) []templates.SentencePart { return []templates.SentencePart{{Text: s}} }

//i18n:use rules.sentence.*
//i18n:use rules.action.*
func sentence(tr func(string, ...any) string, f ruleForm, l ruleLabels) []templates.SentencePart {
	groups := make([]string, 0, len(f.Chats))
	for _, c := range f.Chats {
		groups = append(groups, l.chat(c))
	}
	where := []templates.SentencePart{{Text: tr("rules.sentence.nowhere"), Muted: true}}
	switch {
	case f.DM && f.Groups == groupsThese:
		where = fill(tr, "rules.sentence.where_dm_groups", map[string][]templates.SentencePart{"Groups": orList(tr, groups, tr("rules.sentence.your_groups"), false)})
	case f.DM && f.Groups == groupsAll:
		where = text(tr("rules.sentence.where_dm_any_group"))
	case f.Groups == groupsThese:
		where = fill(tr, "rules.sentence.where_groups", map[string][]templates.SentencePart{"Groups": orList(tr, groups, tr("rules.sentence.your_groups"), false)})
	case f.Groups == groupsAll:
		where = text(tr("rules.sentence.where_any_group"))
	}
	var mention []templates.SentencePart
	if f.Mention && f.Groups != groupsNone {
		mention = text(tr("rules.sentence.mention"))
	}
	people := make([]string, 0, len(f.Senders))
	for _, s := range f.Senders {
		people = append(people, l.sender(tr, s))
	}
	who := text(tr("rules.sentence.who_anyone"))
	switch f.Who {
	case whoOnly:
		who = fill(tr, "rules.sentence.who_only", map[string][]templates.SentencePart{"People": orList(tr, people, "…", false)})
	case whoExcept:
		who = fill(tr, "rules.sentence.who_except", map[string][]templates.SentencePart{"People": orList(tr, people, "…", false)})
	}
	texts := make([]templates.SentencePart, 0, 4*len(f.Texts))
	for _, t := range f.Texts {
		texts = append(texts, fill(tr, textOpKey(t.Op), map[string][]templates.SentencePart{"Words": orList(tr, t.Values, tr("rules.sentence.a_word"), true)})...)
	}
	key := "rules.sentence.full"
	if f.DM && f.Groups == groupsNone {
		key = "rules.sentence.full_dm"
	}
	return fill(tr, key, map[string][]templates.SentencePart{
		"Where": where, "Mention": mention, "Who": who, "Text": texts, "Action": {{Text: tr(actionKey(f.Action)), Bold: true}},
	})
}

func textOpKey(op device.Op) string {
	switch op {
	case device.OpStartsWith:
		return "rules.sentence.text_starts_with"
	case device.OpEquals:
		return "rules.sentence.text_equals"
	}
	return "rules.sentence.text_contains"
}

func actionKey(a device.Action) string {
	if a == device.ActionIgnore {
		return "rules.action.ignore"
	}
	return "rules.action.match"
}

func orList(tr func(string, ...any) string, names []string, empty string, quote bool) []templates.SentencePart {
	if len(names) == 0 {
		return []templates.SentencePart{{Text: empty, Muted: true}}
	}
	out := make([]templates.SentencePart, 0, 2*len(names))
	for i, n := range names {
		switch {
		case i == 0:
		case i == len(names)-1:
			out = append(out, templates.SentencePart{Text: tr("rules.sentence.list_or")})
		default:
			out = append(out, templates.SentencePart{Text: tr("rules.sentence.list_comma")})
		}
		if quote {
			n = tr("rules.sentence.quoted", "Word", n)
		}
		out = append(out, templates.SentencePart{Text: n, Bold: true})
	}
	return out
}

func sentenceName(tr func(string, ...any) string, f ruleForm, l ruleLabels) string {
	r := []rune(strings.TrimSpace(plainText(sentence(tr, f, l))))
	if len(r) <= maxAutoNameRunes {
		return string(r)
	}
	cut := string(r[:maxAutoNameRunes-1])
	if i := strings.LastIndexAny(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:、،") + "…"
}

func plainText(parts []templates.SentencePart) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

//i18n:use rules.custom_summary
func customSentence(tr func(string, ...any) string, trn func(string, int, ...any) string, items []device.Rule) []templates.SentencePart {
	n := 0
	for _, r := range items {
		n += len(r.Conditions)
	}
	counted := func(key string, args ...any) string { return trn(key, n, args...) }
	return fill(counted, "rules.custom_summary", map[string][]templates.SentencePart{"Action": {{Text: tr(actionKey(items[0].Action)), Bold: true}}})
}

func (h *RulesHandler) rulesLayout(sc ProjectScope, dv device.DeviceView, leaf string) web.LayoutData {
	l := h.layout(sc, "")
	l.Title = l.Tr("rules.title")
	l.Crumbs[len(l.Crumbs)-1].Href = h.ProjectURL(sc, "/devices")
	l.Crumbs = append(l.Crumbs, web.Crumb{Label: dv.Device.Name, Href: h.ProjectURL(sc, "/devices/"+dv.Device.PublicID)})
	l.ActiveNav.Parent = "devices"
	rules := web.Crumb{Label: l.Tr("rules.crumb")}
	if leaf == "" {
		l.Crumbs = append(l.Crumbs, rules)
		return l
	}
	rules.Href = h.ProjectURL(sc, rulesPath(dv))
	l.Crumbs = append(l.Crumbs, rules, web.Crumb{Label: leaf})
	return l
}

func (h *RulesHandler) listView(sc ProjectScope, dv device.DeviceView) (templates.RulesListView, error) {
	ctx := sc.req.Context()
	hits, err := h.Messages.RuleHits(ctx, dv.Device.ID)
	if err != nil {
		return templates.RulesListView{}, err
	}
	labels, err := h.labels(ctx, dv)
	if err != nil {
		return templates.RulesListView{}, err
	}
	tr := h.ProjectFragmentBase(sc).Tr
	items := dv.Device.Rules.Items
	groups := ruleGroups(items)
	v := templates.RulesListView{
		ProjectSlug: sc.project.Slug,
		DeviceID:    dv.Device.PublicID,
		Version:     dv.Device.Version,
		CanManage:   h.canManageProject(sc),
		CanAdd:      len(items) < device.MaxRules-1,
		NewRuleID:   newRuleID(),
	}
	for i, g := range groups {
		row := templates.RuleRowView{ID: g.ID, Name: items[g.At].Name, Enabled: items[g.At].Enabled, First: i == 0, Last: i == len(groups)-1, Custom: !g.OK}
		for _, id := range g.IDs {
			hit := hits[id]
			row.Hits += hit.Count
			if hit.Count > 0 && (row.LastAt == nil || hit.LastAt.After(*row.LastAt)) {
				at := hit.LastAt
				row.LastAt = &at
			}
		}
		row.Sentence = sentence(tr, g.Form, labels)
		if !g.OK {
			row.Sentence = customSentence(tr, h.ProjectFragmentBase(sc).TrN, items[g.At:g.At+g.N])
		}
		v.Rows = append(v.Rows, row)
	}
	return v, nil
}

func (h *RulesHandler) writeBuilder(w http.ResponseWriter, sc ProjectScope, dv device.DeviceView, status int, in builderInput) {
	ctx := sc.req.Context()
	labels, err := h.labels(ctx, dv)
	if err != nil {
		h.LogErr("web rules: labels", err)
		h.ErrorPageKey(w, sc.req, http.StatusInternalServerError, "error.load_failed", err)
		return
	}
	base := h.ProjectFragmentBase(sc)
	tr := base.Tr
	f := in.form
	canManage := h.canManageProject(sc)
	v := templates.RuleBuilderView{
		ProjectSlug: sc.project.Slug,
		DeviceID:    dv.Device.PublicID,
		Version:     dv.Device.Version,
		RuleID:      f.ID,
		IsNew:       in.isNew,
		Custom:      in.custom,
		ReadOnly:    in.custom || !canManage,
		CanManage:   canManage,
		Name:        f.Name,
		Action:      string(f.Action),
		Enabled:     f.Enabled,
		DM:          f.DM,
		Groups:      f.Groups,
		Chats:       labels.chatChips(f.Chats),
		Mention:     f.Mention,
		Who:         f.Who,
		Senders:     labels.senderChips(tr, f.Senders),
		DMTakenBy:   earlierAllDMRule(dv.Device.Rules.Items, f.ID),
	}
	for i, t := range f.Texts {
		v.Texts = append(v.Texts, templates.RuleTextView{Index: i, Op: string(t.Op), Values: t.Values})
	}
	v.Sentence = sentence(tr, f, labels)
	if groups := ruleGroups(dv.Device.Rules.Items); in.custom && groupIndex(groups, f.ID) >= 0 {
		g := groups[groupIndex(groups, f.ID)]
		v.Sentence = customSentence(tr, base.TrN, dv.Device.Rules.Items[g.At:g.At+g.N])
	}
	if in.formErr != nil {
		v.ErrorBlock, v.ErrorText = in.formErr.Block, tr(in.formErr.Key)
	}
	switch {
	case in.saveErr == nil:
	case device.IsStaleVersionError(in.saveErr):
		v.Conflict = true
	case device.IsInvalidRulesError(in.saveErr):
		v.SaveError = tr("rules.error.invalid")
	default:
		v.SaveError = tr("rules.error.save_failed")
	}
	v.Test = testView(sc, labels, tr)
	if in.test && in.formErr == nil {
		res := h.testResult(sc, dv, in, labels)
		v.Test.Result = &res
		v.FailedBlock = res.Block
	}
	if in.test || (web.IsHTMXRequest(sc.req) && status == http.StatusOK && sc.req.Method == http.MethodPost) {
		RenderStatus(w, sc.req, status, templates.RuleBuilderBody(base, v))
		return
	}
	leaf := f.Name
	if leaf == "" {
		leaf = tr("rules.new_rule")
	}
	l := h.rulesLayout(sc, dv, leaf)
	l.Dialogs = templates.RulePicker(l, v)
	RenderStatus(w, sc.req, status, templates.RuleBuilderLayout(l, v))
}

func testView(sc ProjectScope, l ruleLabels, tr func(string, ...any) string) templates.RuleTestView {
	v := templates.RuleTestView{
		ChatID:  sc.req.PostForm.Get("test_chat"),
		Sender:  sc.req.PostForm.Get("test_sender"),
		Text:    sc.req.PostForm.Get("test_text"),
		Mention: sc.req.PostForm.Get("test_mention") == "1",
	}
	for _, c := range l.groups {
		v.Groups = append(v.Groups, templates.RuleChip{Value: c.PublicID, Label: c.DisplayName()})
	}
	for _, c := range l.dms {
		v.DMs = append(v.DMs, templates.RuleChip{Value: c.PublicID, Label: c.DisplayName()})
	}
	for _, c := range l.contacts {
		v.Senders = append(v.Senders, templates.RuleChip{Value: c.JID, Label: contactLabel(tr, c)})
	}
	if v.ChatID == "" {
		switch {
		case len(l.groups) > 0:
			v.ChatID = l.groups[0].PublicID
		case len(l.dms) > 0:
			v.ChatID = l.dms[0].PublicID
		}
	}
	if i := slices.IndexFunc(l.dms, func(c *chat.Chat) bool { return c.PublicID == v.ChatID }); i >= 0 {
		v.ChatIsDM = true
		v.Sender = l.dms[i].JID
		v.Senders = []templates.RuleChip{{Value: l.dms[i].JID, Label: l.dms[i].DisplayName()}}
	}
	return v
}

//i18n:use rules.test.*
func (h *RulesHandler) testResult(sc ProjectScope, dv device.DeviceView, in builderInput, l ruleLabels) templates.RuleTestResult {
	ctx := sc.req.Context()
	tr := h.ProjectFragmentBase(sc).Tr
	c, err := h.Chats.Resolve(ctx, sc.req.PostForm.Get("test_chat"))
	if err != nil || c.DeviceID != dv.Device.ID {
		return templates.RuleTestResult{Message: tr("rules.test.pick_chat")}
	}
	match := device.MatchInput{IsGroup: c.Kind == chat.KindGroup, ChatJID: c.JID, Body: sc.req.PostForm.Get("test_text")}
	senderJID := c.JID
	if match.IsGroup {
		senderJID = sc.req.PostForm.Get("test_sender")
		match.MentionedMe = sc.req.PostForm.Get("test_mention") == "1"
	}
	senderName := tr("rules.test.unknown_sender")
	h.fillSender(ctx, dv, senderJID, &match, &senderName)

	items := dv.Device.Rules.Items
	ids := []string{in.form.ID}
	if g := ruleGroups(items); groupIndex(g, in.form.ID) >= 0 {
		ids = g[groupIndex(g, in.form.ID)].IDs
	}
	if !in.custom && h.canManageProject(sc) {
		drafted, ferr := in.form.pairable(newRuleID).rules()
		if ferr != nil {
			return templates.RuleTestResult{Message: tr("rules.test.finish"), Block: ferr.Block}
		}
		items = putGroup(items, in.form.ID, drafted)
		ids = idsOf(drafted)
	}
	e, err := h.Devices.ExplainRules(ctx, dv.Device.ID, device.Rules{IgnoreFromMe: dv.Device.Rules.IgnoreFromMe, Items: items}, match)
	if err != nil {
		h.logUnless(err, "web rules: test")
		return templates.RuleTestResult{Message: tr("rules.test.finish")}
	}
	return explainResult(tr, e, items, ids, match, senderName, l)
}

func (h *RulesHandler) fillSender(ctx context.Context, dv device.DeviceView, jid string, in *device.MatchInput, name *string) {
	if jid == "" {
		return
	}
	c, err := h.Contacts.Get(ctx, dv.Device.ID, jid)
	alt := ""
	if err == nil {
		alt = c.LID
	}
	in.SenderJID, in.SenderLID = message.SplitSender(jid, alt)
	in.SenderPhone = whatsapp.PhoneFromJID(in.SenderJID)
	if err != nil {
		return
	}
	if c.Phone != "" {
		in.SenderPhone = c.Phone
	}
	*name = c.DisplayName()
}

func idsOf(items []device.Rule) []string {
	out := make([]string, 0, len(items))
	for _, r := range items {
		out = append(out, r.ID)
	}
	return out
}

func explainResult(tr func(string, ...any) string, e device.Explanation, items []device.Rule, ids []string, in device.MatchInput, sender string, l ruleLabels) templates.RuleTestResult {
	if slices.Contains(ids, e.Result.RuleID) {
		if e.Result.Outcome == device.OutcomeIgnored {
			return templates.RuleTestResult{OK: true, Message: tr("rules.test.ignored")}
		}
		return templates.RuleTestResult{OK: true, Message: tr("rules.test.sent", "Name", ruleName(tr, e.Result.RuleName))}
	}
	half := ids[0]
	if !in.IsGroup && len(ids) > 1 {
		half = ids[1]
	}
	i := slices.IndexFunc(e.Checks, func(c device.RuleCheck) bool { return c.RuleID == half })
	if i < 0 {
		return templates.RuleTestResult{Message: tr("rules.test.finish")}
	}
	check, rule := e.Checks[i], items[i]
	switch {
	case !check.Enabled:
		return templates.RuleTestResult{Message: tr("rules.test.paused")}
	case check.Held && e.Result.Outcome == device.OutcomeIgnored:
		return templates.RuleTestResult{Message: tr("rules.test.shadowed_ignored", "Name", ruleName(tr, e.Result.RuleName))}
	case check.Held:
		return templates.RuleTestResult{Message: tr("rules.test.shadowed_sent", "Name", ruleName(tr, e.Result.RuleName))}
	case check.DMNotAdmitted:
		return templates.RuleTestResult{Message: tr("rules.test.dm_off"), Block: blockWhere}
	}
	cond := rule.Conditions[check.Failed]
	switch cond.Field {
	case device.FieldChatKind:
		if in.IsGroup {
			return templates.RuleTestResult{Message: tr("rules.test.no_groups"), Block: blockWhere}
		}
		return templates.RuleTestResult{Message: tr("rules.test.dm_off"), Block: blockWhere}
	case device.FieldChat:
		return templates.RuleTestResult{Message: tr("rules.test.not_group", "Chat", l.chat(in.ChatJID)), Block: blockWhere}
	case device.FieldMention:
		return templates.RuleTestResult{Message: tr("rules.test.mention_only"), Block: blockWhere}
	case device.FieldSender:
		if cond.Op == device.OpIsNot {
			return templates.RuleTestResult{Message: tr("rules.test.excluded", "Name", sender), Block: blockWho}
		}
		return templates.RuleTestResult{Message: tr("rules.test.not_only", "Name", sender), Block: blockWho}
	}
	k := 0
	for _, c := range rule.Conditions[:check.Failed] {
		if c.Field == device.FieldText {
			k++
		}
	}
	return templates.RuleTestResult{Message: tr("rules.test.text_needs", "Words", plainText(orList(tr, cond.Values, "", true))), Block: textBlock(k)}
}

func ruleName(tr func(string, ...any) string, name string) string {
	if name == "" {
		return tr("rules.untitled")
	}
	return name
}
