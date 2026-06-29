package channel

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode"
)

const (
	minContextWindow = 4096
	maxContextWindow = 16_000_000
	maxOutputLimit   = 1_000_000

	// DefaultContextFallback is the window assumed for models with no known one.
	DefaultContextFallback = 128000
	minContextFallback     = 16384
	maxContextFallback     = 16_000_000
	defaultMaxOutput       = 8192

	autoWindowMarker = "_riochatContextWindowMode"
)

var contextWindowKeys = []string{"contextWindow", "context_window", "contextWindowTokens", "context_window_tokens"}
var maxOutputKeys = []string{"maxOutputTokens", "max_output_tokens"}

// capsMap is a decoded capabilities document. Numbers are kept as json.Number
// so large integers survive.
type capsMap map[string]any

// parseCaps decodes a capabilities document. Empty input is an empty map; a
// document that is not a JSON object yields an error.
func parseCaps(raw string) (capsMap, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return capsMap{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, ErrInvalidCapabilities
	}
	return capsMap(m), nil
}

// parseCapsLenient never fails; unusable input is an empty map.
func parseCapsLenient(raw string) capsMap {
	m, err := parseCaps(raw)
	if err != nil {
		return capsMap{}
	}
	return m
}

// positiveInt reads a positive integer given as a number or a numeric string.
func positiveInt(v any) (int64, bool) {
	switch t := v.(type) {
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n, n > 0
		}
		if f, err := t.Float64(); err == nil && f == math.Trunc(f) && f > 0 && f < math.MaxInt64 {
			return int64(f), true
		}
	case float64:
		if t == math.Trunc(t) && t > 0 && t < math.MaxInt64 {
			return int64(t), true
		}
	case int:
		return int64(t), t > 0
	case int64:
		return t, t > 0
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64); err == nil {
			return n, n > 0
		}
	}
	return 0, false
}

// lookupInt returns the first key present among keys. present is true when any
// key exists (even with an unusable value); ok when the value is a positive integer.
func (m capsMap) lookupInt(keys []string) (val int64, present, ok bool) {
	for _, k := range keys {
		if v, exists := m[k]; exists {
			n, good := positiveInt(v)
			return n, true, good
		}
	}
	return 0, false, false
}

// contextWindowOverride is the administrator's window (0 when absent/unusable).
func (m capsMap) contextWindowOverride() int {
	if n, _, ok := m.lookupInt(contextWindowKeys); ok && n <= math.MaxInt32*4 {
		return int(n)
	}
	return 0
}

func (m capsMap) maxOutputOverride() int {
	if n, _, ok := m.lookupInt(maxOutputKeys); ok {
		return int(n)
	}
	return 0
}

// validateLimits checks the window and output limits on save.
func (m capsMap) validateLimits() error {
	win, winPresent, winOK := m.lookupInt(contextWindowKeys)
	if winPresent && (!winOK || win < minContextWindow || win > maxContextWindow) {
		return ErrInvalidCapabilities
	}
	out, outPresent, outOK := m.lookupInt(maxOutputKeys)
	if outPresent && (!outOK || out < 1 || out > maxOutputLimit) {
		return ErrInvalidCapabilities
	}
	if winPresent && outPresent && out >= win {
		return ErrInvalidCapabilities
	}
	return nil
}

// familyRule is one entry of the built-in context window table.
type familyRule struct {
	names  []string
	window int
	output int
}

var familyTable = []familyRule{
	{[]string{"claude-sonnet-3.7", "claude-3.7"}, 200000, 16000},
	{[]string{"claude-sonnet-3.5", "claude-haiku-3.5", "claude-3.5"}, 200000, 8192},
	{[]string{"gpt-4.1"}, 1047576, 32768},
	{[]string{"gpt-4.5"}, 128000, 16384},
	{[]string{"gpt-4o"}, 128000, 16384},
	{[]string{"o1", "o3", "o4"}, 200000, 100000},
	{[]string{"gpt-3.5"}, 16385, 4096},
	{[]string{"gemini-2.0", "gemini-2.5", "gemini-1.5"}, 1000000, 8192},
	{[]string{"grok-3"}, 131072, 16384},
}

// nameTokens splits a model name segment into alphabetic and numeric tokens;
// separators are dropped, so "gpt_4_1" and "gpt-4.1" tokenize identically.
func nameTokens(s string) []string {
	var toks []string
	var cur []rune
	kind := 0 // 1 letters, 2 digits
	flush := func() {
		if len(cur) > 0 {
			toks = append(toks, string(cur))
			cur = cur[:0]
		}
		kind = 0
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsDigit(r):
			if kind != 2 {
				flush()
				kind = 2
			}
			cur = append(cur, r)
		case unicode.IsLetter(r):
			if kind != 1 {
				flush()
				kind = 1
			}
			cur = append(cur, r)
		default:
			flush()
		}
	}
	flush()
	return toks
}

func tokenEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if unicode.IsDigit(rune(a[0])) && unicode.IsDigit(rune(b[0])) {
		return strings.TrimLeft(a, "0") == strings.TrimLeft(b, "0")
	}
	return a == b
}

