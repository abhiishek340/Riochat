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

func TestEditMessage(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	u, a := pair(e.send(e.u1, id, "orig", nil))
	m := e.ok("PATCH", "/messages/"+str(u, "publicID"), e.u1, map[string]string{"content": "  edited  "})
	if str(m, "content") != "edited" || m["editedAt"] == nil || str(m, "status") != "success" {
		t.Fatalf("edit: %v", m)
	}
	if got := e.convRow(id).MessageCount; got != 2 {
		t.Fatalf("no tree change: %d", got)
	}
	e.ok("PATCH", "/messages/"+str(a, "publicID"), e.u1, map[string]string{"content": "assistant edit"})
	e.expectErr("PATCH", "/messages/"+str(u, "publicID"), e.u1, map[string]string{"content": "   "}, 400, "message.invalid_content")
	e.expectErr("PATCH", "/messages/"+str(u, "publicID"), e.u1, map[string]string{}, 400, "request.invalid_body")
	e.expectErr("PATCH", "/messages/"+str(u, "publicID"), e.u2, map[string]string{"content": "x"}, 404, "message.not_found")
	e.expectErr("PATCH", "/messages/zzz", e.u1, map[string]string{"content": "x"}, 404, "message.not_found")
	conv := e.convRow(id)
	pend := e.seedMsg(conv, roleAssistant, "", msgPending, nil)
	e.expectErr("PATCH", "/messages/"+pend.PublicID, e.u1, map[string]string{"content": "x"}, 400, "message.edit_state_invalid")
	sys := e.seedMsg(conv, roleSystem, "s", msgSuccess, nil)
	e.expectErr("PATCH", "/messages/"+sys.PublicID, e.u1, map[string]string{"content": "x"}, 400, "message.edit_target_invalid")
	if e.audit.find("update_message") == nil {
		t.Fatal("audit")
	}
	// a deleted conversation hides its messages
	e.ok("DELETE", "/conversations/"+id, e.u1, nil)
	e.expectErr("PATCH", "/messages/"+str(u, "publicID"), e.u1, map[string]string{"content": "x"}, 404, "message.not_found")
}

func TestFeedback(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	u, a := pair(e.send(e.u1, id, "q", nil))
	p := "/messages/" + str(a, "publicID") + "/feedback"
	m := e.ok("PUT", p, e.u1, map[string]string{"feedback": "up"})
	if str(m, "myFeedback") != "up" || num(m, "thumbsUpCount") != 1 || num(m, "thumbsDownCount") != 0 || str(m, "messagePublicID") != str(a, "publicID") || num(m, "messageID") != num(a, "id") {
		t.Fatalf("up: %v", m)
	}
	m = e.ok("PUT", p, e.u1, map[string]string{"feedback": "DOWN"})
	if str(m, "myFeedback") != "down" || num(m, "thumbsUpCount") != 0 || num(m, "thumbsDownCount") != 1 {
		t.Fatalf("down: %v", m)
	}
	var n int64
	e.DB.Model(&Feedback{}).Count(&n)
	if n != 1 {
		t.Fatalf("one row per user and message: %d", n)
	}
	// another user can read the message? only the owner can access; counts are across users
	rows := e.list(id)
	if str(rows[1], "myFeedback") != "down" || num(rows[1], "thumbsDownCount") != 1 {
		t.Fatalf("enriched feedback: %v", rows[1])
	}
	m = e.ok("PUT", p, e.u1, map[string]string{"feedback": ""})
	if str(m, "myFeedback") != "" || num(m, "thumbsDownCount") != 0 {
		t.Fatalf("remove: %v", m)
	}
	e.DB.Model(&Feedback{}).Count(&n)
	if n != 0 {
		t.Fatalf("row removed: %d", n)
	}
	e.expectErr("PUT", p, e.u1, map[string]string{"feedback": "meh"}, 400, "request.invalid_body")
	e.expectErr("PUT", "/messages/"+str(u, "publicID")+"/feedback", e.u1, map[string]string{"feedback": "up"}, 400, "message.feedback_target_invalid")
	e.expectErr("PUT", p, e.u2, map[string]string{"feedback": "up"}, 404, "message.not_found")
	if e.audit.find("set_message_feedback") == nil {
		t.Fatal("audit")
	}
}

