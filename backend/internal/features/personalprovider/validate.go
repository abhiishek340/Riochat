package personalprovider

import (
	"crypto/rand"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits shared by validation and the data model.
const (
	maxNameRunes     = 64
	maxIconLen       = 64
	maxBaseURLLen    = 512
	maxAPIKeyBytes   = 512
	maxModelNameLen  = 100
	maxModelsPerReq  = 200
	maxHostLen       = 255
	maxLastErrorLen  = 200
	maxBlockedHosts  = 4096
	minPublicIDLen   = 8
	maxPublicIDLen   = 32
	generatedIDLen   = 16
	referencePrefix  = "personal:"
	keyHintMask      = "••••"
	minKeyForPartial = 12
)

// ---- public ids and model references -----------------------------------------

func validPublicID(id string) bool {
	if len(id) < minPublicIDLen || len(id) > maxPublicIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

const idAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

func newPublicID() (string, error) {
	buf := make([]byte, generatedIDLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, generatedIDLen)
	for i, b := range buf {
		out[i] = idAlphabet[int(b)%len(idAlphabet)]
	}
	return string(out), nil
}

// IsReference reports whether a model name lies in the reserved personal
// namespace (it need not be well formed).
func IsReference(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), referencePrefix)
}

// FormatReference builds "personal:<providerPublicID>/<upstreamModel>".
func FormatReference(publicID, model string) string {
	return referencePrefix + publicID + "/" + model
}

// ParseReference splits a model reference. Only the first slash separates the
// provider id from the model name, which may itself contain slashes.
func ParseReference(ref string) (publicID, model string, ok bool) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, referencePrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(ref, referencePrefix)
	i := strings.Index(rest, "/")
	if i < 0 {
		return "", "", false
	}
	publicID, model = rest[:i], rest[i+1:]
	if !validPublicID(publicID) || !validModelName(model) {
		return "", "", false
	}
	return publicID, model, true
}

// ---- names, icons, keys -------------------------------------------------------

func validModelName(name string) bool {
	if name == "" || len(name) > maxModelNameLen {
		return false
	}
	for i := 0; i < len(name); i++ {
		if name[i] < 0x21 || name[i] == 0x7f {
			return false
		}
	}
	return true
}

// normalizeName trims and checks a provider name. An empty result is returned
// as is so the caller decides whether a default applies.
func normalizeName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if utf8.RuneCountInString(name) > maxNameRunes {
		return "", ErrInvalidName
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", ErrInvalidName
		}
	}
	return name, nil
}

// normalizeIcon lower-cases and checks an icon slug. Empty is valid.
func normalizeIcon(raw string) (string, error) {
	icon := strings.ToLower(strings.TrimSpace(raw))
	if icon == "" {
		return "", nil
	}
	if len(icon) > maxIconLen || icon[0] == '-' {
		return "", ErrInvalidIcon
	}
	for i := 0; i < len(icon); i++ {
		c := icon[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return "", ErrInvalidIcon
		}
	}
	return icon, nil
}

// normalizeAPIKey trims the key and requires 1..512 bytes of visible ASCII.
func normalizeAPIKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" || len(key) > maxAPIKeyBytes {
		return "", ErrInvalidAPIKey
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x21 || key[i] > 0x7e {
			return "", ErrInvalidAPIKey
		}
	}
	return key, nil
}

// keyHint is the masked rendition of a key that is safe to show.
func keyHint(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	r := []rune(key)
	if len(r) < minKeyForPartial {
		return keyHintMask
	}
	return string(r[:3]) + keyHintMask + string(r[len(r)-4:])
}

func keyBinding(userID uint, publicID string) string {
	return "personal_provider.api_key:" + strconv.FormatUint(uint64(userID), 10) + ":" + publicID
}

// ---- host names and the deny-list ---------------------------------------------

// validHostname checks a bare DNS name (or IPv4 literal): dot separated labels
// of letters, digits and inner hyphens.
func validHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func splitHostList(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
}

// ValidateBlockedHosts is the validator of the blocked_hosts setting: at most
// 4096 characters, every entry (after an optional "*." prefix) a hostname.
func ValidateBlockedHosts(raw string) error {
	if utf8.RuneCountInString(raw) > maxBlockedHosts {
		return fmt.Errorf("blocked_hosts is longer than %d characters", maxBlockedHosts)
	}
	for _, e := range splitHostList(raw) {
		h := strings.TrimPrefix(e, "*.")
		h = strings.TrimSuffix(h, ".")
		if !validHostname(h) {
			return fmt.Errorf("blocked_hosts entry %q is not a valid host name", e)
		}
	}
	return nil
}

// ValidateMaxPerUser is the validator of the max_per_user setting.
func ValidateMaxPerUser(n int) error {
	if n < 1 || n > 50 {
		return fmt.Errorf("max_per_user must be between 1 and 50")
	}
	return nil
}

