// Package contentmoderation is the optional safety layer that screens user
// input and model output through an OpenAI-compatible moderation service.
//
// The package is organised in layers:
//
//	categories.go, baseurl.go, textsplit.go   pure helpers
//	provider.go                               outbound moderation client
//	settings.go                               stored configuration and policy
//	events.go, stats.go                       persistence of events and counters
//	executor.go                               bounded check queue
//	run.go, block.go                          per-turn lifecycle and block application
//	maintenance.go                            retention, recovery and retry loops
//	service.go, http.go                       admin operations and HTTP routes
//	ports.go                                  interfaces to neighbouring areas
//
// Neighbouring areas (conversations, files, audit, users, object storage) are
// reached only through the small interfaces in ports.go, which the integrator
// supplies through MountWith.
package contentmoderation

import (
	"fmt"
	"sort"
	"strings"
)

// Check scope axes.
const (
	DirectionInput  = "input"
	DirectionOutput = "output"
	ModalityText    = "text"
	ModalityImage   = "image"
)

// Result values stored on events and statistics.
const (
	ResultPassed     = "passed"
	ResultHit        = "hit"
	ResultFailedOpen = "failed_open"
)

// textCategories are the thirteen official moderation categories.
var textCategories = []string{
	"hate", "hate/threatening",
	"harassment", "harassment/threatening",
	"self-harm", "self-harm/intent", "self-harm/instructions",
	"sexual", "sexual/minors",
	"violence", "violence/graphic",
	"illicit", "illicit/violent",
}

// imageCategories are the categories that can be evaluated on images.
var imageCategories = []string{
	"self-harm", "self-harm/intent", "self-harm/instructions",
	"sexual", "violence", "violence/graphic",
}

// TextCategories returns a copy of the text category catalog.
func TextCategories() []string { return append([]string(nil), textCategories...) }

// ImageCategories returns a copy of the image category catalog.
func ImageCategories() []string { return append([]string(nil), imageCategories...) }

func inList(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// IsKnownCategory reports whether c is one of the thirteen official categories.
func IsKnownCategory(c string) bool { return inList(textCategories, c) }

// IsImageCategory reports whether c can be evaluated on images.
func IsImageCategory(c string) bool { return inList(imageCategories, c) }

// normalizeCategories trims entries, drops blanks and duplicates and sorts the
// result ascending. Unknown categories, and categories the modality cannot
// evaluate, are rejected.
func normalizeCategories(in []string, modality string) ([]string, error) {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		c := strings.TrimSpace(raw)
		if c == "" || seen[c] {
			continue
		}
		if !IsKnownCategory(c) {
			return nil, fmt.Errorf("unknown category %q", c)
		}
		if modality == ModalityImage && !IsImageCategory(c) {
			return nil, fmt.Errorf("category %q is not supported for images", c)
		}
		seen[c] = true
		out = append(out, c)
	}
	sort.Strings(out)
	return out, nil
}

// Policy selects, per check scope, which categories are enforced. An empty list
// disables that scope.
type Policy struct {
	InputTextCategories   []string `json:"inputTextCategories"`
	OutputTextCategories  []string `json:"outputTextCategories"`
	InputImageCategories  []string `json:"inputImageCategories"`
	OutputImageCategories []string `json:"outputImageCategories"`
	Version               int64    `json:"version"`
}

// nonNil returns a copy of the policy whose lists are never nil (so they render
// as [] rather than null).
func (p Policy) nonNil() Policy {
	fix := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	p.InputTextCategories = fix(p.InputTextCategories)
	p.OutputTextCategories = fix(p.OutputTextCategories)
	p.InputImageCategories = fix(p.InputImageCategories)
	p.OutputImageCategories = fix(p.OutputImageCategories)
	return p
}

// Selected returns the categories enforced for a scope.
func (p Policy) Selected(direction, modality string) []string {
	switch {
	case direction == DirectionInput && modality == ModalityText:
		return p.InputTextCategories
	case direction == DirectionOutput && modality == ModalityText:
		return p.OutputTextCategories
	case direction == DirectionInput && modality == ModalityImage:
		return p.InputImageCategories
	case direction == DirectionOutput && modality == ModalityImage:
		return p.OutputImageCategories
	}
	return nil
}

// AnySelected reports whether at least one scope enforces a category.
func (p Policy) AnySelected() bool {
	return len(p.InputTextCategories)+len(p.OutputTextCategories)+
		len(p.InputImageCategories)+len(p.OutputImageCategories) > 0
}

func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sameCategories compares the four lists (not the version).
func (p Policy) sameCategories(o Policy) bool {
	return sameList(p.InputTextCategories, o.InputTextCategories) &&
		sameList(p.OutputTextCategories, o.OutputTextCategories) &&
		sameList(p.InputImageCategories, o.InputImageCategories) &&
		sameList(p.OutputImageCategories, o.OutputImageCategories)
}

// normalize validates and canonicalises every list of the policy.
func (p Policy) normalize() (Policy, error) {
	var err error
	out := Policy{Version: p.Version}
	if out.InputTextCategories, err = normalizeCategories(p.InputTextCategories, ModalityText); err != nil {
		return Policy{}, err
	}
	if out.OutputTextCategories, err = normalizeCategories(p.OutputTextCategories, ModalityText); err != nil {
		return Policy{}, err
	}
	if out.InputImageCategories, err = normalizeCategories(p.InputImageCategories, ModalityImage); err != nil {
		return Policy{}, err
	}
	if out.OutputImageCategories, err = normalizeCategories(p.OutputImageCategories, ModalityImage); err != nil {
		return Policy{}, err
	}
	return out, nil
}
