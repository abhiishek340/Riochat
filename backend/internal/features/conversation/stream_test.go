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

func TestCancelNonStreamingReturnsCanceledError(t *testing.T) {
	e := newEnv(t)
	started := make(chan struct{})
	e.gen.set(func(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task != taskChat {
			return defaultGenerate(ctx, req, emit)
		}
		close(started)
		<-ctx.Done()
		return GenerateResult{}, ctx.Err()
	})
	id := e.newConv(e.u1, "t", "gpt-4o")
	rec, done := e.stream(context.Background(), "POST", "/conversations/"+id+"/messages", e.u1, sendBody("long", map[string]any{"clientRunID": "c2"}))
	<-started
	e.ok("POST", "/conversation-runs/run_c2/cancel", e.u1, nil)
	waitDone(t, done)
	if !strings.Contains(rec.text(), "conversation_run.canceled") {
		t.Fatalf("body: %s", rec.text())
	}
}

func TestClientDisconnectDoesNotStopGeneration(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	started := make(chan struct{})
	e.gen.set(func(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task != taskChat {
			return defaultGenerate(ctx, req, emit)
		}
		emit(GenerateEvent{Type: "delta", Delta: "one "})
		close(started)
		<-release
		emit(GenerateEvent{Type: "delta", Delta: "two"})
		return GenerateResult{Content: "one two", Usage: Usage{Input: 1, Output: 1}}, nil
	})
	id := e.newConv(e.u1, "t", "gpt-4o")
	ctx, cancel := context.WithCancel(context.Background())
	_, done := e.stream(ctx, "POST", "/conversations/"+id+"/messages/stream", e.u1, sendBody("x", map[string]any{"clientRunID": "dc1"}))
	<-started
	cancel() // the browser went away
	waitDone(t, done)
	close(release)
	eventually(t, "assistant success after disconnect", func() bool {
		var m Message
		return e.DB.Where("role = ? AND status = ?", roleAssistant, msgSuccess).First(&m).Error == nil
	})
	rw := e.call("GET", "/conversation-runs/run_dc1/stream?after=0", e.u1, nil)
	lines := ndjson(t, rw)
	if str(lines[len(lines)-1], "type") != "completed" || len(lines) < 3 {
		t.Fatalf("resumed events: %v", types(lines))
	}
}

func TestResumeLiveStreamFollowsNewEvents(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	started := make(chan struct{})
	e.gen.set(func(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task != taskChat {
			return defaultGenerate(ctx, req, emit)
		}
		emit(GenerateEvent{Type: "delta", Delta: "a"})
		close(started)
		<-release
		emit(GenerateEvent{Type: "delta", Delta: "b"})
		return GenerateResult{Content: "ab", Usage: Usage{Input: 1, Output: 1}}, nil
	})
	id := e.newConv(e.u1, "t", "gpt-4o")
	_, done := e.stream(context.Background(), "POST", "/conversations/"+id+"/messages/stream", e.u1, sendBody("x", map[string]any{"clientRunID": "live1"}))
	<-started
	rrec, rdone := e.stream(context.Background(), "GET", "/conversation-runs/run_live1/stream?snapshot=true", e.u1, nil)
	eventually(t, "snapshot line", func() bool { return strings.Contains(rrec.text(), `"snapshot"`) })
	close(release)
	waitDone(t, rdone)
	waitDone(t, done)
	lines := parseLines(t, rrec.text())
	if str(lines[0], "type") != "snapshot" || str(lines[0], "content") != "a" {
		t.Fatalf("snapshot: %v", lines[0])
	}
	if str(lines[len(lines)-1], "type") != "completed" {
		t.Fatalf("live follow: %v", types(lines))
	}
}