// denyList is a parsed blocked_hosts value.
type denyList struct {
	exact    map[string]bool
	suffixes []string // ".example.com" for "*.example.com"
}

func parseDenyList(raw string) denyList {
	d := denyList{exact: map[string]bool{}}
	for _, e := range splitHostList(raw) {
		e = strings.ToLower(strings.TrimSuffix(e, "."))
		if strings.HasPrefix(e, "*.") {
			if rest := e[2:]; rest != "" {
				d.suffixes = append(d.suffixes, "."+rest)
			}
			continue
		}
		if e != "" {
			d.exact[e] = true
		}
	}
	return d
}

// matches reports whether host (any case, optional trailing dot) is denied. A
// wildcard entry matches subdomains at any depth but never the bare domain.
func (d denyList) matches(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return false
	}
	if d.exact[host] {
		return true
	}
	for _, s := range d.suffixes {
		if strings.HasSuffix(host, s) && len(host) > len(s) {
			return true
		}
	}
	return false
}

// ---- base URL ------------------------------------------------------------------

// parsedURL is a normalized provider address.
type parsedURL struct {
	BaseURL string // https://host[:port]/path
	Host    string // lower-case host name, no port, no brackets, no trailing dot
}

// parseBaseURL applies the syntactic rules and normalization of a base URL.
// Every failure is ErrInvalidBaseURL. Policy checks are separate.
func parseBaseURL(raw string) (parsedURL, error) {
	s := strings.TrimSpace(raw)
	if s == "" || utf8.RuneCountInString(s) > maxBaseURLLen {
		return parsedURL{}, ErrInvalidBaseURL
	}
	if strings.ContainsAny(s, "?#\\") {
		return parsedURL{}, ErrInvalidBaseURL
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return parsedURL{}, ErrInvalidBaseURL
		}
	}
	u, err := url.Parse(s)
	if err != nil || u.Opaque != "" || u.Host == "" {
		return parsedURL{}, ErrInvalidBaseURL
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return parsedURL{}, ErrInvalidBaseURL
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return parsedURL{}, ErrInvalidBaseURL
	}
	if strings.HasSuffix(u.Host, ":") {
		return parsedURL{}, ErrInvalidBaseURL
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" {
		return parsedURL{}, ErrInvalidBaseURL
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return parsedURL{}, ErrInvalidBaseURL
		}
		port = strconv.Itoa(n)
	}
	if len(host) > maxHostLen {
		return parsedURL{}, ErrInvalidBaseURL
	}
	hostPart := host
	if strings.Contains(host, ":") {
		hostPart = "[" + host + "]"
	}
	if port != "" {
		hostPart += ":" + port
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	out := "https://" + hostPart + path
	if utf8.RuneCountInString(out) > maxBaseURLLen {
		return parsedURL{}, ErrInvalidBaseURL
	}
	return parsedURL{BaseURL: out, Host: host}, nil
}

// ---- outbound address policy ----------------------------------------------------

// OutboundPolicy decides whether a host may be called on behalf of a user.
type OutboundPolicy interface {
	// CheckHost returns an error when host (lower case, no port) must never be
	// contacted, for example a loopback, private or metadata address.
	CheckHost(host string) error
}

// PublicOnlyPolicy refuses literal internal addresses and host names that are
// internal by convention. Resolved addresses are checked again at connect
// time by the default HTTP client.
type PublicOnlyPolicy struct{}

var internalSuffixes = []string{
	".localhost", ".local", ".internal", ".localdomain", ".home.arpa", ".lan", ".intranet", ".corp", ".private",
}

// CheckHost implements OutboundPolicy.
func (PublicOnlyPolicy) CheckHost(host string) error {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return ErrBlockedHost
	}
	if ip := net.ParseIP(host); ip != nil {
		if forbiddenIP(ip) {
			return ErrBlockedHost
		}
		return nil
	}
	if host == "localhost" || host == "metadata" || !strings.Contains(host, ".") {
		return ErrBlockedHost
	}
	for _, s := range internalSuffixes {
		if strings.HasSuffix(host, s) {
			return ErrBlockedHost
		}
	}
	// A final label made of digits or a hex prefix is an alternate spelling of
	// an IP address that resolvers accept ("2130706433", "0x7f.1").
	last := host[strings.LastIndex(host, ".")+1:]
	if last == "" || strings.HasPrefix(last, "0x") {
		return ErrBlockedHost
	}
	allDigits := true
	for i := 0; i < len(last); i++ {
		if last[i] < '0' || last[i] > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return ErrBlockedHost
	}
	return nil
}

var forbiddenNets = mustCIDRs(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15",
	"198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
	"::/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/32", "2001:db8::/32", "2002::/16",
	"fc00::/7", "fe80::/10", "fec0::/10", "ff00::/8",
)

func mustCIDRs(list ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(list))
	for _, c := range list {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		out = append(out, n)
	}
	return out
}

// forbiddenIP reports whether ip is outside the public internet.
func forbiddenIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range forbiddenNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
