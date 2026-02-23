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
