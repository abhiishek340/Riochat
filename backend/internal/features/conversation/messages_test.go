package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// chain creates n alternating user/assistant messages via the API.
func chainMessages(e *tenv, hdr map[string]string, conv string, turns int) (users, assts []map[string]any) {
	e.t.Helper()
	for i := 0; i < turns; i++ {
		u, a := pair(e.send(hdr, conv, fmt.Sprintf("q%d", i+1), nil))
		users, assts = append(users, u), append(assts, a)
	}
	return
}

func TestListMessagesModes(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	chainMessages(e, e.u1, id, 5) // 10 messages
	w := e.call("GET", "/conversations/"+id+"/messages?page=1&page_size=4", e.u1, nil)
	total, res := results(t, w)
	if total != 10 || len(res) != 4 || str(res[0], "content") != "q1" {
		t.Fatalf("classic: %d %d", total, len(res))
	}
	w = e.call("GET", "/conversations/"+id+"/messages?page=3&page_size=4", e.u1, nil)
	if total, res = results(t, w); total != 10 || len(res) != 2 {
		t.Fatalf("page 3: %d %d", total, len(res))
	}
	// tail: the last N ascending
	w = e.call("GET", "/conversations/"+id+"/messages?tail=true&page_size=3", e.u1, nil)
	total, res = results(t, w)
	if total != 10 || len(res) != 3 || num(res[0], "id") >= num(res[1], "id") || num(res[2], "id") != num(res[2], "id") {
		t.Fatalf("tail: %v", res)
	}
	lastAll := e.list(id)
	if str(res[2], "publicID") != str(lastAll[9], "publicID") || str(res[0], "publicID") != str(lastAll[7], "publicID") {
		t.Fatalf("tail contents")
	}
	// before_id
	before := int(num(lastAll[5], "id"))
	w = e.call("GET", fmt.Sprintf("/conversations/%s/messages?before_id=%d&page_size=3", id, before), e.u1, nil)
	total, res = results(t, w)
	if total != 10 || len(res) != 3 || str(res[0], "publicID") != str(lastAll[2], "publicID") || str(res[2], "publicID") != str(lastAll[4], "publicID") {
		t.Fatalf("before_id: %d %v", total, res)
	}
	// before_id wins over tail
	w = e.call("GET", fmt.Sprintf("/conversations/%s/messages?before_id=%d&tail=true&page_size=2", id, before), e.u1, nil)
	if _, res = results(t, w); str(res[1], "publicID") != str(lastAll[4], "publicID") {
		t.Fatalf("before_id precedence")
	}
	for _, bad := range []string{"0", "-3", "abc", "1.5"} {
		e.expectErr("GET", "/conversations/"+id+"/messages?before_id="+bad, e.u1, nil, 400, "before_message.invalid_id")
	}
	// page_size clamps
	w = e.call("GET", "/conversations/"+id+"/messages?page_size=5000", e.u1, nil)
	if _, res = results(t, w); len(res) != 10 {
		t.Fatalf("clamp: %d", len(res))
	}
	// empty conversation
	empty := e.newConv(e.u1, "e", "")
	w = e.call("GET", "/conversations/"+empty+"/messages", e.u1, nil)
	if total, res = results(t, w); total != 0 || res == nil || len(res) != 0 {
		t.Fatalf("empty: %d %v", total, res)
	}
}

func TestMessageFieldsAndAttachmentsString(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	chainMessages(e, e.u1, id, 1)
	msgs := e.list(id)
	for _, k := range []string{"id", "conversationID", "userID", "publicID", "parentMessageID", "runID", "role", "contentType", "content", "branchReason",
		"sourceMessageID", "tokenUsage", "inputTokens", "outputTokens", "cacheReadTokens", "cacheWriteTokens", "reasoningTokens", "reasoningEffort", "latencyMS",
		"status", "errorCode", "errorMessage", "errorRequestID", "attachments", "platformModelName", "upstreamModelName", "modelVendor", "modelIcon",
		"parentPublicID", "sourcePublicID", "myFeedback", "thumbsUpCount", "thumbsDownCount", "editedAt", "createdAt", "updatedAt"} {
		if _, ok := msgs[1][k]; !ok {
			t.Errorf("missing field %q", k)
		}
	}
	if s, ok := msgs[1]["attachments"].(string); !ok || s != "[]" {
		t.Fatalf("attachments: %#v", msgs[1]["attachments"])
	}
	if msgs[0]["parentMessageID"] != nil || msgs[0]["sourceMessageID"] != nil || msgs[0]["reasoningEffort"] != nil {
		t.Fatalf("nullable fields: %v", msgs[0])
	}
}

