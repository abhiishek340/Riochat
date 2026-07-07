package memory

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"html"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Tunables of how memory reaches a model request.
const (
	// PrefsTokenBudget bounds the preference block, in estimated tokens.
	PrefsTokenBudget = 400
	// MaxInjected is how many profile or custom entries go into one request.
	MaxInjected = 5
	// SemanticDeadline bounds the query embedding on the chat path.
	SemanticDeadline = 200 * time.Millisecond
	// SemanticMinSimilarity is the lowest cosine similarity of a semantic hit.
	SemanticMinSimilarity = 0.7

	// ArtifactKind is the kind and source type of a memory context artifact.
	ArtifactKind = "user_memory"

	artifactExcerptChars = 600
	prefsHeading         = "# prefs"
)

// TokenEstimator estimates the token count of a text.
type TokenEstimator func(text string) int

// DefaultEstimate is a rough estimator: about four ASCII bytes per token and one
// token per non-ASCII character.
func DefaultEstimate(text string) int {
	ascii, other := 0, 0
	for _, r := range text {
		if r < utf8.RuneSelf {
			ascii++
		} else {
			other++
		}
	}
	return (ascii+3)/4 + other
}

// SplitByScope separates preference entries from profile and custom ones, in
// list order. Entries with any other scope are dropped: chat never reads them.
func SplitByScope(entries []Entry) (prefs, others []Entry) {
	for _, e := range entries {
		switch e.Scope {
		case ScopePreference:
			prefs = append(prefs, e)
		case ScopeProfile, ScopeCustom:
			others = append(others, e)
		}
	}
	return prefs, others
}

// FormatPrefs renders the preference entries as the system-level block
//
//	# prefs
//	- key: value
//
// Lines are added in order until the budget would be exceeded; the rest are
// dropped. It returns "" when no line fits or there is nothing to render.
func FormatPrefs(prefs []Entry, budget int, estimate TokenEstimator) string {
	if estimate == nil {
		estimate = DefaultEstimate
	}
	used := estimate(prefsHeading)
	var b strings.Builder
	n := 0
	for _, e := range prefs {
		key, val := strings.TrimSpace(e.MemoryKey), strings.TrimSpace(e.Value)
		if key == "" || val == "" {
			continue
		}
		line := "- " + key + ": " + val
		cost := estimate("\n" + line)
		if used+cost > budget {
			break
		}
		used += cost
		b.WriteString("\n")
		b.WriteString(line)
		n++
	}
	if n == 0 {
		return ""
	}
	return prefsHeading + b.String()
}

// FormatMems renders entries as the user-side `<mems>` block, escaping keys and
// values so they cannot close their element. Entries with an empty key or value
// are skipped; with none left the result is "".
func FormatMems(entries []Entry) string {
	var b strings.Builder
	n := 0
	for _, e := range entries {
		key, val := strings.TrimSpace(e.MemoryKey), strings.TrimSpace(e.Value)
		if key == "" || val == "" {
			continue
		}
		if n == 0 {
			b.WriteString("<mems>")
		}
		b.WriteString("\n<mem k=\"")
		b.WriteString(html.EscapeString(key))
		b.WriteString("\">")
		b.WriteString(html.EscapeString(val))
		b.WriteString("</mem>")
		n++
	}
	if n == 0 {
		return ""
	}
	b.WriteString("\n</mems>")
	return b.String()
}

// PromptStateSignature is a hex digest of the preference entries: stable for a
// given set regardless of order, different whenever a preference's key, scope,
// value or update time changes. Non-preference and blank entries are ignored.
func PromptStateSignature(entries []Entry) string {
	type rec struct {
		key, scope, value string
		at                int64
	}
	var recs []rec
	for _, e := range entries {
		if e.Scope != ScopePreference {
			continue
		}
		key, val := strings.TrimSpace(e.MemoryKey), strings.TrimSpace(e.Value)
		if key == "" || val == "" {
			continue
		}
		recs = append(recs, rec{key, e.Scope, val, e.UpdatedAt.UnixNano()})
	}
	sort.Slice(recs, func(i, j int) bool {
		a, b := recs[i], recs[j]
		if a.key != b.key {
			return a.key < b.key
		}
		if a.scope != b.scope {
			return a.scope < b.scope
		}
		return a.value < b.value
	})
	h := sha256.New()
	put := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	for _, r := range recs {
		put(r.key)
		put(r.scope)
		put(r.value)
		var t [8]byte
		binary.BigEndian.PutUint64(t[:], uint64(r.at))
		h.Write(t[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}