func tokensHavePrefix(have, want []string) bool {
	if len(want) == 0 || len(have) < len(want) {
		return false
	}
	for i := range want {
		if !tokenEqual(have[i], want[i]) {
			return false
		}
	}
	return true
}

// modelNameSegments splits at "/" and ":".
func modelNameSegments(name string) []string {
	return strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == ':' })
}

// familyLimits looks a model up in the built-in family table.
func familyLimits(name string) (window, output int, ok bool) {
	segs := modelNameSegments(name)
	for _, rule := range familyTable {
		for _, want := range rule.names {
			wt := nameTokens(want)
			for _, seg := range segs {
				if tokensHavePrefix(nameTokens(seg), wt) {
					return rule.window, rule.output, true
				}
			}
		}
	}
	return 0, 0, false
}

// effectiveContextWindow is capabilities override > family table > fallback.
func effectiveContextWindow(name string, caps capsMap, fallback int) int {
	if n := caps.contextWindowOverride(); n > 0 {
		return n
	}
	if w, _, ok := familyLimits(name); ok {
		return w
	}
	return normalizeFallbackWindow(fallback)
}

// effectiveMaxOutput is capabilities override > family table > 8192.
func effectiveMaxOutput(name string, caps capsMap) int {
	if n := caps.maxOutputOverride(); n > 0 {
		return n
	}
	if _, o, ok := familyLimits(name); ok {
		return o
	}
	return defaultMaxOutput
}

func normalizeFallbackWindow(n int) int {
	if n < minContextFallback || n > maxContextFallback {
		return DefaultContextFallback
	}
	return n
}

// displayContextWindow is what users see: the administrator's window, else a
// plausible catalog window, else unknown (nil). The fallback is never shown.
func displayContextWindow(caps capsMap, catalogWindow int) *int {
	if n := caps.contextWindowOverride(); n > 0 {
		return &n
	}
	if catalogWindow >= minContextWindow && catalogWindow <= maxContextWindow {
		return &catalogWindow
	}
	return nil
}

// EffectiveInputBudget is window - min(maxOutput, 20000) - 13000, at least 4000.
func EffectiveInputBudget(window, maxOutput int) int {
	reserve := maxOutput
	if reserve > 20000 {
		reserve = 20000
	}
	b := window - reserve - 13000
	if b < 4000 {
		b = 4000
	}
	return b
}

// CompactionThreshold is floor(budget*percent/100), percent clamped to 100;
// zero or negative percent disables it (0). The minimum is 4000.
func CompactionThreshold(budget, percent int) int {
	if percent <= 0 {
		return 0
	}
	if percent > 100 {
		percent = 100
	}
	t := budget * percent / 100
	if t < 4000 {
		t = 4000
	}
	return t
}

// stripAutoWindow removes the automatically derived context window (marker
// and every window key) from a capabilities document. An emptied object
// becomes the empty string. A manual window is preserved.
func stripAutoWindow(raw string) string {
	m, err := parseCaps(raw)
	if err != nil || len(m) == 0 {
		return raw
	}
	mode, _ := m[autoWindowMarker].(string)
	if !strings.EqualFold(strings.TrimSpace(mode), "auto") {
		return raw
	}
	delete(m, autoWindowMarker)
	for _, k := range contextWindowKeys {
		delete(m, k)
	}
	if len(m) == 0 {
		return ""
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return string(out)
}

// Modalities.
var canonicalModalities = []string{"text", "image", "pdf", "audio", "video"}

func isModality(s string) bool { return containsString(canonicalModalities, s) }

// normalizeModalities keeps known values, de-duplicates and orders them
// canonically. For input, text is always included when the list is non-empty.
func normalizeModalities(in []string, input bool) []string {
	have := map[string]bool{}
	for _, s := range in {
		s = lower(s)
		if isModality(s) {
			have[s] = true
		}
	}
	if input && len(have) > 0 {
		have["text"] = true
	}
	out := []string{}
	for _, m := range canonicalModalities {
		if have[m] {
			out = append(out, m)
		}
	}
	return out
}

// explicitInputModalities reads the capabilities declaration. declared is true
// when the key exists; valid is false when it is not an array of known strings.
func (m capsMap) explicitInputModalities() (mods []string, declared, valid bool) {
	v, ok := m["inputModalities"]
	if !ok {
		return nil, false, true
	}
	arr, isArr := v.([]any)
	if !isArr {
		return nil, true, false
	}
	var list []string
	for _, e := range arr {
		s, isStr := e.(string)
		if !isStr || !isModality(lower(s)) {
			return nil, true, false
		}
		list = append(list, s)
	}
	return normalizeModalities(list, true), true, true
}

// stringList reads an array of strings, ignoring other entries.
func (m capsMap) stringList(key string) []string {
	arr, _ := m[key].([]any)
	var out []string
	for _, e := range arr {
		if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func (m capsMap) object(key string) map[string]any {
	o, _ := m[key].(map[string]any)
	return o
}
