package handlers

import (
	"strings"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/contact"
	"altalune.id/openwa/internal/device"
	"altalune.id/openwa/internal/i18n"
	"altalune.id/openwa/internal/web/templates"
)

func fakeTr(t *testing.T, msgs map[string]string) func(string, ...any) string {
	t.Helper()
	return func(key string, args ...any) string {
		raw, ok := msgs[key]
		if !ok {
			return key
		}
		data := map[string]any{}
		for i := 0; i+1 < len(args); i += 2 {
			data[args[i].(string)] = args[i+1]
		}
		var b strings.Builder
		require.NoError(t, template.Must(template.New(key).Parse(raw)).Execute(&b, data))
		return b.String()
	}
}

func plain(parts []templates.SentencePart) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Bold {
			b.WriteString("[" + p.Text + "]")
			continue
		}
		b.WriteString(p.Text)
	}
	return b.String()
}

func enSentence() map[string]string {
	return map[string]string{
		"rules.sentence.full":          "When a message arrives {{.Where}}{{.Mention}}, {{.Who}}{{.Text}}, then {{.Action}}.",
		"rules.sentence.where_groups":  "in {{.Groups}}",
		"rules.sentence.who_only":      "from {{.People}}",
		"rules.sentence.who_anyone":    "from anyone",
		"rules.sentence.text_contains": ", containing {{.Words}}",
		"rules.sentence.list_comma":    ", ",
		"rules.sentence.list_or":       " or ",
		"rules.sentence.quoted":        "“{{.Word}}”",
		"rules.action.match":           "Send to your agent",
	}
}

func tokoSari() (ruleForm, ruleLabels) {
	f := ruleForm{Groups: groupsThese, Chats: []string{"1@g.us"}, Who: whoOnly, Senders: []string{"phone:6281111222333"}, Action: device.ActionMatch}
	return f, ruleLabels{chats: map[string]string{"1@g.us": "Toko Admin"}}
}

func TestSentence_ReadsTheWalkSentence(t *testing.T) {
	t.Parallel()
	f, l := tokoSari()
	l.contacts = nil
	got := plain(sentence(fakeTr(t, enSentence()), f, l))
	require.Equal(t, "When a message arrives in [Toko Admin], from [+6281111222333], then [Send to your agent].", got)
}

func TestSentence_FollowsTheLocaleWordOrder(t *testing.T) {
	t.Parallel()
	ja := map[string]string{}
	for k, v := range enSentence() {
		ja[k] = v
	}
	ja["rules.sentence.full"] = "{{.Where}}{{.Mention}}で{{.Who}}{{.Text}}メッセージが届いたら、{{.Action}}。"
	ja["rules.sentence.where_groups"] = "{{.Groups}}"
	ja["rules.sentence.who_only"] = "{{.People}}から"
	ja["rules.sentence.text_contains"] = "、{{.Words}}を含む"
	ja["rules.sentence.list_or"] = "または"
	ja["rules.sentence.quoted"] = "「{{.Word}}」"
	f, l := tokoSari()
	f.Texts = []textRow{{Op: device.OpContains, Values: []string{"order", "pesan"}}}
	got := plain(sentence(fakeTr(t, ja), f, l))
	require.Equal(t, "[Toko Admin]で[+6281111222333]から、[「order」]または[「pesan」]を含むメッセージが届いたら、[Send to your agent]。", got)
}

func TestSentence_ListsUseTheLocaleJoiners(t *testing.T) {
	t.Parallel()
	f, l := tokoSari()
	f.Chats = []string{"1@g.us", "2@g.us", "3@g.us"}
	l.chats["2@g.us"], l.chats["3@g.us"] = "Supplier", "Family"
	got := plain(sentence(fakeTr(t, enSentence()), f, l))
	require.Contains(t, got, "in [Toko Admin], [Supplier] or [Family],")
}

func TestSentence_EveryLocaleFillsEverySlot(t *testing.T) {
	t.Parallel()
	bundle := i18n.NewEmbeddedBundle(i18n.EnUS)
	for _, loc := range []i18n.Locale{i18n.EnUS, i18n.IdID, i18n.MsMY, i18n.JaJP, i18n.ArSA} {
		tr := bundle.For(loc).T
		f, l := tokoSari()
		f.DM, f.Mention = true, true
		f.Texts = []textRow{{Op: device.OpStartsWith, Values: []string{"order", "pesan"}}}
		got := plain(sentence(tr, f, l))
		require.Contains(t, got, "[Toko Admin]", loc)
		require.NotContains(t, got, "{{", loc)
		require.NotContains(t, got, "\x00", loc)
		require.NotContains(t, got, "rules.", loc)
		custom := plain(customSentence(tr, bundle.For(loc).Tn, []device.Rule{{Action: device.ActionIgnore, Conditions: make([]device.Condition, 3)}}))
		require.Contains(t, custom, "3", loc)
		require.NotContains(t, custom, "\x00", loc)
	}
}

func TestRuleLabels_NameEachSenderShapeWithoutARawLID(t *testing.T) {
	t.Parallel()
	tr := fakeTr(t, map[string]string{"rules.hidden_number": "Someone with a hidden number"})
	l := ruleLabels{contacts: []*contact.Contact{
		{JID: "6281111222333@s.whatsapp.net", Phone: "6281111222333", PushName: "Sari Wulandari"},
		{JID: "777@lid", LID: "777@lid", PushName: "Hidden"},
	}}
	require.Equal(t, "Sari Wulandari", l.sender(tr, "phone:6281111222333"))
	require.Equal(t, "Hidden", l.sender(tr, "777@lid"))
	require.Equal(t, "+6289999", l.sender(tr, "phone:6289999"))
	require.Equal(t, "Someone with a hidden number", l.sender(tr, "999@lid"))
}

func TestSentenceName_IsThePlainSentenceCutTo60Runes(t *testing.T) {
	t.Parallel()
	f, l := tokoSari()
	got := sentenceName(fakeTr(t, enSentence()), f, l)
	require.Equal(t, "When a message arrives in Toko Admin, from +6281111222333…", got, "cut at a word boundary, trailing comma dropped, ellipsis added")
	require.LessOrEqual(t, len([]rune(got)), 60)

	short := sentenceName(func(string, ...any) string { return "Short rule" }, f, l)
	require.Equal(t, "Short rule", short, "a sentence that fits is kept whole, no ellipsis")
}

func TestCustomSentence_UsesPluralForms(t *testing.T) {
	t.Parallel()
	en := i18n.NewEmbeddedBundle(i18n.EnUS).For(i18n.EnUS)
	one := plain(customSentence(en.T, en.Tn, []device.Rule{{Action: device.ActionMatch, Conditions: make([]device.Condition, 1)}}))
	require.Contains(t, one, "1 condition,")
	two := plain(customSentence(en.T, en.Tn, []device.Rule{{Action: device.ActionMatch, Conditions: make([]device.Condition, 2)}}))
	require.Contains(t, two, "2 conditions,")
}

func TestSentence_ADirectMessageRuleHasItsOwnTemplate(t *testing.T) {
	t.Parallel()
	msgs := enSentence()
	msgs["rules.sentence.full_dm"] = "When a direct message arrives, {{.Who}}{{.Text}}, then {{.Action}}."
	f := ruleForm{DM: true, Groups: groupsNone, Who: whoEveryone, Action: device.ActionMatch}
	require.Equal(t, "When a direct message arrives, from anyone, then [Send to your agent].", plain(sentence(fakeTr(t, msgs), f, ruleLabels{})))
}
