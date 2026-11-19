package templates

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/a-h/templ"

	"altalune.id/openwa/internal/web"
)

// ButtonKind selects the button recipe.
type ButtonKind int

const (
	ButtonPrimary ButtonKind = iota
	ButtonSecondary
	ButtonDanger
	ButtonGhost
	ButtonIcon
)

// ButtonProps configures AppButton; Href renders an <a>, otherwise a <button>.
type ButtonProps struct {
	Kind     ButtonKind
	Type     string
	Label    string
	Icon     string
	Href     string
	Attrs    templ.Attributes
	Disabled bool
}

// IconButtonUnlabelledError reports an icon-only button rendered without an aria-label.
type IconButtonUnlabelledError struct{ Icon string }

func (e *IconButtonUnlabelledError) Error() string {
	return "templates: icon button " + e.Icon + " has no aria-label"
}

// IsIconButtonUnlabelledError reports whether err's tree contains an *IconButtonUnlabelledError.
func IsIconButtonUnlabelledError(err error) bool {
	var target *IconButtonUnlabelledError
	return errors.As(err, &target)
}

// AppButton renders a themed button or link that never wraps; an Icon kind without aria-label fails at render time.
func AppButton(p ButtonProps) templ.Component {
	if p.Kind == ButtonIcon {
		if _, ok := p.Attrs["aria-label"]; !ok {
			return templ.ComponentFunc(func(_ context.Context, _ io.Writer) error {
				return &IconButtonUnlabelledError{Icon: p.Icon}
			})
		}
	}
	return button(p)
}

// ButtonClass returns the daisyUI class string for kind: 44px on phones, btn-sm from lg.
func ButtonClass(kind ButtonKind) string {
	const base = "btn shrink-0 whitespace-nowrap lg:btn-sm"
	switch kind {
	case ButtonSecondary:
		return base + " btn-outline"
	case ButtonDanger:
		return base + " btn-error"
	case ButtonGhost:
		return base + " btn-ghost"
	case ButtonIcon:
		return base + " btn-ghost btn-square"
	default:
		return base + " btn-primary"
	}
}

func buttonType(p ButtonProps) string {
	if p.Type == "" {
		return "button"
	}
	return p.Type
}

// FieldProps configures FormField and InputAttrs.
type FieldProps struct {
	ID       string
	Label    string
	Help     string
	Error    string
	Required bool
}

// InputAttrs returns the id, aria-describedby and aria-invalid attributes for the control inside FormField.
func InputAttrs(p FieldProps) templ.Attributes {
	attrs := templ.Attributes{"id": p.ID}
	var described []string
	if p.Help != "" && p.Error == "" {
		described = append(described, p.ID+"-help")
	}
	if p.Error != "" {
		described = append(described, p.ID+"-error")
		attrs["aria-invalid"] = "true"
	}
	if len(described) > 0 {
		attrs["aria-describedby"] = strings.Join(described, " ")
	}
	if p.Required {
		attrs["required"] = true
	}
	return attrs
}

// InputClass returns the text input recipe.
func InputClass() string {
	return "input w-full lg:input-sm"
}

// ChipProps configures a checkbox chip.
type ChipProps struct {
	Name    string
	Value   string
	Label   string
	Checked bool
}

// Tone selects a status colour family; every value maps to theme tokens.
type Tone int

const (
	ToneNeutral Tone = iota
	TonePrimary
	ToneSuccess
	ToneWarning
	ToneDanger
	ToneInfo
)

func badgeClass(tone Tone) string {
	switch tone {
	case TonePrimary:
		return "badge badge-soft badge-primary shrink-0"
	case ToneSuccess:
		return "badge badge-soft badge-success shrink-0"
	case ToneWarning:
		return "badge badge-soft badge-warning shrink-0"
	case ToneDanger:
		return "badge badge-soft badge-error shrink-0"
	case ToneInfo:
		return "badge badge-soft badge-info shrink-0"
	default:
		return "badge badge-ghost shrink-0"
	}
}

func dotClass(tone Tone) string {
	switch tone {
	case TonePrimary:
		return "status status-primary"
	case ToneSuccess:
		return "status status-success"
	case ToneWarning:
		return "status status-warning"
	case ToneDanger:
		return "status status-error"
	case ToneInfo:
		return "status status-info"
	default:
		return "status"
	}
}