func TestLeaseLossInterruptsStream(t *testing.T) {
	e := newEnvTimed(t, fastTimings)
	release := make(chan struct{})
	started := make(chan struct{})
	e.gen.set(func(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task != taskChat {
			return defaultGenerate(ctx, req, emit)
		}
		emit(GenerateEvent{Type: "delta", Delta: "a"})
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return GenerateResult{Content: "late", Usage: Usage{Input: 1, Output: 1}}, nil
	})
	id := e.newConv(e.u1, "t", "gpt-4o")
	rec, done := e.stream(context.Background(), "POST", "/conversations/"+id+"/messages/stream", e.u1, sendBody("x", map[string]any{"clientRunID": "ll1"}))
	<-started
	_ = e.Deps.Cache.Delete(context.Background(), runKey("run_ll1", "lease"))
	waitDone(t, done)
	lines := parseLines(t, rec.text())
	last := lines[len(lines)-1]
	if str(last, "type") != "error" || str(last, "errorCode") != "conversation_run.stream_interrupted" || num(last, "status") != 503 ||
		str(last, "message") != "generation stream was interrupted; retry this message" {
		t.Fatalf("interruption: %v", last)
	}
	var run Run
	e.DB.Where("run_id = ?", "run_ll1").First(&run)
	if str(last, "requestId") != run.RequestID || run.RequestID == "" {
		t.Fatalf("requestId must reference the original request: %v vs %q", last["requestId"], run.RequestID)
	}
	close(release)
	eventually(t, "messages interrupted", func() bool {
		var m Message
		return e.DB.Where("role = ? AND error_code = ?", roleAssistant, "conversation_run.stream_interrupted").First(&m).Error == nil
	})
	rows := e.list(id)
	if str(rows[1], "status") != "error" || str(rows[1], "errorRequestID") != run.RequestID {
		t.Fatalf("assistant after interruption: %v", rows[1])
	}
	// a resume now sees the terminal error
	rw := e.call("GET", "/conversation-runs/run_ll1/stream", e.u1, nil)
	rl := ndjson(t, rw)
	if str(rl[len(rl)-1], "type") != "error" {
		t.Fatalf("resume after interruption: %v", types(rl))
	}
}

func TestResumeWithoutLeaseOnRemoteRun(t *testing.T) {
	e := newEnvTimed(t, fastTimings)
	id := e.newConv(e.u1, "t", "gpt-4o")
	conv := e.convRow(id)
	u := e.seedMsg(conv, roleUser, "q", msgPending, nil)
	a := e.seedMsg(conv, roleAssistant, "", msgPending, u)
	e.DB.Model(&Message{}).Where("id IN ?", []uint{u.ID, a.ID}).Update("run_id", "run_remote")
	e.DB.Create(&Run{RunID: "run_remote", RequestID: "req-original", UserID: 1, ConversationID: conv.ID, Status: runRunning, StartedAt: time.Now()})
	// the run exists only in the shared store, without a lease
	e.svc.hub.mu.Lock()
	lr := &liveRun{runID: "run_remote", userID: 1, convPublicID: id, requestID: "req-original"}
	e.svc.hub.mu.Unlock()
	e.svc.hub.register(lr)
	e.svc.hub.publish(lr, "delta", map[string]any{"delta": "x"})
	e.svc.hub.mu.Lock()
	delete(e.svc.hub.runs, "run_remote")
	e.svc.hub.mu.Unlock()
	_ = e.Deps.Cache.Delete(context.Background(), runKey("run_remote", "lease"))
	rw := e.call("GET", "/conversation-runs/run_remote/stream", e.u1, nil)
	lines := ndjson(t, rw)
	last := lines[len(lines)-1]
	if str(last, "type") != "error" || str(last, "errorCode") != "conversation_run.stream_interrupted" || str(last, "requestId") != "req-original" {
		t.Fatalf("remote interruption: %v", lines)
	}
	if got := e.msgRow(a.PublicID); got.Status != msgError || got.ErrorCode != "conversation_run.stream_interrupted" {
		t.Fatalf("pending message: %+v", got)
	}
}

func TestRecoverOrphansAtStartup(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	conv := e.convRow(id)
	u := e.seedMsg(conv, roleUser, "q", msgPending, nil)
	a := e.seedMsg(conv, roleAssistant, "", msgPending, u)
	e.DB.Model(&Message{}).Where("id IN ?", []uint{u.ID, a.ID}).Update("run_id", "run_crashed")
	e.DB.Create(&Run{RunID: "run_crashed", UserID: 1, ConversationID: conv.ID, Status: runRunning, StartedAt: time.Now()})
	e.svc.recoverOrphans(context.Background())
	if got := e.msgRow(a.PublicID); got.Status != msgInterrupted {
		t.Fatalf("orphan recovered: %+v", got)
	}
}

