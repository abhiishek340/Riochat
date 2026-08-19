package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

const (
	retryBackoffStep   = 100 * time.Millisecond
	defaultSchemaText  = `{"type":"object","properties":{}}`
	maxModelOutputRune = 16000
)

// ToolSpec is one tool as offered to the model.
type ToolSpec struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// ValidateSelection enforces the per-message limit on selected tool ids. It
// applies to every path that accepts a selection, HTTP or not.
func (s *Service) ValidateSelection(ctx context.Context, ids []uint) error {
	limit := s.runtime(ctx).MaxSelectedToolsPerMessage
	if len(ids) > limit || len(ids) > maxRequestSelectedToolCount {
		return ErrTooManyTools
	}
	return nil
}

// ValidateToolDefaults checks a project's default tool ids: each must exist, be
// active and belong to an active server, and at most one may be an image
// processor. Violations are reported as ErrInvalidSelection (or
// ErrMultipleProcessors); the caller maps them to its own error.
func (s *Service) ValidateToolDefaults(ctx context.Context, toolIDs []uint) error {
	ids := dedupeIDs(toolIDs)
	if len(ids) == 0 {
		return nil
	}
	var tools []Tool
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&tools).Error; err != nil {
		return database.Translate(err)
	}
	if len(tools) != len(ids) {
		return ErrInvalidSelection
	}
	serverIDs := make([]uint, 0, len(tools))
	for _, t := range tools {
		if t.Status != StatusActive {
			return ErrInvalidSelection
		}
		serverIDs = append(serverIDs, t.ServerID)
	}
	var active int64
	if err := s.db.WithContext(ctx).Model(&Server{}).
		Where("id IN ? AND status = ?", dedupeIDs(serverIDs), StatusActive).Count(&active).Error; err != nil {
		return database.Translate(err)
	}
	if int(active) != len(dedupeIDs(serverIDs)) {
		return ErrInvalidSelection
	}
	processors := 0
	for _, t := range tools {
		if t.IsImageProcessor() {
			processors++
		}
	}
	if processors > 1 {
		return ErrMultipleProcessors
	}
	return nil
}

// ValidateMCPTools adapts ValidateToolDefaults to the conversation area's
// capability port (the user id is not needed: the catalog is global).
func (s *Service) ValidateMCPTools(ctx context.Context, _ uint, toolIDs []uint) error {
	return s.ValidateToolDefaults(ctx, toolIDs)
}

func dedupeIDs(ids []uint) []uint {
	seen := make(map[uint]bool, len(ids))
	out := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id != 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// binding is the snapshot used to call one tool during a run.
type binding struct {
	modelName   string
	baseURL     string
	token       string
	headers     map[string]string
	timeout     time.Duration
	serverID    uint
	serverName  string
	toolID      uint
	toolName    string
	displayName string
	schema      string
	price       int64
	attach      attachment
}

// RunTools is the tool set of one generation run. Create it with
// Service.ResolveRunTools. It is safe for concurrent use.
type RunTools struct {
	svc       *Service
	cfg       RuntimeSettings
	specs     []ToolSpec
	guidance  string
	bindings  map[string]*binding
	processor *binding

	mu    sync.Mutex
	seen  map[string]*callEntry
	usage []UsageItem
	fatal bool
}

type callEntry struct {
	done    chan struct{}
	outcome CallOutcome
}

// Tools returns the tools to offer the model (the image processor is hidden).
func (rt *RunTools) Tools() []ToolSpec { return append([]ToolSpec(nil), rt.specs...) }

// Guidance is the system message to insert after any leading system messages;
// it is empty when no tool is offered.
func (rt *RunTools) Guidance() string { return rt.guidance }

// HasTools reports whether any tool is offered to the model.
func (rt *RunTools) HasTools() bool { return len(rt.specs) > 0 }

// Limits are the per-run ceilings from the settings.
func (rt *RunTools) Limits() (maxLLMCalls, maxToolCalls int) {
	return rt.cfg.MaxLLMCallsPerRun, rt.cfg.MaxToolCallsPerRun
}

// Fatal reports that the model requested a tool that is not enabled for the run;
// further tool use should stop.
func (rt *RunTools) Fatal() bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.fatal
}

// Usage returns the aggregated billable calls so far.
func (rt *RunTools) Usage() []UsageItem {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return AggregateUsage(rt.usage)
}

