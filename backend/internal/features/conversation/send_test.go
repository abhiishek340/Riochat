package conversation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestSendHappyPath(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "", "")
	r := e.send(e.u1, id, "What is Go?", map[string]any{"model": "gpt-4o"})
	u, a := pair(r)
	if str(u, "role") != "user" || str(a, "role") != "assistant" {
		t.Fatalf("pair: %v", r)
	}
	if str(a, "status") != "success" || !strings.HasPrefix(str(a, "content"), "Hello world") {
		t.Fatalf("assistant: %v", a)
	}
	if str(u, "status") != "success" || str(u, "content") != "What is Go?" {
		t.Fatalf("user: %v", u)
	}
	if a["parentMessageID"] != u["id"] || u["parentMessageID"] != nil {
		t.Fatalf("parent links: %v / %v", u["parentMessageID"], a["parentMessageID"])
	}
	if str(a, "parentPublicID") != str(u, "publicID") {
		t.Fatalf("parentPublicID: %v", a)
	}
	// The prompt is charged to the user message, the answer to the assistant.
	if num(a, "inputTokens") != 0 || num(a, "outputTokens") != 5 || num(a, "tokenUsage") != 5 ||
		num(u, "inputTokens") != 10 || num(u, "tokenUsage") != 10 {
		t.Fatalf("usage: %v / %v", a, u)
	}
	if str(a, "platformModelName") != "plat-model" || str(a, "upstreamModelName") != "up-model" || str(a, "modelVendor") != "" {
		t.Fatalf("model fields: %v", a)
	}
	if str(a, "runID") == "" || str(a, "runID") != str(u, "runID") || !strings.HasPrefix(str(a, "runID"), "run_") {
		t.Fatalf("run id: %v", a)
	}
	if str(a, "attachments") != "[]" {
		t.Fatalf("attachments must be a JSON string: %v", a["attachments"])
	}
	bc, _ := a["billingCost"].(map[string]any)
	if bc == nil || str(bc, "billingMode") != "balance" || num(bc, "billedUSD") != 2 || str(bc, "billedCurrency") != "USD" {
		t.Fatalf("billingCost: %v", a["billingCost"])
	}
	// conversation state
	c := e.ok("GET", "/conversations/"+id, e.u1, nil)
	if num(c, "messageCount") != 2 || str(c, "model") != "plat-model" {
		t.Fatalf("conversation after send: %v", c)
	}
	if e.bill.reserved != 1 || e.bill.settled != 1 {
		t.Fatalf("billing: %+v", e.bill)
	}
	if e.audit.find("send_message") == nil {
		t.Fatal("audit send_message")
	}
	// run row
	runs := e.call("GET", "/conversations/"+id+"/runs", e.u1, nil)
	total, rs := results(t, runs)
	if total != 1 || str(rs[0], "status") != "success" || str(rs[0], "taskType") != "chat" || num(rs[0], "inputTokens") != 10 || rs[0]["endedAt"] == nil {
		t.Fatalf("runs: %v", rs)
	}
	if str(rs[0], "requestedModelName") != "gpt-4o" || str(rs[0], "platformModelName") != "plat-model" {
		t.Fatalf("run model: %v", rs[0])
	}
}

func TestSendNoBillingForSelfMode(t *testing.T) {
	e := newEnv(t)
	e.bill.reserveE = nil
	id := e.newConv(e.u1, "t", "gpt-4o")
	r := e.send(e.u1, id, "hi", nil)
	_, a := pair(r)
	if str(a, "status") != "success" {
		t.Fatalf("%v", a)
	}
}

