// Package outbound decides which URLs the server may call and builds the HTTP
// clients that enforce that decision at connection time (SSRF defence).
package outbound

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strings"
)

// Sentinel errors, matched with errors.Is.
var (
	// ErrBlocked wraps every refusal made by a Policy.
	ErrBlocked = errors.New("outbound target not permitted")
	// ErrOriginMismatch is returned when a request targets another origin than
	// the endpoint its client was configured for.
	ErrOriginMismatch = errors.New("request target origin differs from the configured origin")
	// ErrRedirectRefused is returned when a redirect is not allowed.
	ErrRedirectRefused = errors.New("redirect refused")
)

// MetadataHost is the cloud metadata DNS name that is never reachable.
const MetadataHost = "metadata.google.internal"

var (
	metadataIPs = []netip.Addr{
		netip.MustParseAddr("100.100.100.200"),
		netip.MustParseAddr("fd00:ec2::254"),
	}
	// Ranges that only the public-only policy rejects.
	publicOnlyRanges = mustPrefixes(
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.51.100.0/24",
		"203.0.113.0/24", "240.0.0.0/4", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64",
		"2001::/23", "2001:db8::/32", "2002::/16", "fec0::/10",
	)
	fakeIPRange = netip.MustParsePrefix("198.18.0.0/15")
)

func mustPrefixes(in ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(in))
	for i, s := range in {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

// Policy is an immutable outbound decision. The zero value performs only URL
// syntax checks.
type Policy struct {
	enforce    bool
	publicOnly bool
	hosts      map[string]struct{}
	cidrs      []netip.Prefix
	trustIPs   []netip.Addr
}

// NewPolicy builds a policy from allowlists. Host entries must be plain DNS
// names; CIDR entries must parse and must not be IPv4-mapped IPv6. Duplicates
// are dropped. Entries are trimmed; hosts are lower-cased.
func NewPolicy(enforce bool, hosts, cidrs []string) (*Policy, error) {
	p := &Policy{enforce: enforce, hosts: map[string]struct{}{}}
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		if err := validateAllowedHost(h); err != nil {
			return nil, err
		}
		p.hosts[h] = struct{}{}
	}
	seen := map[netip.Prefix]bool{}
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		pf, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, fmt.Errorf("invalid allowed cidr %q", c)
		}
		if pf.Addr().Is4In6() {
			return nil, fmt.Errorf("allowed cidr %q must not be an IPv4-mapped IPv6 range", c)
		}
		pf = pf.Masked()
		if !seen[pf] {
			seen[pf] = true
			p.cidrs = append(p.cidrs, pf)
		}
	}
	return p, nil
}

// SplitList splits a comma separated list, trimming blanks.
func SplitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// FromConfig builds the deployment policy (with allowlists) and the strict
// policy (same enforcement, no allowlists). enforce should already include the
// production check. An invalid allowlist degrades to strict for both.
func FromConfig(enforce bool, hostsCSV, cidrsCSV string) (trusted, strict *Policy) {
	strict = &Policy{enforce: enforce, hosts: map[string]struct{}{}}
	trusted, err := NewPolicy(enforce, SplitList(hostsCSV), SplitList(cidrsCSV))
	if err != nil {
		return strict, strict
	}
	return trusted, strict
}

// PublicOnly returns the policy for user-supplied endpoints: always enforced,
// no allowlists, only globally routable addresses.
func PublicOnly() *Policy {
	return &Policy{enforce: true, publicOnly: true, hosts: map[string]struct{}{}}
}

func validateAllowedHost(h string) error {
	if len(h) > 253 || h == MetadataHost {
		return fmt.Errorf("invalid allowed host %q", h)
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return fmt.Errorf("allowed host %q must be a DNS name, not an IP address", h)
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 {
			return fmt.Errorf("invalid allowed host %q", h)
		}
		for _, r := range label {
			ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_'
			if !ok {
				return fmt.Errorf("invalid allowed host %q", h)
			}
		}
	}
	return nil
}

// Enforced reports whether address checks apply.
func (p *Policy) Enforced() bool { return p != nil && p.enforce }

// PublicOnlyMode reports whether this is the user-endpoint policy.
func (p *Policy) PublicOnlyMode() bool { return p != nil && p.publicOnly }

// Hosts returns the allowlisted host names, sorted.
func (p *Policy) Hosts() []string {
	if p == nil {
		return nil
	}
	out := make([]string, 0, len(p.hosts))
	for h := range p.hosts {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// Trust derives a policy that additionally allows exactly the host (or IP) of
// rawURL. Link-local, metadata and invalid hosts are never trusted, and a
// public-only policy is returned unchanged.
func (p *Policy) Trust(rawURL string) *Policy {
	if p == nil {
		p = &Policy{hosts: map[string]struct{}{}}
	}
	if p.publicOnly || !p.enforce {
		return p
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return p
	}
	host := normalizeHost(u.Hostname())
	if host == "" || strings.Contains(host, "%") || host == MetadataHost {
		return p
	}
	cp := *p
	cp.hosts = make(map[string]struct{}, len(p.hosts)+1)
	for k := range p.hosts {
		cp.hosts[k] = struct{}{}
	}
	if a, ok := parseLiteral(host); ok {
		if alwaysForbidden(a) {
			return p
		}
		cp.trustIPs = append(append([]netip.Addr(nil), p.trustIPs...), a)
		return &cp
	}
	cp.hosts[host] = struct{}{}
	return &cp
}

func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimPrefix(h, "[")
	h = strings.TrimSuffix(h, "]")
	return strings.TrimSuffix(h, ".")
}

func parseLiteral(host string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.WithZone("").Unmap(), true
}

func alwaysForbidden(a netip.Addr) bool {
	a = a.Unmap()
	if a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsInterfaceLocalMulticast() ||
		a.IsUnspecified() || a.IsMulticast() {
		return true
	}
	for _, m := range metadataIPs {
		if a == m {
			return true
		}
	}
	return false
}

func isLocalName(host string) bool {
	return host == "localhost" || host == "localhost.localdomain" || host == "ip6-localhost" ||
		strings.HasSuffix(host, ".localhost")
}

func (p *Policy) hostAllowed(host string) bool {
	if p == nil {
		return false
	}
	_, ok := p.hosts[host]
	return ok
}

func (p *Policy) ipAllowlisted(a netip.Addr) bool {
	for _, t := range p.trustIPs {
		if t == a {
			return true
		}
	}
	for _, c := range p.cidrs {
		if c.Contains(a) {
			return true
		}
	}
	return false
}

func blocked(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrBlocked, fmt.Sprintf(format, args...))
}

