package uicomponent

import (
	"context"
	"strconv"
	"strings"
)

// Fence languages of the two block kinds.
const (
	fenceComponent = "riochat-ui"
	fenceWidget    = "riochat-widget"
)

// BlockFormat values a client may declare per request.
const (
	FormatBasic       = 1
	FormatInteractive = 2
)

// maxListed caps the components described in one prompt.
const maxListed = MaxTickedComponents

// redundantWithPanel names built-in components an interactive panel makes
// unnecessary; they are left out of the listing while the panel is selected.
var redundantWithPanel = map[string]bool{"calculator": true}

// Catalog is the prompt section produced from a set of ticked components.
type Catalog struct {
	// Text is the full section. Empty means nothing is injected.
	Text string
	// Format is the normalised block format, 1 or 2.
	Format int
	// Panel and Widget report which guidance tiers are included (format 2).
	Panel  bool
	Widget bool
	// Listed holds the components that appear in the list, in order.
	Listed []UIComponent
}

// Empty reports whether the catalog adds nothing to the prompt.
func (c Catalog) Empty() bool { return c.Text == "" }

// Catalog builds the prompt section for resolved components. When the global
// switch is off at this moment, everything is discarded.
func (s *Service) Catalog(ctx context.Context, comps []UIComponent, format int) Catalog {
	if !s.Enabled(ctx) {
		return Catalog{Format: normalizeFormat(format)}
	}
	return BuildCatalog(comps, format)
}

// NormalizeFormat maps a declared block format to 1 or 2. Absent and unknown
// values mean the basic format.
func normalizeFormat(v int) int {
	if v == FormatInteractive {
		return FormatInteractive
	}
	return FormatBasic
}

func isSwitchRow(c UIComponent, name string) bool {
	return c.RendererKind == RendererBuiltin && c.Name == name
}

// BuildCatalog is the pure core of Catalog: identical inputs give identical
// text. Format 1 text never depends on anything specific to format 2.
func BuildCatalog(comps []UIComponent, format int) Catalog {
	format = normalizeFormat(format)
	out := Catalog{Format: format}

	var panel, widget bool
	rest := make([]UIComponent, 0, len(comps))
	for _, c := range comps {
		switch {
		case isSwitchRow(c, switchPanel):
			panel = true
		case isSwitchRow(c, switchWidget):
			widget = true
		default:
			rest = append(rest, c)
		}
	}
	if format == FormatBasic {
		panel, widget = false, false
	}
	if panel {
		kept := rest[:0:0]
		for _, c := range rest {
			if c.RendererKind == RendererBuiltin && redundantWithPanel[c.Name] {
				continue
			}
			kept = append(kept, c)
		}
		rest = kept
	}
	if len(rest) > maxListed {
		rest = rest[:maxListed]
	}
	if len(rest) == 0 && !panel && !widget {
		return out
	}
	out.Panel, out.Widget, out.Listed = panel, widget, rest

	var b strings.Builder
	if format == FormatBasic {
		writeBasicGuidance(&b)
	} else {
		writeInteractiveGuidance(&b, panel, widget)
	}
	writeComponentList(&b, rest)
	out.Text = b.String()
	return out
}

// RoutingGuidance is the paragraph the HTML-visual format section appends when
// components are also on offer. It is empty for an empty catalog, so the plain
// section stays unchanged.
func (c Catalog) RoutingGuidance() string {
	if c.Empty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("Choosing the output form: plain Markdown is the default. When the content is data the reader may want to adjust, filter or read in a structured way, prefer a UI component over an HTML artifact")
	if c.Format == FormatInteractive {
		var tiers []string
		if c.Panel {
			tiers = append(tiers, "an interactive panel for linked parameters and results")
		}
		if c.Widget {
			tiers = append(tiers, "an inline widget for animation or mechanism demos")
		}
		if len(tiers) > 0 {
			b.WriteString(" (including " + strings.Join(tiers, " and ") + ")")
		}
	}
	b.WriteString(". Use an HTML artifact only for full pages, mini-games or code the user will download or reuse.")
	return b.String()
}

// ValidBlockVersion reports whether a client-declared block format version is
// acceptable at request binding: absent (0), 1 or 2.
func ValidBlockVersion(v int) bool { return v == 0 || v == FormatBasic || v == FormatInteractive }

// escText makes component-derived text safe inside the prompt's tag structure.
func escText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// escAttr is escText for attribute values.
func escAttr(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\n", " ", "\r", " ")
	return r.Replace(s)
}

// usageText is the description without its interactions part.
func usageText(desc string) string {
	if i := strings.Index(desc, interactionMarker); i >= 0 {
		return strings.TrimSpace(desc[:i])
	}
	return strings.TrimSpace(desc)
}

// splitProps separates the props summary from its conventions.
func splitProps(summary string) (props, conventions string) {
	if i := strings.Index(summary, conventionMarker); i >= 0 {
		return strings.TrimSpace(summary[:i]), strings.TrimSpace(summary[i+len(conventionMarker):])
	}
	return strings.TrimSpace(summary), ""
}

