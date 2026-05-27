package conversation

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// Codes owned by other areas, registered the way their packages do.
var (
	errTestSelection  = apperr.Define("test.invalid_tool_selection", "invalid tool selection")
	errTestSettlement = apperr.Define("test.settlement_failed", "settlement failed").WithStatus(409)
)

// funcCaps is a Capabilities double whose tool check can look at the ids.
type funcCaps struct {
	fakeCaps
	tools func(ids []uint) error
}

func (c *funcCaps) ValidateMCPTools(_ context.Context, _ uint, ids []uint) error {
	if c.tools != nil {
		return c.tools(ids)
	}
	return nil
}

func TestFeedbackValueIsCaseInsensitive(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	_, a := pair(e.send(e.u1, id, "q", nil))
	url := "/messages/" + str(a, "publicID") + "/feedback"
	m := e.ok("PUT", url, e.u1, map[string]string{"feedback": "UP"})
	if str(m, "myFeedback") != "up" || num(m, "thumbsUpCount") != 1 {
		t.Fatalf("UP: %v", m)
	}
	m = e.ok("PUT", url, e.u1, map[string]string{"feedback": "dOwN"})
	if str(m, "myFeedback") != "down" || num(m, "thumbsDownCount") != 1 || num(m, "thumbsUpCount") != 0 {
		t.Fatalf("dOwN: %v", m)
	}
	e.expectErr("PUT", url, e.u1, map[string]string{"feedback": "meh"}, 400, "request.invalid_body")
	m = e.ok("PUT", url, e.u1, map[string]string{"feedback": ""})
	if str(m, "myFeedback") != "" {
		t.Fatalf("empty removes: %v", m)
	}
}

func TestProjectCapabilityRejectionsAreInvalidProject(t *testing.T) {
	e := newEnv(t)
	caps := &funcCaps{}
	e.svc.ports.Capabilities = caps
	p := newProject(e, e.u1, map[string]any{"name": "p", "mcpDefaultMode": "custom", "defaultMCPToolIDs": []uint{1}})
	pid := str(p, "publicID")

	// a typed client error from another area becomes the project error
	caps.tools = func([]uint) error { return errTestSelection }
	e.expectErr("PATCH", "/conversation-projects/"+pid, e.u1, map[string]any{"defaultMCPToolIDs": []uint{1, 2}}, 400, "request.invalid_conversation_project")
	e.expectErr("POST", "/conversation-projects", e.u1, map[string]any{"name": "q", "mcpDefaultMode": "custom", "defaultMCPToolIDs": []uint{5}}, 400, "request.invalid_conversation_project")

	// existence is only checked for new tools, the processor limit for the whole set
	var seen [][]uint
	caps.tools = func(ids []uint) error {
		seen = append(seen, append([]uint(nil), ids...))
		if len(ids) > 1 {
			return ErrMultipleImageProcs
		}
		return nil
	}
	e.expectErr("PATCH", "/conversation-projects/"+pid, e.u1, map[string]any{"defaultMCPToolIDs": []uint{1, 2}}, 400, "request.invalid_conversation_project")
	if fmt.Sprint(seen) != "[[2] [1 2]]" {
		t.Fatalf("validation calls: %v", seen)
	}
	// removing a tool re-checks nothing
	seen = nil
	e.ok("PATCH", "/conversation-projects/"+pid, e.u1, map[string]any{"defaultMCPToolIDs": []uint{}})
	if len(seen) != 0 {
		t.Fatalf("no new tools, no checks: %v", seen)
	}
}

func TestProjectKnowledgeBasesNeedTheFeatureForTheWholeState(t *testing.T) {
	e := newEnv(t)
	p := newProject(e, e.u1, map[string]any{"name": "p", "defaultKnowledgeBaseIDs": []string{"kb1"}})
	pid := str(p, "publicID")
	e.set.mutate(func(r *RuntimeSettings) { r.KnowledgeBaseEnabled = false })
	// kept bases still count: the state cannot hold bases while the feature is off
	e.expectErr("PATCH", "/conversation-projects/"+pid, e.u1, map[string]any{"description": "x"}, 400, "request.invalid_conversation_project")
	// dropping them is allowed
	e.ok("PATCH", "/conversation-projects/"+pid, e.u1, map[string]any{"defaultKnowledgeBaseIDs": []string{}})
	e.ok("PATCH", "/conversation-projects/"+pid, e.u1, map[string]any{"description": "x"})
}

func TestSendReferenceErrorsWithoutStatusAreBadRequests(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.caps.toolErr = errTestSelection
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", map[string]any{"selectedToolIDs": []uint{1}}), 400, "test.invalid_tool_selection")
}

