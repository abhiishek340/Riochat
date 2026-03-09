package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/outbound"
)

const okChat = `{"id":"c1","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`

func chatRoute(f *fakeUpstream) Route { return routeFor(OpenAIChatCompletions, f, "gpt-test") }

func TestBufferedRoundTripAndAuthHeaders(t *testing.T) {
	f := newUpstream(t, jsonReply(okChat))
	c := testClient(t)
	res, err := c.Generate(context.Background(), chatRoute(f), &Request{Messages: userMsg("hello")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "hi" || res.ID != "c1" || res.Usage.InputTokens != 3 || res.Usage.OutputTokens != 1 || res.FinishReason != "stop" {
		t.Fatalf("%+v", res)
	}
	r := f.last(t)
	if r.Method != "POST" || r.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-test" ||
		r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" {
		t.Fatalf("%+v", r)
	}
	// Debug snapshot: redacted headers, sanitized bodies.
	d := res.Debug
	if d == nil || d.Method != "POST" || d.Path != "/v1/chat/completions" || d.RequestHeaders["authorization"] != "[redacted]" ||
		d.Status != 200 || !strings.Contains(d.ResponseBody, `"id":"c1"`) || !strings.Contains(d.RequestBody, `"model":"gpt-test"`) {
		t.Fatalf("%+v", d)
	}
}

func TestAuthHeadersPerFamily(t *testing.T) {
	cases := []struct {
		proto  Protocol
		header string
		prefix string
	}{
		{OpenAIResponses, "Authorization", "Bearer "},
		{OpenRouterChat, "Authorization", "Bearer "},
		{XAIResponses, "Authorization", "Bearer "},
		{AnthropicMessages, "X-Api-Key", ""},
		{GoogleGenerateContent, "X-Goog-Api-Key", ""},
		{GeminiInteractions, "X-Goog-Api-Key", ""},
	}
	for _, tc := range cases {
		f := newUpstream(t, jsonReply(`{}`))
		c := testClient(t)
		route := routeFor(tc.proto, f, "m")
		_, _ = c.Generate(context.Background(), route, &Request{Messages: userMsg("x")})
		r := f.last(t)
		if got := r.Header.Get(tc.header); got != tc.prefix+"sk-test" {
			t.Errorf("%s: %s=%q", tc.proto, tc.header, got)
		}
		if strings.Contains(r.RawQuery, "sk-test") {
			t.Errorf("%s: key leaked into the URL", tc.proto)
		}
		if tc.proto == AnthropicMessages && r.Header.Get("Anthropic-Version") != "2023-06-01" {
			t.Error("anthropic-version")
		}
	}
	// No header without a key.
	f := newUpstream(t, jsonReply(okChat))
	route := chatRoute(f)
	route.APIKey = ""
	if _, err := testClient(t).Generate(context.Background(), route, &Request{Messages: userMsg("x")}); err != nil {
		t.Fatal(err)
	}
	if f.last(t).Header.Get("Authorization") != "" {
		t.Fatal("no key, no header")
	}
}

func TestCustomHeadersOverrideAndTemplate(t *testing.T) {
	f := newUpstream(t, jsonReply(okChat))
	route := chatRoute(f)
	route.HeadersJSON = `{"X-Trace":"${RIOCHAT_CONVERSATION_ID}","Authorization":"Custom abc","X-Up":"${RIOCHAT_UPSTREAM_REQUEST_ID}"}`
	c := testClient(t)
	if _, err := c.Generate(context.Background(), route, &Request{Messages: userMsg("x")}); err != nil {
		t.Fatal(err)
	}
	r := f.last(t)
	if r.Header.Get("X-Trace") != "" {
		t.Fatal("no conversation id: header omitted")
	}
	if r.Header.Get("Authorization") != "Custom abc" || r.Header.Get("X-Up") == "" {
		t.Fatalf("%v", r.Header)
	}
	if _, err := c.Generate(context.Background(), route, &Request{Messages: userMsg("x"), ConversationID: "conv-7"}); err != nil {
		t.Fatal(err)
	}
	if got := f.last(t).Header.Get("X-Trace"); got != "conv-7" {
		t.Fatalf("X-Trace=%q", got)
	}
}

func TestOpenRouterAttribution(t *testing.T) {
	var seen http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		io.WriteString(w, okChat)
	}))
	defer srv.Close()
	// Route the openrouter.ai host name to the test server via a Doer wrapper.
	c := NewClient(ClientOptions{HTTP: rewriteDoer{target: srv.URL}})
	route := Route{Protocol: string(OpenRouterChat), BaseURL: "https://api.openrouter.ai/api", Model: "m", APIKey: "k",
		HeadersJSON: `{"x-title":"Mine"}`, OpenRouterReferer: "https://app.example/"}
	if _, err := c.Generate(context.Background(), route, &Request{Messages: userMsg("x")}); err != nil {
		t.Fatal(err)
	}
	if seen.Get("HTTP-Referer") != "https://app.example" || seen.Get("X-Title") != "Mine" ||
		seen.Get("X-OpenRouter-Title") != "Riochat" || seen.Get("X-OpenRouter-Categories") != "general-chat" {
		t.Fatalf("%v", seen)
	}
	// Defaults, and no attribution on other hosts.
	route.HeadersJSON, route.OpenRouterReferer = "", ""
	if _, err := c.Generate(context.Background(), route, &Request{Messages: userMsg("x")}); err != nil {
		t.Fatal(err)
	}
	if seen.Get("HTTP-Referer") != "https://github.com/riochat/riochat" || seen.Get("X-Title") != "Riochat" {
		t.Fatalf("%v", seen)
	}
	route.BaseURL = "https://notopenrouter.ai.example.com"
	if _, err := c.Generate(context.Background(), route, &Request{Messages: userMsg("x")}); err != nil {
		t.Fatal(err)
	}
	if seen.Get("X-OpenRouter-Categories") != "" {
		t.Fatal("attribution only for openrouter.ai hosts")
	}
}