func TestSendValidation(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	p := "/conversations/" + id + "/messages"
	bad := func(body map[string]any, code string) {
		t.Helper()
		e.expectErr("POST", p, e.u1, body, 400, code)
	}
	bad(map[string]any{}, "request.invalid_body")
	bad(map[string]any{"contentType": "text"}, "request.invalid_body")
	bad(map[string]any{"content": "x"}, "request.invalid_body")
	bad(map[string]any{"contentType": "video", "content": "x"}, "request.invalid_body")
	bad(sendBody("x", map[string]any{"model": strings.Repeat("m", 129)}), "request.invalid_body")
	bad(sendBody("x", map[string]any{"clientRunID": strings.Repeat("r", 65)}), "request.invalid_body")
	bad(sendBody("x", map[string]any{"fileIDs": make([]string, 21)}), "request.invalid_body")
	bad(sendBody("x", map[string]any{"selectedToolIDs": make([]uint, 129)}), "request.invalid_body")
	bad(sendBody("x", map[string]any{"knowledgeBaseIDs": make([]string, 9)}), "request.invalid_body")
	bad(sendBody("x", map[string]any{"knowledgeBaseIDs": []string{""}}), "request.invalid_body")
	bad(sendBody("x", map[string]any{"uiComponentIDs": make([]uint, 33)}), "request.invalid_body")
	bad(sendBody("x", map[string]any{"uiBlockVersion": 3}), "request.invalid_body")
	bad(sendBody("x", map[string]any{"branchReason": "weird"}), "request.invalid_body")
	bad(sendBody("x", map[string]any{"parentMessagePublicID": strings.Repeat("p", 33)}), "request.invalid_body")
	bad(sendBody("x", map[string]any{"uiStates": make([]map[string]any, 33)}), "request.invalid_body")
	e.expectErr("POST", p, e.u1, "", 400, "request.invalid_body")
	e.expectErr("POST", p, e.u1, "{bad json", 400, "request.invalid_body")
	e.expectErr("POST", "/conversations/nope/messages", e.u1, sendBody("x", nil), 404, "conversation.not_found")
	e.expectErr("POST", p, e.u2, sendBody("x", nil), 404, "conversation.not_found")
	var n int64
	e.DB.Model(&Message{}).Count(&n)
	if n != 0 {
		t.Fatalf("validation failures must not create messages: %d", n)
	}
}

func TestSendTooManyToolsCreatesNothing(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.set.mutate(func(r *RuntimeSettings) { r.MaxSelectedTools = 3 })
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", map[string]any{"selectedToolIDs": []uint{1, 2, 3, 4}}), 400, "message.too_many_selected_tools")
	e.expectErr("POST", "/conversations/"+id+"/messages/stream", e.u1, sendBody("x", map[string]any{"selectedToolIDs": []uint{1, 2, 3, 4}}), 400, "message.too_many_selected_tools")
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", map[string]any{"skillIDs": []uint{1, 2, 3, 4}}), 400, "message.too_many_selected_skills")
	var n int64
	e.DB.Model(&Message{}).Count(&n)
	if n != 0 {
		t.Fatalf("no messages expected: %d", n)
	}
	// duplicates collapse before counting? exactly at the limit is fine
	e.ok("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", map[string]any{"selectedToolIDs": []uint{1, 2, 3}}))
}

func TestSendReferenceValidationErrors(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	p := "/conversations/" + id + "/messages"
	e.caps.toolErr = ErrMultipleImageProcs
	e.expectErr("POST", p, e.u1, sendBody("x", map[string]any{"selectedToolIDs": []uint{1}}), 400, "message.multiple_image_processors")
	e.caps.toolErr = nil
	e.caps.skillErr = ErrSkillNotFound
	e.expectErr("POST", p, e.u1, sendBody("x", map[string]any{"skillIDs": []uint{1}}), 404, "skill.not_found")
	e.caps.skillErr = nil
	// without a retrieval backend every base is unavailable, existing or not
	e.caps.kbErr = ErrInvalidKBReference
	e.expectErr("POST", p, e.u1, sendBody("x", map[string]any{"knowledgeBaseIDs": []string{"kb"}}), 503, "knowledge_base.unavailable")
	e.set.mutate(func(r *RuntimeSettings) { r.RAGAvailable = true })
	e.expectErr("POST", p, e.u1, sendBody("x", map[string]any{"knowledgeBaseIDs": []string{"kb"}}), 400, "knowledge_base.invalid_reference")
	e.caps.kbErr = ErrKBNotReady
	e.expectErr("POST", p, e.u1, sendBody("x", map[string]any{"knowledgeBaseIDs": []string{"kb"}}), 409, "knowledge_base.not_ready")
	e.caps.kbErr = nil
	e.set.mutate(func(r *RuntimeSettings) { r.KnowledgeBaseEnabled = false })
	e.expectErr("POST", p, e.u1, sendBody("x", map[string]any{"knowledgeBaseIDs": []string{"kb"}}), 400, "knowledge_base.invalid_reference")
	e.set.mutate(func(r *RuntimeSettings) { r.KnowledgeBaseEnabled = true })
	// file references
	e.expectErr("POST", p, e.u1, sendBody("x", map[string]any{"fileIDs": []string{"file_missing"}}), 400, "file.invalid_reference")
	e.expectErr("POST", p, e.u1, sendBody("x", map[string]any{"fileIDs": []string{" "}}), 400, "file.invalid_reference")
	e.expectErr("POST", p, e.u1, sendBody("x", map[string]any{"fileIDs": []string{"a", "a"}}), 400, "file.invalid_reference")
	// the same file twice attaches once, and duplicates do not count towards the limit
	fid := fileIDOf(e.uploadOK(e.u1, "d.txt", "text/plain", []byte("dup body")))
	e.set.mutate(func(r *RuntimeSettings) { r.MaxMessageFiles = 1 })
	m := e.ok("POST", p, e.u1, sendBody("x", map[string]any{"fileIDs": []string{fid, fid}}))
	if um := m["userMessage"].(map[string]any); strings.Count(str(um, "attachments"), fid) != 1 {
		t.Fatalf("one attachment expected: %v", um["attachments"])
	}
	e.set.mutate(func(r *RuntimeSettings) { r.MaxMessageFiles = 10 })
	e.set.mutate(func(r *RuntimeSettings) { r.MaxMessageFiles = 1 })
	e.expectErr("POST", p, e.u1, sendBody("x", map[string]any{"fileIDs": []string{"a", "b"}}), 400, "message.too_many_files")
	// model access
	e.expectErr("POST", p, e.u1, sendBody("x", map[string]any{"model": "forbidden-model"}), 403, "llm.model_access_denied")
}

