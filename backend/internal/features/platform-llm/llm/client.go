package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/outbound"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/trace"
)

// Default timeouts.
const (
	DefaultConnectTimeout    = 10 * time.Second
	DefaultReadTimeout       = 120 * time.Second
	DefaultStreamIdleTimeout = 60 * time.Second
	// DefaultMaxBody bounds buffered and error response bodies.
	DefaultMaxBody = 64 << 20
)

// Doer executes an HTTP request for a configured endpoint; *outbound.Pool
// implements it.
type Doer interface {
	Do(ctx context.Context, ep outbound.Endpoint, req *http.Request) (*http.Response, error)
}

// ClientOptions configures a Client.
type ClientOptions struct {
	HTTP   Doer
	Tracer *trace.Tracer
	// MaxBodyBytes overrides DefaultMaxBody (tests).
	MaxBodyBytes int64
	// Sleep waits between polls; it must honor ctx. Defaults to a timer.
	Sleep func(ctx context.Context, d time.Duration) error
	// NewID generates upstream request ids; defaults to a random UUID.
	NewID func() string
}

// Client performs provider requests.
type Client struct {
	http    Doer
	tracer  *trace.Tracer
	maxBody int64
	sleep   func(ctx context.Context, d time.Duration) error
	newID   func() string
}

// NewClient returns a Client. HTTP is required.
func NewClient(o ClientOptions) *Client {
	c := &Client{http: o.HTTP, tracer: o.Tracer, maxBody: o.MaxBodyBytes, sleep: o.Sleep, newID: o.NewID}
	if c.maxBody <= 0 {
		c.maxBody = DefaultMaxBody
	}
	if c.sleep == nil {
		c.sleep = sleepCtx
	}
	if c.newID == nil {
		c.newID = uuid.NewString
	}
	return c
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func msOr(ms int, def time.Duration) time.Duration {
	if ms <= 0 {
		return def
	}
	return time.Duration(ms) * time.Millisecond
}

// exchange is the per-call context shared by adapters.
type exchange struct {
	c      *Client
	route  Route
	proto  Protocol
	req    *Request
	opts   options
	stream bool
	model  string
	ep     outbound.Endpoint
	vars   map[string]string

	// includeUsage drives chat stream_options.include_usage.
	includeUsage bool
}

func (x *exchange) readTimeout() time.Duration {
	return msOr(x.route.ReadTimeoutMS, DefaultReadTimeout)
}
func (x *exchange) idleTimeout() time.Duration {
	return msOr(x.route.StreamIdleTimeoutMS, DefaultStreamIdleTimeout)
}

func (c *Client) newExchange(route Route, req *Request, stream bool) (*exchange, adapter, error) {
	if c == nil || c.http == nil {
		return nil, nil, invalid(ErrInvalidRequest, "llm client has no HTTP transport")
	}
	proto, err := NormalizeProtocol(route.Protocol)
	if err != nil {
		return nil, nil, err
	}
	if req == nil {
		req = &Request{}
	}
	if stream {
		ok := proto.SupportsStream()
		if proto == OpenAIImageGenerations || proto == OpenAIImageEdits {
			ok = proto.SupportsImageStream(route.Model)
		}
		if !ok {
			return nil, nil, &Error{Kind: KindUnsupported, Sentinel: ErrUnsupportedStream,
				Message: fmt.Sprintf("streaming is not supported by protocol %s", proto)}
		}
	}
	ad := adapterFor(proto)
	if ad == nil {
		return nil, nil, &Error{Kind: KindUnsupported, Sentinel: ErrUnsupportedProtocol, Message: fmt.Sprintf("unsupported protocol %q", proto)}
	}
	x := &exchange{c: c, route: route, proto: proto, req: req, opts: options(req.Options), stream: stream,
		model: strings.TrimSpace(route.Model), vars: headerVars(req), includeUsage: true}
	if x.opts == nil {
		x.opts = options{}
	}
	base := strings.TrimSpace(route.BaseURL)
	if proto.isGoogle() {
		base = googleBase(base)
	}
	kind := outbound.KindTrusted
	redirects := outbound.RedirectDefault
	if route.Untrusted {
		kind, redirects = outbound.KindUserSupplied, outbound.RedirectNone
	}
	x.ep = outbound.Endpoint{URL: base, Kind: kind, Redirects: redirects, Integration: "model endpoint",
		ConnectTimeout: msOr(route.ConnectTimeoutMS, DefaultConnectTimeout)}
	return x, ad, nil
}

// Generate performs a buffered generation.
func (c *Client) Generate(ctx context.Context, route Route, req *Request) (*Result, error) {
	return c.run(ctx, route, req, nil)
}

// Stream performs a streaming generation, delivering increments to sink. The
// final aggregated Result is returned; on a mid-stream failure a partial
// Result may accompany the error.
func (c *Client) Stream(ctx context.Context, route Route, req *Request, sink Sink) (*Result, error) {
	if sink == nil {
		return nil, invalid(ErrInvalidRequest, "stream requires an event sink")
	}
	return c.run(ctx, route, req, sink)
}

func (c *Client) run(ctx context.Context, route Route, req *Request, sink Sink) (*Result, error) {
	x, ad, err := c.newExchange(route, req, sink != nil)
	if err != nil {
		return nil, err
	}
	ctx, span := c.tracer.Start(ctx, "llm.generate", trace.KindInternal,
		trace.String("llm.protocol", string(x.proto)), trace.String("llm.model", x.model), trace.Bool("llm.stream", sink != nil))
	defer span.End()

	var res *Result
	if mv, ok := ad.(multiStep); ok {
		res, err = mv.generate(ctx, x, sink)
	} else {
		var delivered int
		res, delivered, err = c.exec(ctx, x, ad, sink)
		if err != nil && sink != nil && x.proto.isChat() && x.includeUsage &&
			!userSetIncludeUsage(x.opts) && delivered == 0 && rejectsStreamOptions(err) {
			x.includeUsage = false
			res, _, err = c.exec(ctx, x, ad, sink)
		}
	}
	span.RecordError(err)
	return res, err
}

func userSetIncludeUsage(o options) bool {
	so, ok := o["stream_options"].(map[string]any)
	if !ok {
		return false
	}
	_, set := so["include_usage"]
	return set
}

func rejectsStreamOptions(err error) bool {
	e, ok := AsError(err)
	if !ok || e.Kind != KindUpstream || (e.Status != http.StatusBadRequest && e.Status != http.StatusUnprocessableEntity) {
		return false
	}
	t := strings.ToLower(e.Message + " " + e.Body)
	return strings.Contains(t, "stream_options") || strings.Contains(t, "include_usage")
}
