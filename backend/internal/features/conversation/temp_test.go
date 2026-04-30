package conversation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func tempBody(msgs ...[2]string) map[string]any {
	var ms []map[string]string
	for _, m := range msgs {
		ms = append(ms, map[string]string{"role": m[0], "content": m[1]})
	}
	return map[string]any{"sessionID": "sess-1", "clientRunID": "t-run-1", "model": "gpt-4o", "messages": ms}
}

func countRows(e *tenv) map[string]int64 {
	out := map[string]int64{}
	for name, model := range map[string]any{"conv": &Conversation{}, "msg": &Message{}, "run": &Run{}, "file": &FileObject{}, "att": &Attachment{}} {
		var n int64
		e.DB.Unscoped().Model(model).Count(&n)
		out[name] = n
	}
	return out
}

func TestTemporaryChatHappyPathPersistsNothing(t *testing.T) {
	e := newEnv(t)
	w := e.call("POST", "/temporary-chat/messages/stream", e.u1, tempBody([2]string{"user", "hi"}, [2]string{"assistant", "hello"}, [2]string{"user", "and now?"}))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	h := w.Header()
	if h.Get("Cache-Control") != "no-store, no-cache, no-transform" || h.Get("Pragma") != "no-cache" || !strings.HasPrefix(h.Get("Content-Type"), "application/x-ndjson") {
		t.Fatalf("headers: %v", h)
	}
	lines := ndjson(t, w)
	last := lines[len(lines)-1]
	if str(last, "type") != "completed" {
		t.Fatalf("lines: %v", types(lines))
	}
	data := last["data"].(map[string]any)
	if str(data["assistantMessage"].(map[string]any), "status") != "success" || !strings.Contains(str(data["assistantMessage"].(map[string]any), "content"), "and now?") {
		t.Fatalf("data: %v", data)
	}
	if _, has := last["seq"]; has {
		t.Fatalf("completed event must not carry seq: %v", last)
	}
	am, um := data["assistantMessage"].(map[string]any), data["userMessage"].(map[string]any)
	for _, m := range []map[string]any{am, um} {
		if len(str(m, "publicID")) != 32 || str(m, "runID") != "t-run-1" || num(m, "userID") == 0 {
			t.Fatalf("temporary message identity fields missing: %v", m)
		}
		if str(m, "branchReason") != "" || str(m, "attachments") != "" {
			t.Fatalf("temporary message branch/attachments: %v", m)
		}
	}
	if str(am, "publicID") == str(um, "publicID") || str(um, "platformModelName") == "" {
		t.Fatalf("temporary identity: %v %v", am, um)
	}
	for k, n := range countRows(e) {
		if n != 0 {
			t.Fatalf("temporary chat must not persist %s rows (%d)", k, n)
		}
	}
	req := e.gen.requests()[0]
	if !req.Temporary || len(req.Messages) != 3 || req.Model != "gpt-4o" {
		t.Fatalf("generate request: %+v", req)
	}
	// audit: counts and a hash only
	a := e.audit.find("temporary_chat.stream_message")
	if a == nil || a.Detail["status"] != "completed" || a.Detail["content_stored"] != false || a.Detail["session_hash"] == "sess-1" {
		t.Fatalf("audit: %+v", a)
	}
	if strings.Contains(fmt.Sprint(a.Detail), "and now?") {
		t.Fatal("audit must not contain content")
	}
	// moderation is ephemeral
	for _, m := range e.mod.seen {
		if !m.Ephemeral {
			t.Fatalf("moderation must be ephemeral: %+v", m)
		}
	}
	if e.bill.reserved != 1 || e.bill.settled != 1 {
		t.Fatalf("billing: %+v", e.bill)
	}
}