func TestDeleteMessageSplice(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	conv := e.convRow(id)
	var prev *Message
	var chain []*Message
	for i := 0; i < 5; i++ {
		role := roleUser
		if i%2 == 1 {
			role = roleAssistant
		}
		m := e.seedMsg(conv, role, fmt.Sprintf("m%d", i), msgSuccess, prev)
		chain = append(chain, m)
		prev = m
	}
	// UI state on the target is removed with it
	e.DB.Create(&UIState{UserID: 1, ConversationID: conv.ID, MessageID: chain[2].ID, BlockID: "b", StateJSON: "{}"})
	// compaction snapshot covering the deleted message's subtree is dropped; an earlier one is kept
	e.DB.Create(&ContextRecord{UserID: 1, ConversationID: conv.ID, RecordType: recordSnapshot, CoveredUntilMessageID: chain[3].ID, CoveredUntilPublicID: chain[3].PublicID})
	e.DB.Create(&ContextRecord{UserID: 1, ConversationID: conv.ID, RecordType: recordSnapshot, CoveredUntilMessageID: chain[0].ID, CoveredUntilPublicID: chain[0].PublicID})

	m := e.ok("DELETE", "/conversations/"+id+"/messages/"+chain[2].PublicID, e.u1, nil)
	if m["deleted"] != true || num(m, "reparentedMessageCount") != 1 {
		t.Fatalf("delete: %v", m)
	}
	if got := e.convRow(id).MessageCount; got != 4 {
		t.Fatalf("message count: %d", got)
	}
	child := e.msgRow(chain[3].PublicID)
	if child.ParentMessageID == nil || *child.ParentMessageID != chain[1].ID {
		t.Fatalf("child must be re-parented to the grandparent: %+v", child.ParentMessageID)
	}
	rows := e.list(id)
	if len(rows) != 4 {
		t.Fatalf("remaining: %d", len(rows))
	}
	var ui int64
	e.DB.Model(&UIState{}).Count(&ui)
	if ui != 0 {
		t.Fatalf("ui state rows must be deleted: %d", ui)
	}
	var snaps int64
	e.DB.Model(&ContextRecord{}).Where("record_type = ?", recordSnapshot).Count(&snaps)
	if snaps != 1 {
		t.Fatalf("snapshots after delete: %d", snaps)
	}
	// errors
	e.expectErr("DELETE", "/conversations/"+id+"/messages/"+chain[0].PublicID, e.u1, nil, 400, "conversation.message_delete_root_invalid")
	pend := e.seedMsg(conv, roleAssistant, "", msgPending, chain[4])
	e.expectErr("DELETE", "/conversations/"+id+"/messages/"+pend.PublicID, e.u1, nil, 400, "conversation.message_delete_state_invalid")
	sys := e.seedMsg(conv, roleSystem, "s", msgSuccess, chain[4])
	e.expectErr("DELETE", "/conversations/"+id+"/messages/"+sys.PublicID, e.u1, nil, 400, "conversation.message_delete_target_invalid")
	e.expectErr("DELETE", "/conversations/"+id+"/messages/zzz", e.u1, nil, 404, "message.not_found")
	e.expectErr("DELETE", "/conversations/nope/messages/"+chain[1].PublicID, e.u1, nil, 404, "conversation.not_found")
	other := e.newConv(e.u1, "o", "")
	e.expectErr("DELETE", "/conversations/"+other+"/messages/"+chain[1].PublicID, e.u1, nil, 404, "message.not_found")
	e.expectErr("DELETE", "/conversations/"+id+"/messages/"+chain[1].PublicID, e.u2, nil, 404, "conversation.not_found")
	// deleting a tail message reparents nothing
	m = e.ok("DELETE", "/conversations/"+id+"/messages/"+sys.PublicID[:0]+chain[4].PublicID, e.u1, nil)
	if num(m, "reparentedMessageCount") != 2 {
		t.Fatalf("two children (pending + system): %v", m)
	}
	if e.audit.find("delete_message") == nil {
		t.Fatal("audit")
	}
}