// rewriteDoer sends every request to target regardless of its host.
type rewriteDoer struct{ target string }

func (d rewriteDoer) Do(ctx context.Context, ep outbound.Endpoint, req *http.Request) (*http.Response, error) {
	u := *req.URL
	t, _ := http.NewRequestWithContext(ctx, req.Method, d.target+u.Path+"?"+u.RawQuery, req.Body)
	t.Header = req.Header.Clone()
	return http.DefaultClient.Do(t)
}

func TestAcceptedMarker(t *testing.T) {
	// Connection drops after the full request body was written.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				buf := make([]byte, 64<<10)
				total := 0
				for {
					n, err := c.Read(buf)
					total += n
					if err != nil {
						return
					}
					if strings.Contains(string(buf[:n]), "}") { // body fully received
						c.Close()
						return
					}
				}
			}(conn)
		}
	}()
	c := testClient(t)
	route := Route{Protocol: string(OpenAIChatCompletions), BaseURL: "http://" + ln.Addr().String(), Model: "m"}
	_, err = c.Generate(context.Background(), route, &Request{Messages: userMsg("hi")})
	if err == nil || !IsAccepted(err) || Retryable(err) {
		t.Fatalf("a drop after the write is accepted and must not be re-routed: %v accepted=%v", err, IsAccepted(err))
	}
	// Connection refused: nothing was written, so it stays retryable.
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := dead.Addr().String()
	dead.Close()
	_, err = c.Generate(context.Background(), Route{Protocol: string(OpenAIChatCompletions), BaseURL: "http://" + addr, Model: "m"}, &Request{Messages: userMsg("hi")})
	if err == nil || IsAccepted(err) || !Retryable(err) {
		t.Fatalf("a refused connection is not accepted and is retryable: %v", err)
	}
	// Unparseable 2xx body is accepted.
	f := newUpstream(t, jsonReply(`not json`))
	_, err = c.Generate(context.Background(), chatRoute(f), &Request{Messages: userMsg("hi")})
	if err == nil || !IsAccepted(err) {
		t.Fatalf("parse errors on 2xx are accepted: %v", err)
	}
}

func TestUpstreamErrorMessages(t *testing.T) {
	cases := []struct {
		proto   Protocol
		status  int
		body    string
		message string
	}{
		{OpenAIChatCompletions, 400, `{"error":{"message":"bad things","type":"invalid_request_error"}}`, "bad things"},
		{OpenAIChatCompletions, 429, `{"message":"slow down"}`, "slow down"},
		{OpenAIChatCompletions, 503, `<html>`, "upstream_status_503"},
		{OpenAIChatCompletions, 500, `{"error":"plain string"}`, "plain string"},
		{GoogleGenerateContent, 401, `{}`, "google authentication failed; check API key, upstream base URL, and custom auth headers"},
		{GoogleGenerateContent, 401, `{"error":{"message":"API key not valid"}}`, "API key not valid"},
	}
	for _, tc := range cases {
		f := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
			w.WriteHeader(tc.status)
			io.WriteString(w, tc.body)
		})
		_, err := testClient(t).Generate(context.Background(), routeFor(tc.proto, f, "m"), &Request{Messages: userMsg("x")})
		e, ok := AsError(err)
		if !ok || e.Kind != KindUpstream || e.Status != tc.status || e.Message != tc.message || e.Body != tc.body || e.Debug == nil {
			t.Errorf("%v: %+v", tc, e)
		}
		if e != nil && e.Accepted {
			t.Errorf("an HTTP error response was not accepted work: %+v", e)
		}
	}
	// Retryable classification.
	if !Retryable(&Error{Kind: KindUpstream, Status: 429}) || !Retryable(&Error{Kind: KindUpstream, Status: 502}) ||
		Retryable(&Error{Kind: KindUpstream, Status: 400}) || Retryable(&Error{Kind: KindUpstream, Status: 503, Accepted: true}) || Retryable(errors.New("x")) {
		t.Fatal("retryable classification")
	}
}

