package channel

import (
	"encoding/json"
	"net"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

const (
	StatusActive   = "active"
	StatusInactive = "inactive"

	ScopePublic   = "public"
	ScopeInternal = "internal"

	personalPrefix = "personal:"

	defaultConnectTimeoutMS = 10000
	defaultReadTimeoutMS    = 120000
	defaultStreamIdleMS     = 60000
)

// normalizePlatformName trims a platform model name and enforces the naming
// rules: not empty, no control characters, no whitespace other than the plain
// space, and not the reserved personal prefix.
func normalizePlatformName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", ErrInvalidPlatformName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrInvalidPlatformName
		}
		if unicode.IsSpace(r) && r != ' ' {
			return "", ErrInvalidPlatformName
		}
	}
	if strings.HasPrefix(strings.ToLower(name), personalPrefix) {
		return "", ErrInvalidPlatformName
	}
	return name, nil
}

// normalizeStatus trims a status; blank means active. Any other value than
// active/inactive is rejected when strict.
func normalizeStatus(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return StatusActive
	}
	return s
}

func validWritableStatus(s string) bool { return s == StatusActive || s == StatusInactive }

func normalizeAccessScope(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "":
		return ScopePublic, nil
	case ScopePublic, ScopeInternal:
		return s, nil
	}
	return "", ErrInvalidAccessScope
}

func normalizeCBPolicyMode(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "enforced") {
		return "enforced"
	}
	return "default"
}

func normalizeThresholdLogic(raw string) string {
	if strings.TrimSpace(raw) == "and" {
		return "and"
	}
	return "or"
}

func nonNegative(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func atLeastOne(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

func timeoutOrDefault(n, def int) int {
	if n <= 0 {
		return def
	}
	return n
}

// validateSystemPrompt trims and length-checks (in code points) a system prompt.
func validateSystemPrompt(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if runeLen(s) > 20000 {
		return "", ErrSystemPromptLong
	}
	return s, nil
}

// validateJSONText checks that a non-empty string is valid JSON.
func validateJSONText(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if !json.Valid([]byte(raw)) {
		return ErrInvalidJSON
	}
	return nil
}

// validateHeadersJSON accepts any valid JSON (or empty).
func validateHeadersJSON(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if !json.Valid([]byte(raw)) {
		return ErrInvalidHeaders
	}
	return nil
}

var sensitiveHeaderParts = []string{"authorization", "api-key", "apikey", "token", "secret"}

func sensitiveHeaderName(name string) bool {
	n := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), "_", "-"))
	if n == "cookie" || n == "set-cookie" {
		return true
	}
	for _, p := range sensitiveHeaderParts {
		if strings.Contains(n, p) {
			return true
		}
	}
	return false
}

// redactHeadersJSON replaces the values of sensitive headers by "********".
// Invalid JSON becomes "{}" and an empty string stays empty.
func redactHeadersJSON(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
		return "{}"
	}
	for k := range m {
		if sensitiveHeaderName(k) {
			m[k] = "********"
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(out)
}

// mergeHeaders merges the upstream and route header documents; the route wins
// case-insensitively on the header name. Non-object documents are ignored.
func mergeHeaders(upstream, route string) string {
	parse := func(raw string) map[string]any {
		var m map[string]any
		if strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &m) != nil {
			return nil
		}
		return m
	}
	merged := map[string]any{}
	for k, v := range parse(upstream) {
		merged[k] = v
	}
	for k, v := range parse(route) {
		for existing := range merged {
			if strings.EqualFold(existing, k) {
				delete(merged, existing)
			}
		}
		merged[k] = v
	}
	if len(merged) == 0 {
		return "{}"
	}
	raw, _ := json.Marshal(merged)
	return string(raw)
}

var hostLabelRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// forbiddenHostNames are names that are never allowed as an upstream address.
var forbiddenHostNames = map[string]bool{
	"localhost": true, "metadata": true, "metadata.google.internal": true, "instance-data": true,
	"metadata.goog": true,
}