func TestUserRunsStream(t *testing.T) {
	e2 := newEnvTimed(t, fastTimings)
	release := make(chan struct{})
	started := make(chan struct{})
	e2.gen.set(func(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task != taskChat {
			return defaultGenerate(ctx, req, emit)
		}
		close(started)
		<-release
		return GenerateResult{Content: "ok", Usage: Usage{Input: 1, Output: 1}}, nil
	})
	id := e2.newConv(e2.u1, "t", "gpt-4o")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec, done := e2.stream(ctx, "GET", "/conversation-runs/stream", e2.u1, nil)
	eventually(t, "first snapshot", func() bool { return strings.Contains(rec.text(), `"type":"snapshot"`) })
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	_, sdone := e2.stream(context.Background(), "POST", "/conversations/"+id+"/messages/stream", e2.u1, sendBody("x", map[string]any{"clientRunID": "sse1"}))
	<-started
	eventually(t, "started event", func() bool { return strings.Contains(rec.text(), `"type":"started"`) })
	// another user's stream must not see u1's events
	rec2, done2 := e2.stream(ctx, "GET", "/conversation-runs/stream", e2.u2, nil)
	eventually(t, "u2 snapshot", func() bool { return strings.Contains(rec2.text(), `"type":"snapshot"`) })
	// a periodic snapshot lists the active run
	eventually(t, "snapshot with run", func() bool {
		return strings.Contains(rec.text(), `"runs":[{"conversationPublicID":"`+id+`","runID":"run_sse1"}]`)
	})
	close(release)
	waitDone(t, sdone)
	eventually(t, "finished event", func() bool { return strings.Contains(rec.text(), `"type":"finished"`) })
	if strings.Contains(rec2.text(), "run_sse1") {
		t.Fatalf("u2 saw u1's run: %s", rec2.text())
	}
	cancel()
	waitDone(t, done)
	waitDone(t, done2)
	for _, line := range strings.Split(rec.text(), "\n\n") {
		if line != "" && !strings.HasPrefix(line, "data: ") && !strings.HasPrefix(line, ": keepalive") {
			t.Fatalf("bad SSE frame %q", line)
		}
	}
}

func rateLimitOn() testkit.Option {
	return testkit.WithConfig(func(c *config.Config) {
		c.Dynamic.Auth.RateLimitEnabled = true
		c.Dynamic.Auth.RateLimitAuthRPM = 10
		c.Dynamic.Auth.RateLimitPublicRPM = 10
	})
}

func TestRateLimitMessageGeneration(t *testing.T) {
	e := newEnv(t, rateLimitOn())
	id := e.newConv(e.u1, "t", "gpt-4o")
	// message generation allows at least 60 per minute even with a small base
	for i := 1; i <= 60; i++ {
		w := e.call("POST", "/conversations/"+id+"/messages", e.u1, map[string]any{"contentType": "text"})
		if w.Code == 429 {
			t.Fatalf("request %d limited too early", i)
		}
	}
	w := e.call("POST", "/conversations/"+id+"/messages", e.u1, map[string]any{"contentType": "text"})
	testkit.Expect(t, w, 429, "rate_limit.exceeded")
	if w.Header().Get("Retry-After") == "" || w.Header().Get("X-RateLimit-Limit") != "60" || w.Header().Get("X-RateLimit-Remaining") != "0" || w.Header().Get("X-RateLimit-Reset") == "" {
		t.Fatalf("429 headers: %v", w.Header())
	}
	// other buckets and other users are unaffected
	testkit.Expect(t, e.call("GET", "/conversations", e.u1, nil), 200, "")
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u2, sendBody("x", nil), 404, "conversation.not_found")
	// polling bucket is separate and larger
	for i := 0; i < 100; i++ {
		if w := e.call("GET", "/conversations/"+id+"/runs", e.u1, nil); w.Code == 429 {
			t.Fatalf("polling limited at %d", i)
		}
	}
}

func TestPublicShareRateLimitPerIP(t *testing.T) {
	e := newEnv(t, rateLimitOn())
	limited := 0
	for i := 0; i < 605; i++ {
		w := e.call("GET", "/shared-conversations/nope", nil, nil)
		if w.Code == 429 {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("public share routes must be limited per client IP")
	}
}