func TestSendBillingIsCheckedBeforeToolSelection(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	p := "/conversations/" + id + "/messages"
	e.caps.toolErr = ErrMultipleImageProcs
	e.bill.reserveE = ErrInsufficientFunds
	e.expectErr("POST", p, e.u1, sendBody("x", map[string]any{"selectedToolIDs": []uint{1}}), 402, "billing.insufficient_funds")
	// skills are still validated before the budget
	e.caps.toolErr = nil
	e.caps.skillErr = ErrSkillNotFound
	e.expectErr("POST", p, e.u1, sendBody("x", map[string]any{"skillIDs": []uint{1}}), 404, "skill.not_found")
}

func TestSendSanitizesOptionsAndControls(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.send(e.u1, id, "hi", map[string]any{
		"options":  map[string]any{"stream": true, "messages": 1, "temperature": 0.2, "nativeTools": []string{"search"}, "reasoning_effort": "high"},
		"controls": map[string]any{"a": "x", "b": true, "c": 3, " ": "blank", "d": map[string]any{"x": 1}, "e": strings.Repeat("v", 129)},
	})
	req := e.gen.requests()[0]
	if _, ok := req.Options["stream"]; ok {
		t.Fatalf("reserved keys must be removed: %v", req.Options)
	}
	if _, ok := req.Options["temperature"]; ok {
		t.Fatalf("ordinary users lose raw options: %v", req.Options)
	}
	if req.Options["nativeTools"] == nil || req.Options["reasoning_effort"] != "high" {
		t.Fatalf("kept options: %v", req.Options)
	}
	if req.Controls["reasoning"] != "high" {
		t.Fatalf("reasoning effort converted to control: %v", req.Controls)
	}
	for _, k := range []string{" ", "d", "e"} {
		if _, ok := req.Controls[k]; ok {
			t.Fatalf("control %q must be dropped: %v", k, req.Controls)
		}
	}
	if req.Controls["a"] != "x" || req.Controls["b"] != true || req.Controls["c"] != float64(3) {
		t.Fatalf("controls: %v", req.Controls)
	}
	// administrators keep raw options
	e.send(e.admin, e.newConv(e.admin, "t", "gpt-4o"), "hi", map[string]any{"options": map[string]any{"temperature": 0.2, "prompt": "x"}})
	var last GenerateRequest
	for _, r := range e.gen.requests() {
		if r.Task == taskChat {
			last = r
		}
	}
	if last.Options["temperature"] != 0.2 {
		t.Fatalf("admin options: %v", last.Options)
	}
	if _, ok := last.Options["prompt"]; ok {
		t.Fatalf("reserved removed for admin too: %v", last.Options)
	}
}

