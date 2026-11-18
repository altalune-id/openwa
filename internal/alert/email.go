package alert

import (
	htmltemplate "html/template"
	"net/url"
	"strings"
	texttemplate "text/template"
	"time"

	"altalune.id/openwa/internal/i18n"
	"altalune.id/openwa/internal/platform/tenant"
	"altalune.id/openwa/mailer"
)

//i18n:use alertmail.*

type emailView struct {
	Lang, Dir   string
	Align       string
	Brand       string
	Headline    string
	Greeting    string
	Lead        string
	Items       []string
	ProjectLine string
	ActionLabel string
	ActionURL   string
	Reason      string
	UnsubLabel  string
	UnsubURL    string
}

//nolint:gochecknoglobals // Parsed once; templates are immutable.
var (
	htmlEmail = htmltemplate.Must(htmltemplate.New("html").Funcs(htmltemplate.FuncMap{"bidi": bidiHTML, "plain": stripBidi.Replace}).Parse(`<!doctype html>
<html lang="{{.Lang}}" dir="{{.Dir}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><meta name="color-scheme" content="light"><meta name="supported-color-schemes" content="light"></head>
<body style="margin:0;padding:0;background:#f3f4f6" bgcolor="#f3f4f6">
<div style="display:none;max-height:0;overflow:hidden;mso-hide:all">{{plain .Headline}}</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" bgcolor="#f3f4f6" dir="{{.Dir}}"><tr><td align="center" style="padding:24px 12px">
<table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" bgcolor="#ffffff" dir="{{.Dir}}" style="width:100%;max-width:600px;border:1px solid #e5e7eb;border-radius:12px;font-family:Helvetica,Arial,sans-serif;color:#1f2937;text-align:{{.Align}}">
<tr><td dir="{{.Dir}}" align="{{.Align}}" style="padding:20px 24px 0;font-weight:700;font-size:16px;line-height:24px">{{.Brand}}</td></tr>
<tr><td dir="{{.Dir}}" align="{{.Align}}" style="padding:12px 24px 0;font-weight:700;font-size:20px;line-height:28px">{{bidi .Headline}}</td></tr>
<tr><td dir="{{.Dir}}" align="{{.Align}}" style="padding:12px 24px 0;font-size:16px;line-height:24px">{{.Greeting}}</td></tr>
<tr><td dir="{{.Dir}}" align="{{.Align}}" style="padding:12px 24px 0;font-size:16px;line-height:24px">{{bidi .Lead}}</td></tr>
{{- range .Items}}
<tr><td dir="{{$.Dir}}" align="{{$.Align}}" style="padding:8px 24px 0;font-size:16px;line-height:24px">&#8226; {{bidi .}}</td></tr>
{{- end}}
<tr><td dir="{{.Dir}}" align="{{.Align}}" style="padding:12px 24px 0;font-size:16px;line-height:24px;color:#4b5563">{{.ProjectLine}}</td></tr>
{{- if .ActionURL}}
<tr><td dir="{{.Dir}}" align="{{.Align}}" style="padding:24px 24px 0"><table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr><td bgcolor="#ffbb00" style="border-radius:8px"><a href="{{.ActionURL}}" style="display:inline-block;padding:12px 24px;font:700 16px/24px Helvetica,Arial,sans-serif;color:#1c1917;text-decoration:none">{{.ActionLabel}}</a></td></tr></table></td></tr>
{{- end}}
<tr><td dir="{{.Dir}}" align="{{.Align}}" style="padding:24px 24px 0;font-size:14px;line-height:20px;color:#4b5563">{{.Reason}}</td></tr>
<tr><td dir="{{.Dir}}" align="{{.Align}}" style="padding:4px 24px 24px;font-size:14px;line-height:20px"><a href="{{.UnsubURL}}" style="display:inline-block;padding:8px 0;color:#4b5563">{{.UnsubLabel}}</a></td></tr>
</table>
</td></tr></table>
</body></html>
`))
	textEmail = texttemplate.Must(texttemplate.New("text").Parse(`{{.Greeting}}

{{.Lead}}
{{- if .Items}}
{{range .Items}}
- {{.}}{{end}}
{{- end}}

{{.ProjectLine}}
{{if .ActionURL}}
{{.ActionLabel}}: {{.ActionURL}}
{{end}}
--
{{.Reason}}
{{.UnsubLabel}}: {{.UnsubURL}}
`))
)

func (s *Service) disconnectEmail(tr *i18n.Translator, r Recipient, tc tenant.Context, p Project, outages []Outage) (mailer.Message, error) {
	v := s.baseView(tr, p)
	var subject string
	if len(outages) == 1 {
		o := outages[0]
		subject = numberText(tr, "alertmail.subject_one", o)
		v.Lead = numberText(tr, "alertmail.body_one", o, outageTime(tr, o)...)
		v.ActionLabel = tr.T("alertmail.fix_it")
		v.ActionURL = s.link(p, "/devices/"+url.PathEscape(o.DeviceID))
	} else {
		subject = tr.T("alertmail.subject_digest", "Count", len(outages))
		v.Lead = tr.T("alertmail.body_digest", "Project", p.Name)
		for _, o := range outages {
			v.Items = append(v.Items, numberText(tr, "alertmail.digest_item", o, outageTime(tr, o)...))
		}
		v.ActionLabel = tr.T("alertmail.review")
		v.ActionURL = s.link(p, "/overview")
	}
	return s.render(r, tc, subject, v)
}

