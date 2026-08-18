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