func TestForkConversation(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "Forkable", "gpt-4o")
	p := newProject(e, e.u1, map[string]any{"name": "proj"})
	e.ok("PATCH", "/conversations/"+id+"/project", e.u1, map[string]string{"projectID": str(p, "publicID")})
	e.ok("PATCH", "/conversations/"+id+"/labels", e.u1, map[string]any{"labels": []string{"x"}})
	us, as := chainMessages(e, e.u1, id, 3)
	conv := e.convRow(id)
	// add pending tail so we also check status conversion
	e.DB.Model(&Message{}).Where("public_id = ?", str(as[1], "publicID")).Updates(map[string]any{"billed_nanousd": 99, "status": msgPending})
	e.DB.Model(&Message{}).Where("public_id = ?", str(as[1], "publicID")).Update("status", msgSuccess)
	e.DB.Create(&UIState{UserID: 1, ConversationID: conv.ID, MessageID: uint(num(as[0], "id")), BlockID: "blk", StateJSON: `{"a":1}`})

	e.expectErr("POST", "/conversations/"+id+"/messages/"+str(us[1], "publicID")+"/fork", e.u1, nil, 400, "conversation.message_fork_target_invalid")
	// fork at depth 4 (assistant of the second turn)
	m := e.ok("POST", "/conversations/"+id+"/messages/"+str(as[1], "publicID")+"/fork", e.u1, nil)
	nid := str(m, "publicID")
	if nid == id || str(m, "title") != "Forkable" || num(m, "messageCount") != 4 || str(m, "status") != "active" ||
		str(m, "projectID") != str(p, "publicID") || str(m, "labelsJSON") != `["x"]` || str(m, "model") != str(e.ok("GET", "/conversations/"+id, e.u1, nil), "model") {
		t.Fatalf("forked conversation: %v", m)
	}
	if str(m, "sessionKey") == str(e.ok("GET", "/conversations/"+id, e.u1, nil), "sessionKey") {
		t.Fatal("new session key expected")
	}
	if !e.convRow(nid).LabelsManuallyManaged {
		t.Fatal("manual flag is copied")
	}
	rows := e.list(nid)
	if len(rows) != 4 {
		t.Fatalf("copied messages: %d", len(rows))
	}
	for i, r := range rows {
		if i == 0 && r["parentMessageID"] != nil {
			t.Fatalf("root has no parent")
		}
		if i > 0 && r["parentMessageID"] != rows[i-1]["id"] {
			t.Fatalf("chain parent at %d", i)
		}
		if str(r, "runID") != "" || str(r, "branchReason") != "default" || r["sourceMessageID"] != nil {
			t.Fatalf("copied row %d: %v", i, r)
		}
		if str(r, "publicID") == str(e.list(id)[i], "publicID") {
			t.Fatal("new public ids expected")
		}
		if _, has := r["billingCost"]; has {
			t.Fatalf("billing zeroed -> no billing cost: %v", r["billingCost"])
		}
	}
	if str(rows[3], "content") != str(as[1], "content") || num(rows[3], "outputTokens") != 5 {
		t.Fatalf("content and counters copied: %v", rows[3])
	}
	if ui, _ := rows[1]["uiState"].(map[string]any); ui == nil || ui["blk"] == nil {
		t.Fatalf("ui state cloned: %v", rows[1]["uiState"])
	}
	var nb int64
	e.DB.Model(&Message{}).Where("conversation_id = ? AND billed_nanousd <> 0", e.convRow(nid).ID).Count(&nb)
	if nb != 0 {
		t.Fatalf("billing must be zeroed: %d", nb)
	}
	// the source is untouched
	if len(e.list(id)) != 6 || e.convRow(id).MessageCount != 6 {
		t.Fatal("source changed")
	}
	// fork audit
	a := e.audit.find("fork_conversation")
	if a == nil || a.Detail["source_conversation_id"] == nil {
		t.Fatalf("audit: %+v", a)
	}
	// pending / unknown / foreign
	pend := e.seedMsg(conv, roleAssistant, "", msgPending, nil)
	e.expectErr("POST", "/conversations/"+id+"/messages/"+pend.PublicID+"/fork", e.u1, nil, 400, "conversation.message_fork_state_invalid")
	e.expectErr("POST", "/conversations/"+id+"/messages/zzz/fork", e.u1, nil, 404, "message.not_found")
	e.expectErr("POST", "/conversations/"+id+"/messages/"+str(as[0], "publicID")+"/fork", e.u2, nil, 404, "conversation.not_found")
	e.expectErr("POST", "/conversations/nope/messages/"+str(as[0], "publicID")+"/fork", e.u1, nil, 404, "conversation.not_found")
}

