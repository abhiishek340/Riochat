package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// call is a fully described outbound request before headers are applied.
type call struct {
	method      string
	url         string
	header      http.Header // protocol-specific headers
	body        []byte
	contentType string // defaults to application/json when a body exists
}

// adapter converts between the neutral model and one protocol family.
type adapter interface {
	build(x *exchange) (*call, error)
	parse(x *exchange, body []byte) (*Result, error)
	stream(x *exchange) streamer
}

// flusher is implemented by streamers that withhold text until the stream ends.
type flusher interface {
	flush(emit func(Event) error) error
}

// multiStep adapters run their own exchange sequence (submit, poll, fetch).
type multiStep interface {
	generate(ctx context.Context, x *exchange, sink Sink) (*Result, error)
}

// streamer folds server-sent events into a Result.
type streamer interface {
	frame(f sseFrame, emit func(Event) error) error
	result() (*Result, error)
}

func adapterFor(p Protocol) adapter {
	switch p {
	case OpenAIResponses, OpenRouterResponses, XAIResponses:
		return responsesAdapter{}
	case OpenAIChatCompletions, OpenRouterChat:
		return chatAdapter{}
	case AnthropicMessages:
		return anthropicAdapter{}
	case GoogleGenerateContent, GoogleImageGeneration:
		return googleAdapter{}
	case GeminiInteractions:
		return interactionsAdapter{}
	case OpenAIImageGenerations, OpenAIImageEdits, OpenRouterImages, XAIImage, XAIImageEdits:
		return imageAdapter{}
	case XAIVideo, XAIVideoExtensions:
		return videoAdapter{}
	}
	return nil
}

// apiURL builds an endpoint URL from the route's base.
func (x *exchange) apiURL(defaultVersion, endpoint string) (string, error) {
	return buildURL(x.route.BaseURL, defaultVersion, endpoint)
}

// applyHeaders produces the final request headers: standard, then
// attribution, then custom (which override).
func (x *exchange) applyHeaders(h http.Header, cl *call, stream bool) {
	for k, v := range cl.header {
		h[k] = append([]string(nil), v...)
	}
	if len(cl.body) > 0 {
		ct := cl.contentType
		if ct == "" {
			ct = "application/json"
		}
		h.Set("Content-Type", ct)
	}
	if stream {
		h.Set("Accept", "text/event-stream")
	} else if h.Get("Accept") == "" {
		h.Set("Accept", "application/json")
	}
	key := strings.TrimSpace(x.route.APIKey)
	if key != "" {
		switch {
		case x.proto == AnthropicMessages:
			h.Set("x-api-key", key)
		case x.proto.isGoogle() || x.proto == GeminiInteractions:
			h.Set("x-goog-api-key", key)
		default:
			h.Set("Authorization", "Bearer "+key)
		}
	}
	if x.proto == AnthropicMessages && h.Get("anthropic-version") == "" {
		h.Set("anthropic-version", "2023-06-01")
	}
	custom := customHeaders(x.route.HeadersJSON, x.vars)
	supplied := map[string]bool{}
	for _, p := range custom {
		supplied[strings.ToLower(p.Name)] = true
	}
	if u, err := url.Parse(cl.url); err == nil && isOpenRouterHost(u.Hostname()) {
		ref := strings.TrimRight(strings.TrimSpace(x.route.OpenRouterReferer), "/")
		if ref == "" {
			ref = "https://github.com/riochat/riochat"
		}
		title := strings.TrimSpace(x.route.OpenRouterTitle)
		if title == "" {
			title = "Riochat"
		}
		for _, kv := range [][2]string{
			{"HTTP-Referer", ref}, {"X-Title", title}, {"X-OpenRouter-Title", title}, {"X-OpenRouter-Categories", "general-chat"},
		} {
			if !supplied[strings.ToLower(kv[0])] {
				h.Set(kv[0], kv[1])
			}
		}
	}
	for _, p := range custom {
		ln := strings.ToLower(p.Name)
		if ln == "host" || ln == "content-length" {
			continue
		}
		if ln == "anthropic-beta" {
			h.Set("anthropic-beta", unionCSV(h.Get("anthropic-beta"), p.Value))
			continue
		}
		h.Set(p.Name, p.Value)
	}
}

