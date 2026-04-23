package mediadl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/outbound"
)

func newDL(t *testing.T, trusted *outbound.Policy, sleeps *[]time.Duration) *Downloader {
	pool := outbound.NewPool(outbound.PoolOptions{Trusted: trusted, Strict: trusted, Proxy: outbound.NoProxy})
	d := &Downloader{HTTP: pool}
	if sleeps != nil {
		d.Sleep = func(ctx context.Context, dur time.Duration) error { *sleeps = append(*sleeps, dur); return ctx.Err() }
	}
	return d
}

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func TestDownloadBasicsAndMIME(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", r.URL.Query().Get("ct"))
		io.WriteString(w, "PNGDATA")
	})
	d := newDL(t, nil, nil)
	for _, tc := range []struct {
		ct   string
		kind Kind
		want string
	}{
		{"image/png", Image, "image/png"}, {"IMAGE/JPEG; charset=x", Image, "image/jpeg"},
		{"video/mp4", Image, ""}, {"application/octet-stream", Image, ""},
		{"video/mp4", Video, "video/mp4"}, {"image/png", Video, ""}, {"", Image, ""},
	} {
		m, err := d.Download(context.Background(), Request{URL: s.URL + "/a?ct=" + url.QueryEscape(tc.ct), Kind: tc.kind, MaxBytes: 100})
		if err != nil || m.MIME != tc.want || string(m.Data) != "PNGDATA" {
			t.Errorf("%q: %v %+v", tc.ct, err, m)
		}
	}
}

func TestDownloadSizeCapAndPositiveCap(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, strings.Repeat("x", 11)) })
	d := newDL(t, nil, nil)
	if _, err := d.Download(context.Background(), Request{URL: s.URL, MaxBytes: 10}); !errors.Is(err, ErrTooLarge) || !strings.Contains(err.Error(), "10") {
		t.Fatalf("%v", err)
	}
	if m, err := d.Download(context.Background(), Request{URL: s.URL, MaxBytes: 11}); err != nil || len(m.Data) != 11 {
		t.Fatalf("exactly at the cap is fine: %v", err)
	}
	for _, mb := range []int64{0, -1} {
		if _, err := d.Download(context.Background(), Request{URL: s.URL, MaxBytes: mb}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("cap %d: %v", mb, err)
		}
	}
}

func TestDownloadErrorsNeverLeakURLs(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		io.WriteString(w, "secret-body-text")
	})
	d := newDL(t, nil, nil)
	signed := s.URL + "/img?X-Amz-Signature=SIGSECRET"
	_, err := d.Download(context.Background(), Request{URL: signed, MaxBytes: 10})
	if err == nil || !errors.Is(err, ErrHTTPStatus) || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "SIGSECRET") || strings.Contains(err.Error(), "secret-body") {
		t.Fatalf("%v", err)
	}
	s.Close()
	_, err = d.Download(context.Background(), Request{URL: signed, MaxBytes: 10})
	if err == nil || strings.Contains(err.Error(), "SIGSECRET") || strings.Contains(err.Error(), s.URL) {
		t.Fatalf("transport errors drop the URL: %v", err)
	}
}

func TestKeyOnlyGoesToTheModelEndpointOrigin(t *testing.T) {
	var gotAuth atomic.Value
	other := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization") + "|" + r.Header.Get("X-Goog-Api-Key"))
		io.WriteString(w, "x")
	})
	var sameAuth atomic.Value
	model := serve(t, func(w http.ResponseWriter, r *http.Request) {
		sameAuth.Store(r.Header.Get("Authorization") + "|" + r.Header.Get("X-Goog-Api-Key"))
		io.WriteString(w, "x")
	})
	// Both servers are loopback; enforce the policy so only the model origin is trusted.
	policy, _ := outbound.NewPolicy(true, nil, nil)
	d := newDL(t, policy, nil)
	if _, err := d.Download(context.Background(), Request{URL: model.URL + "/a", MaxBytes: 5, EndpointURL: model.URL, APIKey: "KEY"}); err != nil {
		t.Fatalf("same origin inherits trust: %v", err)
	}
	if sameAuth.Load() != "Bearer KEY|" {
		t.Fatalf("same-origin artifacts carry the key: %v", sameAuth.Load())
	}
	if _, err := d.Download(context.Background(), Request{URL: model.URL + "/a", MaxBytes: 5, EndpointURL: model.URL, APIKey: "KEY", GoogleKey: true}); err != nil {
		t.Fatal(err)
	}
	if sameAuth.Load() != "|KEY" {
		t.Fatalf("google style: %v", sameAuth.Load())
	}
	_, err := d.Download(context.Background(), Request{URL: other.URL + "/a", MaxBytes: 5, EndpointURL: model.URL, APIKey: "KEY"})
	if !errors.Is(err, outbound.ErrBlocked) {
		t.Fatalf("a different origin gets the strict policy (loopback blocked): %v", err)
	}
	if gotAuth.Load() != nil {
		t.Fatal("the key must never reach another origin")
	}
	// With enforcement off the request goes through, still without the key.
	d2 := newDL(t, nil, nil)
	if _, err := d2.Download(context.Background(), Request{URL: other.URL + "/a", MaxBytes: 5, EndpointURL: model.URL, APIKey: "KEY"}); err != nil {
		t.Fatal(err)
	}
	if gotAuth.Load() != "|" {
		t.Fatalf("cross-origin request carried credentials: %v", gotAuth.Load())
	}
	// User-supplied endpoints are public-only even for their own origin.
	if _, err := d.Download(context.Background(), Request{URL: model.URL + "/a", MaxBytes: 5, EndpointURL: model.URL, APIKey: "KEY", Untrusted: true}); !errors.Is(err, outbound.ErrBlocked) {
		t.Fatalf("%v", err)
	}
}

