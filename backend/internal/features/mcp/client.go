package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Wire constants of the remote protocol.
const (
	protocolVersion      = "2025-06-18"
	clientName           = "riochat"
	clientVersion        = "0.1.0"
	defaultRPCTimeout    = 10 * time.Second
	maxResponseBytes     = 8 << 20
	sessionHeader        = "Mcp-Session-Id"
	toolErrorPrefix      = "mcp tool error: "
	toolErrorFallback    = "the tool reported an error without details"
	wrappedProtocolError = "MCP error"
)

// Endpoint is everything needed to reach one remote server for one operation.
type Endpoint struct {
	BaseURL string
	Token   string
	Headers map[string]string
}

// RemoteTool is one entry of a server's tool listing.
type RemoteTool struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// CallInput describes one tools/call.
type CallInput struct {
	Name           string
	ArgumentsJSON  string
	UserID         uint
	ConversationID uint
	RequestID      string
}

// Client talks to remote servers. Every operation performs its own handshake.
type Client interface {
	ListTools(ctx context.Context, ep Endpoint, timeout time.Duration) ([]RemoteTool, error)
	CallTool(ctx context.Context, ep Endpoint, in CallInput, timeout time.Duration) (string, error)
	// Close releases idle connections.
	Close()
}

// ToolError is a failure reported by the remote tool itself.
type ToolError struct{ Text string }

func (e *ToolError) Error() string { return toolErrorPrefix + e.Text }

// RPCError is a JSON-RPC error member.
type RPCError struct {
	Code    int
	Message string
}

func (e *RPCError) Error() string { return fmt.Sprintf("mcp jsonrpc error %d: %s", e.Code, e.Message) }

// StatusError is a non-2xx HTTP answer. It carries the status code only; the
// body is never kept.
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return "mcp http status " + strconv.Itoa(e.Code) }

type httpClient struct {
	hc     *http.Client
	nextID atomic.Int64
}

// NewHTTPClient returns the Streamable HTTP client with the outbound policy
// applied (guarded dialing, no proxy, same-origin redirects).
func NewHTTPClient() Client { return newHTTPClientWith(newHTTPClient()) }

func newHTTPClientWith(hc *http.Client) *httpClient { return &httpClient{hc: hc} }

func (c *httpClient) Close() { c.hc.CloseIdleConnections() }

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Result json.RawMessage `json:"result"`
}

// session carries the per-operation handshake state.
type session struct {
	ep   Endpoint
	id   string
	tout time.Duration
}

func (c *httpClient) post(ctx context.Context, s *session, req rpcRequest, wantBody bool) (json.RawMessage, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.tout)
	defer cancel()
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, s.ep.BaseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("mcp request: %w", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Accept", "application/json, text/event-stream")
	if t := strings.TrimSpace(s.ep.Token); t != "" {
		hr.Header.Set("Authorization", "Bearer "+t)
	}
	if s.id != "" {
		hr.Header.Set(sessionHeader, s.id)
	}
	for k, v := range s.ep.Headers {
		if k = strings.TrimSpace(k); k != "" {
			hr.Header.Set(k, strings.TrimSpace(v))
		}
	}
	resp, err := c.hc.Do(hr)
	if err != nil {
		return nil, fmt.Errorf("mcp transport: %w", err)
	}
	defer resp.Body.Close()
	if v := strings.TrimSpace(resp.Header.Get(sessionHeader)); v != "" {
		s.id = v
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, &StatusError{Code: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("mcp read: %w", err)
	}
	if !wantBody {
		return nil, nil
	}
	return parseRPCBody(resp.Header.Get("Content-Type"), body)
}

// parseRPCBody extracts the JSON-RPC result from a JSON or event-stream body.
func parseRPCBody(contentType string, body []byte) (json.RawMessage, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, errors.New("mcp empty response")
	}
	if mt, _, err := mime.ParseMediaType(contentType); err == nil && mt == "text/event-stream" {
		data := firstEventData(body)
		if strings.TrimSpace(data) == "" {
			return nil, errors.New("mcp event stream carried no data")
		}
		body = []byte(data)
	}
	var rr rpcResponse
	if err := json.Unmarshal(bytes.TrimSpace(body), &rr); err != nil {
		return nil, fmt.Errorf("mcp response is not json-rpc: %w", err)
	}
	if rr.Error != nil {
		return nil, &RPCError{Code: rr.Error.Code, Message: rr.Error.Message}
	}
	if len(bytes.TrimSpace(rr.Result)) == 0 || string(bytes.TrimSpace(rr.Result)) == "null" {
		return json.RawMessage("{}"), nil
	}
	return rr.Result, nil
}

