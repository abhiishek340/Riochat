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

// KeywordSelect picks up to limit entries by keyword overlap with the message.
// The score of an entry is the number of message words of at least two
// characters that occur in its lower-cased "key value" text. Ties keep list
// order; with no usable words the first limit entries are taken.
func KeywordSelect(entries []Entry, message string, limit int) []Entry {
	if limit <= 0 {
		return nil
	}
	if len(entries) <= limit {
		return append([]Entry(nil), entries...)
	}
	var words []string
	for _, w := range strings.Fields(strings.ToLower(message)) {
		if utf8.RuneCountInString(w) >= 2 {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return append([]Entry(nil), entries[:limit]...)
	}
	type scored struct {
		e     Entry
		score int
	}
	list := make([]scored, len(entries))
	for i, e := range entries {
		hay := strings.ToLower(e.MemoryKey + " " + e.Value)
		for _, w := range words {
			if strings.Contains(hay, w) {
				list[i].score++
			}
		}
		list[i].e = e
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	out := make([]Entry, limit)
	for i := range out {
		out[i] = list[i].e
	}
	return out
}

// SelectRelevant chooses the profile and custom entries to inject for a user
// message. With MaxInjected or fewer candidates all are used. Otherwise it
// tries semantic recall when an embedder is enabled and the message is not
// blank, and falls back to keyword scoring on a deadline, an error, no hits, or
// no hit that maps onto a candidate.
func (s *Service) SelectRelevant(ctx context.Context, userID uint, candidates []Entry, message string) []Entry {
	if len(candidates) <= MaxInjected {
		return append([]Entry(nil), candidates...)
	}
	if hits := s.semanticSelect(ctx, userID, candidates, message); len(hits) > 0 {
		return hits
	}
	return KeywordSelect(candidates, message, MaxInjected)
}

func (s *Service) semanticSelect(ctx context.Context, userID uint, candidates []Entry, message string) []Entry {
	if s.embed == nil || !s.embed.Enabled() || strings.TrimSpace(message) == "" {
		return nil
	}
	qctx, cancel := context.WithTimeout(ctx, SemanticDeadline)
	defer cancel()
	// The deadline is enforced here as well, so an embedder that ignores its
	// context cannot hold up the chat request.
	type embedded struct {
		vec []float32
		sig string
		err error
	}
	ch := make(chan embedded, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- embedded{err: context.Canceled}
			}
		}()
		v, sg, e := s.embed.Embed(qctx, message)
		ch <- embedded{v, sg, e}
	}()
	var res embedded
	select {
	case res = <-ch:
	case <-qctx.Done():
		return nil
	}
	vec, sig, err := res.vec, res.sig, res.err
	if err != nil || len(vec) == 0 || qctx.Err() != nil {
		return nil
	}
	found, err := s.Search(qctx, userID, vec, sig, MaxInjected, SemanticMinSimilarity)
	if err != nil || len(found) == 0 {
		return nil
	}
	byKey := make(map[string]Entry, len(candidates))
	for _, c := range candidates {
		byKey[strings.TrimSpace(c.MemoryKey)] = c
	}
	seen := map[string]bool{}
	var out []Entry
	for _, h := range found {
		key := strings.TrimSpace(h.MemoryKey)
		cur, ok := byKey[key]
		if !ok || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, cur)
		if len(out) == MaxInjected {
			break
		}
	}
	return out
}

// Artifact describes one injected entry for the per-turn context record.
type Artifact struct {
	Kind          string
	SourceType    string
	SourceID      string
	Title         string
	Content       string
	ContentHash   string
	TokenEstimate int
	Score         float64
	Metadata      map[string]string
}

// SourceRef is the prompt-source reference of an injected entry.
type SourceRef struct {
	ID    string
	Label string
}

// Artifacts builds one artifact per entry that has a key and a value, together
// with the matching prompt source references (same order).
func Artifacts(entries []Entry, estimate TokenEstimator) ([]Artifact, []SourceRef) {
	if estimate == nil {
		estimate = DefaultEstimate
	}
	var arts []Artifact
	var refs []SourceRef
	for _, e := range entries {
		key, val := strings.TrimSpace(e.MemoryKey), strings.TrimSpace(e.Value)
		if key == "" || val == "" {
			continue
		}
		content := clip(val, artifactExcerptChars)
		sum := sha256.Sum256([]byte(ArtifactKind + "\x00" + key + "\x00" + content))
		title := e.Scope
		if title == "" {
			title = key
		}
		arts = append(arts, Artifact{
			Kind: ArtifactKind, SourceType: ArtifactKind, SourceID: key, Title: title,
			Content: content, ContentHash: hex.EncodeToString(sum[:]),
			TokenEstimate: estimate(content), Score: 1,
			Metadata: map[string]string{"memory_key": key, "scope": e.Scope, "updated_by": e.UpdatedBy},
		})
		refs = append(refs, SourceRef{ID: key, Label: e.Scope})
	}
	return arts, refs
}

// PromptContext is everything chat needs from memory for one message.
type PromptContext struct {
	// Prefs is the system-level preference block ("" when none).
	Prefs string
	// Mems is the user-side `<mems>` block ("" when none).
	Mems string
	// State is the preference signature for prompt-cache reuse.
	State string
	// Injected are the profile and custom entries placed in Mems.
	Injected  []Entry
	Artifacts []Artifact
	Sources   []SourceRef
}

// BuildPromptContext loads the user's memories through the cache and prepares
// the blocks for one message. A load failure yields an empty context and the
// error, so the caller can proceed without memory.
func (s *Service) BuildPromptContext(ctx context.Context, userID uint, message string, estimate TokenEstimator) (PromptContext, error) {
	entries, err := s.ListCached(ctx, userID)
	if err != nil {
		return PromptContext{State: PromptStateSignature(nil)}, err
	}
	prefs, others := SplitByScope(entries)
	picked := s.SelectRelevant(ctx, userID, others, message)
	arts, refs := Artifacts(picked, estimate)
	return PromptContext{
		Prefs:     FormatPrefs(prefs, PrefsTokenBudget, estimate),
		Mems:      FormatMems(picked),
		State:     PromptStateSignature(prefs),
		Injected:  picked,
		Artifacts: arts,
		Sources:   refs,
	}, nil
}
