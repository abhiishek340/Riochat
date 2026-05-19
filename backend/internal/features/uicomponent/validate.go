package uicomponent

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

// nameRe is lower-case kebab-case: a letter first, then letters and digits with
// single hyphens between alphanumeric groups.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// primitives are node names reserved for the interactive panel; a component
// carrying one would be indistinguishable from the primitive inside a panel.
var primitives = map[string]bool{
	"panel": true, "row": true, "stack": true, "grid": true, "card": true,
	"tabs": true, "collapse": true, "divider": true, "text": true, "stat": true,
	"badge": true, "progress": true, "callout": true, "formula": true, "scene": true,
	"slider": true, "select": true, "segmented": true, "switch": true, "input": true,
	"button": true,
}

// Names of the two switch rows. They are never usable inside a block.
const (
	switchPanel  = "panel"
	switchWidget = "widget"
)

func reservedName(name string) bool {
	return primitives[name] || name == switchPanel || name == switchWidget
}

// validName reports whether name (already trimmed) may name a custom component.
func validName(name string) bool {
	return len(name) <= maxName && nameRe.MatchString(name) && !reservedName(name)
}

// validDescription checks a trimmed description or summary against its limit.
func validText(s string, limit int) bool {
	return s != "" && utf8.RuneCountInString(s) <= limit && storable(s)
}

// storable rejects text the databases cannot hold: NUL bytes (PostgreSQL text
// refuses them) and invalid UTF-8.
func storable(s string) bool {
	return !strings.ContainsRune(s, 0) && utf8.ValidString(s)
}

// validPropsSchema checks a trimmed schema: empty, or a JSON object within the
// size cap. Only well-formedness is verified.
func validPropsSchema(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > maxPropsSchemaSize || !storable(s) {
		return false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		return false
	}
	return obj != nil
}

// validRendererSource checks the untrimmed source: not blank, within the cap.
func validRendererSource(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= maxRendererSize && storable(s)
}

// rawInput holds a create request as received.
type rawInput struct {
	Name           string
	Version        int
	Description    string
	PropsSummary   string
	PropsSchema    string
	RendererSource string
	Enabled        bool
	SortOrder      int
}

// cleanInput is a validated create request.
type cleanInput = rawInput

func normalizeInput(r rawInput) (cleanInput, error) {
	out := cleanInput{
		Name:           strings.TrimSpace(r.Name),
		Version:        r.Version,
		Description:    strings.TrimSpace(r.Description),
		PropsSummary:   strings.TrimSpace(r.PropsSummary),
		PropsSchema:    strings.TrimSpace(r.PropsSchema),
		RendererSource: r.RendererSource,
		Enabled:        r.Enabled,
		SortOrder:      r.SortOrder,
	}
	if out.Version <= 0 {
		out.Version = 1
	}
	if !validName(out.Name) ||
		!validText(out.Description, maxDescription) ||
		!validText(out.PropsSummary, maxPropsSummary) ||
		!validPropsSchema(out.PropsSchema) ||
		!validRendererSource(out.RendererSource) {
		return cleanInput{}, ErrInvalid
	}
	return out, nil
}

// rawPatch holds the fields a patch request supplied; nil means absent.
type rawPatch struct {
	Name           *string
	Version        *int
	Description    *string
	PropsSummary   *string
	PropsSchema    *string
	RendererSource *string
	Enabled        *bool
	SortOrder      *int
}

// touchesContent reports whether the patch carries any field a built-in row
// refuses (everything except enabled and sortOrder).
func (p rawPatch) touchesContent() bool {
	return p.Name != nil || p.Version != nil || p.Description != nil ||
		p.PropsSummary != nil || p.PropsSchema != nil || p.RendererSource != nil
}

func normalizePatch(r rawPatch) (rawPatch, error) {
	out := rawPatch{Enabled: r.Enabled, SortOrder: r.SortOrder}
	if r.Name != nil {
		v := strings.TrimSpace(*r.Name)
		if !validName(v) {
			return rawPatch{}, ErrInvalid
		}
		out.Name = &v
	}
	if r.Version != nil {
		if *r.Version <= 0 {
			return rawPatch{}, ErrInvalid
		}
		v := *r.Version
		out.Version = &v
	}
	if r.Description != nil {
		v := strings.TrimSpace(*r.Description)
		if !validText(v, maxDescription) {
			return rawPatch{}, ErrInvalid
		}
		out.Description = &v
	}
	if r.PropsSummary != nil {
		v := strings.TrimSpace(*r.PropsSummary)
		if !validText(v, maxPropsSummary) {
			return rawPatch{}, ErrInvalid
		}
		out.PropsSummary = &v
	}
	if r.PropsSchema != nil {
		v := strings.TrimSpace(*r.PropsSchema)
		if !validPropsSchema(v) {
			return rawPatch{}, ErrInvalid
		}
		out.PropsSchema = &v
	}
	if r.RendererSource != nil {
		if !validRendererSource(*r.RendererSource) {
			return rawPatch{}, ErrInvalid
		}
		v := *r.RendererSource
		out.RendererSource = &v
	}
	return out, nil
}