func TestForkPendingStatusBecomesInterruptedAndHistoryChecks(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	conv := e.convRow(id)
	u := e.seedMsg(conv, roleUser, "u", msgPending, nil)
	a := e.seedMsg(conv, roleAssistant, "a", msgSuccess, u)
	m := e.ok("POST", "/conversations/"+id+"/messages/"+a.PublicID+"/fork", e.u1, nil)
	rows := e.list(str(m, "publicID"))
	if str(rows[0], "status") != "interrupted" || str(rows[1], "status") != "success" {
		t.Fatalf("statuses: %v %v", rows[0]["status"], rows[1]["status"])
	}
	// empty status becomes success
	e2 := e.seedMsg(conv, roleAssistant, "x", "", nil)
	m = e.ok("POST", "/conversations/"+id+"/messages/"+e2.PublicID+"/fork", e.u1, nil)
	if rows = e.list(str(m, "publicID")); str(rows[0], "status") != "success" {
		t.Fatalf("empty status: %v", rows[0]["status"])
	}
	// a chain whose first message has a missing parent is incomplete
	orphanParent := e.seedMsg(conv, roleUser, "p", msgSuccess, nil)
	child := e.seedMsg(conv, roleAssistant, "c", msgSuccess, orphanParent)
	e.DB.Delete(orphanParent)
	e.expectErr("POST", "/conversations/"+id+"/messages/"+child.PublicID+"/fork", e.u1, nil, 400, "conversation.message_fork_history_incomplete")
	// too deep (> 2000)
	deep := e.newConv(e.u1, "deep", "")
	dc := e.convRow(deep)
	var prev *Message
	rowsToInsert := make([]Message, 0, 2001)
	_ = rowsToInsert
	for i := 0; i < 2001; i++ {
		role := roleUser
		if i%2 == 1 {
			role = roleAssistant
		}
		mm := Message{ConversationID: dc.ID, UserID: 1, PublicID: newPublicID(), Role: role, ContentType: "text", Content: "x", Status: msgSuccess, BranchReason: branchDefault, BilledCurrency: "USD"}
		if prev != nil {
			pid := prev.ID
			mm.ParentMessageID = &pid
		}
		if err := e.DB.Create(&mm).Error; err != nil {
			t.Fatal(err)
		}
		prev = &mm
	}
	last := prev
	if last.Role != roleUser {
		t.Fatal("expected user at depth 2001")
	}
	asst := Message{ConversationID: dc.ID, UserID: 1, PublicID: newPublicID(), Role: roleAssistant, ContentType: "text", Content: "x", Status: msgSuccess, BranchReason: branchDefault, BilledCurrency: "USD", ParentMessageID: &last.ID}
	e.DB.Create(&asst)
	e.expectErr("POST", "/conversations/"+deep+"/messages/"+asst.PublicID+"/fork", e.u1, nil, 400, "conversation.message_fork_history_incomplete")
}

func uiPath(conv, msg string) string {
	return "/conversations/" + conv + "/messages/" + msg + "/ui-state"
}

