package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// MaxDebugBody is the size above which bodies are replaced by a summary.
const MaxDebugBody = 128 << 10

// Debug is the sanitized snapshot of one HTTP exchange.
type Debug struct {
	Method         string
	Path           string // path with query
	RequestHeaders map[string]string
	RequestBody    string
	// ...OriginalBytes is the size before sanitizing.
	RequestBodyOriginalBytes  int
	RequestBodyTruncated      bool
	Status                    int
	ResponseHeaders           map[string]string
	ResponseBody              string
	ResponseBodyOriginalBytes int
	ResponseBodyTruncated     bool
	// RedactedParts counts values replaced by placeholders in both bodies.
	RedactedParts int
}

// Sanitized is the outcome of SanitizeBody.
type Sanitized struct {
	Text          string
	OriginalBytes int
	Truncated     bool
	Redacted      int
}

func omittedSummary(n int, reason string, redacted int) string {
	return fmt.Sprintf(`{"_debug":{"bodyOmitted":true,"originalBytes":%d,"reason":%q,"redactedParts":%d}}`, n, reason, redacted)
}

// SanitizeBody makes a request or response body safe for debug snapshots,
// error payloads and traces.
func SanitizeBody(body []byte) Sanitized {
	out := Sanitized{OriginalBytes: len(body)}
	if len(body) > MaxDebugBody {
		out.Text, out.Truncated = omittedSummary(len(body), "body_too_large", 0), true
		return out
	}
	if !utf8.Valid(body) {
		out.Text, out.Truncated = omittedSummary(len(body), "binary_body", 0), true
		return out
	}
	trimmed := bytes.TrimSpace(body)
	var text string
	switch {
	case len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') && json.Valid(trimmed):
		v, ok := decodeJSON(trimmed)
		if ok {
			n := 0
			v = redactValue(v, "", "", nil, &n)
			enc, err := encodeJSON(v)
			if err == nil {
				text, out.Redacted = string(enc), n
				break
			}
		}
		text, out.Redacted = redactDataURLs(string(body))
	case looksLikeSSE(trimmed):
		text, out.Redacted = sanitizeSSE(string(body))
	default:
		text, out.Redacted = redactDataURLs(string(body))
	}
	if len(text) > MaxDebugBody {
		out.Text, out.Truncated = omittedSummary(len(body), "body_too_large", out.Redacted), true
		return out
	}
	out.Text = text
	return out
}

func decodeJSON(b []byte) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	return v, true
}

func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func looksLikeSSE(b []byte) bool {
	s := string(b)
	return strings.HasPrefix(s, "data:") || strings.HasPrefix(s, "event:") || strings.Contains(s, "\ndata:") || strings.Contains(s, "\nevent:")
}

func normKey(k string) string {
	k = strings.ToLower(k)
	r := strings.NewReplacer("_", "", "-", "", ".", "")
	return r.Replace(k)
}

var binaryKeys = map[string]bool{
	"b64json": true, "base64": true, "imagebase64": true, "audiobase64": true, "filebase64": true, "filedata": true,
}

var dataParents = map[string]bool{"inlinedata": true, "inlineimage": true, "inputaudio": true, "source": true}

func placeholder(mime string, encoded int) string {
	return fmt.Sprintf("[binary omitted; mime=%s; encoded_bytes=%d]", mime, encoded)
}

// dataURLMIME parses "data:<mime>[;params];base64,<payload>".
func dataURLMIME(s string) (mime string, payloadLen int, ok bool) {
	if !strings.HasPrefix(s, "data:") {
		return "", 0, false
	}
	i := strings.Index(s, ";base64,")
	if i < 0 || i > 200 {
		return "", 0, false
	}
	head := s[5:i]
	if j := strings.Index(head, ";"); j >= 0 {
		head = head[:j]
	}
	if head == "" {
		head = "application/octet-stream"
	}
	return head, len(s) - (i + len(";base64,")), true
}

