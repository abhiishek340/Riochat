package promptreset

import (
	"strings"
	"unicode/utf8"
)

// normalizeTrigger trims, removes every leading slash, then trims again.
func normalizeTrigger(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimLeft(s, "/")
	return strings.TrimSpace(s)
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// input is a normalized, validated set of preset fields for creation.
type input struct {
	Title       string
	Trigger     string
	Description string
	Content     string
	Enabled     bool
	SortOrder   int
}

// rawInput carries the values exactly as received.
type rawInput struct {
	Title       string
	Trigger     string
	Description string
	Content     string
	Enabled     bool
	SortOrder   int
}

func validTitle(s string) bool   { return s != "" && runeLen(s) <= maxTitle }
func validTrigger(s string) bool { return s != "" && runeLen(s) <= maxTrigger }
func validDescription(s string) bool {
	return runeLen(s) <= maxDescription
}
func validContent(s string) bool { return s != "" && runeLen(s) <= maxContent }

// normalizeInput applies the trimming rules and the post-normalization checks.
func normalizeInput(r rawInput) (input, error) {
	in := input{
		Title:       strings.TrimSpace(r.Title),
		Trigger:     normalizeTrigger(r.Trigger),
		Description: strings.TrimSpace(r.Description),
		Content:     strings.TrimSpace(r.Content),
		Enabled:     r.Enabled,
		SortOrder:   r.SortOrder,
	}
	if !validTitle(in.Title) || !validTrigger(in.Trigger) || !validDescription(in.Description) || !validContent(in.Content) {
		return input{}, ErrInvalid
	}
	return in, nil
}

// patch holds the fields present in a partial update, already normalized.
type patch struct {
	Title       *string
	Trigger     *string
	Description *string
	Content     *string
	Enabled     *bool
	SortOrder   *int
}

// rawPatch carries the pointer fields exactly as received.
type rawPatch struct {
	Title       *string
	Trigger     *string
	Description *string
	Content     *string
	Enabled     *bool
	SortOrder   *int
}

func normalizePatch(r rawPatch) (patch, error) {
	p := patch{Enabled: r.Enabled, SortOrder: r.SortOrder}
	if r.Title != nil {
		v := strings.TrimSpace(*r.Title)
		if !validTitle(v) {
			return patch{}, ErrInvalid
		}
		p.Title = &v
	}
	if r.Trigger != nil {
		v := normalizeTrigger(*r.Trigger)
		if !validTrigger(v) {
			return patch{}, ErrInvalid
		}
		p.Trigger = &v
	}
	if r.Description != nil {
		v := strings.TrimSpace(*r.Description)
		if !validDescription(v) {
			return patch{}, ErrInvalid
		}
		p.Description = &v
	}
	if r.Content != nil {
		v := strings.TrimSpace(*r.Content)
		if !validContent(v) {
			return patch{}, ErrInvalid
		}
		p.Content = &v
	}
	return p, nil
}
