package outbound

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/trace"
)

// Defaults from the platform contract.
const (
	DefaultConnectTimeout = 10 * time.Second
	DefaultKeepAlive      = 30 * time.Second
	DefaultPoolCapacity   = 64
	MaxRedirects          = 10
)

// Resolver looks up host addresses; tests substitute their own.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Kind selects which policy an endpoint is held to.
type Kind int

// Endpoint kinds.
const (
	// KindTrusted is a deployment or administrator endpoint: the deployment
	// policy plus exact trust of the endpoint's own host.
	KindTrusted Kind = iota
	// KindUserSupplied is an endpoint typed by an end user: public-only, no
	// redirects.
	KindUserSupplied
	// KindStrict is external content or a fixed public endpoint: the strict
	// policy without allowlists.
	KindStrict
)

// RedirectMode controls redirect handling.
type RedirectMode int

// Redirect modes.
const (
	// RedirectDefault follows same-origin redirects and cross-origin ones that
	// pass the deployment policy.
	RedirectDefault RedirectMode = iota
	// RedirectSameOrigin refuses any cross-origin redirect (identity providers).
	RedirectSameOrigin
	// RedirectNone refuses every redirect.
	RedirectNone
)

// Endpoint describes how one integration is called.
type Endpoint struct {
	// URL is the configured endpoint. When set, every request must target the
	// same origin. Empty means "no configured endpoint" (strict base policy).
	URL            string
	Kind           Kind
	ConnectTimeout time.Duration
	Redirects      RedirectMode
	// Integration names the caller in redirect errors.
	Integration string
}

// PoolOptions configures a Pool.
type PoolOptions struct {
	// Trusted is the deployment policy (allowlists); Strict has none. Nil
	// values mean "not enforced".
	Trusted *Policy
	Strict  *Policy
	// Capacity bounds the number of cached clients (default 64).
	Capacity int
	// Resolver defaults to the system resolver.
	Resolver Resolver
	// Proxy decides the proxy per request; nil uses the environment (read once
	// per process by net/http). Use NoProxy to disable proxying.
	Proxy func(*http.Request) (*url.URL, error)
	// Tracer, when set, wraps every client's transport with request spans.
	Tracer *trace.Tracer
	// TLSInsecure is for tests only.
	Transport http.RoundTripper
}

// NoProxy never proxies.
func NoProxy(*http.Request) (*url.URL, error) { return nil, nil }

// Pool caches one HTTP client per (configured origin, variant).
type Pool struct {
	opts PoolOptions

	mu    sync.Mutex
	items map[string]*list.Element
	order *list.List // front = most recently used

	proxies sync.Map // canonical host:port of proxies we may dial
}

type poolEntry struct {
	key       string
	client    *http.Client
	transport *http.Transport
}

// NewPool returns an empty pool.
func NewPool(o PoolOptions) *Pool {
	if o.Capacity <= 0 {
		o.Capacity = DefaultPoolCapacity
	}
	if o.Resolver == nil {
		o.Resolver = net.DefaultResolver
	}
	if o.Proxy == nil {
		o.Proxy = http.ProxyFromEnvironment
	}
	if o.Trusted == nil {
		o.Trusted = &Policy{}
	}
	if o.Strict == nil {
		o.Strict = &Policy{}
	}
	return &Pool{opts: o, items: map[string]*list.Element{}, order: list.New()}
}

// Len reports the number of cached clients.
func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.order.Len()
}

// Has reports whether a client for the endpoint is cached.
func (p *Pool) Has(ep Endpoint) bool {
	key, err := p.key(ep)
	if err != nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.items[key]
	return ok
}

// CloseIdle closes idle connections of every cached client.
func (p *Pool) CloseIdle() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for e := p.order.Front(); e != nil; e = e.Next() {
		e.Value.(*poolEntry).transport.CloseIdleConnections()
	}
}

func (p *Pool) key(ep Endpoint) (string, error) {
	origin := "*"
	if ep.URL != "" {
		o, err := Origin(ep.URL)
		if err != nil {
			return "", err
		}
		origin = o
	}
	return fmt.Sprintf("%s|k%d|c%d|r%d|%s", origin, ep.Kind, ep.ConnectTimeout.Milliseconds(), ep.Redirects, ep.Integration), nil
}

