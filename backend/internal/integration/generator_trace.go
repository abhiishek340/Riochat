package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/features/conversation"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/llm"
)

// llmCall is the timing record of one upstream call of a run.
type llmCall struct {
	start, end time.Time
	toolCalls  int
	failed     bool
}

func tokenEstimate(s string) int {
	n := len([]rune(s))
	if n == 0 {
		return 0
	}
	return n/charsPerToken + 1
}

func messageChars(msgs []llm.Message) (chars int, hasher string) {
	h := sha256.New()
	for _, m := range msgs {
		chars += len([]rune(m.Content))
		h.Write([]byte(m.Role))
		h.Write([]byte{0})
		h.Write([]byte(m.Content))
		h.Write([]byte{0})
	}
	return chars, hex.EncodeToString(h.Sum(nil))
}

// snapshotPrompt remembers what was sent so the trace can describe it.
func (c *chatRun) snapshotPrompt(msgs []llm.Message, instructions string) {
	chars, fp := messageChars(msgs)
	sum := sha256.Sum256([]byte(instructions + "\n" + fp))
	c.promptMsgCount = len(msgs)
	c.promptHistoryTokens = chars/charsPerToken + 1
	c.promptInstrTokens = tokenEstimate(instructions)
	c.promptFingerprint = hex.EncodeToString(sum[:])
}

func jsonString(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(raw)
}