var forbiddenCIDRs = mustCIDRs(
	"169.254.0.0/16", // link-local, includes the cloud metadata address
	"fe80::/10",      // IPv6 link-local
	"0.0.0.0/8",      // "this network"
	"100.100.100.200/32",
	"fd00:ec2::254/128",
	"224.0.0.0/4", // multicast
	"ff00::/8",
)

func mustCIDRs(list ...string) []*net.IPNet {
	var out []*net.IPNet
	for _, c := range list {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		out = append(out, n)
	}
	return out
}

// validateUpstreamBaseURL checks an upstream base URL. Private and internal
// addresses are allowed (the administrator is trusted); link-local, metadata
// and similar always-forbidden destinations are not. It returns the trimmed URL.
func validateUpstreamBaseURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 512 {
		return "", ErrInvalidBaseURL
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", ErrInvalidBaseURL
	}
	host := u.Hostname()
	if host == "" || strings.Contains(host, "%") {
		return "", ErrInvalidBaseURL
	}
	if ip := net.ParseIP(host); ip != nil {
		for _, n := range forbiddenCIDRs {
			if n.Contains(ip) {
				return "", ErrInvalidBaseURL
			}
		}
		if ip.IsUnspecified() {
			return "", ErrInvalidBaseURL
		}
		return s, nil
	}
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if forbiddenHostNames[h] || strings.HasSuffix(h, ".localhost") {
		return "", ErrInvalidBaseURL
	}
	for _, label := range strings.Split(h, ".") {
		if !hostLabelRe.MatchString(label) {
			return "", ErrInvalidBaseURL
		}
	}
	return s, nil
}

var (
	assetRefRe  = regexp.MustCompile(`^asset:(ico_[0-9a-f]{32})$`)
	assetIDRe   = regexp.MustCompile(`^ico_[0-9a-f]{32}$`)
	iconSlugRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	assetPrefix = "asset:"
)

// iconRef is a syntactically validated icon reference.
type iconRef struct {
	Value   string // normalized value to store
	AssetID string // set for asset references
}

// parseIconRef validates the syntax of an icon reference. The empty string is
// allowed and means "auto". Asset existence is checked by the caller.
func parseIconRef(raw string) (iconRef, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return iconRef{}, nil
	}
	if runeLen(s) > 2048 {
		return iconRef{}, ErrIconInvalid
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return iconRef{}, ErrIconInvalid
		}
	}
	if len(s) >= len(assetPrefix) && strings.EqualFold(s[:len(assetPrefix)], assetPrefix) {
		norm := strings.ToLower(s)
		m := assetRefRe.FindStringSubmatch(norm)
		if m == nil {
			return iconRef{}, ErrIconInvalid
		}
		return iconRef{Value: norm, AssetID: m[1]}, nil
	}
	if iconSlugRe.MatchString(strings.ToLower(s)) {
		return iconRef{Value: strings.ToLower(s)}, nil
	}
	if strings.HasPrefix(s, "/") {
		if strings.HasPrefix(s, "//") || strings.Contains(s, "\\") {
			return iconRef{}, ErrIconInvalid
		}
		return iconRef{Value: s}, nil
	}
	u, err := url.Parse(s)
	if err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil {
		return iconRef{Value: s}, nil
	}
	return iconRef{}, ErrIconInvalid
}

func parseAssetID(raw string) (string, bool) {
	id := strings.ToLower(strings.TrimSpace(raw))
	return id, assetIDRe.MatchString(id)
}

// normalizeVendorOrGroupName trims a display name and checks 1..64 code points.
func normalizeDisplayName(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" || runeLen(s) > 64 {
		return "", false
	}
	return s, true
}

// uniqueUints de-duplicates ids keeping order.
func uniqueUints(in []uint) []uint {
	seen := map[uint]bool{}
	out := make([]uint, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
