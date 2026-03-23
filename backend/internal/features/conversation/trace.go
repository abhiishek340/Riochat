package conversation

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

// TraceBlock is a titled section of the process trace.
type TraceBlock struct {
	Title           string     `json:"title"`
	Summary         string     `json:"summary"`
	ContentMarkdown string     `json:"contentMarkdown"`
	Status          string     `json:"status"`
	Stage           string     `json:"stage,omitempty"`
	RoundID         string     `json:"roundID,omitempty"`
	ParentEventID   string     `json:"parentEventID,omitempty"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	PayloadJSON     string     `json:"payloadJSON,omitempty"`
}

// TraceEvent is one step of the process trace.
type TraceEvent struct {
	EventID         string     `json:"eventID"`
	EventType       string     `json:"eventType"`
	Phase           string     `json:"phase"`
	Stage           string     `json:"stage,omitempty"`
	RoundID         string     `json:"roundID,omitempty"`
	ParentEventID   string     `json:"parentEventID,omitempty"`
	Title           string     `json:"title"`
	Summary         string     `json:"summary"`
	ContentMarkdown string     `json:"contentMarkdown"`
	Status          string     `json:"status"`
	Seq             int        `json:"seq"`
	StartedAt       time.Time  `json:"startedAt"`
	EndedAt         *time.Time `json:"endedAt,omitempty"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	PayloadJSON     string     `json:"payloadJSON,omitempty"`
}

// SourceRef points at the source behind a prompt block.
type SourceRef struct {
	SourceType string `json:"sourceType"`
	SourceID   string `json:"sourceID"`
	Title      string `json:"title"`
	ArtifactID *uint  `json:"artifactID,omitempty"`
}

// PromptBlock is one section of the assembled prompt.
type PromptBlock struct {
	Kind          string      `json:"kind"`
	Title         string      `json:"title"`
	TokenEstimate int         `json:"tokenEstimate"`
	Cacheable     bool        `json:"cacheable"`
	SourceCount   int         `json:"sourceCount"`
	SourceRefs    []SourceRef `json:"sourceRefs,omitempty"`
}

// PromptTrace describes how the prompt was assembled.
type PromptTrace struct {
	Mode                  string        `json:"mode"`
	PromptFingerprint     string        `json:"promptFingerprint"`
	StatefulUsed          bool          `json:"statefulUsed"`
	StatefulDisabledReson string        `json:"statefulDisabledReason"`
	TotalTokenEstimate    int           `json:"totalTokenEstimate"`
	SentTokenEstimate     int           `json:"sentTokenEstimate"`
	FullMessageCount      int           `json:"fullMessageCount"`
	SentMessageCount      int           `json:"sentMessageCount"`
	StatefulSavedMessages int           `json:"statefulSavedMessages"`
	StatefulSavedTokens   int           `json:"statefulSavedTokens"`
	Blocks                []PromptBlock `json:"blocks"`
}

// ToolCallRecord is the stored detail of one tool call.
type ToolCallRecord struct {
	ToolCallID string
	ToolName   string
	Status     string
	MCPServer  string
	InputJSON  string
	OutputJSON string
	ErrorJSON  string
}

// ContextArtifactInput is prompt-context evidence kept for the prompt trace.
type ContextArtifactInput struct {
	Kind        string
	SourceType  string
	SourceID    string
	SourceTitle string
	Content     string
	// ContentHash is SHA-256 over the kind, source id and full content; it is
	// computed from Content when empty.
	ContentHash   string
	TokenEstimate int
	Score         float64
	MetadataJSON  string
}

// TraceData is the trace a generator returns.
type TraceData struct {
	Process       *TraceBlock
	Tools         *TraceBlock
	UpstreamThink *TraceBlock
	PromptTrace   *PromptTrace
	Events        []TraceEvent
	ToolCalls     []ToolCallRecord
	Artifacts     []ContextArtifactInput
}

// ProcessTrace is the processTrace object of a message.
type ProcessTrace struct {
	Enabled       bool         `json:"enabled"`
	Status        string       `json:"status"`
	Process       *TraceBlock  `json:"process,omitempty"`
	Tools         *TraceBlock  `json:"tools,omitempty"`
	UpstreamThink *TraceBlock  `json:"upstreamThink,omitempty"`
	PromptTrace   *PromptTrace `json:"promptTrace,omitempty"`
	Events        []TraceEvent `json:"events,omitempty"`
	DetailOmitted bool         `json:"detailOmitted,omitempty"`
}

const (
	blockProcess       = "process"
	blockTools         = "tools"
	blockUpstreamThink = "upstream_think"
	blockPrompt        = "prompt_trace"

	summaryThinkRunes   = 400
	toolPayloadMaxBytes = 64 * 1024
)