func unionCSV(a, b string) string {
	seen := map[string]bool{}
	var out []string
	for _, s := range []string{a, b} {
		for _, p := range strings.Split(s, ",") {
			p = strings.TrimSpace(p)
			if p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return strings.Join(out, ",")
}

// sendState carries per-request flags out of the HTTP layer.
type sendState struct {
	wrote atomic.Bool
}

func (c *Client) newHTTPRequest(ctx context.Context, x *exchange, cl *call, stream bool, st *sendState) (*http.Request, error) {
	var body io.Reader
	if len(cl.body) > 0 {
		body = bytes.NewReader(cl.body)
	}
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteRequest: func(i httptrace.WroteRequestInfo) {
			if i.Err == nil {
				st.wrote.Store(true)
			}
		},
	})
	req, err := http.NewRequestWithContext(ctx, cl.method, cl.url, body)
	if err != nil {
		return nil, invalid(ErrInvalidBaseURL, "invalid request url")
	}
	x.applyHeaders(req.Header, cl, stream)
	return req, nil
}

func (cl *call) pathWithQuery() string {
	u, err := url.Parse(cl.url)
	if err != nil {
		return ""
	}
	p := u.EscapedPath()
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return p
}

func (x *exchange) debugFor(cl *call, req *http.Request) *Debug {
	d := &Debug{Method: cl.method, Path: cl.pathWithQuery(), RequestHeaders: debugHeaders(req.Header)}
	if len(cl.body) > 0 {
		s := SanitizeBody(cl.body)
		d.RequestBody, d.RequestBodyOriginalBytes, d.RequestBodyTruncated = s.Text, s.OriginalBytes, s.Truncated
		d.RedactedParts += s.Redacted
	}
	return d
}

func (d *Debug) setResponse(status int, hdr http.Header, body []byte) {
	d.Status = status
	d.ResponseHeaders = debugHeaders(hdr)
	if body != nil {
		s := SanitizeBody(body)
		d.ResponseBody, d.ResponseBodyOriginalBytes, d.ResponseBodyTruncated = s.Text, s.OriginalBytes, s.Truncated
		d.RedactedParts += s.Redacted
	}
}

// readLimited reads a body up to the client's cap.
func (c *Client) readLimited(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, c.maxBody+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > c.maxBody {
		return nil, fmt.Errorf("%w: upstream response body exceeds %d bytes", ErrBodyTooLarge, c.maxBody)
	}
	return b, nil
}

// bodyTooLargeMessage keeps the contractual text without the sentinel prefix.
func bodyTooLarge(c *Client) *Error {
	return &Error{Kind: KindTransport, Sentinel: ErrBodyTooLarge, Accepted: true,
		Message: fmt.Sprintf("upstream response body exceeds %d bytes", c.maxBody)}
}

func (c *Client) classifyTransport(err error, st *sendState, firstByteFired bool) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		err = ue.Err // drop the URL from the message
	}
	if firstByteFired {
		return &Error{Kind: KindTransport, Sentinel: ErrFirstByteTimeout, Cause: err, Message: ErrFirstByteTimeout.Error(), Accepted: st.wrote.Load()}
	}
	return transportErr(err, st.wrote.Load())
}

