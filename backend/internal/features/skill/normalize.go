package skill

import (
	"strings"
	"unicode/utf8"
)

// normTitle trims surrounding whitespace.
func normTitle(s string) string { return strings.TrimSpace(s) }

// normTrigger trims, removes every leading slash and trims again, so "/plan",
// "//plan" and " /plan " all become "plan".
func normTrigger(s string) string {
	return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(s), "/"))
}

func chars(s string) int { return utf8.RuneCountInString(s) }

func validTitle(s string) bool       { n := chars(s); return n >= 1 && n <= maxTitle }
func validTrigger(s string) bool     { n := chars(s); return n >= 1 && n <= maxTrigger }
func validDescription(s string) bool { return chars(s) <= maxDescription }
func validMarkdown(s string) bool    { n := chars(s); return n >= 1 && n <= maxMarkdown }

// Input carries the writable fields of a create.
type Input struct {
	Title       string
	Trigger     string
	Description string
	Markdown    string
	SortOrder   int
}

// Patch carries the optional fields of an update; nil means "unchanged".
type Patch struct {
	Title       *string
	Trigger     *string
	Description *string
	Markdown    *string
	Enabled     *bool
	SortOrder   *int
}

// normalized returns a copy of in with normalization applied, or
// ErrInvalidSkill when a limit is violated.
func (in Input) normalized() (Input, error) {
	in.Title = normTitle(in.Title)
	in.Trigger = normTrigger(in.Trigger)
	in.Description = strings.TrimSpace(in.Description)
	in.Markdown = strings.TrimSpace(in.Markdown)
	if !validTitle(in.Title) || !validTrigger(in.Trigger) || !validDescription(in.Description) || !validMarkdown(in.Markdown) {
		return in, ErrInvalidSkill
	}
	return in, nil
}

// changes validates the supplied fields and converts them to a column map.
func (p Patch) changes() (map[string]any, error) {
	out := map[string]any{}
	if p.Title != nil {
		v := normTitle(*p.Title)
		if !validTitle(v) {
			return nil, ErrInvalidSkill
		}
		out["title"] = v
	}
	if p.Trigger != nil {
		v := normTrigger(*p.Trigger)
		if !validTrigger(v) {
			return nil, ErrInvalidSkill
		}
		out["trigger"] = v
	}
	if p.Description != nil {
		v := strings.TrimSpace(*p.Description)
		if !validDescription(v) {
			return nil, ErrInvalidSkill
		}
		out["description"] = v
	}
	if p.Markdown != nil {
		v := strings.TrimSpace(*p.Markdown)
		if !validMarkdown(v) {
			return nil, ErrInvalidSkill
		}
		out["markdown"] = v
	}
	if p.Enabled != nil {
		out["enabled"] = *p.Enabled
	}
	if p.SortOrder != nil {
		out["sort_order"] = *p.SortOrder
	}
	return out, nil
}

// NormalizeIDs drops zero ids and duplicates, keeping first-seen order. The
// result is never nil.
func NormalizeIDs(ids []uint) []uint {
	out := make([]uint, 0, len(ids))
	seen := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// Selection limit shared with MCP tool selection.
const (
	defaultSelectionLimit = 32
	maxSelectionLimit     = 128
)

// SelectionLimit converts the configured "max selected tools per message"
// setting into the effective limit: non-positive falls back to 32, values above
// 128 are capped at 128.
func SelectionLimit(configured int) int {
	switch {
	case configured <= 0:
		return defaultSelectionLimit
	case configured > maxSelectionLimit:
		return maxSelectionLimit
	}
	return configured
}