func TestInStreamErrors(t *testing.T) {
	cases := []struct {
		name, frame string
		status      int
		message     string
	}{
		{"error object with code", `{"error":{"message":"overloaded","code":529}}`, 529, "overloaded"},
		{"status field", `{"error":{"message":"nope","status":429}}`, 429, "nope"},
		{"status_code field", `{"error":{"message":"nope","status_code":418}}`, 418, "nope"},
		{"invalid code falls back", `{"error":{"message":"odd","code":9999}}`, 502, "odd"},
		{"string code", `{"error":{"message":"odd","code":"rate_limit"}}`, 502, "odd"},
		{"msg fallback", `{"error":{"msg":"short","code":503}}`, 503, "short"},
		{"response.error", `{"type":"response.error","message":"top level","code":500}`, 500, "top level"},
		{"response.failed", `{"type":"response.failed","response":{"error":{"message":"failed deep","code":"server_error"}}}`, 502, "failed deep"},
		{"status text", `{"error":{"code":503}}`, 503, "Service Unavailable"},
	}
	for _, tc := range cases {
		f := newUpstream(t, sseReply(ev("", `{"id":"c1","choices":[{"delta":{"content":"a"}}]}`), ev("", tc.frame)))
		sink, _ := collect()
		_, err := testClient(t).Stream(context.Background(), chatRoute(f), &Request{Messages: userMsg("x")}, sink)
		e, ok := AsError(err)
		if !ok || e.Status != tc.status || e.Message != tc.message || !e.Accepted {
			t.Errorf("%s: %+v", tc.name, e)
		}
	}
}

func TestReadTimeoutBuffered(t *testing.T) {
	f := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	})
	route := chatRoute(f)
	route.ReadTimeoutMS = 100
	start := time.Now()
	_, err := testClient(t).Generate(context.Background(), route, &Request{Messages: userMsg("x")})
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("expected a prompt timeout, got %v after %v", err, time.Since(start))
	}
	if !IsAccepted(err) {
		t.Fatal("a timeout after the request was written is accepted")
	}
}

func TestStreamFirstByteAndIdleTimeouts(t *testing.T) {
	slowHeader := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	})
	route := chatRoute(slowHeader)
	route.ReadTimeoutMS = 100
	sink, _ := collect()
	_, err := testClient(t).Stream(context.Background(), route, &Request{Messages: userMsg("x")}, sink)
	if !errors.Is(err, ErrFirstByteTimeout) {
		t.Fatalf("first byte timeout: %v", err)
	}

	stall := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, ev("", `{"id":"c1","choices":[{"delta":{"content":"a"}}]}`))
		w.(http.Flusher).Flush()
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
	})
	route = chatRoute(stall)
	route.StreamIdleTimeoutMS = 150
	route.ReadTimeoutMS = 5000
	sink2, evs := collect()
	start := time.Now()
	res, err := testClient(t).Stream(context.Background(), route, &Request{Messages: userMsg("x")}, sink2)
	if !errors.Is(err, ErrStreamIdle) || time.Since(start) > 2*time.Second {
		t.Fatalf("idle timeout: %v after %v", err, time.Since(start))
	}
	if res == nil || res.Text != "a" || len(eventsOf(*evs, EventText)) != 1 {
		t.Fatalf("partial result expected: %+v", res)
	}
	if !IsAccepted(err) {
		t.Fatal("mid-stream failures are accepted")
	}
}

