package textutil

import (
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits of the tool-result preview.
const (
	// PreviewMaxInputBytes is the input size above which a result is only
	// previewed when it is plainly prose.
	PreviewMaxInputBytes = 256 << 10
	// PreviewMaxTokens bounds the preview text (estimated tokens).
	PreviewMaxTokens = 512
	// PreviewMaxSources bounds the number of sources.
	PreviewMaxSources = 8
	// PreviewMaxURL, PreviewMaxTitle and PreviewMaxSnippet bound source fields.
	PreviewMaxURL     = 2048
	PreviewMaxTitle   = 180
	PreviewMaxSnippet = 320

	previewMaxNodes = 512
	previewMaxDepth = 24
)

// Source is one cited web page found in a tool result.
type Source struct {
	Title   string
	URL     string
	Snippet string
}

// Preview is the compact, UI-facing summary of a tool result.
type Preview struct {
	Text    string
	Sources []Source
}

var (
	urlKeys     = []string{"url", "uri", "link", "href", "retrieved_url", "source_url"}
	titleKeys   = []string{"title", "name", "source", "site_name", "sitename", "displayname"}
	snippetKeys = []string{"snippet", "description", "summary", "excerpt", "content", "text"}
	topTextKeys = []string{"answer", "summary", "message"}
	priorityKey = []string{"content", "answer", "summary", "message", "result", "structuredcontent", "results", "items", "data", "sources", "citations"}

	htmlImgRe  = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	mdImageRe  = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	blankRunRe = regexp.MustCompile(`\n{3,}`)
	mdLinkRe   = regexp.MustCompile(`\[([^\]\n]{1,300})\]\((https?://[^)\s]+)\)`)
	titleLine  = regexp.MustCompile(`^\s*Title\s*[:\x{ff1a}]\s*(.+?)\s*$`)
	urlLine    = regexp.MustCompile(`^\s*URL\s*[:\x{ff1a}]\s*(\S+)\s*$`)
)

// normKey folds camelCase and snake_case spellings of a key together.
func normKey(k string) string {
	k = strings.ToLower(k)
	k = strings.ReplaceAll(k, "_", "")
	return strings.ReplaceAll(k, "-", "")
}

// PresentToolResult builds the compact preview of a tool result. ok is false
// when there is neither text nor a source to show.
func PresentToolResult(raw string) (Preview, bool) {
	t := strings.TrimSpace(raw)
	if t == "" {
		return Preview{}, false
	}
	if len(raw) > PreviewMaxInputBytes {
		if IsOpaque(t) || t[0] == '{' || t[0] == '[' || t[0] == '"' {
			return Preview{}, false
		}
		t = truncateTokens(t, PreviewMaxTokens)
	}
	t = strings.TrimSpace(SanitizeOpaque(t))

	var text string
	var sources []Source
	if v, ok := decodeJSONValue(t); ok {
		w := &previewWalker{}
		w.top(v)
		w.walk(v, 0, true)
		text, sources = w.text(), w.sources
	} else {
		text = t
	}
	text = cleanPreviewText(text)
	sources = append(sources, detectTextSources(text)...)
	sources = finalizeSources(sources)
	text = truncateTokens(text, PreviewMaxTokens)
	if text == "" && len(sources) == 0 {
		return Preview{}, false
	}
	return Preview{Text: text, Sources: sources}, true
}

// decodeJSONValue parses s as a complete JSON object, array or string.
func decodeJSONValue(s string) (any, bool) {
	if s == "" || (s[0] != '{' && s[0] != '[' && s[0] != '"') {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return nil, false
	}
	if _, err := dec.Token(); err == nil {
		return nil, false // trailing data
	}
	return v, true
}

type previewWalker struct {
	nodes    int
	sources  []Source
	answer   string
	typed    string
	fallback string
	narr     []string
}

// top picks answer/summary/message from the top-level object.
func (w *previewWalker) top(v any) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	byKey := map[string]string{}
	for k, e := range m {
		if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
			byKey[normKey(k)] = s
		}
	}
	for _, k := range topTextKeys {
		if s, ok := byKey[k]; ok {
			w.answer = s
			return
		}
	}
}

func (w *previewWalker) text() string {
	switch {
	case w.answer != "":
		return w.answer
	case w.typed != "":
		return w.typed
	}
	return w.fallback
}

func orderedKeys(m map[string]any) []string {
	rank := map[string]int{}
	for i, k := range priorityKey {
		rank[k] = i
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		ni, nj := normKey(keys[i]), normKey(keys[j])
		ri, oki := rank[ni]
		rj, okj := rank[nj]
		switch {
		case oki && okj:
			if ri != rj {
				return ri < rj
			}
		case oki:
			return true
		case okj:
			return false
		}
		if ni != nj {
			return ni < nj
		}
		return keys[i] < keys[j]
	})
	return keys
}

func (w *previewWalker) walk(v any, depth int, root bool) {
	if w.nodes >= previewMaxNodes || depth > previewMaxDepth {
		return
	}
	w.nodes++
	switch x := v.(type) {
	case map[string]any:
		w.object(x)
		for _, k := range orderedKeys(x) {
			if isURLKey(k) {
				continue
			}
			w.walk(x[k], depth+1, false)
		}
	case []any:
		for _, e := range x {
			w.walk(e, depth+1, false)
		}
	case string:
		w.str(x, depth)
	}
}

func isURLKey(k string) bool {
	n := normKey(k)
	for _, u := range urlKeys {
		if n == normKey(u) {
			return true
		}
	}
	return false
}