func TestTemporaryChatValidation(t *testing.T) {
	e := newEnv(t)
	p := "/temporary-chat/messages/stream"
	sem := func(body map[string]any) {
		t.Helper()
		e.expectErr("POST", p, e.u1, body, 400, "request.invalid_temporary_chat_messages")
	}
	body := func(msgs ...[2]string) map[string]any { return tempBody(msgs...) }
	sem(body([2]string{"user", "a"}, [2]string{"user", "b"}))
	sem(body([2]string{"user", "a"}, [2]string{"assistant", "b"}))
	sem(body([2]string{"assistant", "a"}))
	sem(body([2]string{"user", "   "}))
	e.expectErr("POST", p, e.u1, body([2]string{"system", "a"}, [2]string{"user", "b"}), 400, "request.invalid_body")
	sem(body([2]string{"user", "a"}, [2]string{"assistant", ""}, [2]string{"user", "b"}))
	b := body([2]string{"user", "a"})
	b["knowledgeBaseIDs"] = []string{"kb", "kb"}
	sem(b)
	b = body([2]string{"user", "a"})
	b["knowledgeBaseIDs"] = []string{" "}
	sem(b)
	// too many messages -> body validation
	var many [][2]string
	for i := 0; i < 101; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		many = append(many, [2]string{role, "x"})
	}
	e.expectErr("POST", p, e.u1, body(many...), 400, "request.invalid_body")
	e.expectErr("POST", p, e.u1, map[string]any{"sessionID": "s", "clientRunID": "r", "model": "gpt-4o", "messages": []any{}}, 400, "request.invalid_body")
	for _, drop := range []string{"sessionID", "clientRunID", "model", "messages"} {
		b = body([2]string{"user", "a"})
		delete(b, drop)
		e.expectErr("POST", p, e.u1, b, 400, "request.invalid_body")
	}
	b = body([2]string{"user", "a"})
	b["sessionID"] = strings.Repeat("s", 65)
	e.expectErr("POST", p, e.u1, b, 400, "request.invalid_body")
	e.expectErr("POST", p, e.u1, body([2]string{"user", strings.Repeat("c", 200001)}), 400, "request.invalid_body")
	b = body([2]string{"user", "a"})
	b["uiStates"] = []map[string]any{{"messageIndex": 100, "blockID": "b", "state": map[string]any{"a": 1}}}
	e.expectErr("POST", p, e.u1, b, 400, "request.invalid_body")
	b = body([2]string{"user", "a"})
	b["uiBlockVersion"] = 3
	e.expectErr("POST", p, e.u1, b, 400, "request.invalid_body")
	b = body([2]string{"user", "a"})
	e.set.mutate(func(r *RuntimeSettings) { r.MaxSelectedTools = 2 })
	b["selectedToolIDs"] = []uint{1, 2, 3}
	e.expectErr("POST", p, e.u1, b, 400, "message.too_many_selected_tools")
	e.expectErr("POST", p, e.u1, "", 400, "request.invalid_body")
	e.expectErr("POST", p, e.u1, body([2]string{"user", "a"}), 200, "")
	// total content cap (1,000,000 runes) with many large messages
	var big [][2]string
	for i := 0; i < 11; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		big = append(big, [2]string{role, strings.Repeat("y", 100000)})
	}
	sem(body(big...))
	// unavailable model
	b = body([2]string{"user", "a"})
	b["model"] = "forbidden"
	e.expectErr("POST", p, e.u1, b, 403, "llm.model_access_denied")
	testkit.Expect(t, e.call("POST", p, nil, b), 401, "")
	// draining
	e.Deps.Lifecycle.Drain().Begin()
	e.expectErr("POST", p, e.u1, body([2]string{"user", "a"}), 503, "service.unavailable")
}