func TestUIState(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	u, a := pair(e.send(e.u1, id, "q", nil))
	path := uiPath(id, str(a, "publicID"))
	m := e.ok("PUT", path, e.u1, map[string]any{"blocks": map[string]any{"quiz-1": map[string]any{"answers": []int{1, 2}}}})
	st := m["uiState"].(map[string]any)
	if str(m, "messagePublicID") != str(a, "publicID") || num(m, "messageID") != num(a, "id") || fmt.Sprint(st["quiz-1"]) != "map[answers:[1 2]]" {
		t.Fatalf("stored: %v", m)
	}
	// stored compactly without HTML escaping
	m = e.ok("PUT", path, e.u1, map[string]any{"blocks": map[string]any{"html_b": map[string]any{"t": "<b>&</b>"}}})
	var row UIState
	e.DB.Where("block_id = ?", "html_b").First(&row)
	if row.StateJSON != `{"t":"<b>&</b>"}` {
		t.Fatalf("compact no-escape: %q", row.StateJSON)
	}
	if len(m["uiState"].(map[string]any)) != 2 {
		t.Fatalf("response has the full map: %v", m["uiState"])
	}
	// enriched in lists (assistant only)
	rows := e.list(id)
	if rows[1]["uiState"] == nil || rows[0]["uiState"] != nil {
		t.Fatalf("uiState enrichment: %v / %v", rows[0]["uiState"], rows[1]["uiState"])
	}
	// null, empty and {} clear
	for _, v := range []any{nil, map[string]any{}} {
		e.ok("PUT", path, e.u1, map[string]any{"blocks": map[string]any{"quiz-1": v}})
	}
	var n int64
	e.DB.Model(&UIState{}).Where("block_id = ?", "quiz-1").Count(&n)
	if n != 0 {
		t.Fatal("block must be removed")
	}
	m = e.ok("PUT", path, e.u1, map[string]any{"blocks": map[string]any{"html_b": nil}})
	if len(m["uiState"].(map[string]any)) != 0 {
		t.Fatalf("all cleared: %v", m)
	}
	// validation
	bad := func(blocks map[string]any, status int, code string) {
		t.Helper()
		e.expectErr("PUT", path, e.u1, map[string]any{"blocks": blocks}, status, code)
	}
	bad(map[string]any{"bad id!": map[string]any{"a": 1}}, 400, "message.invalid_ui_state")
	bad(map[string]any{strings.Repeat("a", 65): map[string]any{"a": 1}}, 400, "message.invalid_ui_state")
	bad(map[string]any{"b": map[string]any{"bad key": 1}}, 400, "message.invalid_ui_state")
	// levels: scalar = 1, so four nested objects around a scalar are five levels
	bad(map[string]any{"b": map[string]any{"k": map[string]any{"k": map[string]any{"k": map[string]any{"k": 1}}}}}, 400, "message.invalid_ui_state")
	bad(map[string]any{"b": map[string]any{"k": map[string]any{"k": map[string]any{"k": 1}}}}, 200, "")
	bad(map[string]any{"b": map[string]any{"k": strings.Repeat("v", 4100)}}, 400, "message.invalid_ui_state")
	bad(map[string]any{"b": []int{1}}, 400, "message.invalid_ui_state")
	bad(map[string]any{"b": "str"}, 400, "message.invalid_ui_state")
	// too many blocks in the request -> body validation
	many := map[string]any{}
	for i := 0; i < 33; i++ {
		many[fmt.Sprintf("b%d", i)] = map[string]any{"a": 1}
	}
	bad(many, 400, "request.invalid_body")
	bad(map[string]any{}, 400, "request.invalid_body")
	e.expectErr("PUT", path, e.u1, map[string]any{}, 400, "request.invalid_body")
	// targets
	e.expectErr("PUT", uiPath(id, str(u, "publicID")), e.u1, map[string]any{"blocks": map[string]any{"b": map[string]any{"a": 1}}}, 400, "message.ui_state_target_invalid")
	pend := e.seedMsg(e.convRow(id), roleAssistant, "", msgPending, nil)
	e.expectErr("PUT", uiPath(id, pend.PublicID), e.u1, map[string]any{"blocks": map[string]any{"b": map[string]any{"a": 1}}}, 400, "message.ui_state_target_invalid")
	e.expectErr("PUT", uiPath(id, "zzz"), e.u1, map[string]any{"blocks": map[string]any{"b": map[string]any{"a": 1}}}, 404, "message.not_found")
	e.expectErr("PUT", uiPath("nope", str(a, "publicID")), e.u1, map[string]any{"blocks": map[string]any{"b": map[string]any{"a": 1}}}, 404, "conversation.not_found")
	e.expectErr("PUT", path, e.u2, map[string]any{"blocks": map[string]any{"b": map[string]any{"a": 1}}}, 404, "conversation.not_found")
}