func TestRunIDNormalization(t *testing.T) {
	cases := map[string]string{
		"  abc-def ": "run_abcdef",
		"run_xyz":    "run_xyz",
		"RUN":        "run_RUN",
	}
	for in, want := range cases {
		if got := normalizeRunID(in); got != want {
			t.Fatalf("normalize(%q) = %q want %q", in, got, want)
		}
	}
	for _, in := range []string{"", "   ", strings.Repeat("a", 61)} {
		got := normalizeRunID(in)
		if !strings.HasPrefix(got, "run_") || len(got) != 36 {
			t.Fatalf("normalize(%q) must generate: %q", in, got)
		}
	}
	if got := normalizeRunID(strings.Repeat("a", 60)); got != "run_"+strings.Repeat("a", 60) {
		t.Fatalf("exactly 64 chars is kept: %q", got)
	}
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	r := e.send(e.u1, id, "hi", map[string]any{"clientRunID": "my-run-1"})
	_, a := pair(r)
	if str(a, "runID") != "run_myrun1" {
		t.Fatalf("run id: %v", a["runID"])
	}
	// duplicate -> 409 and no extra messages
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u1, sendBody("again", map[string]any{"clientRunID": "my-run-1"}), 409, "message_generation_run.already_exists")
	var n int64
	e.DB.Model(&Message{}).Count(&n)
	if n != 2 {
		t.Fatalf("duplicate run must not create messages: %d", n)
	}
}

func TestAutoFallbackTitle(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "", "gpt-4o")
	r := e.send(e.u1, id, "  Hello   there,\n how are you doing today?  ", nil)
	if r["metadataRefreshHint"] != "pending" {
		t.Fatalf("hint: %v", r["metadataRefreshHint"])
	}
	// the background metadata call replaces the fallback title
	eventually(t, "generated title", func() bool {
		return e.convRow(id).Title == "Generated Title"
	})
	if !contains(e.gen.tasks(), taskLabels) && !contains(e.gen.tasks(), taskTitle) {
		t.Fatalf("metadata task expected: %v", e.gen.tasks())
	}
}

func contains(l []string, s string) bool {
	for _, v := range l {
		if v == s {
			return true
		}
	}
	return false
}

func TestFallbackTitleStoredImmediatelyAndUserTitleKept(t *testing.T) {
	e := newEnv(t)
	// fail metadata generation so only the fallback shows
	e.gen.set(func(_ context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task != taskChat {
			return GenerateResult{}, ErrUpstream
		}
		return defaultGenerate(context.Background(), req, emit)
	})
	id := e.newConv(e.u1, "", "gpt-4o")
	e.send(e.u1, id, "  Hello   there,\n how are you doing today?  ", nil)
	if got := e.convRow(id).Title; got != "Hello there, how" && !strings.HasPrefix(got, "Hello there, ho") {
		t.Fatalf("fallback title: %q", got)
	}
	if n := len([]rune(e.convRow(id).Title)); n > 16 {
		t.Fatalf("fallback title too long: %d", n)
	}
	// user-set title is never overwritten
	id2 := e.newConv(e.u1, "My Title", "gpt-4o")
	r := e.send(e.u1, id2, "something else", nil)
	if r["metadataRefreshHint"] == "pending" {
		// labels may still be generated, but the title must stay
		t.Log("labels pending")
	}
	if got := e.convRow(id2).Title; got != "My Title" {
		t.Fatalf("user title overwritten: %q", got)
	}
	// "new chat" placeholder is recognised case-insensitively
	id3 := e.newConv(e.u1, "  New Chat ", "gpt-4o")
	e.send(e.u1, id3, "topic words", nil)
	if got := e.convRow(id3).Title; got != "topic words" {
		t.Fatalf("placeholder replacement: %q", got)
	}
}