// firstEventData returns the data of the first event that has any. Lines are
// split on LF, CRLF or CR; only "data:" lines count, with one leading space
// removed; the lines of one event are joined with newlines.
func firstEventData(body []byte) string {
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	var lines []string
	flush := func() (string, bool) {
		if len(lines) == 0 {
			return "", false
		}
		joined := strings.Join(lines, "\n")
		lines = nil
		return joined, strings.TrimSpace(joined) != ""
	}
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			if d, ok := flush(); ok {
				return d
			}
			continue
		}
		if rest, ok := strings.CutPrefix(line, "data:"); ok {
			lines = append(lines, strings.TrimPrefix(rest, " "))
		}
	}
	d, _ := flush()
	return d
}

func (c *httpClient) open(ctx context.Context, ep Endpoint, timeout time.Duration) (*session, error) {
	if timeout <= 0 {
		timeout = defaultRPCTimeout
	}
	s := &session{ep: ep, tout: timeout}
	id := c.nextID.Add(1)
	_, err := c.post(ctx, s, rpcRequest{JSONRPC: "2.0", ID: &id, Method: "initialize", Params: map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": clientName, "version": clientVersion},
	}}, true)
	if err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	if _, err := c.post(ctx, s, rpcRequest{JSONRPC: "2.0", Method: "notifications/initialized"}, false); err != nil {
		return nil, fmt.Errorf("initialized notification: %w", err)
	}
	return s, nil
}

// ListTools implements Client.
func (c *httpClient) ListTools(ctx context.Context, ep Endpoint, timeout time.Duration) ([]RemoteTool, error) {
	s, err := c.open(ctx, ep, timeout)
	if err != nil {
		return nil, err
	}
	id := c.nextID.Add(1)
	raw, err := c.post(ctx, s, rpcRequest{JSONRPC: "2.0", ID: &id, Method: "tools/list", Params: map[string]any{}}, true)
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	var out struct {
		Tools []RemoteTool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("tools/list result: %w", err)
	}
	return out.Tools, nil
}

// CallTool implements Client. The returned text is the raw result JSON.
func (c *httpClient) CallTool(ctx context.Context, ep Endpoint, in CallInput, timeout time.Duration) (string, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return "", errors.New("mcp tool name is empty")
	}
	args, err := parseArguments(in.ArgumentsJSON)
	if err != nil {
		return "", err
	}
	s, err := c.open(ctx, ep, timeout)
	if err != nil {
		return "", err
	}
	id := c.nextID.Add(1)
	raw, err := c.post(ctx, s, rpcRequest{JSONRPC: "2.0", ID: &id, Method: "tools/call", Params: map[string]any{
		"name":      name,
		"arguments": args,
		"_meta": map[string]any{
			"user_id":         in.UserID,
			"conversation_id": in.ConversationID,
			"request_id":      strings.TrimSpace(in.RequestID),
		},
	}}, true)
	if err != nil {
		return "", fmt.Errorf("tools/call: %w", err)
	}
	return normalizeCallResult(raw)
}

// parseArguments decodes tool arguments: empty is {}, anything but one JSON
// object is an error, integers stay integers and other numbers become floats.
func parseArguments(text string) (map[string]any, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("mcp tool arguments are not valid json: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("mcp tool arguments are not valid json: trailing data")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("mcp tool arguments must be a json object")
	}
	return convertNumbers(obj).(map[string]any), nil
}

// convertNumbers replaces json.Number values with int64, float64 or, when
// neither fits, the original text.
func convertNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		if f, err := x.Float64(); err == nil {
			return f
		}
		return x.String()
	case map[string]any:
		for k, e := range x {
			x[k] = convertNumbers(e)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = convertNumbers(e)
		}
		return x
	}
	return v
}

// normalizeCallResult turns the raw tools/call result into the stored output or
// a ToolError.
func normalizeCallResult(raw json.RawMessage) (string, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return "{}", nil
	}
	var res struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		// Not an object shaped result: hand it back untouched.
		return text, nil
	}
	var parts []string
	for _, c := range res.Content {
		if t := strings.TrimSpace(c.Text); t != "" {
			parts = append(parts, t)
		}
	}
	joined := strings.Join(parts, "\n")
	if res.IsError {
		switch {
		case joined != "":
			return "", &ToolError{Text: joined}
		case text != "" && text != "{}":
			return "", &ToolError{Text: text}
		}
		return "", &ToolError{Text: toolErrorFallback}
	}
	if strings.HasPrefix(joined, wrappedProtocolError+" ") || strings.HasPrefix(joined, wrappedProtocolError+":") {
		return "", &ToolError{Text: joined}
	}
	return text, nil
}
