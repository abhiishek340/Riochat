package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/outbound"
)

type recorded struct {
	Method, Path, RawQuery string
	Header                 http.Header
	Body                   []byte
}

func (r recorded) JSON(t testing.TB) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		t.Fatalf("request body is not JSON: %v\n%s", err, r.Body)
	}
	return m
}

type fakeUpstream struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []recorded
}

func (f *fakeUpstream) all() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.reqs...)
}

func (f *fakeUpstream) last(t testing.TB) recorded {
	t.Helper()
	r := f.all()
	if len(r) == 0 {
		t.Fatal("no request reached the upstream")
	}
	return r[len(r)-1]
}

// newUpstream starts a server; h writes the response for each request.
func newUpstream(t testing.TB, h func(w http.ResponseWriter, r *http.Request, rec recorded)) *fakeUpstream {
	f := &fakeUpstream{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec := recorded{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Header: r.Header.Clone(), Body: body}
		f.mu.Lock()
		f.reqs = append(f.reqs, rec)
		f.mu.Unlock()
		h(w, r, rec)
	}))
	t.Cleanup(f.Close)
	return f
}

func jsonReply(body string) func(http.ResponseWriter, *http.Request, recorded) {
	return func(w http.ResponseWriter, r *http.Request, rec recorded) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}
}

func sseReply(frames ...string) func(http.ResponseWriter, *http.Request, recorded) {
	return func(w http.ResponseWriter, r *http.Request, rec recorded) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, f := range frames {
			io.WriteString(w, f)
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
		}
	}
}

// ev builds one SSE frame.
func ev(event, data string) string {
	if event == "" {
		return "data: " + data + "\n\n"
	}
	return "event: " + event + "\ndata: " + data + "\n\n"
}

func testClient(t testing.TB, opts ...func(*ClientOptions)) *Client {
	pool := outbound.NewPool(outbound.PoolOptions{Proxy: outbound.NoProxy})
	co := ClientOptions{HTTP: pool, Sleep: func(ctx context.Context, d time.Duration) error { return ctx.Err() }}
	for _, o := range opts {
		o(&co)
	}
	return NewClient(co)
}

func routeFor(proto Protocol, f *fakeUpstream, model string) Route {
	return Route{Protocol: string(proto), BaseURL: f.URL, APIKey: "sk-test", Model: model}
}

func userMsg(text string) []Message { return []Message{{Role: RoleUser, Content: text}} }

// at reads a dotted path ("a.0.b") from decoded JSON.
func at(v any, path string) any {
	for _, seg := range strings.Split(path, ".") {
		switch x := v.(type) {
		case map[string]any:
			v = x[seg]
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(x) {
				return nil
			}
			v = x[i]
		default:
			return nil
		}
	}
	return v
}

func mustEq(t testing.TB, got, want any, what string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

func collect() (Sink, *[]Event) {
	var evs []Event
	return func(e Event) error { evs = append(evs, e); return nil }, &evs
}

func eventsOf(evs []Event, t EventType) []Event {
	var out []Event
	for _, e := range evs {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}