// buildTrace assembles the processTrace of one run from its stored rows. It
// returns nil when the run has no trace data.
func buildTrace(rows []RunEvent, status string) *ProcessTrace {
	if len(rows) == 0 {
		return nil
	}
	t := &ProcessTrace{Enabled: true, Status: status}
	found := false
	for _, r := range rows {
		switch r.EventScope {
		case scopeTraceBlock:
			switch r.EventID {
			case blockProcess:
				t.Process = rowToBlock(r)
				found = true
			case blockTools:
				t.Tools = rowToBlock(r)
				found = true
			case blockUpstreamThink:
				t.UpstreamThink = rowToBlock(r)
				found = true
			case blockPrompt:
				var pt PromptTrace
				if json.Unmarshal([]byte(r.PayloadJSON), &pt) == nil {
					if pt.Blocks == nil {
						pt.Blocks = []PromptBlock{}
					}
					t.PromptTrace = &pt
					found = true
				}
			}
		case scopeTraceEvent:
			t.Events = append(t.Events, rowToEvent(r))
			found = true
		}
	}
	if !found {
		return nil
	}
	return t
}

func rowToBlock(r RunEvent) *TraceBlock {
	return &TraceBlock{
		Title: r.Title, Summary: r.Summary, ContentMarkdown: r.ContentMarkdown, Status: r.Status,
		Stage: r.Stage, RoundID: r.RoundID, ParentEventID: r.ParentEventID,
		StartedAt: r.StartedAt, UpdatedAt: r.UpdatedAt, PayloadJSON: r.PayloadJSON,
	}
}

func rowToEvent(r RunEvent) TraceEvent {
	ev := TraceEvent{
		EventID: r.EventID, EventType: r.EventType, Phase: r.Phase, Stage: r.Stage, RoundID: r.RoundID,
		ParentEventID: r.ParentEventID, Title: r.Title, Summary: r.Summary, ContentMarkdown: r.ContentMarkdown,
		Status: r.Status, Seq: r.Seq, EndedAt: r.EndedAt, UpdatedAt: r.UpdatedAt, PayloadJSON: r.PayloadJSON,
	}
	if r.StartedAt != nil {
		ev.StartedAt = *r.StartedAt
	}
	return ev
}

// traceRows converts generator trace data into storable rows for a run.
func traceRows(runID string, td *TraceData, now time.Time) []RunEvent {
	if td == nil {
		return nil
	}
	var rows []RunEvent
	addBlock := func(id string, b *TraceBlock) {
		if b == nil {
			return
		}
		rows = append(rows, RunEvent{
			RunID: runID, EventScope: scopeTraceBlock, EventID: id, Title: b.Title, Summary: b.Summary,
			ContentMarkdown: b.ContentMarkdown, Status: b.Status, Stage: b.Stage, RoundID: b.RoundID,
			ParentEventID: b.ParentEventID, StartedAt: b.StartedAt, PayloadJSON: b.PayloadJSON,
		})
	}
	addBlock(blockProcess, td.Process)
	addBlock(blockTools, td.Tools)
	addBlock(blockUpstreamThink, td.UpstreamThink)
	if td.PromptTrace != nil {
		raw, _ := json.Marshal(td.PromptTrace)
		rows = append(rows, RunEvent{RunID: runID, EventScope: scopeTraceBlock, EventID: blockPrompt, Title: "prompt", Status: "success", PayloadJSON: string(raw)})
	}
	for i, ev := range td.Events {
		id := ev.EventID
		if id == "" {
			id = "event-" + itoa(i+1)
		}
		started := ev.StartedAt
		if started.IsZero() {
			started = now
		}
		seq := ev.Seq
		if seq == 0 {
			seq = i + 1
		}
		var latency int64
		if ev.EndedAt != nil && ev.EndedAt.After(started) {
			latency = ev.EndedAt.Sub(started).Milliseconds()
		}
		rows = append(rows, RunEvent{
			LatencyMS: latency,
			RunID:     runID, EventScope: scopeTraceEvent, EventID: id, ParentEventID: ev.ParentEventID,
			EventType: ev.EventType, Phase: ev.Phase, Stage: ev.Stage, RoundID: ev.RoundID, Title: ev.Title,
			Summary: ev.Summary, ContentMarkdown: ev.ContentMarkdown, Status: ev.Status, Seq: seq,
			PayloadJSON: ev.PayloadJSON, StartedAt: &started, EndedAt: ev.EndedAt,
		})
	}
	for i, tc := range td.ToolCalls {
		id := tc.ToolCallID
		if id == "" {
			id = "call-" + itoa(i+1)
		}
		rows = append(rows, RunEvent{
			RunID: runID, EventScope: scopeToolCall, EventID: id, ToolCallID: id, ToolName: tc.ToolName,
			Status: tc.Status, MCPServerSnapshot: tc.MCPServer, InputJSON: tc.InputJSON,
			OutputJSON: tc.OutputJSON, ErrorJSON: tc.ErrorJSON, Seq: i + 1,
		})
	}
	return rows
}