// doBuffered runs a request to completion. generation marks POSTs whose
// transport failures after the write must be flagged as accepted.
func (c *Client) doBuffered(ctx context.Context, x *exchange, cl *call, generation bool) (int, []byte, http.Header, *Debug, error) {
	ctx, cancel := context.WithTimeout(ctx, x.readTimeout())
	defer cancel()
	st := &sendState{}
	req, err := c.newHTTPRequest(ctx, x, cl, false, st)
	if err != nil {
		return 0, nil, nil, nil, err
	}
	dbg := x.debugFor(cl, req)
	resp, err := c.http.Do(req.Context(), x.ep, req)
	if err != nil {
		te := c.classifyTransport(err, st, false)
		te.Accepted = te.Accepted && generation
		te.Debug = dbg
		return 0, nil, nil, dbg, te
	}
	defer resp.Body.Close()
	body, rerr := c.readLimited(resp.Body)
	if rerr != nil {
		var te *Error
		if errors.Is(rerr, ErrBodyTooLarge) {
			te = bodyTooLarge(c)
		} else {
			te = transportErr(rerr, true)
		}
		te.Accepted = generation && resp.StatusCode/100 == 2
		te.Debug = dbg
		dbg.setResponse(resp.StatusCode, resp.Header, nil)
		return resp.StatusCode, nil, resp.Header, dbg, te
	}
	dbg.setResponse(resp.StatusCode, resp.Header, body)
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, body, resp.Header, dbg, upstreamError(x.proto, resp.StatusCode, body, dbg)
	}
	return resp.StatusCode, body, resp.Header, dbg, nil
}

// streamState owns a live streaming response.
type streamState struct {
	resp   *http.Response
	body   io.Reader
	debug  *Debug
	idle   *idleBody
	cancel context.CancelFunc
	timer  *time.Timer
}

func (s *streamState) close() {
	if s.timer != nil {
		s.timer.Stop()
	}
	if s.idle != nil {
		s.idle.stop()
	}
	s.resp.Body.Close()
	s.cancel()
}

// openStream sends a streaming request and returns once headers arrive. The
// read timeout applies to the first byte only; afterwards the idle timeout
// bounds the gap between chunks.
func (c *Client) openStream(ctx context.Context, x *exchange, cl *call, generation bool) (*streamState, error) {
	cctx, cancel := context.WithCancel(ctx)
	var fired atomic.Bool
	timer := time.AfterFunc(x.readTimeout(), func() {
		fired.Store(true)
		cancel()
	})
	st := &sendState{}
	req, err := c.newHTTPRequest(cctx, x, cl, true, st)
	if err != nil {
		timer.Stop()
		cancel()
		return nil, err
	}
	dbg := x.debugFor(cl, req)
	resp, err := c.http.Do(req.Context(), x.ep, req)
	timer.Stop()
	if err != nil {
		cancel()
		te := c.classifyTransport(err, st, fired.Load())
		te.Accepted = te.Accepted && generation
		te.Debug = dbg
		return nil, te
	}
	if resp.StatusCode/100 != 2 {
		defer cancel()
		defer resp.Body.Close()
		body, rerr := c.readLimited(resp.Body)
		if rerr != nil {
			dbg.setResponse(resp.StatusCode, resp.Header, nil)
			te := transportErr(rerr, true)
			te.Debug = dbg
			return nil, te
		}
		dbg.setResponse(resp.StatusCode, resp.Header, body)
		return nil, upstreamError(x.proto, resp.StatusCode, body, dbg)
	}
	dbg.setResponse(resp.StatusCode, resp.Header, nil)
	ss := &streamState{resp: resp, debug: dbg, cancel: cancel}
	ss.idle = newIdleBody(resp.Body, x.idleTimeout(), cancel)
	ss.body = ss.idle
	return ss, nil
}

// idleBody enforces the maximum gap between chunks.
type idleBody struct {
	rc    io.Reader
	d     time.Duration
	timer *time.Timer
	fired atomic.Bool
}

func newIdleBody(rc io.Reader, d time.Duration, cancel context.CancelFunc) *idleBody {
	b := &idleBody{rc: rc, d: d}
	b.timer = time.AfterFunc(d, func() {
		b.fired.Store(true)
		cancel()
	})
	return b
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.timer.Reset(b.d)
	}
	if err != nil && b.fired.Load() {
		err = ErrStreamIdle
	}
	return n, err
}

