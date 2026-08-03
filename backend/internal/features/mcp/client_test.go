package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/mcp/mcptest"
)

func TestFirstEventData(t *testing.T) {
	long := strings.Repeat("x", 200000)
	cases := []struct{ name, in, want string }{
		{"simple", "event: message\ndata: {\"a\":1}\n\n", `{"a":1}`},
		{"crlf", "event: message\r\ndata: {\"a\":1}\r\n\r\n", `{"a":1}`},
		{"cr only", "data: {\"a\":1}\r\r", `{"a":1}`},
		{"comments", ": ping\n: another\ndata: {\"a\":1}\n\n", `{"a":1}`},
		{"multi line", "data: {\"a\":\ndata: 1}\n\n", "{\"a\":\n1}"},
		{"no space", "data:{\"a\":1}\n\n", `{"a":1}`},
		{"only one space stripped", "data:  x\n\n", " x"},
		{"first event only", "data: first\n\ndata: second\n\n", "first"},
		{"skips empty events", "event: ping\n\n: c\n\ndata: real\n\n", "real"},
		{"no trailing blank", "data: tail", "tail"},
		{"ignores other fields", "id: 4\nretry: 5\nevent: x\ndata: v\n\n", "v"},
		{"very long line", "data: " + long + "\n\n", long},
		{"nothing", "event: ping\n\n", ""},
		{"empty data event skipped", "data:\n\ndata: z\n\n", "z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstEventData([]byte(tc.in)); got != tc.want {
				n := len(got)
				t.Fatalf("got %d bytes %.60q, want %.60q", n, got, tc.want)
			}
		})
	}
}

func TestParseRPCBody(t *testing.T) {
	if _, err := parseRPCBody("application/json", []byte("  ")); err == nil {
		t.Error("empty body is an error")
	}
	if _, err := parseRPCBody("text/event-stream", []byte("event: ping\n\n")); err == nil {
		t.Error("an event stream without data is an error")
	}
	if _, err := parseRPCBody("application/json", []byte("<html>")); err == nil {
		t.Error("non json is an error")
	}
	_, err := parseRPCBody("application/json", []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"bad params"}}`))
	var re *RPCError
	if !errors.As(err, &re) || re.Code != -32602 || !strings.Contains(err.Error(), "-32602") || !strings.Contains(err.Error(), "bad params") {
		t.Fatalf("json-rpc error: %v", err)
	}
	for _, body := range []string{`{"jsonrpc":"2.0","id":1}`, `{"jsonrpc":"2.0","id":1,"result":null}`} {
		raw, err := parseRPCBody("application/json; charset=utf-8", []byte(body))
		if err != nil || string(raw) != "{}" {
			t.Errorf("missing result is {}: %q %v", raw, err)
		}
	}
	raw, err := parseRPCBody("text/event-stream; charset=utf-8", []byte("data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"k\":1}}\n\n"))
	if err != nil || string(raw) != `{"k":1}` {
		t.Fatalf("sse: %q %v", raw, err)
	}
}

func TestParseArguments(t *testing.T) {
	got, err := parseArguments(`{"i":3,"f":2.5,"big":123456789012345678901234567890,"n":[1,{"x":7}],"s":"4"}`)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := got["i"].(int64); !ok || v != 3 {
		t.Errorf("integer stays integer: %#v", got["i"])
	}
	if v, ok := got["f"].(float64); !ok || v != 2.5 {
		t.Errorf("float: %#v", got["f"])
	}
	if _, ok := got["big"].(float64); !ok {
		t.Errorf("too big for int64 becomes float: %#v", got["big"])
	}
	arr := got["n"].([]any)
	if arr[0].(int64) != 1 || arr[1].(map[string]any)["x"].(int64) != 7 {
		t.Errorf("nested: %#v", arr)
	}
	if got["s"] != "4" {
		t.Errorf("strings are strings: %#v", got["s"])
	}
	if got, err := parseArguments("  "); err != nil || len(got) != 0 {
		t.Errorf("empty is {}: %v %v", got, err)
	}
	e1 := func() string { _, e := parseArguments("{"); return e.Error() }()
	e2 := func() string { _, e := parseArguments("[1]"); return e.Error() }()
	if e1 == e2 || !strings.Contains(e2, "object") {
		t.Errorf("distinct wording: %q vs %q", e1, e2)
	}
	if _, err := parseArguments(`{"a":1} extra`); err == nil {
		t.Error("trailing data rejected")
	}
}

func TestNormalizeCallResult(t *testing.T) {
	ok, err := normalizeCallResult(json.RawMessage(` {"content":[{"type":"text","text":"fine"}]} `))
	if err != nil || ok != `{"content":[{"type":"text","text":"fine"}]}` {
		t.Fatalf("normal result is verbatim: %q %v", ok, err)
	}
	if out, err := normalizeCallResult(json.RawMessage(``)); err != nil || out != "{}" {
		t.Errorf("empty: %q %v", out, err)
	}
	if out, err := normalizeCallResult(json.RawMessage(`[1,2]`)); err != nil || out != "[1,2]" {
		t.Errorf("non object result is returned as is: %q %v", out, err)
	}
	var te *ToolError
	_, err = normalizeCallResult(json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"boom"},{"type":"text","text":"  "},{"type":"text","text":"bang"}]}`))
	if !errors.As(err, &te) || te.Text != "boom\nbang" || !strings.HasPrefix(err.Error(), toolErrorPrefix) {
		t.Fatalf("isError: %v", err)
	}
	_, err = normalizeCallResult(json.RawMessage(`{"isError":true,"detail":"x"}`))
	if !errors.As(err, &te) || !strings.Contains(te.Text, `"detail"`) {
		t.Fatalf("isError without text falls back to the raw result: %v", err)
	}
	_, err = normalizeCallResult(json.RawMessage(`{"isError":true}`))
	if !errors.As(err, &te) || te.Text != `{"isError":true}` {
		t.Fatalf("isError raw: %v", err)
	}
	for _, text := range []string{"MCP error -32602: invalid params", "MCP error: nope"} {
		_, err = normalizeCallResult(json.RawMessage(`{"content":[{"type":"text","text":"` + text + `"}]}`))
		if !errors.As(err, &te) || te.Text != text {
			t.Errorf("wrapped protocol error %q: %v", text, err)
		}
	}
	if _, err = normalizeCallResult(json.RawMessage(`{"content":[{"type":"text","text":"MCP errors are fine"}]}`)); err != nil {
		t.Errorf("only the exact prefix counts: %v", err)
	}
}