func TestRedirectsFromTrustedOriginMustStayOnHost(t *testing.T) {
	target := serve(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "t") })
	var model *httptest.Server
	model = serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hop":
			http.Redirect(w, r, "/final", 302)
		case "/final":
			io.WriteString(w, "ok")
		case "/away":
			http.Redirect(w, r, target.URL+"/", 302)
		}
	})
	d := newDL(t, nil, nil)
	m, err := d.Download(context.Background(), Request{URL: model.URL + "/hop", MaxBytes: 5, EndpointURL: model.URL, APIKey: "K"})
	if err != nil || string(m.Data) != "ok" {
		t.Fatalf("%v", err)
	}
	_, err = d.Download(context.Background(), Request{URL: model.URL + "/away", MaxBytes: 5, EndpointURL: model.URL, APIKey: "K"})
	if !errors.Is(err, outbound.ErrRedirectRefused) {
		t.Fatalf("leaving the trusted origin is refused: %v", err)
	}
}

func geminiServer(t *testing.T, states []string, polls *int32) (*httptest.Server, func(string) string) {
	var s *httptest.Server
	s = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Goog-Api-Key") != "GK" || r.Header.Get("Authorization") != "" {
			w.WriteHeader(401)
			return
		}
		if strings.HasSuffix(r.URL.Path, ":download") {
			if r.URL.Query().Get("alt") != "media" {
				w.WriteHeader(400)
				return
			}
			io.WriteString(w, "VIDEOBYTES")
			return
		}
		n := int(atomic.AddInt32(polls, 1)) - 1
		if n >= len(states) {
			n = len(states) - 1
		}
		fmt.Fprintf(w, `{"state":%q,"mimeType":"video/mp4"}`, states[n])
	}))
	t.Cleanup(s.Close)
	return s, func(p string) string { return s.URL + p }
}

// geminiDoer rewrites the Gemini host to the local test server.
type geminiDoer struct {
	target string
	inner  Doer
}

func (g geminiDoer) Do(ctx context.Context, ep outbound.Endpoint, req *http.Request) (*http.Response, error) {
	if req.URL.Hostname() == "generativelanguage.googleapis.com" {
		u := *req.URL
		u.Scheme, u.Host = "http", strings.TrimPrefix(g.target, "http://")
		req.URL = &u
		req.Host = u.Host
	}
	return g.inner.Do(ctx, ep, req)
}

func TestGeminiFileFlow(t *testing.T) {
	var polls int32
	srv, _ := geminiServer(t, []string{"PROCESSING", "PROCESSING", "ACTIVE"}, &polls)
	var sleeps []time.Duration
	d := newDL(t, nil, &sleeps)
	d.HTTP = geminiDoer{target: srv.URL, inner: d.HTTP}
	m, err := d.Download(context.Background(), Request{URL: "https://generativelanguage.googleapis.com/v1beta/files/abc123", Kind: Video, MaxBytes: 100, APIKey: "GK"})
	if err != nil || string(m.Data) != "VIDEOBYTES" || m.MIME != "video/mp4" {
		t.Fatalf("%v %+v", err, m)
	}
	if polls != 3 || len(sleeps) != 2 || sleeps[0] != 5*time.Second {
		t.Fatalf("polls=%d sleeps=%v", polls, sleeps)
	}
}

func TestGeminiFileNotReadyAndRules(t *testing.T) {
	var polls int32
	srv, _ := geminiServer(t, []string{"PROCESSING"}, &polls)
	var sleeps []time.Duration
	d := newDL(t, nil, &sleeps)
	d.HTTP = geminiDoer{target: srv.URL, inner: d.HTTP}
	u := "https://generativelanguage.googleapis.com/v1beta/files/abc123"
	_, err := d.Download(context.Background(), Request{URL: u, Kind: Video, MaxBytes: 100, APIKey: "GK"})
	if !errors.Is(err, ErrNotReady) || !strings.Contains(err.Error(), "PROCESSING") || polls != 12 || len(sleeps) != 11 {
		t.Fatalf("12 polls then a not-ready error stating the state: %v polls=%d sleeps=%d", err, polls, len(sleeps))
	}
	if _, err := d.Download(context.Background(), Request{URL: u, Kind: Image, MaxBytes: 100, APIKey: "GK"}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("images are unsupported: %v", err)
	}
	if _, err := d.Download(context.Background(), Request{URL: u, Kind: Video, MaxBytes: 100}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("a key is required: %v", err)
	}
	failed, _ := geminiServer(t, []string{"FAILED"}, new(int32))
	d.HTTP = geminiDoer{target: failed.URL, inner: newDL(t, nil, nil).HTTP}
	if _, err := d.Download(context.Background(), Request{URL: u, Kind: Video, MaxBytes: 100, APIKey: "GK"}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("FAILED is terminal: %v", err)
	}
	// Cancellation during the wait is honored.
	d3 := newDL(t, nil, nil)
	d3.HTTP = geminiDoer{target: srv.URL, inner: d3.HTTP}
	ctx, cancel := context.WithCancel(context.Background())
	d3.Sleep = func(ctx context.Context, _ time.Duration) error { cancel(); return ctx.Err() }
	if _, err := d3.Download(ctx, Request{URL: u, Kind: Video, MaxBytes: 100, APIKey: "GK"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}