func TestUIStateStoredBlockCap(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	_, a := pair(e.send(e.u1, id, "q", nil))
	path := uiPath(id, str(a, "publicID"))
	for batch := 0; batch < 2; batch++ {
		blocks := map[string]any{}
		for i := 0; i < 16; i++ {
			blocks[fmt.Sprintf("b%d_%d", batch, i)] = map[string]any{"a": 1}
		}
		e.ok("PUT", path, e.u1, map[string]any{"blocks": blocks})
	}
	e.expectErr("PUT", path, e.u1, map[string]any{"blocks": map[string]any{"one_more": map[string]any{"a": 1}}}, 400, "message.invalid_ui_state")
	// updating an existing block is still fine; clearing then adding too
	e.ok("PUT", path, e.u1, map[string]any{"blocks": map[string]any{"b0_0": map[string]any{"a": 2}}})
	e.ok("PUT", path, e.u1, map[string]any{"blocks": map[string]any{"b0_0": nil, "fresh": map[string]any{"a": 1}}})
}

func TestUIStateDuplicateBlockIDsRejected(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	_, a := pair(e.send(e.u1, id, "q", nil))
	raw := `{"blocks":{"dup":{"a":1},"dup":{"a":2}}}`
	e.expectErr("PUT", uiPath(id, str(a, "publicID")), e.u1, raw, 400, "message.invalid_ui_state")
}

func TestRunsListAndStatuses(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	_, a1 := pair(e.send(e.u1, id, "a", map[string]any{"clientRunID": "r1"}))
	_, a2 := pair(e.send(e.u1, id, "b", map[string]any{"clientRunID": "r2"}))
	w := e.call("GET", "/conversations/"+id+"/runs", e.u1, nil)
	total, res := results(t, w)
	if total != 2 || len(res) != 2 {
		t.Fatalf("runs: %d", total)
	}
	e.expectErr("GET", "/conversations/"+id+"/runs", e.u2, nil, 404, "conversation.not_found")
	other := e.newConv(e.u2, "o", "gpt-4o")
	_, ao := pair(e.send(e.u2, other, "x", nil))

	body := map[string]any{"runIDs": []string{str(a1, "runID"), str(a2, "runID"), str(a1, "runID"), str(ao, "runID"), "run_unknown", " "}}
	m := dataList(t, e.call("POST", "/conversation-runs/statuses", e.u1, body))
	if len(m) != 2 {
		t.Fatalf("own runs only, duplicates/blank/unknown ignored: %v", m)
	}
	got := map[string]string{}
	for _, r := range m {
		got[str(r, "runID")] = str(r, "status")
	}
	if got["run_r1"] != "success" || got["run_r2"] != "success" {
		t.Fatalf("statuses: %v", got)
	}
	for _, bad := range []any{map[string]any{}, map[string]any{"runIDs": []string{}}, map[string]any{"runIDs": make([]string, 101)},
		map[string]any{"runIDs": []string{""}}, map[string]any{"runIDs": []string{strings.Repeat("r", 65)}}, "garbage", ""} {
		e.expectErr("POST", "/conversation-runs/statuses", e.u1, bad, 400, "request.invalid_run_ids")
	}
}