func TestTemporaryChatValidatesReferencesAndNormalizesBlockVersion(t *testing.T) {
	e := newEnv(t)
	body := tempBody([2]string{"user", "hi"})
	body["selectedToolIDs"] = []uint{4}
	e.caps.toolErr = ErrMultipleImageProcs
	e.expectErr("POST", "/temporary-chat/messages/stream", e.u1, body, 400, "message.multiple_image_processors")
	e.caps.toolErr = nil
	e.caps.skillErr = ErrSkillNotFound
	body["selectedToolIDs"] = []uint{}
	body["skillIDs"] = []uint{9}
	e.expectErr("POST", "/temporary-chat/messages/stream", e.u1, body, 404, "skill.not_found")
	e.caps.skillErr = nil
	delete(body, "skillIDs")
	if w := e.call("POST", "/temporary-chat/messages/stream", e.u1, body); w.Code != 200 {
		t.Fatalf("temp: %d %s", w.Code, w.Body)
	}
	reqs := e.gen.requests()
	if got := reqs[len(reqs)-1].UIBlockVersion; got != 1 {
		t.Fatalf("absent uiBlockVersion means 1, got %d", got)
	}
}

func TestMediaOptionsKeepEverythingButReservedKeysForOrdinaryUsers(t *testing.T) {
	e := newEnv(t)
	e.cat.available["img-model"] = true
	e.gen.set(mediaGen(nil))
	id := e.newConv(e.u1, "t", "img-model")
	e.call("POST", "/conversations/"+id+"/media/images/generations/stream", e.u1, map[string]any{
		"prompt": "a cat", "options": map[string]any{"size": "1024x1024", "quality": "high", "prompt": "hijack", "stream": true},
	})
	reqs := e.gen.requests()
	if len(reqs) == 0 {
		t.Fatal("generator not called")
	}
	opts := reqs[0].Options
	if opts["size"] != "1024x1024" || opts["quality"] != "high" {
		t.Fatalf("media options dropped: %v", opts)
	}
	if _, bad := opts["prompt"]; bad {
		t.Fatalf("reserved key kept: %v", opts)
	}
	if _, bad := opts["stream"]; bad {
		t.Fatalf("reserved key kept: %v", opts)
	}
}

func TestSettlementFailureAfterSuccessIsAnHTTPError(t *testing.T) {
	e := newEnv(t)
	e.bill.settleE = errTestSettlement
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", nil), 409, "test.settlement_failed")
}

func TestFileRenameTooLongIsInvalidName(t *testing.T) {
	e := newEnv(t)
	f := e.uploadOK(e.u1, "a.txt", "text/plain", []byte("abc"))["file"].(map[string]any)
	// A renamed file keeps whatever length was sent, and only the last path
	// part of the name counts.
	long := e.ok("PATCH", "/files/"+str(f, "fileID"), e.u1, map[string]any{"fileName": strings.Repeat("n", 300)})
	if len(str(long, "fileName")) != 300 {
		t.Fatalf("long name: %v", long["fileName"])
	}
	base := e.ok("PATCH", "/files/"+str(f, "fileID"), e.u1, map[string]any{"fileName": "a/b"})
	if str(base, "fileName") != "b" {
		t.Fatalf("path name: %v", base["fileName"])
	}
}

func TestFileListLastUsedPutsNeverUsedLast(t *testing.T) {
	e := newEnv(t)
	a := e.uploadOK(e.u1, "a.txt", "text/plain", []byte("aaa"))["file"].(map[string]any)
	b := e.uploadOK(e.u1, "b.txt", "text/plain", []byte("bbb"))["file"].(map[string]any)
	c := e.uploadOK(e.u1, "c.txt", "text/plain", []byte("ccc"))["file"].(map[string]any)
	// only a file that was read has a last-use time
	if w := e.call("GET", "/files/"+str(a, "fileID")+"/content", e.u1, nil); w.Code != 200 {
		t.Fatalf("content: %d", w.Code)
	}
	e.Clock.Advance(1)
	if w := e.call("GET", "/files/"+str(c, "fileID")+"/content", e.u1, nil); w.Code != 200 {
		t.Fatalf("content: %d", w.Code)
	}
	w := e.call("GET", "/files?sort=last_used", e.u1, nil)
	_, rows := results(t, w)
	if len(rows) != 3 || str(rows[2], "fileID") != str(b, "fileID") {
		t.Fatalf("never used file must come last: %v", rows)
	}
}

