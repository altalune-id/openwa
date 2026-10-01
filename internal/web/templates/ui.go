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

// ButtonProps configures Button; Href renders an <a>, otherwise a <button>.
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

// Button renders a themed button or link; an Icon kind without aria-label fails at render time.
func Button(p ButtonProps) templ.Component {
	if p.Kind == ButtonIcon {
		if _, ok := p.Attrs["aria-label"]; !ok {
			return templ.ComponentFunc(func(_ context.Context, _ io.Writer) error {
				return &IconButtonUnlabelledError{Icon: p.Icon}
			})
		}
	}
	return button(p)
}

const buttonBase = "inline-flex items-center gap-2 rounded-md text-sm font-medium transition-colors disabled:pointer-events-none disabled:opacity-50"

// ButtonClass returns the class string for kind, for the rare inline case.
func ButtonClass(kind ButtonKind) string {
	switch kind {
	case ButtonSecondary:
		return buttonBase + " border border-border bg-card px-4 py-2 text-foreground shadow-sm hover:bg-muted"
	case ButtonDanger:
		return buttonBase + " bg-destructive px-4 py-2 text-destructive-foreground shadow-sm hover:bg-destructive/90"
	case ButtonGhost:
		return buttonBase + " px-3 py-2 text-muted-foreground hover:bg-muted hover:text-foreground"
	case ButtonIcon:
		return "inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:pointer-events-none disabled:opacity-50"
	default:
		return buttonBase + " bg-primary px-4 py-2 text-primary-foreground shadow-sm hover:bg-primary/90"
	}
}

func buttonType(p ButtonProps) string {
	if p.Type == "" {
		return "button"
	}
	return p.Type
}

// FieldProps configures Field and InputAttrs.
type FieldProps struct {
	ID       string
	Label    string
	Help     string
	Error    string
	Required bool
}

// InputAttrs returns the id, aria-describedby and aria-invalid attributes for the control inside Field.
func InputAttrs(p FieldProps) templ.Attributes {
	attrs := templ.Attributes{"id": p.ID}
	var described []string
	if p.Help != "" {
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
	return "mt-1.5 block w-full rounded-md border border-input bg-card px-3 py-2 text-sm text-foreground placeholder:text-muted-foreground shadow-sm focus:border-ring focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-1"
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
	const base = "inline-flex shrink-0 items-center rounded-full px-2 py-0.5 text-xs font-medium "
	switch tone {
	case TonePrimary:
		return base + "bg-primary/10 text-primary"
	case ToneSuccess:
		return base + "bg-success/10 text-success"
	case ToneWarning:
		return base + "bg-warning/10 text-warning"
	case ToneDanger:
		return base + "bg-destructive/10 text-destructive"
	case ToneInfo:
		return base + "bg-info/10 text-info"
	default:
		return base + "bg-muted text-muted-foreground"
	}
}

func dotClass(tone Tone) string {
	const base = "inline-block h-2 w-2 shrink-0 rounded-full "
	switch tone {
	case TonePrimary:
		return base + "bg-primary"
	case ToneSuccess:
		return base + "bg-success"
	case ToneWarning:
		return base + "bg-warning"
	case ToneDanger:
		return base + "bg-destructive"
	case ToneInfo:
		return base + "bg-info"
	default:
		return base + "bg-border"
	}
}

func toastClass(kind web.FlashKind) string {
	const base = "flex items-start gap-3 rounded-md border p-3 shadow-sm "
	switch kind {
	case web.FlashOK:
		return base + "border-success/30 bg-success/10 text-success"
	case web.FlashWarn:
		return base + "border-warning/30 bg-warning/10 text-warning"
	case web.FlashErr:
		return base + "border-destructive/30 bg-destructive/10 text-destructive"
	case web.FlashInfo:
		return base + "border-info/30 bg-info/10 text-info"
	default:
		return base + "border-border bg-card text-foreground"
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
	ID    string
	URL   string
	Every string
	Done  bool
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

// HiddenField is one hidden input inside a ConfirmDialog form.
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

// ConfirmProps configures ConfirmDialog; HxTarget empty means a full-page POST that ends in a redirect and a flash.
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

func confirmTrigger(p ConfirmProps) ButtonProps {
	trig := p.Trigger
	attrs := make(templ.Attributes, len(trig.Attrs)+3)
	for k, v := range trig.Attrs {
		attrs[k] = v
	}
	attrs["data-confirm-open"] = p.ID
	attrs["aria-haspopup"] = "dialog"
	attrs["aria-controls"] = p.ID
	trig.Attrs = attrs
	return trig
}