// ValidateURL applies the textual rules to a destination URL.
func (p *Policy) ValidateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%w: invalid url", ErrBlocked)
	}
	if u.Scheme == "" || u.Host == "" {
		return blocked("url needs a scheme and host")
	}
	if s := strings.ToLower(u.Scheme); s != "http" && s != "https" {
		return blocked("scheme %q is not allowed", u.Scheme)
	}
	if u.User != nil {
		return blocked("url must not contain user info")
	}
	if !p.Enforced() {
		return nil
	}
	host := normalizeHost(u.Hostname())
	if host == "" || strings.Contains(host, "%") {
		return blocked("invalid host")
	}
	if host == MetadataHost {
		return blocked("metadata host")
	}
	if a, ok := parseLiteral(host); ok {
		return p.checkIP(a, true)
	}
	if looksNumeric(host) {
		// Short forms such as 127.1 or 0x7f000001 are read as IPv4 by lenient
		// resolvers; never accept them as names.
		return blocked("numeric host %q is not allowed", host)
	}
	if isLocalName(host) && !p.hostAllowed(host) {
		return blocked("host %q is local", host)
	}
	return nil
}

// looksNumeric reports names a lenient IPv4 parser would accept.
func looksNumeric(host string) bool {
	if host == "" {
		return false
	}
	labels := strings.Split(host, ".")
	for _, l := range labels {
		if l == "" {
			return false
		}
		if strings.HasPrefix(l, "0x") || strings.HasPrefix(l, "0X") {
			for _, r := range l[2:] {
				if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
					return false
				}
			}
			continue
		}
		for _, r := range l {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// CheckIP validates one resolved address (a DNS result, not a typed literal).
func (p *Policy) CheckIP(a netip.Addr) error { return p.checkIP(a, false) }

func (p *Policy) checkIP(a netip.Addr, literal bool) error {
	a = a.WithZone("").Unmap()
	if alwaysForbidden(a) {
		return blocked("address %s is never reachable", a)
	}
	if !p.Enforced() {
		return nil
	}
	if p.publicOnly {
		if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() {
			return blocked("address %s is not public", a)
		}
		for _, r := range publicOnlyRanges {
			if r.Contains(a) {
				return blocked("address %s is in a reserved range", a)
			}
		}
		if literal && fakeIPRange.Contains(a) {
			return blocked("address %s is in a reserved range", a)
		}
		return nil
	}
	if a.IsPrivate() || a.IsLoopback() {
		if p.ipAllowlisted(a) {
			return nil
		}
		return blocked("address %s is private", a)
	}
	return nil
}

// CheckResolved validates the addresses a host name resolved to. Allowlisted
// host names may resolve to private addresses (never to forbidden ones).
func (p *Policy) CheckResolved(host string, addrs []netip.Addr) error {
	host = normalizeHost(host)
	if len(addrs) == 0 {
		return blocked("host %q did not resolve", host)
	}
	for _, a := range addrs {
		a = a.WithZone("").Unmap()
		if p.Enforced() && !p.publicOnly && p.hostAllowed(host) {
			if alwaysForbidden(a) {
				return blocked("address %s is never reachable", a)
			}
			continue
		}
		if err := p.checkIP(a, false); err != nil {
			return err
		}
	}
	return nil
}

// Origin returns lower-case scheme + host (+ port unless it is the scheme's
// default) of a URL.
func Origin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Hostname() == "" {
		return "", fmt.Errorf("invalid url %q", raw)
	}
	scheme := strings.ToLower(u.Scheme)
	host := normalizeHost(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host, nil
}

// SameOrigin compares the origins of two URLs.
func SameOrigin(a, b string) bool {
	oa, err1 := Origin(a)
	ob, err2 := Origin(b)
	return err1 == nil && err2 == nil && oa == ob
}

// RedactHeadersJSON masks sensitive custom header values in a JSON object for
// administrators. Invalid JSON yields "{}"; empty stays empty.
func RedactHeadersJSON(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	m := map[string]any{}
	if err := jsonUnmarshal([]byte(raw), &m); err != nil {
		return "{}"
	}
	for k := range m {
		if sensitiveHeader(k) {
			m[k] = "********"
		}
	}
	b, _ := jsonMarshal(m)
	return string(b)
}

func sensitiveHeader(name string) bool {
	n := strings.ToLower(strings.ReplaceAll(name, "_", "-"))
	if n == "cookie" || n == "set-cookie" {
		return true
	}
	for _, s := range []string{"authorization", "api-key", "apikey", "token", "secret"} {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

// hostPortIP splits a dial address.
func hostPort(addr string) (string, string) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, ""
	}
	return h, p
}
