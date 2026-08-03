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