func (b *idleBody) stop() { b.timer.Stop() }

// ---- errors from bodies ---------------------------------------------------

// upstreamError converts a non-2xx answer into an Error.
func upstreamError(p Protocol, status int, body []byte, dbg *Debug) *Error {
	msg, code := errorMessage(body)
	if msg == "" {
		if (p.isGoogle() || p == GeminiInteractions) && status == http.StatusUnauthorized {
			msg = "google authentication failed; check API key, upstream base URL, and custom auth headers"
		} else {
			msg = fmt.Sprintf("upstream_status_%d", status)
		}
	}
	raw := string(body)
	if len(raw) > 64<<10 {
		raw = raw[:64<<10]
	}
	return &Error{Kind: KindUpstream, Status: status, Code: code, Message: msg, Body: raw, Debug: dbg}
}

func errorMessage(body []byte) (msg, code string) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return "", ""
	}
	return errorFromMap(m)
}

func errorFromMap(m map[string]any) (msg, code string) {
	switch e := m["error"].(type) {
	case map[string]any:
		if s, ok := e["message"].(string); ok && strings.TrimSpace(s) != "" {
			msg = strings.TrimSpace(s)
		}
		code = stringOf(e["code"])
		if code == "" {
			code = stringOf(e["type"])
		}
		if code == "" {
			code = stringOf(e["status"])
		}
	case string:
		msg = strings.TrimSpace(e)
	}
	if msg == "" {
		if s, ok := m["message"].(string); ok {
			msg = strings.TrimSpace(s)
		}
	}
	return msg, code
}

func stringOf(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case json.Number:
		return x.String()
	}
	return ""
}

// inStreamError recognises error events inside a stream: an "error" object, or
// response.error / response.failed. It returns nil for ordinary frames.
func inStreamError(f sseFrame) *Error {
	data := strings.TrimSpace(f.Data)
	if data == "" || data[0] != '{' {
		if f.Event == "error" && data != "" {
			return &Error{Kind: KindUpstream, Status: 502, Message: data, Accepted: true}
		}
		return nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(data), &m) != nil {
		return nil
	}
	typ, _ := m["type"].(string)
	if typ == "" {
		typ = f.Event
	}
	var holder map[string]any
	switch {
	case typ == "response.failed":
		if r, ok := m["response"].(map[string]any); ok {
			if _, has := r["error"].(map[string]any); has {
				holder = r
			}
		}
		if holder == nil {
			holder = m
		}
	case typ == "response.error" || typ == "error":
		holder = m
	default:
		if e, ok := m["error"].(map[string]any); ok && e != nil {
			holder = m
		}
	}
	if holder == nil {
		return nil
	}
	e, _ := holder["error"].(map[string]any)
	if e == nil {
		e = holder
	}
	status := 502
	for _, src := range []map[string]any{e, holder, m} {
		for _, k := range []string{"status", "status_code", "code"} {
			if v, ok := src[k]; ok {
				if n, ok := toInt(v); ok && n >= 100 && n <= 599 {
					status = int(n)
					goto found
				}
				if s, ok := v.(string); ok {
					if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n >= 100 && n <= 599 {
						status = n
						goto found
					}
				}
			}
		}
	}
found:
	msg := ""
	for _, k := range []string{"message", "msg"} {
		if s, ok := e[k].(string); ok && strings.TrimSpace(s) != "" {
			msg = strings.TrimSpace(s)
			break
		}
	}
	if msg == "" {
		if s, ok := m["message"].(string); ok {
			msg = strings.TrimSpace(s)
		}
	}
	if msg == "" {
		if s, ok := e["type"].(string); ok && s != "" {
			msg = s
		}
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	code := stringOf(e["code"])
	if code == "" {
		code = stringOf(e["type"])
	}
	return &Error{Kind: KindUpstream, Status: status, Code: code, Message: msg, Body: data, Accepted: true}
}
