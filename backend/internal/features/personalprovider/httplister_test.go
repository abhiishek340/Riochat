package personalprovider

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// testLister talks to a local TLS server: the guarded client is built with a
// deny function that lets loopback through and certificate checks are off.
func testLister(t *testing.T, handler http.Handler) (*httpLister, string) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	c := newGuardedClient(nil, func(net.IP) bool { return false })
	c.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	return &httpLister{client: c}, srv.URL
}

func TestListerOpenAIStyle(t *testing.T) {
	var gotAuth, gotPath string
	l, base := testLister(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"o3"}]}`))
	}))
	names, err := l.ListModels(context.Background(), ListRequest{Protocol: ProtoOpenAIChat, BaseURL: base + "/v1", APIKey: "sk-secret-key"})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "gpt-4o,o3" || gotAuth != "Bearer sk-secret-key" || gotPath != "/v1/models" {
		t.Fatalf("%v %q %q", names, gotAuth, gotPath)
	}
}

func TestListerAnthropicHeaders(t *testing.T) {
	var key, ver string
	l, base := testLister(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ver = r.Header.Get("x-api-key"), r.Header.Get("anthropic-version")
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-x"}]}`))
	}))
	names, err := l.ListModels(context.Background(), ListRequest{Protocol: ProtoAnthropic, BaseURL: base, APIKey: "k1"})
	if err != nil || len(names) != 1 || key != "k1" || ver == "" {
		t.Fatalf("%v %v %q %q", names, err, key, ver)
	}
}

func TestListerGooglePagingAndPrefix(t *testing.T) {
	var keyHdr string
	l, base := testLister(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keyHdr = r.Header.Get("x-goog-api-key")
		if r.URL.Query().Get("pageToken") == "" {
			_, _ = w.Write([]byte(`{"models":[{"name":"models/gemini-a"}],"nextPageToken":"t2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"models/gemini-b"}]}`))
	}))
	names, err := l.ListModels(context.Background(), ListRequest{Protocol: ProtoGoogleGenerate, BaseURL: base + "/v1beta", APIKey: "gk"})
	if err != nil || strings.Join(names, ",") != "gemini-a,gemini-b" || keyHdr != "gk" {
		t.Fatalf("%v %v %q", names, err, keyHdr)
	}
}

func TestListerStatusClassification(t *testing.T) {
	for _, status := range []int{401, 403, 404, 500} {
		l, base := testLister(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("echo sk-secret-key"))
		}))
		_, err := l.ListModels(context.Background(), ListRequest{Protocol: ProtoOpenAIChat, BaseURL: base, APIKey: "sk-secret-key"})
		var se *UpstreamStatusError
		if !errors.As(err, &se) || se.Status != status {
			t.Fatalf("%d: %v", status, err)
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatalf("error leaks body: %v", err)
		}
		pub := classifyUpstream(err)
		want := ErrUpstreamDown.Code
		if status == 401 || status == 403 {
			want = ErrUpstreamRejected.Code
		}
		if code, _ := asTyped(pub); code != want {
			t.Fatalf("%d -> %s", status, code)
		}
	}
}

func TestListerBadBodyAndRedirects(t *testing.T) {
	l, base := testLister(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redir/models" {
			http.Redirect(w, r, "https://elsewhere.invalid/", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("<html>not json sk-secret-key</html>"))
	}))
	_, err := l.ListModels(context.Background(), ListRequest{Protocol: ProtoOpenAIChat, BaseURL: base, APIKey: "sk-secret-key"})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("%v", err)
	}
	_, err = l.ListModels(context.Background(), ListRequest{Protocol: ProtoOpenAIChat, BaseURL: base + "/redir", APIKey: "k"})
	var se *UpstreamStatusError
	if !errors.As(err, &se) || se.Status != 302 {
		t.Fatalf("redirects must not be followed: %v", err)
	}
}

func TestListerBlocksInternalAddressesAtConnectTime(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the internal server must never be reached")
	}))
	defer srv.Close()
	l := NewHTTPLister(PublicOnlyPolicy{})
	_, err := l.ListModels(context.Background(), ListRequest{Protocol: ProtoOpenAIChat, BaseURL: srv.URL, APIKey: "k"})
	if err == nil {
		t.Fatal("loopback must be refused")
	}
	// A host name that resolves to loopback is refused after resolution too.
	_, err = l.ListModels(context.Background(), ListRequest{Protocol: ProtoOpenAIChat, BaseURL: "https://localtest.me:" + portOf(srv.URL), APIKey: "k"})
	if err == nil {
		t.Fatal("name resolving to loopback must be refused")
	}
}

func portOf(u string) string { return u[strings.LastIndex(u, ":")+1:] }