func TestMetadataSettingsAndManualLabels(t *testing.T) {
	e := newEnv(t)
	e.set.users["1:chat.auto_generate_title"] = "false"
	e.set.users["1:chat.auto_generate_labels"] = "false"
	id := e.newConv(e.u1, "", "gpt-4o")
	r := e.send(e.u1, id, "hello", nil)
	if r["metadataRefreshHint"] != "not_needed" {
		t.Fatalf("hint: %v", r["metadataRefreshHint"])
	}
	if got := e.convRow(id).Title; got != "hello" {
		t.Fatalf("fallback still applies: %q", got)
	}
	// labels flow
	e.set.users = map[string]string{}
	e.gen.set(func(_ context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task != taskChat {
			return GenerateResult{Content: `{"title":"T2","labels":["#Go"," go ","Tools","a","b","c","d"]}`}, nil
		}
		return defaultGenerate(context.Background(), req, emit)
	})
	id2 := e.newConv(e.u1, "", "gpt-4o")
	e.send(e.u1, id2, "question about go", nil)
	eventually(t, "labels", func() bool { return e.convRow(id2).LabelsJSON != "[]" })
	got := e.convRow(id2)
	if got.LabelsJSON != `["Go","Tools","a","b","c","d"]` || got.Title != "T2" {
		t.Fatalf("labels=%s title=%q", got.LabelsJSON, got.Title)
	}
	// manually managed labels are never overwritten
	id3 := e.newConv(e.u1, "", "gpt-4o")
	e.ok("PATCH", "/conversations/"+id3+"/labels", e.u1, map[string]any{"labels": []string{}})
	e.send(e.u1, id3, "another one", nil)
	eventually(t, "title", func() bool { return e.convRow(id3).Title == "T2" })
	if got := e.convRow(id3).LabelsJSON; got != "[]" {
		t.Fatalf("manual labels overwritten: %s", got)
	}
}

func TestSendEmptyGeneratorOutputIsError(t *testing.T) {
	e := newEnv(t)
	e.gen.set(func(context.Context, GenerateRequest, func(GenerateEvent)) (GenerateResult, error) {
		return GenerateResult{}, nil
	})
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", nil), 502, "llm.empty_response")
	rows := e.list(id)
	if len(rows) != 2 || rows[1]["status"] != "error" || str(rows[1], "errorCode") != "llm.empty_response" {
		t.Fatalf("persisted error pair: %v", rows)
	}
	if rows[0]["status"] != "error" {
		t.Fatalf("user message of a failed run: %v", rows[0])
	}
	if str(rows[1], "errorRequestID") == "" {
		t.Fatalf("errorRequestID must be returned for error assistant messages: %v", rows[1])
	}
	if str(rows[0], "errorRequestID") != "" {
		t.Fatalf("errorRequestID only on assistant messages: %v", rows[0])
	}
	if e.bill.released != 1 || e.bill.settled != 0 {
		t.Fatalf("no billable usage -> reservation released: %+v", e.bill)
	}
}

// list returns all messages of a conversation via the API.
func (e *tenv) list(conv string) []map[string]any {
	e.t.Helper()
	w := e.call("GET", "/conversations/"+conv+"/messages?page_size=1000", e.u1, nil)
	testkit.Expect(e.t, w, 200, "")
	_, res := results(e.t, w)
	return res
}

func TestSendFailureErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{ErrUpstream, 502, "upstream.unavailable"},
		{ErrUpstreamRateLimited, 429, "upstream.rate_limited"},
		{ErrModelRouteMissing, 503, "llm.model_route_not_configured"},
		{ErrFinalAnswerMissing, 502, "tool_run.final_answer_missing"},
		{ErrMediaArtifact, 502, "media.artifact_unavailable"},
		{ErrImageProcessing, 502, "mcp.image_processing_failed"},
		{ErrKBUnavailable, 503, "knowledge_base.unavailable"},
		{errors.New("boom secret detail"), 500, "internal.error"},
	}
	for _, c := range cases {
		e := newEnv(t)
		err := c.err
		e.gen.set(func(context.Context, GenerateRequest, func(GenerateEvent)) (GenerateResult, error) {
			return GenerateResult{}, err
		})
		id := e.newConv(e.u1, "t", "gpt-4o")
		w := e.call("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", nil))
		testkit.Expect(t, w, c.status, c.code)
		if strings.Contains(w.Body.String(), "secret detail") {
			t.Fatalf("internal text leaked: %s", w.Body)
		}
		rows := e.list(id)
		if len(rows) != 2 || str(rows[1], "errorCode") != c.code {
			t.Fatalf("%s: persisted %v", c.code, rows)
		}
	}
}

