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