func writeComponentList(b *strings.Builder, comps []UIComponent) {
	if len(comps) == 0 {
		return
	}
	b.WriteString("<available_components>\n")
	for _, c := range comps {
		b.WriteString(`<component name="` + escAttr(c.Name) + `"`)
		if c.Version > 1 {
			b.WriteString(` version="` + strconv.Itoa(c.Version) + `"`)
		}
		b.WriteString(">\n")
		b.WriteString("<usage>" + escText(usageText(c.Description)) + "</usage>\n")
		props, conv := splitProps(c.PropsSummary)
		b.WriteString("<props>" + escText(props) + "</props>\n")
		if conv != "" {
			b.WriteString("<conventions>" + escText(conv) + "</conventions>\n")
		}
		b.WriteString("</component>\n")
	}
	b.WriteString("</available_components>\n")
}

const fence = "```"

func writeBasicGuidance(b *strings.Builder) {
	b.WriteString("# Interactive UI components\n\n")
	b.WriteString("UI components are an output format, not tools. You write them as fenced Markdown blocks inside your reply and the client renders them. Never invoke a component through function calling and never wait for a result from one.\n\n")
	b.WriteString("Use a component only when it is clearly better than plain Markdown. How many to use is decided by the content and may be zero.\n\n")
	b.WriteString("Syntax: a fenced block whose language is `" + fenceComponent + "` and whose body is valid JSON with exactly three fields: `component` (a name from the list below), `id` (a short English identifier, unique within the conversation) and `props` (the component's properties). Add no other fields, no comments and no trailing commas, and escape quotation marks inside strings.\n\n")
	b.WriteString("Place the block where it belongs in the reply, with a natural sentence leading into it and, where useful, one following it. Do not repeat the block's data again outside it.\n\n")
	b.WriteString("Example (syntax only; use just the components listed below):\n\n")
	b.WriteString(fence + fenceComponent + "\n")
	b.WriteString(`{"component":"stat-grid","id":"sales-overview","props":{"title":"Quarter overview","items":[{"label":"Revenue","value":"1.2","unit":"M","delta":"+8%","trend":"up"}]}}` + "\n")
	b.WriteString(fence + "\n\n")
	b.WriteString("Components you may use:\n")
}

func writeInteractiveGuidance(b *strings.Builder, panel, widget bool) {
	b.WriteString("# Interactive UI components\n\n")
	b.WriteString("UI components are an output format, not tools. You write them as fenced Markdown blocks inside your reply and the client renders them. Never invoke a component through function calling and never wait for a result from one.\n\n")

	b.WriteString("## When to use them\n\n")
	b.WriteString("Weigh each reply on its content. A component earns its place when the reader benefits from adjusting, filtering, comparing or exploring structured data. Prefer plain text for chit-chat, opinions, very small data sets, requests that ask for plain text, and anything for which you have no reliable data. Never invent numbers to fill a component.\n\n")
	b.WriteString("Keep your conclusions in prose so the reply still stands without the component. Normally use one block per reply; never more than three.\n\n")
	tiers := "Choose the lightest tier that is enough: first a catalog component"
	if panel {
		tiers += ", then an interactive panel"
	}
	if widget {
		tiers += ", then an inline widget"
	}
	b.WriteString(tiers + ". Only the tiers described in this section are available.\n\n")

	b.WriteString("## Follow-ups\n\n")
	b.WriteString("When the user changes values inside a component, the new values reach you on the next turn as injected state keyed by component id. Answer from those current values and do not emit the same block again. When asked to modify a component, emit a new block with a new id, initialised from the user's current values.\n\n")

	b.WriteString("## Syntax\n\n")
	b.WriteString("A fenced block whose language is `" + fenceComponent + "` and whose body is valid JSON: `component` (a name from the list below), `id` (letters, digits, hyphen and underscore; unique within the conversation) and the component's properties, written either flat at the top level or nested under `props`. No comments, no trailing commas, and escape quotation marks inside strings. Place the block where it belongs, introduced by a natural sentence, and do not repeat its data outside it.\n\n")
	b.WriteString("Example (syntax only; use just the components listed below):\n\n")
	b.WriteString(fence + fenceComponent + "\n")
	b.WriteString(`{"component":"chart","id":"monthly-trend","props":{"type":"line","x":["Jan","Feb","Mar"],"series":[{"name":"Visits","data":[120,150,170]}]}}` + "\n")
	b.WriteString(fence + "\n\n")

	if panel {
		writePanelGuidance(b)
	}
	if widget {
		writeWidgetGuidance(b)
	}
	b.WriteString("Components you may use:\n")
}