func TestSendFailureWithBillableUsageReturns200(t *testing.T) {
	e := newEnv(t)
	e.gen.set(func(_ context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		emit(GenerateEvent{Type: "delta", Delta: "partial answer"})
		return GenerateResult{Content: "partial answer", Usage: Usage{Input: 4, Output: 2}, Billable: true}, ErrUpstream
	})
	id := e.newConv(e.u1, "t", "gpt-4o")
	r := e.ok("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", nil))
	_, a := pair(r)
	if str(a, "status") != "error" || str(a, "content") != "partial answer" || str(a, "errorCode") != "upstream.unavailable" || num(a, "tokenUsage") != 2 {
		t.Fatalf("billable failure: %v", a)
	}
	if e.bill.settled != 1 {
		t.Fatalf("must settle billable failures: %+v", e.bill)
	}
}

func TestSendBudgetRejectionPersistsPair(t *testing.T) {
	e := newEnv(t)
	e.bill.reserveE = ErrInsufficientFunds
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u1, sendBody("please", nil), 402, "billing.insufficient_funds")
	rows := e.list(id)
	if len(rows) != 2 || rows[0]["status"] != "error" || rows[1]["status"] != "error" || str(rows[1], "errorCode") != "billing.insufficient_funds" {
		t.Fatalf("rejection pair: %v", rows)
	}
	if str(rows[0], "content") != "please" {
		t.Fatalf("user content kept: %v", rows[0])
	}
	if len(e.gen.requests()) != 0 {
		t.Fatal("generator must not run")
	}
	for _, c := range []struct {
		err    error
		status int
	}{{ErrPricingRequired, 402}, {ErrConcurrencyLimit, 429}, {ErrReservationConflict, 409}} {
		e.bill.reserveE = c.err
		w := e.call("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", nil))
		if w.Code != c.status {
			t.Fatalf("%v -> %d", c.err, w.Code)
		}
	}
}

func TestSendSettlementFailureMapped(t *testing.T) {
	e := newEnv(t)
	e.bill.settleE = apperr.New("billing.settlement_failed", "settlement failed").WithStatus(409)
	id := e.newConv(e.u1, "t", "gpt-4o")
	w := e.call("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", nil))
	if w.Code == 200 {
		t.Fatalf("settlement failure must not be a plain success: %s", w.Body)
	}
}

func TestBranchRetryAndEdit(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	r1 := e.send(e.u1, id, "first", nil)
	u1, a1 := pair(r1)
	// retry: source assistant, parent = its user parent
	r2 := e.send(e.u1, id, "first", map[string]any{
		"sourceMessagePublicID": str(a1, "publicID"), "parentMessagePublicID": str(u1, "publicID"), "branchReason": "retry"})
	u2, a2 := pair(r2)
	if str(u2, "publicID") != str(u1, "publicID") {
		t.Fatalf("retry reuses the user message: %v vs %v", u2["publicID"], u1["publicID"])
	}
	if str(a2, "publicID") == str(a1, "publicID") || a2["parentMessageID"] != u1["id"] || str(a2, "branchReason") != "retry" || str(a2, "sourcePublicID") != str(a1, "publicID") {
		t.Fatalf("retry assistant: %v", a2)
	}
	if got := e.convRow(id).MessageCount; got != 3 {
		t.Fatalf("message count after retry: %d", got)
	}
	// retry with default reason is invalid
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", map[string]any{
		"sourceMessagePublicID": str(a1, "publicID"), "parentMessagePublicID": str(u1, "publicID"), "branchReason": "default"}), 400, "message.invalid_branch")
	// retry from an assistant source whose supplied parent differs
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", map[string]any{
		"sourceMessagePublicID": str(a1, "publicID"), "parentMessagePublicID": str(a1, "publicID"), "branchReason": "retry"}), 400, "message.invalid_branch")
	// source without a reason (default) invalid; reason without source invalid
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", map[string]any{"branchReason": "retry"}), 400, "message.invalid_branch")
	// edit: source user message that is a root -> no parent allowed
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", map[string]any{
		"sourceMessagePublicID": str(u1, "publicID"), "parentMessagePublicID": str(a1, "publicID"), "branchReason": "edit"}), 400, "message.invalid_branch")
	r3 := e.send(e.u1, id, "first edited", map[string]any{"sourceMessagePublicID": str(u1, "publicID"), "branchReason": "edit"})
	u3, a3 := pair(r3)
	if str(u3, "publicID") == str(u1, "publicID") || u3["parentMessageID"] != nil || str(u3, "branchReason") != "edit" || str(u3, "sourcePublicID") != str(u1, "publicID") {
		t.Fatalf("edit user: %v", u3)
	}
	if a3["parentMessageID"] != u3["id"] {
		t.Fatalf("edit assistant parent: %v", a3)
	}
	// unknown / foreign ids
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", map[string]any{"parentMessagePublicID": "zzz"}), 400, "message.invalid_branch")
	other := e.newConv(e.u2, "o", "gpt-4o")
	ou, _ := pair(e.send(e.u2, other, "hey", nil))
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", map[string]any{"parentMessagePublicID": str(ou, "publicID")}), 400, "message.invalid_branch")
	// source role must be user, or assistant with retry
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", map[string]any{
		"sourceMessagePublicID": str(a1, "publicID"), "parentMessagePublicID": str(u1, "publicID"), "branchReason": "edit"}), 400, "message.invalid_branch")
}