func TestMessageModelFallbackToConversation(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "claude-sonnet")
	conv := e.convRow(id)
	e.seedMsg(conv, roleUser, "x", msgSuccess, nil)
	rows := e.list(id)
	if str(rows[0], "platformModelName") != "claude-sonnet" {
		t.Fatalf("fallback model: %v", rows[0])
	}
}

func traceGenerator() func(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
	return func(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task != taskChat {
			return defaultGenerate(ctx, req, emit)
		}
		now := time.Now()
		return GenerateResult{
			Content: "answer", Usage: Usage{Input: 3, Output: 2},
			Trace: &TraceData{
				Process:       &TraceBlock{Title: "Process", Status: "success", PayloadJSON: `{"upstreamName":"secret-up","keep":1}`},
				Tools:         &TraceBlock{Title: "Tools", Status: "success", PayloadJSON: `{"calls":[{"name":"x"}]}`},
				UpstreamThink: &TraceBlock{Title: "Think", Status: "success", ContentMarkdown: strings.Repeat("t", 1000)},
				PromptTrace:   &PromptTrace{Mode: "full", TotalTokenEstimate: 10, Blocks: []PromptBlock{{Kind: "history", Title: "h", TokenEstimate: 5}}},
				Events: []TraceEvent{
					{EventID: "e1", EventType: "process_stage", Phase: "process", Title: "stage", Status: "success", StartedAt: now},
					{EventID: "e2", EventType: "tool_call", Phase: "tool", Title: "tool", Status: "success", StartedAt: now, PayloadJSON: `{"authorization":"Bearer zzz","apiKey":"k","x":1}`},
				},
				ToolCalls: []ToolCallRecord{{ToolCallID: "call-1", ToolName: "search", Status: "success", OutputJSON: `{"r":1}`},
					{ToolCallID: "call-big", ToolName: "dump", Status: "success", OutputJSON: `"` + strings.Repeat("z", 100*1024) + `"`}},
				Artifacts: []ContextArtifactInput{{Kind: "kb", SourceType: "knowledge", SourceID: "k1", SourceTitle: "Doc", Content: "evidence", TokenEstimate: 4, Score: 0.9, MetadataJSON: `{"a":1}`}},
			},
			KnowledgeSources: []KnowledgeSource{{FileName: "f.txt", FileID: "file_x", ChunkIndex: 2, Score: 0.5, Preview: "prev"}},
		}, nil
	}
}

func TestTraceSummaryVsFull(t *testing.T) {
	e := newEnv(t)
	e.gen.set(traceGenerator())
	id := e.newConv(e.u1, "t", "gpt-4o")
	_, a := pair(e.send(e.u1, id, "q", nil))
	pt, _ := a["processTrace"].(map[string]any)
	if pt == nil || pt["enabled"] != true || pt["promptTrace"] == nil || len(pt["events"].([]any)) != 2 {
		t.Fatalf("full trace in send result: %v", a["processTrace"])
	}
	if strings.Contains(fmt.Sprint(pt["process"]), "secret-up") {
		t.Fatalf("upstreamName must be stripped for the owner: %v", pt["process"])
	}
	if ks, _ := a["knowledgeSources"].([]any); len(ks) != 1 {
		t.Fatalf("knowledgeSources: %v", a["knowledgeSources"])
	}
	// summary
	w := e.call("GET", "/conversations/"+id+"/messages?trace=summary", e.u1, nil)
	_, res := results(t, w)
	st := res[1]["processTrace"].(map[string]any)
	if st["detailOmitted"] != true {
		t.Fatalf("detailOmitted: %v", st)
	}
	if think := st["upstreamThink"].(map[string]any); len([]rune(str(think, "contentMarkdown"))) >= 1000 {
		t.Fatalf("think must be truncated")
	}
	evs := st["events"].([]any)
	if len(evs) != 1 || evs[0].(map[string]any)["eventID"] != "e2" {
		t.Fatalf("process-stage events dropped in summary: %v", evs)
	}
	// full via /messages/{id}/trace
	m := e.ok("GET", "/messages/"+str(a, "publicID")+"/trace", e.u1, nil)
	ft := m["processTrace"].(map[string]any)
	if ft["detailOmitted"] != nil || len(ft["events"].([]any)) != 2 {
		t.Fatalf("full trace: %v", ft)
	}
	e.expectErr("GET", "/messages/"+str(a, "publicID")+"/trace", e.u2, nil, 404, "message.not_found")
	e.expectErr("GET", "/messages/nope/trace", e.u1, nil, 404, "message.not_found")
	// errored messages keep full trace in summary mode
	conv := e.convRow(id)
	run := Run{RunID: "run_err", UserID: 1, ConversationID: conv.ID, Status: runError, StartedAt: time.Now()}
	e.DB.Create(&run)
	em := e.seedMsg(conv, roleAssistant, "", msgError, nil)
	e.DB.Model(em).Update("run_id", "run_err")
	e.DB.Create(&RunEvent{RunID: "run_err", EventScope: scopeTraceEvent, EventID: "pe", EventType: "process_stage", Phase: "process", Title: "p", Status: "error", Seq: 1})
	w = e.call("GET", "/conversations/"+id+"/messages?trace=summary&tail=true&page_size=1", e.u1, nil)
	_, res = results(t, w)
	et := res[0]["processTrace"].(map[string]any)
	if et["detailOmitted"] == true || len(et["events"].([]any)) != 1 {
		t.Fatalf("error messages keep the full trace: %v", et)
	}
}

