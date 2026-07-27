package mcp

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

const maxBaseURLLength = 2048

// metadataHosts are cloud metadata endpoints that are never reachable, whatever
// else an administrator allows.
var metadataAddrs = []netip.Addr{
	netip.MustParseAddr("169.254.169.254"),
	netip.MustParseAddr("100.100.100.200"),
	netip.MustParseAddr("fd00:ec2::254"),
}

// forbiddenAddr reports whether an address is never a legal destination:
// link-local, unspecified, multicast and cloud metadata addresses. Loopback and
// private ranges are allowed on purpose, an administrator may register an
// internal service.
func forbiddenAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsUnspecified() || a.IsMulticast() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() {
		return true
	}
	for _, m := range metadataAddrs {
		if a == m {
			return true
		}
	}
	return false
}

func forbiddenHostname(h string) bool {
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	return h == "metadata.google.internal"
}

func validHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(h, "."), ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
			if !ok {
				return false
			}
		}
	}
	return true
}

// checkEndpoint validates a server base URL. It returns the trimmed URL.
func checkEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxBaseURLLength {
		return "", errors.New("base url is empty or too long")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("base url does not parse: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("base url scheme must be http or https")
	}
	if u.User != nil {
		return "", errors.New("base url must not embed credentials")
	}
	host := u.Hostname()
	if host == "" {
		return "", errors.New("base url has no host")
	}
	if p := u.Port(); p != "" {
		var n int
		if _, err := fmt.Sscanf(p, "%d", &n); err != nil || n < 1 || n > 65535 {
			return "", errors.New("base url port is invalid")
		}
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if forbiddenAddr(ip) {
			return "", errors.New("base url address is not allowed")
		}
		return raw, nil
	}
	if forbiddenHostname(host) || !validHostname(host) {
		return "", errors.New("base url host is not allowed")
	}
	return raw, nil
}

// dialGuard runs after name resolution for every connection attempt and refuses
// forbidden destinations, which also defeats DNS rebinding and numeric host
// spellings the URL check cannot see.
func dialGuard(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("destination %q is not an address", host)
	}
	if forbiddenAddr(ip) {
		return errors.New("destination address is not allowed")
	}
	return nil
}

const maxRedirects = 3

// sameOrigin compares scheme, host and port.
func sameOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return a.Scheme == b.Scheme && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

// newHTTPClient builds the outbound client: no proxy, guarded dialing, and
// redirects only within the registered origin.
func newHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: dialGuard}
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 0,
	}
	return &http.Client{
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return errors.New("too many redirects")
			}
			if !sameOrigin(req.URL, via[0].URL) {
				return errors.New("redirect leaves the registered origin")
			}
			if _, err := checkEndpoint(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
}
