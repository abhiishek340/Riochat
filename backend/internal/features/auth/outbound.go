package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/config"
)

const (
	outboundTimeout = 10 * time.Second
	maxBodyBytes    = 1 << 20
	maxRedirects    = 5
)

// errBodyTooLarge is returned when an upstream body exceeds maxBodyBytes.
var errBodyTooLarge = errors.New("auth: upstream response too large")

// origin renders scheme://host:port with the effective port.
func originOf(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return strings.ToLower(u.Scheme) + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func originOfString(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	return originOf(u), true
}

var cgnat = mustCIDR("100.64.0.0/10")

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// ipAlwaysBlocked covers addresses no outbound call may reach: link-local
// (which includes cloud metadata endpoints), unspecified and multicast.
func ipAlwaysBlocked(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	return ip.Equal(net.ParseIP("fd00:ec2::254"))
}

// ipInternal covers loopback and private ranges.
func ipInternal(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	return ip.IsLoopback() || ip.IsPrivate() || cgnat.Contains(ip)
}

type egressPolicy struct {
	trustedOrigins map[string]bool
	trustedHosts   map[string]bool // host:port pairs derived from trustedOrigins
	allowHosts     map[string]bool
	allowNets      []*net.IPNet
}

func newEgressPolicy(cfg *config.Config, trusted []string) *egressPolicy {
	p := &egressPolicy{trustedOrigins: map[string]bool{}, trustedHosts: map[string]bool{},
		allowHosts: map[string]bool{}}
	for _, raw := range trusted {
		if o, ok := originOfString(raw); ok {
			p.trustedOrigins[o] = true
			if u, err := url.Parse(o); err == nil {
				p.trustedHosts[u.Host] = true
			}
		}
	}
	for _, h := range config.SplitList(cfg.Security.SSRFAllowedHosts) {
		p.allowHosts[strings.ToLower(h)] = true
	}
	for _, c := range config.SplitList(cfg.Security.SSRFAllowedCIDRs) {
		if _, n, err := net.ParseCIDR(c); err == nil {
			p.allowNets = append(p.allowNets, n)
		}
	}
	return p
}

// dial resolves addr and connects to the first permitted address. Internal
// addresses are reachable only for trusted origins or explicitly allowed
// hosts and networks; link-local and metadata addresses never are.
func (p *egressPolicy) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	trusted := p.trustedHosts[strings.ToLower(net.JoinHostPort(host, port))] || p.allowHosts[strings.ToLower(host)]
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
	}
	d := &net.Dialer{Timeout: outboundTimeout}
	var lastErr error = fmt.Errorf("auth: no permitted address for %s", host)
	for _, ip := range ips {
		if ipAlwaysBlocked(ip) {
			lastErr = errors.New("auth: destination address is blocked")
			continue
		}
		if ipInternal(ip) && !trusted && !p.netAllowed(ip) {
			lastErr = errors.New("auth: destination address is not permitted")
			continue
		}
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (p *egressPolicy) netAllowed(ip net.IP) bool {
	for _, n := range p.allowNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// httpClient returns the outbound client. trusted lists URLs whose origins may
// resolve to internal addresses and be redirected to.
func (s *Service) httpClient(trusted ...string) *http.Client {
	if s.h.HTTPClient != nil {
		return s.h.HTTPClient
	}
	policy := newEgressPolicy(s.cfg(), trusted)
	return &http.Client{
		Timeout: outboundTimeout,
		Transport: &http.Transport{
			DialContext:           policy.dial,
			Proxy:                 nil,
			TLSHandshakeTimeout:   outboundTimeout,
			ResponseHeaderTimeout: outboundTimeout,
			MaxIdleConns:          4,
			IdleConnTimeout:       30 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("auth: too many redirects")
			}
			if !policy.trustedOrigins[originOf(req.URL)] {
				return errors.New("auth: redirect leaves the trusted origin")
			}
			return nil
		},
	}
}

// readCapped reads at most maxBodyBytes of r.
func readCapped(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxBodyBytes {
		return nil, errBodyTooLarge
	}
	return b, nil
}

// doCapped performs req and returns the status and the capped body.
func doCapped(client *http.Client, req *http.Request) (int, []byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := readCapped(resp.Body)
	return resp.StatusCode, body, err
}

// validateEndpointURL checks an administrator-supplied provider endpoint:
// absolute http(s) URL with a host, no credentials, and not a link-local or
// metadata address. Private and loopback hosts are allowed on purpose.
func validateEndpointURL(raw string) error {
	bad := ErrProviderInvalid.WithMessage("provider endpoint is invalid")
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return bad
	}
	host := strings.ToLower(u.Hostname())
	switch host {
	case "metadata.google.internal", "metadata", "instance-data":
		return bad
	}
	if ip := net.ParseIP(host); ip != nil && ipAlwaysBlocked(ip) {
		return bad
	}
	return nil
}
