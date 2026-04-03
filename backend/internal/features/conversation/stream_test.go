package conversation

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func fastTimings(t *timings) {
	t.leaseCheck = 30 * time.Millisecond
	t.cancelPoll = 10 * time.Millisecond
	t.leaseRefresh = time.Hour
	t.userSnapshot = 80 * time.Millisecond
	t.remotePoll = 10 * time.Millisecond
}

func TestStreamHappyPath(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	w := e.call("POST", "/conversations/"+id+"/messages/stream", e.u1, sendBody("stream me", map[string]any{"clientRunID": "s1"}))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	h := w.Header()
	if h.Get("Content-Type") != "application/x-ndjson; charset=utf-8" || h.Get("Cache-Control") != "no-cache, no-transform" ||
		h.Get("Connection") != "keep-alive" || h.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("headers: %v", h)
	}
	lines := ndjson(t, w)
	got := types(lines)
	if len(got) < 3 || got[len(got)-1] != "completed" {
		t.Fatalf("types: %v", got)
	}
	completed := 0
	deltas := ""
	prev := 0.0
	for _, l := range lines {
		if str(l, "type") == "completed" {
			completed++
		}
		if str(l, "type") == "delta" {
			deltas += str(l, "delta")
		}
		if num(l, "seq") <= prev {
			t.Fatalf("seq must increase: %v", lines)
		}
		prev = num(l, "seq")
	}
	if completed != 1 || deltas != "Hello world" {
		t.Fatalf("completed=%d deltas=%q", completed, deltas)
	}
	data := lines[len(lines)-1]["data"].(map[string]any)
	a := data["assistantMessage"].(map[string]any)
	u := data["userMessage"].(map[string]any)
	if str(a, "status") != "success" || a["parentMessageID"] != u["id"] {
		t.Fatalf("completed data: %v", data)
	}
	rows := e.list(id)
	if len(rows) != 2 || str(rows[1], "status") != "success" {
		t.Fatalf("persisted: %v", rows)
	}
	eventually(t, "async stream audit", func() bool { return e.audit.find("stream_message") != nil })
}

func TestStreamPreambleErrorsAreEnvelopes(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	p := "/conversations/" + id + "/messages/stream"
	e.expectErr("POST", p, e.u1, map[string]any{}, 400, "request.invalid_body")
	e.expectErr("POST", "/conversations/nope/messages/stream", e.u1, sendBody("x", nil), 404, "conversation.not_found")
	e.bill.reserveE = ErrInsufficientFunds
	w := e.call("POST", p, e.u1, sendBody("x", nil))
	testkit.Expect(t, w, 402, "billing.insufficient_funds")
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("content type: %q", w.Header().Get("Content-Type"))
	}
	e.bill.reserveE = nil
	// draining server
	e.Deps.Lifecycle.Drain().Begin()
	e.expectErr("POST", p, e.u1, sendBody("x", nil), 503, "service.unavailable")
}

func TestStreamErrorEventCarriesPersistedPair(t *testing.T) {
	e := newEnv(t)
	e.gen.set(func(_ context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		emit(GenerateEvent{Type: "delta", Delta: "par"})
		return GenerateResult{Content: "par"}, ErrUpstream
	})
	id := e.newConv(e.u1, "t", "gpt-4o")
	w := e.call("POST", "/conversations/"+id+"/messages/stream", e.u1, sendBody("x", nil))
	if w.Code != 200 {
		t.Fatalf("stream failures are in-band: %d", w.Code)
	}
	lines := ndjson(t, w)
	last := lines[len(lines)-1]
	if str(last, "type") != "error" || num(last, "status") != 502 || str(last, "errorCode") != "upstream.unavailable" || str(last, "message") == "" || str(last, "requestId") == "" {
		t.Fatalf("error event: %v", last)
	}
	data, _ := last["data"].(map[string]any)
	if data == nil {
		t.Fatalf("persisted pair expected in error data: %v", last)
	}
	a := data["assistantMessage"].(map[string]any)
	if str(a, "status") != "error" || str(a, "content") != "par" {
		t.Fatalf("partial answer persisted: %v", a)
	}
	for _, l := range lines[:len(lines)-1] {
		if isTerminalType(str(l, "type")) {
			t.Fatalf("exactly one terminal event: %v", lines)
		}
	}
}

