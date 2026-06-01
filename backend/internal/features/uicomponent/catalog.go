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