// summarizeTrace returns the lightweight form used by message lists.
func summarizeTrace(t *ProcessTrace) *ProcessTrace {
	if t == nil {
		return nil
	}
	out := *t
	out.DetailOmitted = true
	if t.Tools != nil {
		b := *t.Tools
		b.PayloadJSON = ""
		out.Tools = &b
	}
	if t.UpstreamThink != nil {
		b := *t.UpstreamThink
		b.ContentMarkdown = truncateRunes(b.ContentMarkdown, summaryThinkRunes)
		b.PayloadJSON = ""
		out.UpstreamThink = &b
	}
	if t.Process != nil {
		b := *t.Process
		b.PayloadJSON = ""
		out.Process = &b
	}
	out.Events = nil
	for _, ev := range t.Events {
		if ev.Phase == "process" || ev.EventType == "process_stage" {
			continue
		}
		ev.PayloadJSON = ""
		out.Events = append(out.Events, ev)
	}
	return &out
}

// ----- payload sanitising -----

func normKey(k string) string {
	k = strings.ToLower(k)
	k = strings.ReplaceAll(k, "_", "")
	return strings.ReplaceAll(k, "-", "")
}

// scrubMode says how strictly payloads are cleaned.
type scrubMode int

const (
	scrubOwner  scrubMode = iota // the owner's own view: upstream names only
	scrubPublic                  // anonymous share readers
	scrubClone                   // copies made for another account or conversation
)

var publicOnlyKeys = map[string]bool{
	"upstreamdebug": true, "authorization": true, "proxyauthorization": true, "cookie": true,
	"setcookie": true, "citations": true, "fileid": true, "filename": true, "filenames": true,
	"preview": true,
}

// cloneOnlyKeys are removed from stored copies: debug data, credentials and
// the identifiers of tool calls whose rows are not copied.
var cloneOnlyKeys = map[string]bool{
	"upstreamdebug": true, "authorization": true, "proxyauthorization": true, "cookie": true,
	"setcookie": true, "toolcallid": true,
}

func secretKey(n string) bool {
	return strings.Contains(n, "apikey") || strings.Contains(n, "secretkey") || strings.Contains(n, "accesskey")
}

func dropKey(n string, mode scrubMode) bool {
	switch mode {
	case scrubPublic:
		return publicOnlyKeys[n] || secretKey(n)
	case scrubClone:
		return cloneOnlyKeys[n] || secretKey(n)
	}
	return false
}

// sanitizePayload removes internal fields from a JSON payload string. With
// public set it also removes credentials and file references. Invalid JSON is
// returned as is for the owner and dropped for public readers.
func sanitizePayload(raw string, public bool) string {
	if public {
		return sanitizePayloadFor(raw, scrubPublic)
	}
	return sanitizePayloadFor(raw, scrubOwner)
}

func sanitizePayloadFor(raw string, mode scrubMode) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		if mode != scrubOwner {
			return ""
		}
		return raw
	}
	v = scrub(v, mode, false)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return ""
	}
	return strings.TrimRight(buf.String(), "\n")
}

func scrub(v any, mode scrubMode, underUpstream bool) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			n := normKey(k)
			if n == "upstreamname" {
				continue
			}
			if underUpstream && (n == "name" || n == "displayname") {
				continue
			}
			if dropKey(n, mode) {
				continue
			}
			out[k] = scrub(val, mode, n == "upstream")
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = scrub(val, mode, false)
		}
		return out
	default:
		return v
	}
}

// sanitizeTrace cleans every payload of a trace for the owner or the public.
func sanitizeTrace(t *ProcessTrace, public bool) *ProcessTrace {
	if t == nil {
		return nil
	}
	out := *t
	clean := func(b *TraceBlock) *TraceBlock {
		if b == nil {
			return nil
		}
		c := *b
		c.PayloadJSON = sanitizePayload(c.PayloadJSON, public)
		return &c
	}
	out.Process, out.Tools, out.UpstreamThink = clean(t.Process), clean(t.Tools), clean(t.UpstreamThink)
	out.Events = nil
	for _, ev := range t.Events {
		ev.PayloadJSON = sanitizePayload(ev.PayloadJSON, public)
		out.Events = append(out.Events, ev)
	}
	if public {
		out.DetailOmitted = false
	}
	return &out
}

func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// compactJSON returns the compact form of a JSON document without HTML escaping.
func compactJSON(raw []byte) (string, bool) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return "", false
	}
	return buf.String(), true
}

// marshalNoEscape marshals without HTML escaping.
func marshalNoEscape(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return ""
	}
	return strings.TrimRight(buf.String(), "\n")
}