// buildTrace describes the finished (or failed) run for the process trace and
// the administrators' event list. It returns nil for runs that keep no trace.
func (c *chatRun) buildTrace(failed bool) *conversation.TraceData {
	if c.req.Task != "chat" || c.req.Temporary || c.calls == 0 {
		return nil
	}
	now := time.Now()
	status := "completed"
	if failed {
		status = "error"
	}
	elapsed := now.Sub(c.started)

	// Prompt trace.
	blocks := []conversation.PromptBlock{{Kind: "system", Title: "System instructions", TokenEstimate: c.promptInstrTokens, Cacheable: true, SourceCount: 1}}
	if c.prompt.memoryBlock != "" {
		blocks = append(blocks, conversation.PromptBlock{Kind: "memory", Title: "Memories", TokenEstimate: tokenEstimate(c.prompt.memoryBlock), SourceCount: 1})
	}
	if c.prompt.skillCount > 0 {
		blocks = append(blocks, conversation.PromptBlock{Kind: "skills", Title: "Skills", SourceCount: c.prompt.skillCount})
	}
	if c.tools != nil && c.tools.HasTools() {
		blocks = append(blocks, conversation.PromptBlock{Kind: "tools", Title: "Tools", SourceCount: len(c.tools.Tools())})
	}
	if strings.TrimSpace(c.req.DynamicContext) != "" {
		blocks = append(blocks, conversation.PromptBlock{Kind: "retrieval", Title: "Retrieved context", TokenEstimate: tokenEstimate(c.req.DynamicContext), SourceCount: 1})
	}
	blocks = append(blocks, conversation.PromptBlock{Kind: "history", Title: "Conversation", TokenEstimate: c.promptHistoryTokens, Cacheable: true, SourceCount: c.promptMsgCount})
	total := 0
	for _, b := range blocks {
		total += b.TokenEstimate
	}
	pt := &conversation.PromptTrace{
		Mode: "full", PromptFingerprint: c.promptFingerprint, TotalTokenEstimate: total, SentTokenEstimate: total,
		FullMessageCount: c.promptMsgCount, SentMessageCount: c.promptMsgCount, Blocks: blocks,
	}

	// Stages shown above the answer.
	stages := []map[string]any{{
		"kind": "context_planning", "status": "completed", "label": "Context planning",
		"detail": "Sent " + itoa(c.promptMsgCount) + " messages (about " + itoa(total) + " tokens) with full context",
	}}
	if c.prompt.skillCount > 0 {
		stages = append(stages, map[string]any{
			"kind": "skill_context", "status": "completed", "label": "Skills", "detail": itoa(c.prompt.skillCount) + " skills added to the prompt",
		})
	}
	td := &conversation.TraceData{PromptTrace: pt}
	started := c.started
	td.Process = &conversation.TraceBlock{
		Title: "Process", Summary: "Finished in " + elapsed.Round(time.Millisecond).String(),
		ContentMarkdown: "**Context planning**: " + stages[0]["detail"].(string), Status: status, StartedAt: &started, UpdatedAt: now,
		PayloadJSON: jsonString(map[string]any{"trace_stages": stages}),
	}

	seq := 0
	nextSeq := func() int { seq++; return seq }
	td.Events = append(td.Events, conversation.TraceEvent{
		EventID: "planning", EventType: "process_stage", Phase: "process", Stage: "context_planning", Title: "Context planning",
		Summary: stages[0]["detail"].(string), Status: "completed", Seq: nextSeq(), StartedAt: started, UpdatedAt: now,
		PayloadJSON: jsonString(map[string]any{"trace_stages": stages}),
	})
	if r := strings.TrimSpace(c.reasoning.String()); r != "" {
		end := now
		td.UpstreamThink = &conversation.TraceBlock{
			Title: "Deep reasoning", Summary: truncateRunes(r, 120), ContentMarkdown: r, Status: "completed",
			RoundID: "think-1", StartedAt: &started, UpdatedAt: now,
		}
		td.Events = append(td.Events, conversation.TraceEvent{
			EventID: "think-1", EventType: "upstream_think", Phase: "think", Stage: "think", RoundID: "think-1",
			Title: "Deep reasoning", Summary: truncateRunes(r, 120), ContentMarkdown: r, Status: "completed",
			Seq: nextSeq(), StartedAt: started, EndedAt: &end, UpdatedAt: now,
		})
	}
	for i, call := range c.callLog {
		end := call.end
		if end.IsZero() {
			end = now
		}
		st := "completed"
		if call.failed {
			st = "error"
		}
		td.Events = append(td.Events, conversation.TraceEvent{
			EventID: "llm-call-" + itoa(i+1), EventType: "llm_call", Phase: "model", Stage: "model_call",
			Title: "Model call " + itoa(i+1), Summary: c.route.PlatformModelName, Status: st, Seq: nextSeq(),
			StartedAt: call.start, EndedAt: &end, UpdatedAt: end,
			PayloadJSON: jsonString(map[string]any{"platform_model_name": c.route.PlatformModelName, "tool_calls": call.toolCalls}),
		})
	}
	if len(c.toolCalls) > 0 {
		td.ToolCalls = c.toolCalls
		var list []map[string]any
		for _, tc := range c.toolCalls {
			item := map[string]any{"tool_call_id": tc.ToolCallID, "name": tc.ToolName, "type": "mcp", "status": tc.Status}
			if tc.InputJSON != "" {
				item["input"] = rawOrString(tc.InputJSON)
			}
			if tc.OutputJSON != "" {
				item["output"] = rawOrString(tc.OutputJSON)
			}
			if tc.ErrorJSON != "" {
				item["error"] = rawOrString(tc.ErrorJSON)
			}
			list = append(list, item)
		}
		payload := jsonString(map[string]any{"tool_calls": list})
		td.Tools = &conversation.TraceBlock{
			Title: "Tools", Summary: itoa(len(c.toolCalls)) + " tool calls", Status: status, StartedAt: &started, UpdatedAt: now,
		}
		td.Events = append(td.Events, conversation.TraceEvent{
			EventID: "tools-1", EventType: "tool_call", Phase: "tool", Stage: "tool", Title: "Tool calls",
			Summary: itoa(len(c.toolCalls)) + " tool calls", Status: status, Seq: nextSeq(), StartedAt: started, UpdatedAt: now,
			PayloadJSON: payload,
		})
	}
	return td
}

func rawOrString(s string) any {
	var v any
	if json.Unmarshal([]byte(s), &v) == nil {
		return v
	}
	return s
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func itoa(n int) string { return strconv.Itoa(n) }
