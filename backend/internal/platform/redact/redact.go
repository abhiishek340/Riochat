// Package redact scrubs secrets from text that is about to be logged or
// persisted (query strings, error causes, diagnostic details).
package redact

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Placeholder replaces every redacted value.
const Placeholder = "REDACTED"

// MaxCauseBytes bounds persisted and logged cause text.
const MaxCauseBytes = 4096

var sensitiveQueryKeys = map[string]struct{}{
	"access_token": {}, "api_key": {}, "apikey": {}, "code": {}, "id_token": {},
	"key": {}, "password": {}, "refresh_token": {}, "secret": {}, "sig": {},
	"signature": {}, "state": {}, "ticket": {}, "token": {},
	"x-amz-credential": {}, "x-amz-security-token": {}, "x-amz-signature": {},
	"x-goog-credential": {}, "x-goog-signature": {},
}

// IsSensitiveQueryKey reports whether a query key (any case) carries a secret.
func IsSensitiveQueryKey(key string) bool {
	_, ok := sensitiveQueryKeys[strings.ToLower(key)]
	return ok
}

// Query redacts the values of sensitive keys in a raw query string. Key names,
// order and the encoding of untouched pairs are preserved.
func Query(raw string) string {
	if raw == "" {
		return raw
	}
	parts := strings.Split(raw, "&")
	for i, p := range parts {
		key, _, hasValue := strings.Cut(p, "=")
		if !hasValue {
			continue
		}
		name := key
		if dec, err := unescape(key); err == nil {
			name = dec
		}
		if IsSensitiveQueryKey(name) {
			parts[i] = key + "=" + Placeholder
		}
	}
	return strings.Join(parts, "&")
}

// PathWithQuery renders "path?query" with the query redacted.
func PathWithQuery(path, rawQuery string) string {
	if rawQuery == "" {
		return path
	}
	return path + "?" + Query(rawQuery)
}

func unescape(s string) (string, error) {
	s = strings.ReplaceAll(s, "+", " ")
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			b.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
			i += 2
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String(), nil
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

var (
	urlRe       = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>` + "`" + `]+`)
	userinfoRe  = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]*://)[^/?#@\s]*@`)
	bearerRe    = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=\-]{16,}`)
	assignRe    = regexp.MustCompile(`(?i)\b(api_key|api-key|access_token|refresh_token|client_secret|secret|password|token)=([^\s&"',;<>]+)`)
	bearerLabel = regexp.MustCompile(`(?i)^bearer`)
)

// Text scrubs credentials from free-form error text: URL user info, sensitive
// URL query values, "Bearer <token>" and key=value secret assignments.
func Text(s string) string {
	if s == "" {
		return s
	}
	s = urlRe.ReplaceAllStringFunc(s, redactURL)
	s = bearerRe.ReplaceAllStringFunc(s, func(m string) string {
		return bearerLabel.FindString(m) + " " + Placeholder
	})
	s = assignRe.ReplaceAllString(s, "${1}="+Placeholder)
	return s
}

func redactURL(u string) string {
	// Trailing sentence punctuation is not part of the URL.
	trail := ""
	for len(u) > 0 && strings.ContainsRune(".,;:!?)]}", rune(u[len(u)-1])) {
		trail = u[len(u)-1:] + trail
		u = u[:len(u)-1]
	}
	u = userinfoRe.ReplaceAllString(u, "${1}"+Placeholder+"@")
	if i := strings.IndexByte(u, '?'); i >= 0 {
		query, frag := u[i+1:], ""
		if j := strings.IndexByte(query, '#'); j >= 0 {
			query, frag = query[:j], query[j:]
		}
		u = u[:i+1] + Query(query) + frag
	}
	return u + trail
}

// Truncate cuts s to at most max bytes on a character boundary and appends a
// suffix naming the original size.
func Truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return fmt.Sprintf("%s...(truncated, %d bytes total)", s[:cut], len(s))
}

// Cause redacts then truncates error text to MaxCauseBytes.
func Cause(s string) string { return Truncate(Text(s), MaxCauseBytes) }