func lookup(m map[string]any, keys []string) string {
	by := map[string]string{}
	for k, e := range m {
		if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
			by[normKey(k)] = s
		}
	}
	for _, k := range keys {
		if s, ok := by[normKey(k)]; ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func (w *previewWalker) object(m map[string]any) {
	if typ, _ := m["type"].(string); typ == "text" && w.typed == "" {
		if s, ok := m["text"].(string); ok && strings.TrimSpace(s) != "" {
			w.typed = s
		}
	}
	if u := lookup(m, urlKeys); u != "" {
		w.sources = append(w.sources, Source{
			URL: u, Title: lookup(m, titleKeys), Snippet: lookup(m, snippetKeys),
		})
	}
}

// str handles a string value: embedded standalone JSON documents are walked,
// the remaining narrative becomes the fallback text.
func (w *previewWalker) str(s string, depth int) {
	narrative, docs := splitEmbeddedJSON(s)
	for _, d := range docs {
		w.walk(d, depth+1, false)
	}
	if w.fallback == "" && narrativeLooking(narrative) {
		w.fallback = strings.TrimSpace(narrative)
	}
}

func narrativeLooking(s string) bool {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) < 8 || strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || IsOpaque(s) {
		return false
	}
	letters, space, cjk := 0, false, false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
			letters++
			if isCJK(r) {
				cjk = true
			}
		case unicode.IsSpace(r):
			space = true
		}
	}
	return letters >= 4 && (space || cjk)
}

// splitEmbeddedJSON separates standalone JSON objects/arrays that start a line
// from the surrounding prose. Fenced code blocks and lines indented by four or
// more spaces (or a tab) are left alone.
func splitEmbeddedJSON(s string) (string, []any) {
	if !strings.ContainsAny(s, "{[") {
		return s, nil
	}
	if v, ok := decodeJSONValue(strings.TrimSpace(s)); ok {
		if _, isStr := v.(string); !isStr {
			return "", []any{v}
		}
	}
	var out strings.Builder
	var docs []any
	fenced := false
	rest := s
	for rest != "" {
		line := rest
		nl := strings.IndexByte(rest, '\n')
		consumed := len(rest)
		if nl >= 0 {
			line, consumed = rest[:nl], nl+1
		}
		trim := strings.TrimLeft(line, " ")
		switch {
		case strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~"):
			fenced = !fenced
		case !fenced && len(line)-len(trim) < 4 && !strings.HasPrefix(line, "\t") && (strings.HasPrefix(trim, "{") || strings.HasPrefix(trim, "[")):
			dec := json.NewDecoder(strings.NewReader(rest[len(line)-len(trim):]))
			dec.UseNumber()
			var v any
			if dec.Decode(&v) == nil {
				switch v.(type) {
				case map[string]any, []any:
					docs = append(docs, v)
					end := len(line) - len(trim) + int(dec.InputOffset())
					rest = rest[end:]
					if i := strings.IndexByte(rest, '\n'); i >= 0 {
						rest = rest[i+1:]
					} else {
						rest = ""
					}
					continue
				}
			}
		}
		out.WriteString(rest[:consumed])
		rest = rest[consumed:]
	}
	return out.String(), docs
}

func cleanPreviewText(s string) string {
	s = htmlImgRe.ReplaceAllString(s, "")
	s = mdImageRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = blankRunRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// truncateTokens cuts s to at most max estimated tokens, adding an ellipsis.
func truncateTokens(s string, max int) string {
	if EstimateTokens(s) <= max {
		return s
	}
	runes := []rune(s)
	lo, hi := 0, len(runes)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if EstimateTokens(string(runes[:mid])) <= max {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return strings.TrimRight(string(runes[:lo]), " \t\r\n") + "…"
}

// detectTextSources finds Markdown links and consecutive Title:/URL: lines.
func detectTextSources(text string) []Source {
	var out []Source
	for _, m := range mdLinkRe.FindAllStringSubmatch(text, -1) {
		out = append(out, Source{Title: strings.TrimSpace(m[1]), URL: m[2]})
	}
	lines := strings.Split(text, "\n")
	for i := 0; i+1 < len(lines); i++ {
		tm := titleLine.FindStringSubmatch(lines[i])
		if tm == nil {
			continue
		}
		if um := urlLine.FindStringSubmatch(lines[i+1]); um != nil {
			out = append(out, Source{Title: tm[1], URL: um[1]})
			i++
		}
	}
	return out
}

const trailingPunct = ".,;:!?)]}>'\"" + "\u3002\uff0c\uff1b\uff1a\uff01\uff1f\uff09\u3011\u300b\u300d\u300f\u201d\u2019\u3001"

// CleanSourceURL trims trailing punctuation and validates an http(s) URL. It
// returns "" for anything unusable.
func CleanSourceURL(raw string) string {
	u := strings.TrimSpace(raw)
	for u != "" {
		r, size := utf8.DecodeLastRuneInString(u)
		if !strings.ContainsRune(trailingPunct, r) {
			break
		}
		u = u[:len(u)-size]
	}
	if u == "" || len(u) > PreviewMaxURL {
		return ""
	}
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return ""
	}
	if s := strings.ToLower(p.Scheme); s != "http" && s != "https" {
		return ""
	}
	return u
}

func clipRunes(s string, max int, ellipsis bool) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	if ellipsis {
		return string(r[:max-1]) + "…"
	}
	return string(r[:max])
}

func finalizeSources(in []Source) []Source {
	seen := map[string]bool{}
	var out []Source
	for _, s := range in {
		u := CleanSourceURL(s.URL)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, Source{
			URL:     u,
			Title:   clipRunes(s.Title, PreviewMaxTitle, false),
			Snippet: clipRunes(s.Snippet, PreviewMaxSnippet, true),
		})
		if len(out) == PreviewMaxSources {
			break
		}
	}
	return out
}