// ResolveRunTools turns the user's selection into the run's tool set.
func (s *Service) ResolveRunTools(ctx context.Context, selected []uint) (*RunTools, error) {
	if err := s.ValidateSelection(ctx, selected); err != nil {
		return nil, err
	}
	cfg := s.runtime(ctx)
	rt := &RunTools{svc: s, cfg: cfg, bindings: map[string]*binding{}, seen: map[string]*callEntry{}}
	ids := dedupeIDs(selected)
	if len(ids) == 0 || !cfg.Enabled {
		return rt, nil
	}
	var tools []Tool
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&tools).Error; err != nil {
		return nil, database.Translate(err)
	}
	serverIDs := make([]uint, 0, len(tools))
	for _, t := range tools {
		serverIDs = append(serverIDs, t.ServerID)
	}
	servers := map[uint]Server{}
	if len(serverIDs) > 0 {
		var list []Server
		if err := s.db.WithContext(ctx).Where("id IN ?", dedupeIDs(serverIDs)).Find(&list).Error; err != nil {
			return nil, database.Translate(err)
		}
		for _, srv := range list {
			servers[srv.ID] = srv
		}
	}
	sort.SliceStable(tools, func(i, j int) bool {
		a, b := tools[i], tools[j]
		sa, sb := servers[a.ServerID], servers[b.ServerID]
		switch {
		case sa.SortOrder != sb.SortOrder:
			return sa.SortOrder < sb.SortOrder
		case a.ServerID != b.ServerID:
			return a.ServerID < b.ServerID
		case a.SortOrder != b.SortOrder:
			return a.SortOrder < b.SortOrder
		case a.Name != b.Name:
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})

	processors := 0
	for _, t := range tools {
		if t.Status == StatusActive && t.IsImageProcessor() {
			processors++
		}
	}
	if processors > 1 {
		return nil, ErrMultipleProcessors
	}

	used := map[string]bool{}
	for _, t := range tools {
		if t.Status != StatusActive {
			continue
		}
		isProc := t.IsImageProcessor()
		b, err := s.bind(servers, t, cfg)
		if err != nil {
			if isProc {
				return nil, ErrImageProcessing.WithCause(err)
			}
			s.logSkip(t, err)
			continue
		}
		if isProc {
			rt.processor = b
			continue
		}
		base := modelToolName(t.Name)
		if base == "" {
			continue
		}
		name := uniqueModelName(base, used)
		used[name] = true
		b.modelName = name
		rt.bindings[name] = b
		schema := strings.TrimSpace(t.InputSchemaJSON)
		if schema == "" || schema == "null" {
			schema = defaultSchemaText
		}
		rt.specs = append(rt.specs, ToolSpec{Name: name, Description: strings.TrimSpace(t.Description), InputSchema: json.RawMessage(schema)})
	}
	if len(rt.specs) > 0 {
		rt.guidance = guidanceText(cfg.ToolPrompt)
	}
	return rt, nil
}

func (s *Service) logSkip(t Tool, err error) {
	if s.log != nil {
		s.log.Info("mcp_tool_skipped", logx.F("tool_id", t.ID), logx.F("reason", err.Error()))
	}
}

var errServerUnavailable = errors.New("mcp server is missing or inactive")

// bind snapshots what is needed to call a tool.
func (s *Service) bind(servers map[uint]Server, t Tool, cfg RuntimeSettings) (*binding, error) {
	srv, ok := servers[t.ServerID]
	if !ok || srv.Status != StatusActive {
		return nil, errServerUnavailable
	}
	if _, err := checkEndpoint(srv.BaseURL); err != nil {
		return nil, err
	}
	token, err := s.decryptToken(srv.AuthTokenEnc)
	if err != nil {
		return nil, err
	}
	schema := strings.TrimSpace(t.InputSchemaJSON)
	return &binding{
		baseURL: srv.BaseURL, token: token, headers: parseHeadersLenient(srv.HeadersJSON),
		timeout:  time.Duration(cfg.ToolTimeoutSeconds) * time.Second,
		serverID: srv.ID, serverName: srv.Name, toolID: t.ID, toolName: t.Name, displayName: t.DisplayName,
		schema: schema, price: t.PriceNanoUSD, attach: t.attachment(),
	}, nil
}

// CallRequest is one model-requested tool call.
type CallRequest struct {
	// ModelName is the name the model used (a Specs name).
	ModelName string
	// ArgumentsJSON is the model's argument text.
	ArgumentsJSON  string
	UserID         uint
	ConversationID uint
	RequestID      string
}

// Tool types recorded on tool-call rows.
const (
	ToolTypeMCP        = "mcp"
	ToolTypeAttachment = "mcp_attachment"
)

// Call statuses recorded on tool-call rows.
const (
	CallRequested = "requested"
	CallSuccess   = "success"
	CallError     = "error"
)

// CallOutcome is the result of one call, ready to be recorded.
type CallOutcome struct {
	// ToolType is ToolTypeMCP, or ToolTypeAttachment for image processing.
	ToolType   string
	ModelName  string
	ToolName   string
	ServerID   uint
	ServerName string
	Status     string
	// Input is the normalized argument JSON (or an audit summary for images).
	Input string
	// Output is the stored result text on success ("{}" when empty).
	Output string
	// Error is the stored error text on failure.
	Error string
	// ModelOutput is what the model should see: sanitized and bounded.
	ModelOutput string
	LatencyMS   int64
	// Repeated marks an identical call answered from the earlier result.
	Repeated bool
	// Billable is true when the call reached the server and succeeded.
	Billable bool
	// Fatal marks a call to a tool that is not enabled for the run.
	Fatal bool
}