func TestToolCallDetailAndArtifact(t *testing.T) {
	e := newEnv(t)
	e.gen.set(traceGenerator())
	id := e.newConv(e.u1, "t", "gpt-4o")
	_, a := pair(e.send(e.u1, id, "q", map[string]any{"clientRunID": "tc1"}))
	run := str(a, "runID")
	m := e.ok("GET", "/conversation-runs/"+run+"/tool-calls/call-1", e.u1, nil)
	if str(m, "toolName") != "search" || str(m, "outputJSON") != `{"r":1}` || m["outputOmitted"] != false || num(m, "outputSizeBytes") != 7 {
		t.Fatalf("tool call: %v", m)
	}
	m = e.ok("GET", "/conversation-runs/"+run+"/tool-calls/call-big", e.u1, nil)
	if m["outputOmitted"] != true || str(m, "outputJSON") != "" || num(m, "outputSizeBytes") < 100*1024 {
		t.Fatalf("oversized output omitted: %v", fmt.Sprint(m["outputOmitted"], m["outputSizeBytes"], len(str(m, "outputJSON"))))
	}
	e.expectErr("GET", "/conversation-runs/"+run+"/tool-calls/nope", e.u1, nil, 404, "tool_call.not_found")
	e.expectErr("GET", "/conversation-runs/"+run+"/tool-calls/call-1", e.u2, nil, 404, "tool_call.not_found")
	e.expectErr("GET", "/conversation-runs/"+strings.Repeat("r", 65)+"/tool-calls/call-1", e.u1, nil, 400, "run.invalid_id")
	e.expectErr("GET", "/conversation-runs/%20/tool-calls/call-1", e.u1, nil, 400, "run.invalid_id")
	e.expectErr("GET", "/conversation-runs/"+run+"/tool-calls/%20", e.u1, nil, 400, "tool_call.invalid_id")

	// artifacts
	var rec ContextRecord
	if err := e.DB.Where("record_type = ?", recordArtifact).First(&rec).Error; err != nil {
		t.Fatalf("artifact not stored: %v", err)
	}
	if rec.UserID != 1 {
		// ownership is derived from the run's message; fix up for the check
		e.DB.Model(&rec).Updates(map[string]any{"user_id": 1})
	}
	art := e.ok("GET", fmt.Sprintf("/context-artifacts/%d", rec.ID), e.u1, nil)
	if str(art, "content") != "evidence" || str(art, "kind") != "kb" || str(art, "sourceTitle") != "Doc" {
		t.Fatalf("artifact: %v", art)
	}
	e.expectErr("GET", fmt.Sprintf("/context-artifacts/%d", rec.ID), e.u2, nil, 404, "context_artifact.not_found")
	e.expectErr("GET", "/context-artifacts/0", e.u1, nil, 400, "context_artifact.invalid_id")
	e.expectErr("GET", "/context-artifacts/abc", e.u1, nil, 400, "context_artifact.invalid_id")
	e.expectErr("GET", "/context-artifacts/99999", e.u1, nil, 404, "context_artifact.not_found")
}

func TestPreviewMessages(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	w := e.call("GET", "/conversations/"+id+"/messages/preview", e.u1, nil)
	if l := dataList(t, w); l == nil || len(l) != 0 {
		t.Fatalf("empty preview must be []: %s", w.Body)
	}
	chainMessages(e, e.u1, id, 7) // 14 messages on one branch
	l := dataList(t, e.call("GET", "/conversations/"+id+"/messages/preview", e.u1, nil))
	if len(l) != 10 {
		t.Fatalf("preview size: %d", len(l))
	}
	if str(l[0], "content") != "q3" || !strings.HasPrefix(str(l[9], "content"), "Hello world") {
		t.Fatalf("preview window: first=%q last=%q", str(l[0], "content"), str(l[9], "content"))
	}
	for _, k := range []string{"publicID", "role", "content", "errorMessage"} {
		if _, ok := l[0][k]; !ok {
			t.Fatalf("preview field %s", k)
		}
	}
	e.expectErr("GET", "/conversations/"+id+"/messages/preview", e.u2, nil, 404, "conversation.not_found")
}