func TestClientWireFormat(t *testing.T) {
	fake := mcptest.New(t)
	fake.SetTools(toolOf("search", "Search", "d", searchSchema))
	c := NewHTTPClient()
	defer c.Close()
	ctx := context.Background()

	if _, err := c.ListTools(ctx, Endpoint{BaseURL: fake.URL, Token: "tok", Headers: map[string]string{"X-Custom": " v ", "  ": "x"}}, time.Second); err != nil {
		t.Fatal(err)
	}
	reqs := fake.Requests()
	if len(reqs) != 3 {
		t.Fatalf("handshake is initialize, initialized, request: %d", len(reqs))
	}
	init, note, list := reqs[0], reqs[1], reqs[2]
	if init.Method != "initialize" || init.Params["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize: %+v", init)
	}
	if ci, _ := init.Params["clientInfo"].(map[string]any); ci["name"] != "riochat" || ci["version"] != "0.1.0" {
		t.Fatalf("clientInfo: %v", init.Params["clientInfo"])
	}
	if caps, ok := init.Params["capabilities"].(map[string]any); !ok || len(caps) != 0 {
		t.Fatalf("capabilities: %v", init.Params["capabilities"])
	}
	if note.Method != "notifications/initialized" || len(note.ID) != 0 {
		t.Fatalf("notification must have no id: %+v", note)
	}
	if init.Header.Get("Mcp-Session-Id") != "" {
		t.Fatal("the first request has no session")
	}
	for _, r := range []mcptest.Request{note, list} {
		if r.Header.Get("Mcp-Session-Id") != "sess-1" {
			t.Fatalf("%s must carry the session id: %v", r.Method, r.Header)
		}
	}
	for _, r := range reqs {
		if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("Content-Type") != "application/json" ||
			r.Header.Get("Accept") != "application/json, text/event-stream" || r.Header.Get("X-Custom") != "v" {
			t.Fatalf("%s headers: %v", r.Method, r.Header)
		}
	}
	// JSON-RPC ids increase.
	var id0, id2 int
	_ = json.Unmarshal(init.ID, &id0)
	_ = json.Unmarshal(list.ID, &id2)
	if id2 <= id0 {
		t.Fatalf("ids %d then %d", id0, id2)
	}

	fake.Reset()
	out, err := c.CallTool(ctx, Endpoint{BaseURL: fake.URL}, CallInput{Name: " search ", ArgumentsJSON: `{"q":"x","n":3}`, UserID: 7, ConversationID: 9, RequestID: " req-1 "}, time.Second)
	if err != nil || !strings.Contains(out, "ok") {
		t.Fatalf("call: %q %v", out, err)
	}
	call, _ := fake.Last("tools/call")
	if call.Params["name"] != "search" {
		t.Fatalf("name: %v", call.Params["name"])
	}
	args := call.Params["arguments"].(map[string]any)
	if args["q"] != "x" || args["n"] != float64(3) {
		t.Fatalf("arguments: %v", args)
	}
	meta := call.Params["_meta"].(map[string]any)
	if meta["user_id"] != float64(7) || meta["conversation_id"] != float64(9) || meta["request_id"] != "req-1" {
		t.Fatalf("_meta: %v", meta)
	}
	if call.Header.Get("Authorization") != "" {
		t.Fatal("no Authorization header without a token")
	}
}