// Call validates and executes one tool call of the run.
func (rt *RunTools) Call(ctx context.Context, req CallRequest) CallOutcome {
	b := rt.bindings[req.ModelName]
	if b == nil {
		rt.mu.Lock()
		rt.fatal = true
		rt.mu.Unlock()
		msg := fmt.Sprintf("tool %q is not enabled for this run", req.ModelName)
		return CallOutcome{ToolType: ToolTypeMCP, ModelName: req.ModelName, ToolName: req.ModelName, Status: CallError, Input: strings.TrimSpace(req.ArgumentsJSON),
			Error: msg, ModelOutput: msg, Fatal: true}
	}
	base := CallOutcome{ToolType: ToolTypeMCP, ModelName: req.ModelName, ToolName: b.toolName, ServerID: b.serverID, ServerName: b.serverName}
	normalized, err := prepareArguments(req.ArgumentsJSON, b.schema)
	if err != nil {
		base.Status, base.Input, base.Error = CallError, strings.TrimSpace(req.ArgumentsJSON), err.Error()
		base.ModelOutput = base.Error
		return base
	}
	base.Input = normalized

	key := fmt.Sprintf("%d\x00%s\x00%s", b.serverID, b.toolName, normalized)
	rt.mu.Lock()
	if e, ok := rt.seen[key]; ok {
		rt.mu.Unlock()
		select {
		case <-e.done:
		case <-ctx.Done():
			base.Status, base.Error = CallError, ctx.Err().Error()
			base.ModelOutput = base.Error
			return base
		}
		out := e.outcome
		out.ModelName = req.ModelName
		out.Repeated, out.Billable, out.LatencyMS = true, false, 0
		return out
	}
	entry := &callEntry{done: make(chan struct{})}
	rt.seen[key] = entry
	rt.mu.Unlock()

	out := rt.execute(ctx, b, base, req, normalized)
	entry.outcome = out
	close(entry.done)
	return out
}

// execute performs the remote call with concurrency limiting, signing and
// retries, and records usage.
func (rt *RunTools) execute(ctx context.Context, b *binding, out CallOutcome, req CallRequest, args string) CallOutcome {
	s := rt.svc
	fail := func(err error) CallOutcome {
		out.Status, out.Error, out.Output = CallError, err.Error(), ""
		out.ModelOutput = trimHeadTail(out.Error, maxModelOutputRune)
		return out
	}
	if s.client == nil {
		return fail(ErrClientUnavailable)
	}
	headers, err := s.signedHeaders(b, req)
	if err != nil {
		return fail(err)
	}
	if err := s.limiter.acquire(ctx, rt.cfg.MaxConcurrentCalls); err != nil {
		return fail(err)
	}
	defer s.limiter.release()

	ep := Endpoint{BaseURL: b.baseURL, Token: b.token, Headers: headers}
	in := CallInput{Name: b.toolName, ArgumentsJSON: args, UserID: req.UserID, ConversationID: req.ConversationID, RequestID: req.RequestID}
	started := s.clock.Now()
	attempts := 1 + rt.cfg.RetryCount
	var result string
	for attempt := 1; ; attempt++ {
		result, err = s.client.CallTool(ctx, ep, in, b.timeout)
		if err == nil || attempt >= attempts || ctx.Err() != nil {
			break
		}
		select {
		case <-time.After(time.Duration(attempt) * retryBackoffStep):
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	out.LatencyMS = s.clock.Now().Sub(started).Milliseconds()
	if err != nil {
		res := fail(err)
		res.LatencyMS = out.LatencyMS
		return res
	}
	if strings.TrimSpace(result) == "" {
		result = "{}"
	}
	out.Status, out.Output, out.Billable = CallSuccess, result, true
	out.ModelOutput = trimHeadTail(SanitizeToolOutput(result), maxModelOutputRune)
	rt.mu.Lock()
	rt.usage = append(rt.usage, UsageItem{ServerID: b.serverID, ServerName: b.serverName, ToolName: b.toolName, Count: 1, PriceNanoUSD: b.price})
	rt.mu.Unlock()
	return out
}

// signedHeaders returns the headers for a call, substituting one freshly
// signed user context for every placeholder value (all get the same token, and
// retries reuse it).
func (s *Service) signedHeaders(b *binding, req CallRequest) (map[string]string, error) {
	headers := make(map[string]string, len(b.headers))
	need := false
	for k, v := range b.headers {
		headers[k] = v
		if isPlaceholder(v) {
			need = true
		}
	}
	if !need {
		return headers, nil
	}
	secret := ""
	if s.signKey != nil {
		secret = s.signKey()
	}
	tok, err := SignUserContext(secret, UserContextClaims{
		UserID: req.UserID, ConversationID: req.ConversationID, RequestID: strings.TrimSpace(req.RequestID),
		Audience: b.baseURL, JTI: newJTI(), ExpiresAt: s.clock.Now().Add(userContextTTL).Unix(),
	})
	if err != nil {
		return nil, fmt.Errorf("user-context signing failed: %w", err)
	}
	for k, v := range headers {
		if isPlaceholder(v) {
			headers[k] = tok
		}
	}
	return headers, nil
}