func (s *Service) testEmail(tr *i18n.Translator, r Recipient, tc tenant.Context, p Project) (mailer.Message, error) {
	v := s.baseView(tr, p)
	v.Lead = tr.T("alertmail.test_body", "Project", p.Name)
	return s.render(r, tc, tr.T("alertmail.test_subject"), v)
}

func outageTime(tr *i18n.Translator, o Outage) []any {
	return []any{"Duration", duration(tr, time.Since(o.DisconnectedAt)), "Time", ltr(o.DisconnectedAt.UTC().Format("2006-01-02 15:04"))}
}

func duration(tr *i18n.Translator, d time.Duration) string {
	switch {
	case d < time.Hour:
		return tr.T("alertmail.duration_minutes", "Count", max(int(d/time.Minute), 1))
	case d < 48*time.Hour:
		return tr.T("alertmail.duration_hours", "Count", int(d/time.Hour))
	default:
		return tr.Tn("alertmail.duration_days", int(d/(24*time.Hour)))
	}
}

// NOTE: the name and masked phone travel as one isolated unit, so right-to-left text never splits or reorders them.
func numberText(tr *i18n.Translator, key string, o Outage, args ...any) string {
	return tr.T(key+"_name", append([]any{"Name", numberLabel(o)}, args...)...)
}

// SECURITY: the name is user text, so its own bidi controls are stripped before it is isolated.
func numberLabel(o Outage) string {
	label := stripBidi.Replace(o.Name)
	if phone := maskPhone(o.Phone); phone != "" {
		label += " (" + ltr(phone) + ")"
	}
	return "\u2068" + label + "\u2069"
}

//nolint:gochecknoglobals // Immutable replacers.
var (
	stripBidi  = strings.NewReplacer("\u202a", "", "\u202b", "", "\u202c", "", "\u202d", "", "\u202e", "", "\u2066", "", "\u2067", "", "\u2068", "", "\u2069", "")
	isolateTag = strings.NewReplacer(
		"\u2066", `<span dir="ltr" style="unicode-bidi:isolate;white-space:nowrap">`,
		"\u2068", `<span dir="auto" style="unicode-bidi:isolate">`,
		"\u2069", `</span>`,
	)
)

// NOTE: email clients ignore isolate characters, so HTML carries them as isolating spans; the text part keeps the characters.
func bidiHTML(s string) htmltemplate.HTML {
	return htmltemplate.HTML(isolateTag.Replace(htmltemplate.HTMLEscapeString(s))) //nolint:gosec // G203: escaped first; only the fixed span tags are added.
}

// NOTE: LRI/PDI keep a masked phone or a timestamp in reading order inside right-to-left text.
func ltr(s string) string { return "\u2066" + s + "\u2069" }

func (s *Service) baseView(tr *i18n.Translator, p Project) emailView {
	return emailView{
		Brand:       s.opts.Brand,
		Lang:        string(tr.Locale()),
		Dir:         tr.Locale().Dir(),
		Align:       align(tr.Locale().Dir()),
		Greeting:    tr.T("alertmail.greeting"),
		ProjectLine: tr.T("alertmail.project_line", "Project", p.Name),
		Reason:      tr.T("alertmail.reason", "Project", p.Name),
		UnsubLabel:  tr.T("alertmail.unsubscribe"),
	}
}

func align(dir string) string {
	if dir == "rtl" {
		return "right"
	}
	return "left"
}

func (s *Service) link(p Project, suffix string) string {
	return s.opts.LinkBase + "/orgs/" + url.PathEscape(p.OrgSlug) + "/projects/" + url.PathEscape(p.Slug) + suffix
}

func (s *Service) render(r Recipient, tc tenant.Context, subject string, v emailView) (mailer.Message, error) {
	token, err := sealOptOut(s.d.Sealer, optOut{OrgID: tc.OrgID, ProjectID: tc.ProjectID, Email: r.Email})
	if err != nil {
		return mailer.Message{}, err
	}
	v.UnsubURL = s.opts.LinkBase + "/alerts/unsubscribe?t=" + token
	v.Headline = subject
	var html, text strings.Builder
	if err := htmlEmail.Execute(&html, v); err != nil {
		return mailer.Message{}, err
	}
	if err := textEmail.Execute(&text, v); err != nil {
		return mailer.Message{}, err
	}
	return mailer.Message{
		To:       r.Email,
		Subject:  subject,
		TextBody: text.String(),
		HTMLBody: html.String(),
		Headers: map[string]string{
			"List-Unsubscribe":      "<" + v.UnsubURL + ">",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		},
	}, nil
}