// PageHeaderProps configures PageHeader.
type PageHeaderProps struct {
	Title    string
	Subtitle string
	Eyebrow  string
	Actions  templ.Component
}

// CardProps configures Card.
type CardProps struct {
	Title       string
	Description string
}

// EmptyStateProps configures EmptyState.
type EmptyStateProps struct {
	Icon  string
	Title string
	Body  string
	CTA   *ButtonProps
}

// PollProps configures Poll; Done omits the trigger so polling stops.
type PollProps struct {
	ID      string
	URL     string
	Every   string
	Done    bool
	Swap    string
	Include string
}

func (p PollProps) swap() string {
	if p.Swap == "" {
		return "outerHTML"
	}
	return p.Swap
}

// Tab is one tab in Tabs; Attrs land on the tabpanel element.
type Tab struct {
	Key   string
	Label string
	Panel templ.Component
	Attrs templ.Attributes
}

// TabsProps configures Tabs; Label is the tablist's accessible name.
type TabsProps struct {
	ID    string
	Label string
	Tabs  []Tab
}

func withNonce(d web.LayoutData, attrs templ.Attributes) templ.Attributes {
	out := make(templ.Attributes, len(attrs)+1)
	hx := false
	for k, v := range attrs {
		out[k] = v
		if strings.HasPrefix(k, "hx-") {
			hx = true
		}
	}
	if hx {
		out["hx-nonce"] = d.Nonce
	}
	return out
}

func codeLines(code string) []string {
	return strings.Split(strings.TrimRight(code, "\n"), "\n")
}

func avatarInitial(name string) string {
	r := []rune(strings.TrimSpace(name))
	if len(r) == 0 {
		return "?"
	}
	return strings.ToUpper(string(r[0]))
}

func tabIndex(selected bool) string {
	if selected {
		return "0"
	}
	return "-1"
}

// HiddenField is one hidden input inside a ConfirmModal form.
type HiddenField struct {
	Name  string
	Value string
}

func confirmLabel(d web.LayoutData, p ConfirmProps) string {
	if p.ConfirmLabel != "" {
		return p.ConfirmLabel
	}
	return d.Tr("common.confirm")
}

// ConfirmProps configures ConfirmModal; HxTarget empty means a full-page POST that ends in a redirect and a flash.
type ConfirmProps struct {
	ID           string
	Title        string
	Body         string
	ConfirmLabel string
	Kind         ButtonKind
	Action       string
	HxTarget     string
	HxSwap       string
	Hidden       []HiddenField
	Trigger      ButtonProps
}

// ConfirmOpener returns the attributes that make an element open the ConfirmDialog with id.
func ConfirmOpener(id string) templ.Attributes {
	return templ.Attributes{"data-dialog-open": id, "aria-haspopup": "dialog", "aria-controls": id}
}

func confirmTrigger(p ConfirmProps) ButtonProps {
	trig := p.Trigger
	attrs := ConfirmOpener(p.ID)
	for k, v := range trig.Attrs {
		attrs[k] = v
	}
	trig.Attrs = attrs
	return trig
}

const proseClass = "[&_h1]:mb-4 [&_h1]:text-2xl [&_h1]:font-semibold [&_h2]:mt-7 [&_h2]:mb-2 [&_h2]:text-lg [&_h2]:font-semibold [&_p]:mb-3 [&_p]:leading-relaxed [&_ul]:mb-3 [&_ul]:list-disc [&_ul]:ps-5 [&_ol]:mb-3 [&_ol]:list-decimal [&_ol]:ps-5 [&_li]:mb-1 [&_a]:underline [&_strong]:font-semibold"

//i18n:use connect.fail_status
//i18n:use connect.fail_unreachable
func connectFailKey(reason string) string {
	if reason == "status" {
		return "connect.fail_status"
	}
	return "connect.fail_unreachable"
}

//i18n:use apikey.confirm_send
//i18n:use apikey.confirm_read
//i18n:use apikey.confirm_custom
func presetConfirmKey(value string) string {
	return "apikey.confirm_" + value
}
