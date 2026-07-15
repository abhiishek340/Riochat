package billing

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/riochat/riochat/backend/internal/platform/config"
)

// RequestInfo is the part of an HTTP request used to derive public addresses.
type RequestInfo struct {
	Origin         string
	ForwardedProto string
	ForwardedHost  string
	Host           string
	TLS            bool
}

func requestInfoOf(r *http.Request) RequestInfo {
	return RequestInfo{
		Origin: r.Header.Get("Origin"), ForwardedProto: r.Header.Get("X-Forwarded-Proto"),
		ForwardedHost: r.Header.Get("X-Forwarded-Host"), Host: r.Host, TLS: r.TLS != nil,
	}
}

var errNoPublicBase = errors.New("public base url is not configured")

func trimBase(raw string) (string, error) {
	u, err := httpURL(raw)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/"), nil
}

// publicBase resolves a public base address: the configured one, else (outside
// production) one derived from the request. Production never trusts request
// headers for callback addresses.
func publicBase(cfg *config.Config, configured string, ri RequestInfo, fromOrigin bool) (string, error) {
	if strings.TrimSpace(configured) != "" {
		return trimBase(configured)
	}
	if cfg.IsProd() {
		return "", errNoPublicBase
	}
	if fromOrigin {
		if o := strings.TrimSpace(ri.Origin); o != "" {
			if u, err := httpURL(o); err == nil {
				return u.Scheme + "://" + u.Host, nil
			}
		}
	}
	proto, host := strings.TrimSpace(ri.ForwardedProto), strings.TrimSpace(ri.ForwardedHost)
	if host == "" {
		host = strings.TrimSpace(ri.Host)
		if proto == "" {
			if ri.TLS {
				proto = "https"
			} else {
				proto = "http"
			}
		}
	}
	if proto == "" {
		proto = "http"
	}
	if host == "" {
		return "", errNoPublicBase
	}
	return proto + "://" + host, nil
}

// resolveReturnURL validates a client supplied return address against the
// public web base. Empty input selects defaultPath on the base.
func resolveReturnURL(base, supplied, defaultPath string) (string, error) {
	supplied = strings.TrimSpace(supplied)
	if supplied == "" {
		return base + defaultPath, nil
	}
	if strings.HasPrefix(supplied, "//") {
		return "", ErrReturnURLCrossOrigin
	}
	if strings.HasPrefix(supplied, "/") {
		if strings.HasPrefix(supplied, "/\\") {
			return "", ErrReturnURLInvalid
		}
		return base + supplied, nil
	}
	u, err := url.Parse(supplied)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil {
		return "", ErrReturnURLInvalid
	}
	b, err := url.Parse(base)
	if err != nil {
		return "", ErrReturnURLInvalid
	}
	if !strings.EqualFold(u.Scheme, b.Scheme) || !strings.EqualFold(u.Host, b.Host) {
		return "", ErrReturnURLCrossOrigin
	}
	return supplied, nil
}