func TestStreamModerationBlockedEvent(t *testing.T) {
	e := newEnv(t)
	e.mod.block[dirInput] = true
	id := e.newConv(e.u1, "t", "gpt-4o")
	w := e.call("POST", "/conversations/"+id+"/messages/stream", e.u1, sendBody("nope", nil))
	lines := ndjson(t, w)
	last := lines[len(lines)-1]
	if str(last, "type") != "moderation_blocked" || str(last, "direction") != "input" || str(last, "eventID") != "evt_1" {
		t.Fatalf("blocked event: %v", last)
	}
	cats, _ := last["categories"].([]any)
	if len(cats) != 1 {
		t.Fatalf("categories: %v", last)
	}
}

func TestStreamProgressEventsForwarded(t *testing.T) {
	e := newEnv(t)
	e.gen.set(func(_ context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		emit(GenerateEvent{Type: "rag_search", Fields: map[string]any{"query": "q"}})
		emit(GenerateEvent{Type: "upstream_think_delta", Delta: "hmm"})
		emit(GenerateEvent{Type: "process_update", Fields: map[string]any{"title": "step"}})
		emit(GenerateEvent{Type: "delta", Delta: "ok"})
		emit(GenerateEvent{Type: "usage", Fields: map[string]any{"inputTokens": 3}})
		return GenerateResult{Content: "ok", Reasoning: "hmm", Usage: Usage{Input: 3, Output: 1}}, nil
	})
	id := e.newConv(e.u1, "t", "gpt-4o")
	w := e.call("POST", "/conversations/"+id+"/messages/stream", e.u1, sendBody("x", nil))
	got := strings.Join(types(ndjson(t, w)), ",")
	if got != "rag_search,upstream_think_delta,process_update,delta,usage,completed" {
		t.Fatalf("types: %s", got)
	}
	m := e.msgRow(str(e.list(id)[1], "publicID"))
	if m.ReasoningContent != "hmm" {
		t.Fatalf("reasoning kept for passback: %q", m.ReasoningContent)
	}
}

func TestResumeReplaysBufferedEvents(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	w := e.call("POST", "/conversations/"+id+"/messages/stream", e.u1, sendBody("x", map[string]any{"clientRunID": "resume1"}))
	orig := ndjson(t, w)
	run := "run_resume1"

	rw := e.call("GET", "/conversation-runs/"+run+"/stream?after=0", e.u1, nil)
	if rw.Code != 200 || rw.Header().Get("Content-Type") != "application/x-ndjson; charset=utf-8" {
		t.Fatalf("resume: %d %v", rw.Code, rw.Header())
	}
	replay := ndjson(t, rw)
	if len(replay) != len(orig) || str(replay[len(replay)-1], "type") != "completed" {
		t.Fatalf("replay differs: %v vs %v", types(replay), types(orig))
	}
	// after=<seq> skips seen events; negatives and junk mean 0
	rw = e.call("GET", "/conversation-runs/"+run+"/stream?after=1", e.u1, nil)
	replay = ndjson(t, rw)
	if len(replay) != len(orig)-1 || num(replay[0], "seq") != 2 {
		t.Fatalf("after=1: %v", types(replay))
	}
	for _, a := range []string{"-5", "abc", ""} {
		rw = e.call("GET", "/conversation-runs/"+run+"/stream?after="+a, e.u1, nil)
		if r := ndjson(t, rw); len(r) != len(orig) {
			t.Fatalf("after=%q: %d", a, len(r))
		}
	}
	// after the terminal seq: nothing more
	rw = e.call("GET", "/conversation-runs/"+run+"/stream?after=999", e.u1, nil)
	if r := ndjson(t, rw); len(r) != 0 {
		t.Fatalf("after beyond end: %v", r)
	}
	// snapshot
	rw = e.call("GET", "/conversation-runs/"+run+"/stream?snapshot=true", e.u1, nil)
	r := ndjson(t, rw)
	if str(r[0], "type") != "snapshot" || str(r[0], "content") != "Hello world" {
		t.Fatalf("snapshot: %v", r[0])
	}
	// another user and unknown runs
	e.expectErr("GET", "/conversation-runs/"+run+"/stream", e.u2, nil, 404, "conversation_run.stream_not_found")
	e.expectErr("GET", "/conversation-runs/run_unknown/stream", e.u1, nil, 404, "conversation_run.stream_not_found")
	e.expectErr("GET", "/conversation-runs/%20/stream", e.u1, nil, 400, "run.invalid_id")
	// after the retention window the buffer is gone
	e.Clock.Advance(16 * time.Minute)
	e.expectErr("GET", "/conversation-runs/"+run+"/stream", e.u1, nil, 404, "conversation_run.stream_not_found")
}