func keyMIME(key string) string {
	k := strings.ToLower(key)
	switch {
	case strings.Contains(k, "image"):
		return "image/*"
	case strings.Contains(k, "audio"):
		return "audio/*"
	}
	return "application/octet-stream"
}

func looksBase64(s string) bool {
	if len(s) < 256 || len(s)%4 != 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/' || c == '=' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func declaresBase64(m map[string]any) bool {
	for _, k := range []string{"type", "encoding", "format"} {
		if s, ok := m[k].(string); ok && strings.EqualFold(s, "base64") {
			return true
		}
	}
	for _, k := range []string{"media_type", "mime_type"} {
		if s, ok := m[k].(string); ok && strings.HasPrefix(strings.ToLower(s), "image/") {
			return true
		}
	}
	return false
}

// redactValue walks a decoded JSON value, replacing binary payloads. key is
// the object key holding v, parentKey the key holding the enclosing object and
// parent that object.
func redactValue(v any, key, parentKey string, parent map[string]any, n *int) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = redactValue(e, k, key, x, n)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = redactValue(e, key, parentKey, parent, n)
		}
		return x
	case string:
		if mime, plen, ok := dataURLMIME(x); ok {
			*n++
			return placeholder(mime, plen)
		}
		nk := normKey(key)
		if binaryKeys[nk] && x != "" {
			*n++
			return placeholder(keyMIME(key), len(x))
		}
		if nk == "data" && x != "" {
			if dataParents[normKey(parentKey)] || (parent != nil && declaresBase64(parent)) || looksBase64(x) {
				*n++
				return placeholder(keyMIME(key), len(x))
			}
		}
		if t := strings.TrimSpace(x); len(t) > 1 && (t[0] == '{' || t[0] == '[') && json.Valid([]byte(t)) {
			if inner, ok := decodeJSON([]byte(t)); ok {
				c := 0
				inner = redactValue(inner, "", "", nil, &c)
				if c > 0 {
					if enc, err := encodeJSON(inner); err == nil {
						*n += c
						return string(enc)
					}
				}
			}
		}
		return x
	}
	return v
}

var dataURLRe = regexp.MustCompile(`data:[A-Za-z0-9.+/_-]*(?:;[A-Za-z0-9=._+-]+)*;base64,[^\s"'<>\[\]\)\(,]*`)

// redactDataURLs replaces base64 data URLs found in plain text.
func redactDataURLs(s string) (string, int) {
	n := 0
	out := dataURLRe.ReplaceAllStringFunc(s, func(m string) string {
		mime, plen, ok := dataURLMIME(m)
		if !ok {
			return m
		}
		n++
		return placeholder(mime, plen)
	})
	return out, n
}

// sanitizeSSE sanitizes each data: frame line of an event stream.
func sanitizeSSE(s string) (string, int) {
	lines := strings.Split(s, "\n")
	total := 0
	for i, ln := range lines {
		trimmed := strings.TrimRight(ln, "\r")
		if !strings.HasPrefix(trimmed, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		if payload[0] == '{' || payload[0] == '[' {
			if v, ok := decodeJSON([]byte(payload)); ok {
				n := 0
				v = redactValue(v, "", "", nil, &n)
				if enc, err := encodeJSON(v); err == nil {
					lines[i] = "data: " + string(enc)
					total += n
					continue
				}
			}
		}
		red, n := redactDataURLs(payload)
		lines[i] = "data: " + red
		total += n
	}
	return strings.Join(lines, "\n"), total
}

// ---- headers --------------------------------------------------------------

func sensitiveDebugHeader(name string) bool {
	n := strings.ToLower(name)
	switch n {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "api-key":
		return true
	}
	return strings.Contains(n, "token") || strings.Contains(n, "secret") || strings.Contains(n, "key")
}

func debugHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lk := strings.ToLower(k)
		if sensitiveDebugHeader(lk) {
			out[lk] = "[redacted]"
			continue
		}
		out[lk] = strings.Join(h[k], ", ")
	}
	return out
}