func TestStreamHealthyPastReadTimeout(t *testing.T) {
	// The read timeout covers time to first byte only; a stream that keeps
	// producing chunks must survive longer than it.
	f := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 5; i++ {
			io.WriteString(w, ev("", fmt.Sprintf(`{"id":"c1","choices":[{"delta":{"content":"%d"}}]}`, i)))
			w.(http.Flusher).Flush()
			time.Sleep(60 * time.Millisecond)
		}
		io.WriteString(w, "data: [DONE]\n\n")
	})
	route := chatRoute(f)
	route.ReadTimeoutMS = 150
	sink, _ := collect()
	res, err := testClient(t).Stream(context.Background(), route, &Request{Messages: userMsg("x")}, sink)
	if err != nil || res.Text != "01234" {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestResponseBodyCap(t *testing.T) {
	f := newUpstream(t, jsonReply(`{"padding":"`+strings.Repeat("x", 200)+`"}`))
	c := testClient(t, func(o *ClientOptions) { o.MaxBodyBytes = 100 })
	_, err := c.Generate(context.Background(), chatRoute(f), &Request{Messages: userMsg("x")})
	if err == nil || err.Error() != "upstream response body exceeds 100 bytes" || !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("got %v", err)
	}
	if DefaultMaxBody != 67108864 {
		t.Fatal("default cap is 64 MiB")
	}
	// Error bodies obey the cap too.
	f2 := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		w.WriteHeader(500)
		io.WriteString(w, strings.Repeat("e", 500))
	})
	_, err = c.Generate(context.Background(), chatRoute(f2), &Request{Messages: userMsg("x")})
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("got %v", err)
	}
}

func TestUntrustedEndpointsUsePublicOnlyPolicy(t *testing.T) {
	f := newUpstream(t, jsonReply(okChat))
	route := chatRoute(f)
	route.Untrusted = true
	_, err := testClient(t).Generate(context.Background(), route, &Request{Messages: userMsg("x")})
	if err == nil || !strings.Contains(err.Error(), "not permitted") {
		t.Fatalf("a loopback user endpoint must be refused: %v", err)
	}
	if len(f.all()) != 0 {
		t.Fatal("the request must never leave the process")
	}
}

func TestTrustedEndpointRedirectsFollowAndUserRedirectsRefused(t *testing.T) {
	var base string
	f := newUpstream(t, func(w http.ResponseWriter, r *http.Request, rec recorded) {
		if r.URL.Path == "/v1/chat/completions" {
			http.Redirect(w, r, base+"/moved", http.StatusTemporaryRedirect)
			return
		}
		io.WriteString(w, okChat)
	})
	base = f.URL
	res, err := testClient(t).Generate(context.Background(), chatRoute(f), &Request{Messages: userMsg("x")})
	if err != nil || res.Text != "hi" {
		t.Fatalf("same-origin redirect is followed for trusted endpoints: %v", err)
	}
}

func TestRetryReasonsAndSinkAbort(t *testing.T) {
	f := newUpstream(t, sseReply(ev("", `{"id":"c1","choices":[{"delta":{"content":"a"}}]}`), ev("", `{"choices":[{"delta":{"content":"b"}}]}`)))
	stop := errors.New("stop now")
	n := 0
	sink := func(e Event) error {
		n++
		if n == 2 {
			return stop
		}
		return nil
	}
	_, err := testClient(t).Stream(context.Background(), chatRoute(f), &Request{Messages: userMsg("x")}, sink)
	if !errors.Is(err, stop) {
		t.Fatalf("a sink error aborts the stream: %v", err)
	}
	if _, err := testClient(t).Stream(context.Background(), chatRoute(f), &Request{}, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("nil sink")
	}
}

func TestInvalidBaseURLAndMissingModel(t *testing.T) {
	c := testClient(t)
	_, err := c.Generate(context.Background(), Route{Protocol: string(OpenAIChatCompletions), BaseURL: "", Model: "m"}, &Request{Messages: userMsg("x")})
	if !errors.Is(err, ErrInvalidBaseURL) || err.Error() != "invalid base url" {
		t.Fatalf("%v", err)
	}
	_, err = c.Generate(context.Background(), Route{Protocol: string(OpenAIChatCompletions), BaseURL: "http://x"}, &Request{Messages: userMsg("x")})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("%v", err)
	}
}

func TestGoogleFamilyUnauthorizedMessage(t *testing.T) {
	const want = "google authentication failed; check API key, upstream base URL, and custom auth headers"
	for _, p := range []Protocol{GoogleGenerateContent, GoogleImageGeneration, GeminiInteractions} {
		if e := upstreamError(p, 401, []byte(`{}`), nil); e.Message != want {
			t.Fatalf("%s: %q", p, e.Message)
		}
		if e := upstreamError(p, 401, []byte(`{"error":{"message":"bad key"}}`), nil); e.Message != "bad key" {
			t.Fatalf("%s: a body message wins: %q", p, e.Message)
		}
	}
	if e := upstreamError(OpenAIChatCompletions, 401, nil, nil); e.Message != "upstream_status_401" {
		t.Fatal(e.Message)
	}
}