func TestResumeSnapshotHasCompleteTextEvenAfterBufferTrim(t *testing.T) {
	e := newEnvTimed(t, func(tm *timings) { tm.maxEvents = 3 })
	e.gen.set(func(_ context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task != taskChat {
			return defaultGenerate(context.Background(), req, emit)
		}
		var all strings.Builder
		for i := 0; i < 8; i++ {
			d := fmt.Sprintf("part%d ", i)
			all.WriteString(d)
			emit(GenerateEvent{Type: "delta", Delta: d})
		}
		return GenerateResult{Content: all.String(), Usage: Usage{Input: 1, Output: 1}}, nil
	})
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.call("POST", "/conversations/"+id+"/messages/stream", e.u1, sendBody("x", map[string]any{"clientRunID": "trim1"}))
	rw := e.call("GET", "/conversation-runs/run_trim1/stream?snapshot=true", e.u1, nil)
	lines := ndjson(t, rw)
	if len(lines) == 0 || str(lines[0], "type") != "snapshot" {
		t.Fatalf("snapshot first: %v", lines)
	}
	want := "part0 part1 part2 part3 part4 part5 part6 part7 "
	if str(lines[0], "content") != want {
		t.Fatalf("snapshot content %q", str(lines[0], "content"))
	}
}

func TestCloneLinksSourceWhenTheSourceBranchIsCopiedLater(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "Original", "gpt-4o")
	u1, a1 := pair(e.send(e.u1, id, "q", nil))
	// a retry that is newer than its source, while the older answer stays the default branch
	_, a2 := pair(e.send(e.u1, id, "q", map[string]any{
		"sourceMessagePublicID": str(a1, "publicID"), "parentMessagePublicID": str(u1, "publicID"), "branchReason": "retry"}))
	s := e.ok("POST", "/conversations/"+id+"/share", e.u1, map[string]any{"defaultMessagePublicIDs": []string{str(u1, "publicID"), str(a1, "publicID")}})
	m := e.ok("POST", "/shared-conversations/"+str(s, "shareID")+"/clone", e.u2, nil)
	var retry, original map[string]any
	for _, r := range func() []map[string]any {
		_, rows := results(t, e.call("GET", "/conversations/"+str(m, "publicID")+"/messages", e.u2, nil))
		return rows
	}() {
		switch {
		case str(r, "role") == "assistant" && str(r, "branchReason") == "retry":
			retry = r
		case str(r, "role") == "assistant":
			original = r
		}
	}
	if retry == nil || original == nil {
		t.Fatalf("cloned branches missing (retry source %s)", str(a2, "publicID"))
	}
	if str(retry, "sourcePublicID") == "" || str(retry, "sourcePublicID") != str(original, "publicID") {
		t.Fatalf("source link must point at the copied original: %v / %v", retry, original)
	}
}

func TestForkKeepsOwnerTraceFileReferences(t *testing.T) {
	e := newEnv(t)
	e.gen.set(func(_ context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task != taskChat {
			return defaultGenerate(context.Background(), req, emit)
		}
		return GenerateResult{Content: "answer", Usage: Usage{Input: 1, Output: 1}, Trace: &TraceData{
			Process: &TraceBlock{Title: "Process", Status: "success", PayloadJSON: `{"fileName":"report.pdf","upstreamName":"hidden","keep":1}`},
		}}, nil
	})
	id := e.newConv(e.u1, "t", "gpt-4o")
	_, a := pair(e.send(e.u1, id, "q", nil))
	m := e.ok("POST", "/conversations/"+id+"/messages/"+str(a, "publicID")+"/fork", e.u1, nil)
	rows := e.list(str(m, "publicID"))
	trace := fmt.Sprint(rows[1]["processTrace"])
	if !strings.Contains(trace, "report.pdf") || strings.Contains(trace, "hidden") {
		t.Fatalf("fork trace keeps the owner's file names and drops upstream names: %s", trace)
	}
}

func TestPublicShareKeepsToolCallIDsInTrace(t *testing.T) {
	e := newEnv(t)
	e.gen.set(func(_ context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task != taskChat {
			return defaultGenerate(context.Background(), req, emit)
		}
		return GenerateResult{Content: "answer", Usage: Usage{Input: 1, Output: 1}, Trace: &TraceData{
			Tools: &TraceBlock{Title: "Tools", Status: "success", PayloadJSON: `{"calls":[{"toolCallId":"tc1","apiKey":"k","fileName":"x"}]}`},
		}}, nil
	})
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.send(e.u1, id, "q", nil)
	s := e.ok("POST", "/conversations/"+id+"/share", e.u1, nil)
	pub := e.call("GET", "/shared-conversations/"+str(s, "shareID"), nil, nil)
	body := pub.Body.String()
	if !strings.Contains(body, "tc1") || strings.Contains(body, `"apiKey"`) || strings.Contains(body, `"fileName":"x"`) {
		t.Fatalf("public trace: %s", body)
	}
}