func (p *Pool) policyFor(ep Endpoint) *Policy {
	switch ep.Kind {
	case KindUserSupplied:
		return PublicOnly()
	case KindStrict:
		return p.opts.Strict
	default:
		if ep.URL == "" {
			return p.opts.Trusted
		}
		return p.opts.Trusted.Trust(ep.URL)
	}
}

// Client returns the (possibly cached) client for an endpoint.
func (p *Pool) Client(ep Endpoint) (*http.Client, error) {
	key, err := p.key(ep)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if el, ok := p.items[key]; ok {
		p.order.MoveToFront(el)
		return el.Value.(*poolEntry).client, nil
	}
	e := p.build(key, ep)
	p.items[key] = p.order.PushFront(e)
	for p.order.Len() > p.opts.Capacity {
		last := p.order.Back()
		old := p.order.Remove(last).(*poolEntry)
		delete(p.items, old.key)
		old.transport.CloseIdleConnections()
	}
	return e.client, nil
}

func (p *Pool) build(key string, ep Endpoint) *poolEntry {
	policy := p.policyFor(ep)
	connect := ep.ConnectTimeout
	if connect <= 0 {
		connect = DefaultConnectTimeout
	}
	g := &guard{
		policy:   policy,
		resolver: p.opts.Resolver,
		dialer:   &net.Dialer{Timeout: connect, KeepAlive: DefaultKeepAlive},
		proxies:  &p.proxies,
	}
	tr := &http.Transport{
		Proxy:                 p.proxyFunc(policy),
		DialContext:           g.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   connect,
		ExpectContinueTimeout: time.Second,
	}
	var rt http.RoundTripper = tr
	if p.opts.Transport != nil {
		rt = p.opts.Transport
	}
	client := &http.Client{
		Transport:     trace.Transport(rt, p.opts.Tracer),
		CheckRedirect: p.redirectPolicy(ep),
	}
	return &poolEntry{key: key, client: client, transport: tr}
}

// proxyFunc validates destinations that will travel through a proxy, since the
// dialer only ever sees the proxy's address in that case.
func (p *Pool) proxyFunc(policy *Policy) func(*http.Request) (*url.URL, error) {
	return func(r *http.Request) (*url.URL, error) {
		pu, err := p.opts.Proxy(r)
		if err != nil || pu == nil {
			return pu, err
		}
		p.proxies.Store(canonicalAddr(pu), struct{}{})
		if !policy.Enforced() {
			return pu, nil
		}
		if err := policy.ValidateURL(r.URL.String()); err != nil {
			return nil, err
		}
		host := normalizeHost(r.URL.Hostname())
		if _, lit := parseLiteral(host); lit {
			return pu, nil
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		addrs, lerr := p.opts.Resolver.LookupNetIP(ctx, "ip", host)
		if lerr != nil {
			return pu, nil // only the proxy may be able to resolve it
		}
		if err := policy.CheckResolved(host, addrs); err != nil {
			return nil, err
		}
		return pu, nil
	}
}

func canonicalAddr(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		default:
			port = "80"
		}
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func (p *Pool) redirectPolicy(ep Endpoint) func(*http.Request, []*http.Request) error {
	name := ep.Integration
	if name == "" {
		name = "outbound request"
	}
	policy := p.policyFor(ep)
	// Cross-origin hops are judged without the endpoint-specific trust.
	var base *Policy
	switch ep.Kind {
	case KindUserSupplied:
		base = PublicOnly()
	case KindStrict:
		base = p.opts.Strict
	default:
		base = p.opts.Trusted
	}
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= MaxRedirects {
			return fmt.Errorf("%s: stopped after %d redirects", name, MaxRedirects)
		}
		if ep.Kind == KindUserSupplied {
			return fmt.Errorf("%w: redirects are not allowed for user-configured endpoints", ErrRedirectRefused)
		}
		if ep.Redirects == RedirectNone {
			return fmt.Errorf("%w: redirects are not allowed for %s", ErrRedirectRefused, name)
		}
		from := ep.URL
		if from == "" {
			from = via[0].URL.String()
		}
		if SameOrigin(from, req.URL.String()) {
			return policy.ValidateURL(req.URL.String())
		}
		if ep.Redirects == RedirectSameOrigin {
			return fmt.Errorf("%w: %s does not follow cross-origin redirects", ErrRedirectRefused, name)
		}
		if err := base.ValidateURL(req.URL.String()); err != nil {
			return err
		}
		for _, h := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key", "Api-Key", "Cookie", "Proxy-Authorization"} {
			req.Header.Del(h)
		}
		return nil
	}
}

