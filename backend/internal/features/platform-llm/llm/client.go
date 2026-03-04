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

// exec runs one HTTP exchange for an adapter, buffered or streaming.
func (c *Client) exec(ctx context.Context, x *exchange, ad adapter, sink Sink) (*Result, int, error) {
	cl, err := ad.build(x)
	if err != nil {
		return nil, 0, err
	}
	if sink == nil {
		st, body, hdr, dbg, err := c.doBuffered(ctx, x, cl, true)
		if err != nil {
			return nil, 0, err
		}
		res, perr := ad.parse(x, body)
		if perr != nil {
			if e, ok := AsError(perr); ok {
				e.Accepted = true
				e.Debug = dbg
			}
			return nil, 0, perr
		}
		_ = st
		_ = hdr
		res.Debug = dbg
		return res, 0, nil
	}
	return c.execStream(ctx, x, ad, cl, sink)
}

func (c *Client) execStream(ctx context.Context, x *exchange, ad adapter, cl *call, sink Sink) (*Result, int, error) {
	delivered := 0
	emit := func(ev Event) error {
		delivered++
		return sink(ev)
	}
	ss, err := c.openStream(ctx, x, cl, true)
	if err != nil {
		return nil, 0, err
	}
	defer ss.close()

	// An upstream that ignores "stream" and answers with plain JSON.
	if ct := strings.ToLower(ss.resp.Header.Get("Content-Type")); !strings.Contains(ct, "text/event-stream") && strings.Contains(ct, "json") {
		body, err := c.readLimited(ss.body)
		if err != nil {
			return nil, 0, c.streamReadErr(err, ss)
		}
		res, perr := ad.parse(x, body)
		if perr != nil {
			if e, ok := AsError(perr); ok {
				e.Accepted, e.Debug = true, ss.debug
			}
			return nil, 0, perr
		}
		if err := replayAsEvents(res, emit, x.proto.isImageProtocol()); err != nil {
			return nil, delivered, err
		}
		res.Debug = ss.debug
		return res, delivered, nil
	}

	st := ad.stream(x)
	rd := newSSEReader(ss.body)
	for {
		f, err := rd.next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return partial(st), delivered, c.streamReadErr(err, ss)
		}
		data := strings.TrimSpace(f.Data)
		if data == "[DONE]" {
			break
		}
		if data == "" && f.Event == "" {
			continue
		}
		if e := inStreamError(f); e != nil {
			e.Debug = ss.debug
			return partial(st), delivered, e
		}
		if err := st.frame(f, emit); err != nil {
			if e, ok := AsError(err); ok {
				e.Accepted = true
				if e.Debug == nil {
					e.Debug = ss.debug
				}
			}
			return partial(st), delivered, err
		}
	}
	if fl, ok := st.(flusher); ok {
		if err := fl.flush(emit); err != nil {
			if e, ok := AsError(err); ok {
				e.Accepted, e.Debug = true, ss.debug
			}
			return partial(st), delivered, err
		}
	}
	res, err := st.result()
	if err != nil {
		if e, ok := AsError(err); ok {
			e.Accepted = true
			e.Debug = ss.debug
		}
		return res, delivered, err
	}
	res.Debug = ss.debug
	return res, delivered, nil
}

func partial(st streamer) *Result {
	r, _ := st.result()
	return r
}

// replayAsEvents turns a buffered result into streaming events.
func replayAsEvents(r *Result, emit func(Event) error, alwaysUsage bool) error {
	if r.ID != "" {
		if err := emit(Event{Type: EventCreated, ResponseID: r.ID, ServiceTier: r.ServiceTier}); err != nil {
			return err
		}
	}
	if r.Reasoning != "" {
		if err := emit(Event{Type: EventReasoning, Text: r.Reasoning}); err != nil {
			return err
		}
	}
	if r.Text != "" {
		if err := emit(Event{Type: EventText, Text: r.Text}); err != nil {
			return err
		}
	}
	for i := range r.ToolCalls {
		tc := r.ToolCalls[i]
		if err := emit(Event{Type: EventToolCall, Index: i, ToolCall: &tc}); err != nil {
			return err
		}
	}
	for i := range r.Images {
		im := r.Images[i]
		if err := emit(Event{Type: EventImage, Index: im.Index, Image: &im}); err != nil {
			return err
		}
	}
	if alwaysUsage || !r.Usage.IsZero() {
		u := r.Usage
		return emit(Event{Type: EventUsage, Usage: &u})
	}
	return nil
}

func (c *Client) streamReadErr(err error, ss *streamState) error {
	if ss.idle != nil && ss.idle.fired.Load() {
		return &Error{Kind: KindTransport, Sentinel: ErrStreamIdle, Cause: err, Message: ErrStreamIdle.Error(), Accepted: true, Debug: ss.debug}
	}
	if errors.Is(err, ErrBodyTooLarge) {
		return &Error{Kind: KindTransport, Sentinel: ErrBodyTooLarge, Message: err.Error(), Accepted: true, Debug: ss.debug}
	}
	e := transportErr(err, true)
	e.Debug = ss.debug
	return e
}

// ---- model listing and background responses ------------------------------

// RetrieveResponse fetches an official OpenAI background response.
func (c *Client) RetrieveResponse(ctx context.Context, route Route, id string) (*Result, error) {
	return c.responseCall(ctx, route, http.MethodGet, id, "")
}

// CancelResponse cancels an official OpenAI background response.
func (c *Client) CancelResponse(ctx context.Context, route Route, id string) (*Result, error) {
	return c.responseCall(ctx, route, http.MethodPost, id, "/cancel")
}

func (c *Client) responseCall(ctx context.Context, route Route, method, id, suffix string) (*Result, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "/?# ") {
		return nil, invalid(ErrInvalidRequest, "invalid response id")
	}
	route.Protocol = string(OpenAIResponses)
	x, _, err := c.newExchange(route, &Request{}, false)
	if err != nil {
		return nil, err
	}
	u, err := buildURL(route.BaseURL, "v1", "/responses/"+id+suffix)
	if err != nil {
		return nil, err
	}
	cl := &call{method: method, url: u}
	if method == http.MethodPost {
		cl.body = []byte("{}")
	}
	_, body, _, dbg, err := c.doBuffered(ctx, x, cl, method == http.MethodPost)
	if err != nil {
		return nil, err
	}
	res, perr := responsesAdapter{}.parse(x, body)
	if perr != nil {
		return nil, perr
	}
	res.Debug = dbg
	return res, nil
}
