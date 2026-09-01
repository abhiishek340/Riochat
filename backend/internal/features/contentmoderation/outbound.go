package contentmoderation

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/config"
)

// outboundTimeout bounds one HTTP exchange; each check additionally has its own
// (shorter) deadline.
const outboundTimeout = 30 * time.Second

// newOutboundClient builds the default HTTP client. Address checks follow the
// live security settings, so changing them takes effect without a restart.
// Redirects are followed only within the origin of the original request.
func newOutboundClient(security func() config.SecurityConfig) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           guardedDial(dialer, security),
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: outboundTimeout,
		MaxIdleConns:          16,
		IdleConnTimeout:       60 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   outboundTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			first := via[0].URL
			if !strings.EqualFold(req.URL.Scheme, first.Scheme) || !strings.EqualFold(req.URL.Host, first.Host) {
				return errors.New("redirect leaves the configured origin")
			}
			return nil
		},
	}
}

// guardedDial resolves the target and refuses internal addresses unless the
// operator allowed the host or network.
func guardedDial(d *net.Dialer, security func() config.SecurityConfig) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		sec := security()
		if !sec.SSRFProtectionEnabled {
			return d.DialContext(ctx, network, addr)
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("no addresses for %s", host)
		}
		hostAllowed := listContainsFold(sec.SSRFAllowedHosts, host)
		for _, ip := range ips {
			if hostAllowed || !isInternalIP(ip.IP) || ipInCIDRList(sec.SSRFAllowedCIDRs, ip.IP) {
				continue
			}
			return nil, fmt.Errorf("address %s is not permitted by the outbound policy", ip.IP)
		}
		var lastErr error
		for _, ip := range ips {
			conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

func isInternalIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast()
}

func listContainsFold(csv, v string) bool {
	for _, item := range strings.Split(csv, ",") {
		if item = strings.TrimSpace(item); item != "" && strings.EqualFold(item, v) {
			return true
		}
	}
	return false
}

func ipInCIDRList(csv string, ip net.IP) bool {
	for _, item := range strings.Split(csv, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(item); err == nil && n.Contains(ip) {
			return true
		}
		if single := net.ParseIP(item); single != nil && single.Equal(ip) {
			return true
		}
	}
	return false
}
