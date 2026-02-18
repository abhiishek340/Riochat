package trace

import (
	"io"
	"net/http"
	"strconv"
)

// Transport wraps base so every exchange is recorded as a client span and
// carries trace propagation headers. The query string is never recorded.
func Transport(base http.RoundTripper, t *Tracer) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if t == nil {
		return base
	}
	return &tracingTransport{base: base, t: t}
}

type tracingTransport struct {
	base http.RoundTripper
	t    *Tracer
}

func (tt *tracingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	attrs := []Attr{String("http.request.method", req.Method), String("server.address", req.URL.Hostname())}
	if p := req.URL.Port(); p != "" {
		attrs = append(attrs, String("server.port", p))
	}
	if !urlSuppressed(req.Context()) {
		attrs = append(attrs, String("url.full", req.URL.Scheme+"://"+req.URL.Host+req.URL.Path))
	}
	ctx, span := tt.t.Start(req.Context(), "HTTP "+req.Method, KindClient, attrs...)
	out := req.Clone(ctx)
	Inject(ctx, out.Header)
	resp, err := tt.base.RoundTrip(out)
	if err != nil {
		span.RecordError(err)
		span.End()
		return nil, err
	}
	span.SetAttr(String("http.response.status_code", strconv.Itoa(resp.StatusCode)))
	if resp.StatusCode >= 500 {
		span.RecordError(&statusError{resp.StatusCode})
	}
	// End the span when the body is closed so streaming exchanges are timed
	// end to end.
	resp.Body = &spanBody{ReadCloser: resp.Body, span: span}
	return resp, nil
}

type statusError struct{ code int }

func (e *statusError) Error() string { return "http status " + strconv.Itoa(e.code) }

type spanBody struct {
	io.ReadCloser
	span *Span
}

func (b *spanBody) Close() error {
	err := b.ReadCloser.Close()
	b.span.End()
	return err
}