func writePanelGuidance(b *strings.Builder) {
	b.WriteString("## Interactive panel\n\n")
	b.WriteString("Use `component: \"panel\"` to build a small tool the user can adjust. Its properties are:\n")
	b.WriteString("- `state`: an object of user-editable values with their initial values.\n")
	b.WriteString("- `computed`: optional derived values written as formulas; they may refer to state names and to each other, but not in a cycle.\n")
	b.WriteString("- `children`: nodes rendered from top to bottom, each a primitive or a catalog component. Each node names its kind in `component`. Nested nodes carry no id. Controls bind to a state name through `bind`. A `visible` formula hides a node when it is false.\n\n")
	b.WriteString("Interpolation: inside text, `{formula}` is replaced by its value. A text that is exactly one `{...}` keeps the native type of the value; inside longer text, numbers are shown with thousands separators and at most two decimals. Write `{{` and `}}` for literal braces.\n\n")
	b.WriteString("Formula language: numbers, strings, arithmetic with `+ - * / % ^`, comparisons, logical operators, the ternary `a ? b : c`, member access, indexing and list literals; constants `pi` and `e`; functions abs, sqrt, floor, ceil, log, log2, log10, exp, sin, cos, tan, asin, acos, atan, pow, min, max, round, fixed, clamp, range (both ends inclusive, at most 1000 items), sum, avg, len and contains. There is no assignment, no loop and no user-defined function. Formulas also supply data: chart series may be formulas, and data-table and card-grid accept a `where` formula that filters rows.\n\n")
	b.WriteString("Buttons: `action` is one of `ask` (a pre-filled follow-up message the user may edit and send), `set` (changes a state value) or `copy` (copies text to the clipboard).\n\n")
	b.WriteString("Primitives and their properties:\n")
	b.WriteString("- Layout: `panel`, `row`, `stack`, `grid` (`columns` 1 to 4), `card`, `tabs`, `collapse`, `divider`.\n")
	b.WriteString("- Display: `text`, `stat` (label, value, unit), `badge`, `progress`, `callout`.\n")
	b.WriteString("- Explanation: `formula` (`expr`, `name`, `unit`, `precision`, `label`, `with`) and `scene`, a drawing of `rect`, `circle`, `line`, `arrow` and `text` shapes (each names its kind in `shape`) in pixel coordinates with the origin at the top left. Use a width of 240 to 400, as few shapes as will do, and margins of at least 16.\n")
	b.WriteString("- Controls: `slider`, `select`, `segmented`, `switch`, `input`, `button`.\n")
	b.WriteString("- Tone values: default, muted, positive, negative, warning, info.\n")
	b.WriteString("The layout adapts automatically when a scene is present.\n\n")
	b.WriteString("Example, a loan calculator:\n\n")
	b.WriteString(fence + fenceComponent + "\n")
	b.WriteString(`{"component":"panel","id":"loan-calc","state":{"amount":500000,"rate":4.2,"years":20},"computed":{"n":"years*12","r":"rate/1200","payment":"amount*r/(1-pow(1+r,-n))"},"children":[{"component":"slider","bind":"amount","label":"Amount","min":50000,"max":2000000,"step":10000},{"component":"slider","bind":"rate","label":"Annual rate (%)","min":1,"max":10,"step":0.1},{"component":"slider","bind":"years","label":"Years","min":5,"max":30},{"component":"stat","label":"Monthly payment","value":"{fixed(payment,2)}"}]}` + "\n")
	b.WriteString(fence + "\n\n")
	b.WriteString("Example, a physical relation with a formula, sliders and a scene:\n\n")
	b.WriteString(fence + fenceComponent + "\n")
	b.WriteString(`{"component":"panel","id":"free-fall","state":{"h":20},"computed":{"t":"sqrt(2*h/9.8)"},"children":[{"component":"formula","expr":"sqrt(2*h/g)","name":"t","unit":"s","precision":2,"label":"Fall time","with":{"g":9.8}},{"component":"slider","bind":"h","label":"Height (m)","min":1,"max":100},{"component":"scene","width":320,"height":200,"shapes":[{"shape":"line","x1":40,"y1":180,"x2":280,"y2":180},{"shape":"circle","cx":160,"cy":"180-h*1.5","r":10}]}]}` + "\n")
	b.WriteString(fence + "\n\n")
}

func writeWidgetGuidance(b *strings.Builder) {
	b.WriteString("## Inline widget\n\n")
	b.WriteString("Use a widget for animation, time evolution, 3D, particles, geometry or mechanism demos that go beyond what a panel and its scene can show. A widget is a fenced block whose language is `" + fenceWidget + "` and whose body is one self-contained HTML fragment: no html, head or body elements, the title in a title element, inline style and script, drawing with SVG or canvas.\n\n")
	b.WriteString("It runs in a sandbox with no network, only after the block is complete. It cannot load external resources and cannot use alert, prompt, form submission, localStorage or cookies.\n\n")
	b.WriteString("Design rules: fill the container width; keep the height at 640px or less; use only theme-variable colours; keep the background transparent; stay legible in dark and light themes; provide play and pause controls for anything that moves; respect reduced-motion preferences; stay within 12KB; leave no placeholders.\n\n")
	b.WriteString("The host provides: 16px padding and styled controls; a way to persist small JSON state under names of letters, digits and hyphens (4KB in total); a way to propose a follow-up question of at most 120 characters; and a callback fired when the theme changes.\n\n")
	b.WriteString("Full web pages, mini-games and downloadable code are still HTML artifacts, not widgets.\n\n")
}