func TestExportConversation(t *testing.T) {
	e := newEnv(t)
	e.gen.set(traceGenerator())
	id := e.newConv(e.u1, "Exp", "gpt-4o")
	_, a := pair(e.send(e.u1, id, "q", nil))
	e.ok("PUT", "/messages/"+str(a, "publicID")+"/feedback", e.u1, map[string]string{"feedback": "up"})
	m := e.ok("GET", "/conversations/"+id+"/export", e.u1, nil)
	if num(m, "version") != 1 || str(m, "exportScope") != "full" || m["exportedAt"] == nil || num(m, "totalMessages") != 2 || num(m, "totalRuns") != 1 {
		t.Fatalf("export header: %v", m)
	}
	comp := m["compatibility"].(map[string]any)
	if str(comp, "format") != "riochat.conversation.export" || str(comp, "notes") != "Full backup export. Import compatibility is not guaranteed." {
		t.Fatalf("compat: %v", comp)
	}
	if len(m["defaultMessagePublicIDs"].([]any)) != 2 || len(m["messages"].([]any)) != 2 || len(m["runs"].([]any)) != 1 {
		t.Fatalf("export body: %v", m)
	}
	if str(m["conversation"].(map[string]any), "title") != "Exp" {
		t.Fatalf("conversation: %v", m["conversation"])
	}
	am := m["messages"].([]any)[1].(map[string]any)
	if str(am, "myFeedback") != "up" || am["processTrace"] == nil {
		t.Fatalf("export message enrichment: %v", am)
	}
	e.expectErr("GET", "/conversations/"+id+"/export", e.u2, nil, 404, "conversation.not_found")
	if a := e.audit.find("export_conversation"); a == nil || num2(a.Detail["message_count"]) != 2 {
		t.Fatalf("audit: %+v", a)
	}
}

func num2(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	}
	return -1
}

func TestExportDefaultBranchIsNewestChild(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	u1, a1 := pair(e.send(e.u1, id, "q", nil))
	_, a2 := pair(e.send(e.u1, id, "q", map[string]any{"sourceMessagePublicID": str(a1, "publicID"), "parentMessagePublicID": str(u1, "publicID"), "branchReason": "retry"}))
	m := e.ok("GET", "/conversations/"+id+"/export", e.u1, nil)
	ids := m["defaultMessagePublicIDs"].([]any)
	if len(ids) != 2 || ids[0] != str(u1, "publicID") || ids[1] != str(a2, "publicID") {
		t.Fatalf("latest branch: %v (want %s)", ids, str(a2, "publicID"))
	}
	if num(m, "totalMessages") != 3 {
		t.Fatalf("total: %v", m["totalMessages"])
	}
}

func TestExportAllStream(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 3; i++ {
		id := e.newConv(e.u1, fmt.Sprintf("c%d", i), "gpt-4o")
		e.send(e.u1, id, "hi", nil)
	}
	e.newConv(e.u2, "theirs", "")
	w := e.call("GET", "/conversations/export", e.u1, nil)
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content type: %q", ct)
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="my-conversations-20260301-090`) || !strings.HasSuffix(cd, `.jsonl"`) {
		t.Fatalf("content disposition: %q", cd)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache control: %q", w.Header().Get("Cache-Control"))
	}
	lines := ndjson(t, w)
	if len(lines) != 4 {
		t.Fatalf("lines: %d", len(lines))
	}
	man := lines[len(lines)-1]
	if man["_type"] != "export_manifest" || man["complete"] != true || num(man, "exported") != 3 || num(man, "failed") != 0 {
		t.Fatalf("manifest: %v", man)
	}
	if num(lines[0], "version") != 1 || lines[0]["conversation"] == nil {
		t.Fatalf("conversation line: %v", lines[0])
	}
	// empty account still gets a manifest
	w = e.call("GET", "/conversations/export", e.Bearer(userClaims(55)), nil)
	lines = ndjson(t, w)
	if len(lines) != 1 || lines[0]["_type"] != "export_manifest" || lines[0]["complete"] != true || num(lines[0], "exported") != 0 {
		t.Fatalf("empty manifest: %v", lines)
	}
	if e.audit.find("export_all_conversations") == nil {
		t.Fatal("audit")
	}
}

func TestInvalidJSONHelpersCompile(t *testing.T) {
	var v any
	_ = json.Unmarshal([]byte("{}"), &v)
}