// Do executes req with the client for ep. When ep.URL is set the request's
// origin must equal it.
func (p *Pool) Do(ctx context.Context, ep Endpoint, req *http.Request) (*http.Response, error) {
	if ep.URL != "" {
		want, err := Origin(ep.URL)
		if err != nil {
			return nil, err
		}
		got, err := Origin(req.URL.String())
		if err != nil {
			return nil, err
		}
		if want != got {
			return nil, fmt.Errorf("%w (target %s, configured %s)", ErrOriginMismatch, got, want)
		}
	}
	if err := p.policyFor(ep).ValidateURL(req.URL.String()); err != nil {
		return nil, err
	}
	c, err := p.Client(ep)
	if err != nil {
		return nil, err
	}
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	return c.Do(req)
}

// ---- dialer ---------------------------------------------------------------

type guard struct {
	policy   *Policy
	resolver Resolver
	dialer   *net.Dialer
	proxies  *sync.Map
}

func (g *guard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if _, ok := g.proxies.Load(strings.ToLower(addr)); ok || !g.policy.Enforced() {
		return g.dialer.DialContext(ctx, network, addr)
	}
	host, port := hostPort(addr)
	host = normalizeHost(host)
	if a, ok := parseLiteral(host); ok {
		if err := g.policy.checkIP(a, true); err != nil {
			return nil, err
		}
		return g.dialer.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
	}
	if host == MetadataHost {
		return nil, blocked("metadata host")
	}
	if isLocalName(host) && !g.policy.hostAllowed(host) {
		return nil, blocked("host %q is local", host)
	}
	lookup := "ip"
	switch network {
	case "tcp4":
		lookup = "ip4"
	case "tcp6":
		lookup = "ip6"
	}
	addrs, err := g.resolver.LookupNetIP(ctx, lookup, host)
	if err != nil {
		return nil, err
	}
	var usable []netip.Addr
	for _, a := range addrs {
		a = a.WithZone("")
		if lookup == "ip4" && !a.Unmap().Is4() || lookup == "ip6" && a.Unmap().Is4() {
			continue
		}
		usable = append(usable, a)
	}
	if err := g.policy.CheckResolved(host, usable); err != nil {
		return nil, err
	}
	var last error
	for _, a := range usable {
		conn, err := g.dialer.DialContext(ctx, network, net.JoinHostPort(a.Unmap().String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
		if ctx.Err() != nil {
			break
		}
	}
	if last == nil {
		last = errors.New("no usable address")
	}
	return nil, last
}

// ---- proxy reporting ------------------------------------------------------

// ProxyReport describes the process-level proxy state for the startup log.
type ProxyReport struct {
	Event  string // outbound_proxy_enabled | outbound_proxy_disabled | outbound_proxy_ignored
	Warn   bool
	Fields map[string]string
}

// ReportProxy inspects the proxy environment. getenv is injectable.
func ReportProxy(getenv func(string) string, ssrf bool) ProxyReport {
	pick := func(names ...string) string {
		for _, n := range names {
			if v := strings.TrimSpace(getenv(n)); v != "" {
				return v
			}
		}
		return ""
	}
	hp := pick("HTTP_PROXY", "http_proxy")
	sp := pick("HTTPS_PROXY", "https_proxy")
	np := pick("NO_PROXY", "no_proxy")
	all := pick("ALL_PROXY", "all_proxy")
	ssrfText := fmt.Sprint(ssrf)
	switch {
	case hp != "" || sp != "":
		return ProxyReport{Event: "outbound_proxy_enabled", Fields: map[string]string{
			"http_proxy": redactProxy(hp), "https_proxy": redactProxy(sp), "no_proxy": np, "ssrf_protection": ssrfText}}
	case all != "":
		return ProxyReport{Event: "outbound_proxy_ignored", Warn: true, Fields: map[string]string{
			"reason": "ALL_PROXY is not honored; set HTTP_PROXY and HTTPS_PROXY", "ssrf_protection": ssrfText}}
	}
	return ProxyReport{Event: "outbound_proxy_disabled", Fields: map[string]string{"ssrf_protection": ssrfText}}
}

// redactProxy keeps scheme://host:port and drops credentials and paths.
func redactProxy(raw string) string {
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "invalid"
	}
	return u.Scheme + "://" + u.Host
}