func TestResumeFromCacheWhenProcessLostMemory(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	w := e.call("POST", "/conversations/"+id+"/messages/stream", e.u1, sendBody("x", map[string]any{"clientRunID": "cache1"}))
	orig := ndjson(t, w)
	// simulate another server instance: no in-process state, only the shared store
	e.svc.hub.mu.Lock()
	e.svc.hub.runs = map[string]*liveRun{}
	e.svc.hub.mu.Unlock()
	rw := e.call("GET", "/conversation-runs/run_cache1/stream", e.u1, nil)
	replay := ndjson(t, rw)
	if len(replay) != len(orig) || str(replay[len(replay)-1], "type") != "completed" {
		t.Fatalf("shared-store replay: %v", types(replay))
	}
	e.expectErr("GET", "/conversation-runs/run_cache1/stream", e.u2, nil, 404, "conversation_run.stream_not_found")
}

func TestResumeUnknownRunMarksPendingInterrupted(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	conv := e.convRow(id)
	u := e.seedMsg(conv, roleUser, "q", msgPending, nil)
	a := e.seedMsg(conv, roleAssistant, "", msgPending, u)
	e.DB.Model(&Message{}).Where("id IN ?", []uint{u.ID, a.ID}).Update("run_id", "run_orphan")
	e.DB.Create(&Run{RunID: "run_orphan", UserID: 1, ConversationID: conv.ID, Status: runRunning, StartedAt: time.Now()})
	e.expectErr("GET", "/conversation-runs/run_orphan/stream", e.u1, nil, 404, "conversation_run.stream_not_found")
	got := e.msgRow(a.PublicID)
	if got.Status != msgInterrupted || got.ErrorCode != "conversation_run.stream_interrupted" {
		t.Fatalf("pending message must be interrupted: %+v", got)
	}
	var run Run
	e.DB.Where("run_id = ?", "run_orphan").First(&run)
	if run.Status != runInterrupted {
		t.Fatalf("run status: %s", run.Status)
	}
	// someone else's request must not touch it
	a2 := e.seedMsg(conv, roleAssistant, "", msgPending, u)
	e.DB.Model(a2).Update("run_id", "run_orphan2")
	e.expectErr("GET", "/conversation-runs/run_orphan2/stream", e.u2, nil, 404, "conversation_run.stream_not_found")
	if e.msgRow(a2.PublicID).Status != msgPending {
		t.Fatal("other users cannot interrupt a run")
	}
}

func TestCancelRun(t *testing.T) {
	e := newEnv(t)
	started := make(chan struct{})
	e.gen.set(func(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task != taskChat {
			return defaultGenerate(ctx, req, emit)
		}
		emit(GenerateEvent{Type: "delta", Delta: "partial"})
		close(started)
		<-ctx.Done()
		return GenerateResult{Content: "partial"}, ctx.Err()
	})
	id := e.newConv(e.u1, "t", "gpt-4o")
	rec, done := e.stream(context.Background(), "POST", "/conversations/"+id+"/messages/stream", e.u1, sendBody("long", map[string]any{"clientRunID": "c1"}))
	<-started
	// not the owner
	m := e.ok("POST", "/conversation-runs/run_c1/cancel", e.u2, nil)
	if m["canceled"] != false {
		t.Fatalf("other user's cancel: %v", m)
	}
	m = e.ok("POST", "/conversation-runs/run_c1/cancel", e.u1, nil)
	if m["canceled"] != true {
		t.Fatalf("cancel: %v", m)
	}
	waitDone(t, done)
	lines := parseLines(t, rec.text())
	last := lines[len(lines)-1]
	if str(last, "type") != "error" || str(last, "errorCode") != "conversation_run.canceled" {
		t.Fatalf("terminal event: %v", last)
	}
	rows := e.list(id)
	if str(rows[0], "status") != "success" || str(rows[1], "status") != "canceled" || str(rows[1], "errorCode") != "conversation_run.canceled" || str(rows[1], "content") != "partial" {
		t.Fatalf("persisted: %v / %v", rows[0], rows[1])
	}
	// finished run: not cancelable any more
	m = e.ok("POST", "/conversation-runs/run_c1/cancel", e.u1, nil)
	if m["canceled"] != false {
		t.Fatalf("finished: %v", m)
	}
	m = e.ok("POST", "/conversation-runs/run_missing/cancel", e.u1, nil)
	if m["canceled"] != false {
		t.Fatalf("unknown: %v", m)
	}
	e.expectErr("POST", "/conversation-runs/%20/cancel", e.u1, nil, 400, "run.invalid_id")
	var run Run
	e.DB.Where("run_id = ?", "run_c1").First(&run)
	if run.Status != runCanceled {
		t.Fatalf("run status: %s", run.Status)
	}
}