func multipartTemp(t *testing.T, payload map[string]any, indexes []int, files [][2]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	raw, _ := json.Marshal(payload)
	_ = mw.WriteField("payload", string(raw))
	if indexes != nil {
		ix, _ := json.Marshal(indexes)
		_ = mw.WriteField("attachmentMessageIndexes", string(ix))
	}
	for _, f := range files {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="attachments"; filename="%s"`, f[0]))
		h.Set("Content-Type", "text/plain")
		part, _ := mw.CreatePart(h)
		_, _ = part.Write([]byte(f[1]))
	}
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

func (e *tenv) postMultipart(buf *bytes.Buffer, ct string) (code int, body string, w *syncRecorderResult) {
	hh := map[string]string{"Content-Type": ct}
	for k, v := range e.u1 {
		hh[k] = v
	}
	rec := e.Do(http.MethodPost, e.basePath+"/temporary-chat/messages/stream", hh, buf.Bytes())
	return rec.Code, rec.Body.String(), &syncRecorderResult{rec.Header().Get("Content-Type")}
}

type syncRecorderResult struct{ ct string }

func TestTemporaryChatMultipart(t *testing.T) {
	e := newEnv(t)
	payload := tempBody([2]string{"user", "read this"})
	buf, ct := multipartTemp(t, payload, []int{0}, [][2]string{{"note.txt", "attachment body"}})
	code, body, _ := e.postMultipart(buf, ct)
	if code != 200 || !strings.Contains(body, `"completed"`) {
		t.Fatalf("multipart: %d %s", code, body)
	}
	req := e.gen.requests()[0]
	files := req.Messages[0].Files
	if len(files) != 1 || files[0].FileName != "note.txt" || files[0].Text != "attachment body" {
		t.Fatalf("attachment injected into context: %+v", files)
	}
	for k, n := range countRows(e) {
		if n != 0 {
			t.Fatalf("no %s rows expected", k)
		}
	}
	// a user message with only an attachment is fine
	buf, ct = multipartTemp(t, tempBody([2]string{"user", ""}), []int{0}, [][2]string{{"a.txt", "x"}})
	if code, body, _ = e.postMultipart(buf, ct); code != 200 {
		t.Fatalf("attachment-only message: %d %s", code, body)
	}
	// mismatch between index list and parts
	buf, ct = multipartTemp(t, payload, []int{0, 0}, [][2]string{{"a.txt", "x"}})
	code, body, _ = e.postMultipart(buf, ct)
	if code != 400 || !strings.Contains(body, "file.invalid_reference") {
		t.Fatalf("mismatch: %d %s", code, body)
	}
	// zero file parts in multipart mode
	buf, ct = multipartTemp(t, payload, nil, nil)
	if code, body, _ = e.postMultipart(buf, ct); code != 400 || !strings.Contains(body, "file.invalid_reference") {
		t.Fatalf("no parts: %d %s", code, body)
	}
	// 21 files
	var many [][2]string
	var idx []int
	for i := 0; i < 21; i++ {
		many = append(many, [2]string{fmt.Sprintf("f%d.txt", i), "x"})
		idx = append(idx, 0)
	}
	buf, ct = multipartTemp(t, payload, idx, many)
	if code, body, _ = e.postMultipart(buf, ct); code != 400 || !strings.Contains(body, "file.invalid_reference") {
		t.Fatalf("21 files: %d %s", code, body)
	}
	// more than the per-message limit
	e.set.mutate(func(r *RuntimeSettings) { r.MaxMessageFiles = 2 })
	buf, ct = multipartTemp(t, payload, []int{0, 0, 0}, [][2]string{{"a.txt", "x"}, {"b.txt", "y"}, {"c.txt", "z"}})
	if code, body, _ = e.postMultipart(buf, ct); code != 400 || !strings.Contains(body, "message.too_many_files") {
		t.Fatalf("per-message limit: %d %s", code, body)
	}
	// index pointing at an assistant message / out of range
	two := tempBody([2]string{"user", "a"}, [2]string{"assistant", "b"}, [2]string{"user", "c"})
	buf, ct = multipartTemp(t, two, []int{1}, [][2]string{{"a.txt", "x"}})
	if code, body, _ = e.postMultipart(buf, ct); code != 400 || !strings.Contains(body, "request.invalid_temporary_chat_messages") {
		t.Fatalf("assistant index: %d %s", code, body)
	}
	buf, ct = multipartTemp(t, payload, []int{5}, [][2]string{{"a.txt", "x"}})
	if code, body, _ = e.postMultipart(buf, ct); code != 400 || !strings.Contains(body, "request.invalid_temporary_chat_messages") {
		t.Fatalf("range: %d %s", code, body)
	}
	// blocked file type
	e.set.mutate(func(r *RuntimeSettings) { r.MaxMessageFiles = 10 })
	buf, ct = multipartTemp(t, payload, []int{0}, [][2]string{{"evil.exe", "MZ"}})
	if code, body, _ = e.postMultipart(buf, ct); code != 400 || !strings.Contains(body, "file.type_blocked") {
		t.Fatalf("blocked: %d %s", code, body)
	}
}

func TestTemporaryChatOversize(t *testing.T) {
	e := newEnv(t)
	huge := tempBody([2]string{"user", strings.Repeat("z", 8<<20+10)})
	w := e.call("POST", "/temporary-chat/messages/stream", e.u1, huge)
	testkit.Expect(t, w, 413, "temporary_chat.context_too_large")
	// multipart total = 8 MiB + upload limit * 20
	e.set.mutate(func(r *RuntimeSettings) { r.MaxUploadBytes = 100 })
	payload := tempBody([2]string{"user", "a"})
	buf, ct := multipartTemp(t, payload, []int{0}, [][2]string{{"a.txt", strings.Repeat("a", 8<<20+3000)}})
	code, body, _ := e.postMultipart(buf, ct)
	if code != 413 {
		t.Fatalf("multipart oversize: %d %.200s", code, body)
	}
	if !strings.Contains(body, "temporary_chat.context_too_large") {
		t.Fatalf("code: %.300s", body)
	}
	// a single file above the per-file limit
	buf, ct = multipartTemp(t, payload, []int{0}, [][2]string{{"a.txt", strings.Repeat("a", 500)}})
	if code, body, _ = e.postMultipart(buf, ct); code != 413 || !strings.Contains(body, "file.too_large") {
		t.Fatalf("per-file: %d %.200s", code, body)
	}
}

func TestTemporaryChatFailureModes(t *testing.T) {
	e := newEnv(t)
	p := "/temporary-chat/messages/stream"
	b := tempBody([2]string{"user", "hi"})
	// upstream failure -> error event, no data payload
	e.gen.set(func(context.Context, GenerateRequest, func(GenerateEvent)) (GenerateResult, error) {
		return GenerateResult{}, ErrUpstream
	})
	lines := ndjson(t, e.call("POST", p, e.u1, b))
	last := lines[len(lines)-1]
	if str(last, "type") != "error" || str(last, "errorCode") != "upstream.unavailable" || num(last, "status") != 502 || last["data"] != nil || str(last, "requestId") == "" {
		t.Fatalf("error: %v", last)
	}
	if a := e.audit.find("temporary_chat.stream_message"); a == nil || a.Detail["status"] != "failed" {
		t.Fatalf("audit: %+v", a)
	}
	if e.bill.released == 0 {
		t.Fatal("reservation released after unbillable failure")
	}
	// billing rejection is an ordinary HTTP error
	e.bill.reserveE = ErrInsufficientFunds
	e.expectErr("POST", p, e.u1, b, 402, "billing.insufficient_funds")
	found := false
	e.audit.mu.Lock()
	for _, en := range e.audit.entries {
		if en.Action == "temporary_chat.stream_message" && en.Detail["status"] == "billing_failed" {
			found = true
		}
	}
	e.audit.mu.Unlock()
	if !found {
		t.Fatal("billing_failed audit")
	}
	e.bill.reserveE = nil
	// moderation
	e.gen.set(nil)
	e.mod.block[dirInput] = true
	lines = ndjson(t, e.call("POST", p, e.u1, b))
	if str(lines[len(lines)-1], "type") != "moderation_blocked" || str(lines[len(lines)-1], "direction") != "input" {
		t.Fatalf("blocked: %v", lines)
	}
	e.mod.block = map[string]bool{dirOutput: true}
	lines = ndjson(t, e.call("POST", p, e.u1, b))
	if str(lines[len(lines)-1], "type") != "moderation_blocked" || str(lines[len(lines)-1], "direction") != "output" {
		t.Fatalf("blocked output: %v", lines)
	}
	for k, n := range countRows(e) {
		if n != 0 {
			t.Fatalf("no %s rows even on failure", k)
		}
	}
}

func TestTemporaryChatClientDisconnectCancelsGeneration(t *testing.T) {
	e := newEnv(t)
	started := make(chan struct{})
	stopped := make(chan struct{})
	e.gen.set(func(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return GenerateResult{}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	_, done := e.stream(ctx, "POST", "/temporary-chat/messages/stream", e.u1, tempBody([2]string{"user", "hi"}))
	<-started
	cancel()
	waitDone(t, done)
	select {
	case <-stopped:
	default:
		t.Fatal("generation must be bound to the request")
	}
}

func TestTemporaryChatSanitizesAndPassesSelections(t *testing.T) {
	e := newEnv(t)
	e.set.mutate(func(r *RuntimeSettings) { r.RAGAvailable = true })
	b := tempBody([2]string{"user", "hi"})
	b["options"] = map[string]any{"temperature": 1, "reasoning_effort": "low", "stream": true}
	b["controls"] = map[string]any{"k": "v", "bad": []int{1}}
	b["selectedToolIDs"] = []uint{4, 5}
	b["skillIDs"] = []uint{0, 9}
	b["knowledgeBaseIDs"] = []string{"kb1"}
	b["htmlVisualPrompt"] = true
	b["uiStates"] = []map[string]any{{"messageIndex": 0, "blockID": "b", "title": "t", "state": map[string]any{"a": 1}}}
	e.call("POST", "/temporary-chat/messages/stream", e.u1, b)
	req := e.gen.requests()[0]
	if _, ok := req.Options["temperature"]; ok || req.Options["reasoning_effort"] != "low" || req.Controls["reasoning"] != "low" || req.Controls["k"] != "v" || req.Controls["bad"] != nil {
		t.Fatalf("sanitised: %+v %+v", req.Options, req.Controls)
	}
	if fmt.Sprint(req.ToolIDs) != "[4 5]" || fmt.Sprint(req.SkillIDs) != "[9]" || fmt.Sprint(req.KnowledgeBaseIDs) != "[kb1]" || !req.HTMLVisualPrompt || len(req.UIStates) != 1 {
		t.Fatalf("selections: %+v", req)
	}
}
