package middleware

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

// ProxySet is a set of trusted proxy addresses. A nil or empty set trusts no
// proxy.
type ProxySet struct{ prefixes []netip.Prefix }

// NewProxySet parses IP and CIDR entries (a bare IP is a /32 or /128). An
// unparsable entry is an error (startup fails).
func NewProxySet(entries []string) (*ProxySet, error) {
	s := &ProxySet{}
	for _, e := range entries {
		p, err := config.ParseProxyEntry(e)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q: %w", e, err)
		}
		s.prefixes = append(s.prefixes, p)
	}
	return s, nil
}

// Contains reports whether addr falls inside any entry.
func (s *ProxySet) Contains(addr netip.Addr) bool {
	if s == nil {
		return false
	}
	addr = addr.Unmap()
	for _, p := range s.prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// resolveClient returns the client address and whether the TCP peer is a
// trusted proxy. When the peer is trusted the address comes from
// X-Forwarded-For (right-most address that is not itself a trusted proxy),
// falling back to X-Real-IP; otherwise it is the TCP peer.
func resolveClient(r *http.Request, set *ProxySet) (ip string, trusted bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, perr := netip.ParseAddr(strings.TrimSpace(host))
	if perr != nil {
		return host, false
	}
	if !set.Contains(peer) {
		return peer.Unmap().String(), false
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		leftmost := ""
		for i := len(parts) - 1; i >= 0; i-- {
			a, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
			if err != nil {
				continue
			}
			leftmost = a.Unmap().String()
			if !set.Contains(a) {
				return a.Unmap().String(), true
			}
		}
		if leftmost != "" {
			return leftmost, true
		}
	}
	if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
		if a, err := netip.ParseAddr(real); err == nil {
			return a.Unmap().String(), true
		}
	}
	return peer.Unmap().String(), true
}

// TrustedProxy creates the per-request state, resolves the client address and
// records whether the peer is a trusted proxy. It must run before RequestID.
func TrustedProxy(set *ProxySet) gin.HandlerFunc {
	return func(c *gin.Context) {
		state := reqctx.NewState("", "")
		ip, trusted := resolveClient(c.Request, set)
		state.SetClient(ip, trusted)
		c.Request = c.Request.WithContext(reqctx.Attach(c.Request.Context(), state))
		c.Next()
	}
}

// ClientIP returns the resolved client address of the request.
func ClientIP(c *gin.Context) string { return reqctx.FromRequest(c.Request).ClientIP() }
