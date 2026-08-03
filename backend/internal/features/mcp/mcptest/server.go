// Package mcptest is a scriptable fake MCP server for tests. It speaks the
// Streamable HTTP flavor of JSON-RPC 2.0 and records every request it gets.
package mcptest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Request is one recorded HTTP request.
type Request struct {
	Method string
	ID     json.RawMessage
	Params map[string]any
	Header http.Header
}

// Reply scripts the answer to a tools/call.
type Reply struct {
	// Result is encoded as the JSON-RPC result.
	Result any
	// RPCErrorCode, when non-zero, answers with a JSON-RPC error instead.
	RPCErrorCode int
	RPCErrorText string
	// Status, when non-zero, answers with that HTTP status and a body that
	// must never be echoed to clients.
	Status int
}

// Server is the fake. Configure the exported fields before use.
type Server struct {
	URL string

	mu        sync.Mutex
	requests  []Request
	tools     []map[string]any
	sse       bool
	sessionID string
	listCode  int
	call      func(name string, args map[string]any, attempt int) Reply
	attempts  map[string]int
	srv       *httptest.Server
}

// New starts a server that is closed when the test ends.
func New(t testing.TB) *Server {
	s := &Server{sessionID: "sess-1", attempts: map[string]int{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	s.URL = s.srv.URL + "/mcp"
	t.Cleanup(s.srv.Close)
	return s
}

// SetTools sets the tools/list answer.
func (s *Server) SetTools(tools ...map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools = tools
}

// UseSSE answers every request as an event stream.
func (s *Server) UseSSE(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sse = on
}

// FailList makes tools/list answer with an HTTP status (0 restores success).
func (s *Server) FailList(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCode = code
}

// OnCall scripts tools/call; attempt counts calls to the same tool, from 1.
func (s *Server) OnCall(f func(name string, args map[string]any, attempt int) Reply) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.call = f
}

// Requests returns a copy of everything received so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Reset forgets recorded requests and attempt counters.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = nil
	s.attempts = map[string]int{}
}

// Count returns how many requests used the JSON-RPC method.
func (s *Server) Count(method string) int {
	n := 0
	for _, r := range s.Requests() {
		if r.Method == method {
			n++
		}
	}
	return n
}

// Last returns the most recent request with the method.
func (s *Server) Last(method string) (Request, bool) {
	reqs := s.Requests()
	for i := len(reqs) - 1; i >= 0; i-- {
		if reqs[i].Method == method {
			return reqs[i], true
		}
	}
	return Request{}, false
}

// Tool builds a tools/list entry.
func Tool(name, title, description string, schema any) map[string]any {
	t := map[string]any{"name": name}
	if title != "" {
		t["title"] = title
	}
	if description != "" {
		t["description"] = description
	}
	if schema != nil {
		t["inputSchema"] = schema
	}
	return t
}

// Text builds a successful tools/call result with one text part.
func Text(text string) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var env struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params map[string]any  `json:"params"`
	}
	_ = json.Unmarshal(raw, &env)
	rec := Request{Method: env.Method, ID: env.ID, Params: env.Params, Header: r.Header.Clone()}

	s.mu.Lock()
	s.requests = append(s.requests, rec)
	sse, sid, listCode, tools, call := s.sse, s.sessionID, s.listCode, s.tools, s.call
	var attempt int
	if env.Method == "tools/call" {
		name, _ := env.Params["name"].(string)
		s.attempts[name]++
		attempt = s.attempts[name]
	}
	s.mu.Unlock()

	reply := func(result any) {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(orNull(env.ID)), "result": result})
		write(w, sse, body)
	}
	switch env.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", sid)
		reply(map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "fake", "version": "1"}})
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		if listCode != 0 {
			http.Error(w, "internal secret-body http://leak.example/", listCode)
			return
		}
		if tools == nil {
			tools = []map[string]any{}
		}
		reply(map[string]any{"tools": tools})
	case "tools/call":
		if call == nil {
			reply(Text("ok"))
			return
		}
		name, _ := env.Params["name"].(string)
		args, _ := env.Params["arguments"].(map[string]any)
		rep := call(name, args, attempt)
		switch {
		case rep.Status != 0:
			http.Error(w, "upstream body secret-body", rep.Status)
		case rep.RPCErrorCode != 0:
			body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(orNull(env.ID)),
				"error": map[string]any{"code": rep.RPCErrorCode, "message": rep.RPCErrorText}})
			write(w, sse, body)
		default:
			reply(rep.Result)
		}
	default:
		http.Error(w, "unknown method", http.StatusBadRequest)
	}
}

func orNull(id json.RawMessage) []byte {
	if len(id) == 0 {
		return []byte("null")
	}
	return id
}

func write(w http.ResponseWriter, sse bool, body []byte) {
	if sse {
		w.Header().Set("Content-Type", "text/event-stream")
		// Comment lines and CRLF endings must be tolerated by the client.
		fmt.Fprintf(w, ": keep-alive\r\nevent: message\r\ndata: %s\r\n\r\n", strings.TrimSpace(string(body)))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