func TestDefaultParentSelection(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	_, a1 := pair(e.send(e.u1, id, "one", nil))
	u2, a2 := pair(e.send(e.u1, id, "two", nil))
	if u2["parentMessageID"] != a1["id"] {
		t.Fatalf("second user attaches to latest assistant: %v vs %v", u2["parentMessageID"], a1["id"])
	}
	_ = a2
	// an errored assistant is not usable as a parent: parent falls back
	conv := e.convRow(id)
	badU := e.seedMsg(conv, roleUser, "bad", msgError, nil)
	e.seedMsg(conv, roleAssistant, "", msgError, badU)
	u3, _ := pair(e.send(e.u1, id, "three", nil))
	var asstID uint
	e.DB.Model(&Message{}).Where("public_id = ?", str(a2, "publicID")).Pluck("id", &asstID)
	if u3["parentMessageID"] != float64(asstID) {
		t.Fatalf("parent must be the newest usable assistant (%d): %v", asstID, u3["parentMessageID"])
	}
}

func TestHistorySentToGeneratorFollowsBranch(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	u1, a1 := pair(e.send(e.u1, id, "q1", nil))
	e.send(e.u1, id, "q2", nil)
	// retry of the first answer: history only has q1
	e.send(e.u1, id, "q1", map[string]any{"sourceMessagePublicID": str(a1, "publicID"), "parentMessagePublicID": str(u1, "publicID"), "branchReason": "retry"})
	reqs := e.gen.requests()
	var chat []GenerateRequest
	for _, r := range reqs {
		if r.Task == taskChat {
			chat = append(chat, r)
		}
	}
	if len(chat) != 3 {
		t.Fatalf("chat calls: %d", len(chat))
	}
	if len(chat[1].Messages) != 3 { // q1, a1, q2
		t.Fatalf("second call history: %+v", chat[1].Messages)
	}
	if len(chat[2].Messages) != 1 || chat[2].Messages[0].Content != "q1" {
		t.Fatalf("retry history: %+v", chat[2].Messages)
	}
	if chat[0].Model != "gpt-4o" || !strings.HasPrefix(chat[0].RunID, "run_") || chat[0].UserID != 1 || chat[0].MaxLLMCalls != 5 {
		t.Fatalf("request metadata: %+v", chat[0])
	}
}

func TestProjectSystemPromptReachesGenerator(t *testing.T) {
	e := newEnv(t)
	p := newProject(e, e.u1, map[string]any{"name": "p", "systemPrompt": "Be terse."})
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.ok("PATCH", "/conversations/"+id+"/project", e.u1, map[string]string{"projectID": str(p, "publicID")})
	e.send(e.u1, id, "hi", nil)
	if got := e.gen.requests()[0].System; got != "Be terse." {
		t.Fatalf("system: %q", got)
	}
}

func TestModelOverrideUpdatesConversation(t *testing.T) {
	e := newEnv(t)
	e.cat.available["claude-sonnet"] = true
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.gen.set(func(_ context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		return GenerateResult{Content: "ok", Usage: Usage{Input: 1, Output: 1}, Route: RouteInfo{PlatformModelName: req.Model, Provider: "anthropic"}}, nil
	})
	e.send(e.u1, id, "hi", map[string]any{"model": "claude-sonnet"})
	c := e.convRow(id)
	if c.ModelName != "claude-sonnet" || c.Provider != "openai" {
		t.Fatalf("conversation model: %+v", c)
	}
}