func TestClientCustomHeadersOverrideDefaults(t *testing.T) {
	fake := mcptest.New(t)
	c := NewHTTPClient()
	defer c.Close()
	_, err := c.ListTools(context.Background(), Endpoint{BaseURL: fake.URL, Token: "tok", Headers: map[string]string{"Authorization": "Custom abc", "Accept": "text/plain"}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range fake.Requests() {
		if r.Header.Get("Authorization") != "Custom abc" || r.Header.Get("Accept") != "text/plain" {
			t.Fatalf("%s: %v", r.Method, r.Header)
		}
	}
}

func TestClientErrors(t *testing.T) {
	fake := mcptest.New(t)
	c := NewHTTPClient()
	defer c.Close()
	ctx := context.Background()
	ep := Endpoint{BaseURL: fake.URL, Token: "tok-secret"}

	fake.FailList(503)
	_, err := c.ListTools(ctx, ep, time.Second)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 503 {
		t.Fatalf("status error: %v", err)
	}
	for _, leak := range []string{"secret-body", "leak.example", "tok-secret"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("error leaks %q: %v", leak, err)
		}
	}
	fake.FailList(0)

	fake.OnCall(func(string, map[string]any, int) mcptest.Reply {
		return mcptest.Reply{RPCErrorCode: -32601, RPCErrorText: "no such tool"}
	})
	_, err = c.CallTool(ctx, ep, CallInput{Name: "x"}, time.Second)
	var re *RPCError
	if !errors.As(err, &re) || re.Code != -32601 {
		t.Fatalf("rpc error: %v", err)
	}
	fake.OnCall(func(string, map[string]any, int) mcptest.Reply { return mcptest.Reply{Status: 500} })
	if _, err = c.CallTool(ctx, ep, CallInput{Name: "x"}, time.Second); !errors.As(err, &se) || se.Code != 500 || strings.Contains(err.Error(), "secret-body") {
		t.Fatalf("status on call: %v", err)
	}
	fake.Reset()
	if _, err = c.CallTool(ctx, ep, CallInput{Name: "  "}, time.Second); err == nil || fake.Count("initialize") != 0 {
		t.Fatalf("an empty tool name fails before any request: %v", err)
	}
	if _, err = c.CallTool(ctx, ep, CallInput{Name: "x", ArgumentsJSON: "[1]"}, time.Second); err == nil || fake.Count("initialize") != 0 {
		t.Fatalf("bad arguments fail before any request: %v", err)
	}
	fake.Reset()
	if _, err = c.CallTool(ctx, Endpoint{BaseURL: "http://127.0.0.1:1/"}, CallInput{Name: "x"}, time.Second); err == nil {
		t.Fatal("connection refused is an error")
	}
}

func TestClientTimeoutPerRoundTrip(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	c := NewHTTPClient()
	defer c.Close()
	start := time.Now()
	_, err := c.ListTools(context.Background(), Endpoint{BaseURL: slow.URL}, 150*time.Millisecond)
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("must time out quickly: %v after %v", err, time.Since(start))
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Fatalf("hits %d", hits)
	}
}

func TestClientDefaultTimeoutAndRedirects(t *testing.T) {
	fake := mcptest.New(t)
	c := NewHTTPClient()
	defer c.Close()
	// Non-positive timeout falls back to ten seconds, so a healthy call works.
	if _, err := c.ListTools(context.Background(), Endpoint{BaseURL: fake.URL}, 0); err != nil {
		t.Fatal(err)
	}
	// A redirect to another origin is refused.
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer elsewhere.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusTemporaryRedirect)
	}))
	defer redir.Close()
	if _, err := c.ListTools(context.Background(), Endpoint{BaseURL: redir.URL}, time.Second); err == nil {
		t.Fatal("cross-origin redirect must fail")
	}
}

func TestClientRefusesForbiddenDestinationsAtDialTime(t *testing.T) {
	c := NewHTTPClient()
	defer c.Close()
	_, err := c.ListTools(context.Background(), Endpoint{BaseURL: "http://169.254.169.254/latest"}, time.Second)
	if err == nil {
		t.Fatal("metadata address must be refused")
	}
}

func TestHeadersSnapshotDoesNotAliasInput(t *testing.T) {
	in := map[string]string{"A": "1"}
	b := &binding{headers: in}
	s := &Service{}
	out, err := s.signedHeaders(b, CallRequest{})
	if err != nil || !reflect.DeepEqual(out, in) {
		t.Fatalf("%v %v", out, err)
	}
	out["A"] = "changed"
	if in["A"] != "1" {
		t.Fatal("signedHeaders must copy")
	}
}