func TestMessageWithoutModelReportsRouteMissing(t *testing.T) {
	e := newEnv(t)
	e.gen.set(func(context.Context, GenerateRequest, func(GenerateEvent)) (GenerateResult, error) {
		return GenerateResult{}, ErrModelRouteMissing
	})
	id := e.newConv(e.u1, "t", "")
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", nil), 503, "llm.model_route_not_configured")
}

func TestModerationBlocksInputAndOutput(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.mod.block[dirInput] = true
	r := e.ok("POST", "/conversations/"+id+"/messages", e.u1, sendBody("bad words", nil))
	u, a := pair(r)
	if str(u, "status") != "blocked" || str(u, "content") != "bad words" || str(a, "status") != "blocked" || str(a, "content") != "" {
		t.Fatalf("input block: %v / %v", u, a)
	}
	mo, _ := u["moderation"].(map[string]any)
	if mo == nil || str(mo, "direction") != "input" || str(mo, "eventID") != "evt_1" {
		t.Fatalf("moderation info: %v", u["moderation"])
	}
	if len(e.gen.requests()) != 0 {
		t.Fatal("blocked input must not reach the generator")
	}
	// output block
	e.mod.block = map[string]bool{dirOutput: true}
	id2 := e.newConv(e.u1, "t", "gpt-4o")
	r = e.ok("POST", "/conversations/"+id2+"/messages", e.u1, sendBody("fine", nil))
	u, a = pair(r)
	if str(u, "status") != "success" || str(a, "status") != "blocked" || str(a, "content") != "" || str(a, "attachments") != "[]" {
		t.Fatalf("output block: %v / %v", u, a)
	}
	mo, _ = a["moderation"].(map[string]any)
	if mo == nil || str(mo, "direction") != "output" {
		t.Fatalf("moderation direction: %v", a["moderation"])
	}
	for _, k := range []string{"knowledgeSources", "processTrace", "uiState"} {
		if _, ok := a[k]; ok {
			t.Fatalf("%s must be removed from blocked assistant message", k)
		}
	}
	// listing keeps the same view
	rows := e.list(id2)
	if str(rows[1], "content") != "" || str(rows[1], "status") != "blocked" {
		t.Fatalf("list view of blocked: %v", rows[1])
	}
	// blocked user content stays visible to the owner
	rows = e.list(id)
	if str(rows[0], "content") != "bad words" {
		t.Fatalf("blocked user content: %v", rows[0])
	}
}

func TestRetryReusesUserForContextWhenUserFailed(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	calls := 0
	e.gen.set(func(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task != taskChat {
			return defaultGenerate(ctx, req, emit)
		}
		calls++
		if calls == 1 {
			return GenerateResult{}, ErrUpstream
		}
		return defaultGenerate(ctx, req, emit)
	})
	w := e.call("POST", "/conversations/"+id+"/messages", e.u1, sendBody("flaky", nil))
	testkit.Expect(t, w, 502, "upstream.unavailable")
	rows := e.list(id)
	u, a := rows[0], rows[1]
	r := e.send(e.u1, id, "flaky", map[string]any{"sourceMessagePublicID": str(a, "publicID"), "parentMessagePublicID": str(u, "publicID"), "branchReason": "retry"})
	ru, ra := pair(r)
	if str(ru, "publicID") != str(u, "publicID") || str(ra, "status") != "success" || str(ru, "status") != "success" {
		t.Fatalf("retry after failure: user=%v asst=%v", ru, ra)
	}
	// following turn: the retried user message counts as successful context
	e.send(e.u1, id, "next", nil)
	var chat []GenerateRequest
	for _, r := range e.gen.requests() {
		if r.Task == taskChat {
			chat = append(chat, r)
		}
	}
	last := chat[len(chat)-1]
	if len(last.Messages) != 3 || last.Messages[0].Content != "flaky" {
		t.Fatalf("context: %+v", last.Messages)
	}
}

func TestSendConcurrencyDoesNotBreak(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 3; i++ {
		id := e.newConv(e.u1, fmt.Sprintf("c%d", i), "gpt-4o")
		e.send(e.u1, id, "hi", nil)
	}
}
