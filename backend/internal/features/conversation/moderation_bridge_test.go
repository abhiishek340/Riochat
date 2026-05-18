package conversation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

// ---- fakes ------------------------------------------------------------------------

// fakeSessMod is a SessionModeration that behaves like the engine would: it
// withdraws a blocked turn through the bridge, discards retained deltas and
// emits the blocked event, so the area's own ordering can be observed.
type fakeSessMod struct {
	fakeModeration
	smu    sync.Mutex
	bridge *ModerationBridge

	hitInputText string // input text that hits (cancels generation)
	blockOutput  bool   // the output side hits

	begun       []ModerationBegin
	inputTexts  []string
	inputFiles  [][]ModerationFile
	outputs     []ModerationOutput
	outFailures int
	closed      int
	cancelCalls int
	finished    int
}

type fakeSess struct {
	m   *fakeSessMod
	b   ModerationBegin
	hit bool
}

func (m *fakeSessMod) Begin(_ context.Context, b ModerationBegin) ModerationSession {
	m.smu.Lock()
	m.begun = append(m.begun, b)
	m.smu.Unlock()
	return &fakeSess{m: m, b: b}
}

func (s *fakeSess) CheckInputText(text string) {
	s.m.smu.Lock()
	s.m.inputTexts = append(s.m.inputTexts, text)
	hit := s.m.hitInputText != "" && text == s.m.hitInputText
	s.m.smu.Unlock()
	if hit {
		s.hit = true
		s.m.smu.Lock()
		s.m.cancelCalls++
		s.m.smu.Unlock()
		s.b.CancelGeneration()
	}
}

func (s *fakeSess) CheckInputImages(files []ModerationFile) {
	s.m.smu.Lock()
	s.m.inputFiles = append(s.m.inputFiles, files)
	s.m.smu.Unlock()
}

func (s *fakeSess) ReportOutputImageFailure(context.Context) {
	s.m.smu.Lock()
	s.m.outFailures++
	s.m.smu.Unlock()
}

func (s *fakeSess) Finish(ctx context.Context, out ModerationOutput) ModerationVerdict {
	s.m.smu.Lock()
	s.m.outputs = append(s.m.outputs, out)
	s.m.finished++
	s.m.smu.Unlock()
	dir := ""
	switch {
	case s.hit:
		dir = dirInput
	case s.m.blockOutput:
		dir = dirOutput
	}
	if dir == "" {
		return ModerationVerdict{}
	}
	v := ModerationVerdict{Blocked: true, EventID: "evt_9", Direction: dir, Categories: []string{"violence"}}
	if s.b.PaidReservation {
		v.BilledReason = "moderation_blocked_upstream_usage"
	}
	// What the engine does at the barrier.
	_, _ = s.m.bridge.ApplyBlock(ctx, s.b.RunID, v.EventID, dir, v.Categories)
	s.m.bridge.DiscardRetained(ctx, s.b.RunID)
	s.m.bridge.Emit(ctx, s.b.RunID, evModerationBlocked, map[string]any{"eventID": v.EventID, "direction": dir, "categories": v.Categories})
	return v
}

func (s *fakeSess) Close() {
	s.m.smu.Lock()
	s.m.closed++
	s.m.smu.Unlock()
}

func newSessEnv(t *testing.T, objects ObjectBytes, opts ...testkit.Option) (*tenv, *fakeSessMod) {
	t.Helper()
	sm := &fakeSessMod{fakeModeration: fakeModeration{block: map[string]bool{}}}
	e := newEnvPorts(t, nil, func(p *Ports, te *tenv) {
		p.Moderation = sm
		if objects != nil {
			p.Objects = objects
		}
	}, opts...)
	sm.bridge = &ModerationBridge{s: e.svc}
	return e, sm
}

// memObjects is an in-memory ObjectBytes.
type memObjects struct {
	mu      sync.Mutex
	data    map[string][]byte
	failDel bool
	puts    int
}

func newMemObjects() *memObjects { return &memObjects{data: map[string][]byte{}} }

func (m *memObjects) Put(_ context.Context, key string, data []byte, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = append([]byte(nil), data...)
	m.puts++
	return nil
}

func (m *memObjects) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.data[key]
	if !ok {
		return nil, ErrObjectNotFound
	}
	return b, nil
}

func (m *memObjects) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failDel {
		return errors.New("store down")
	}
	delete(m.data, key)
	return nil
}

func (m *memObjects) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.data)
}

func (m *memObjects) keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for k := range m.data {
		out = append(out, k)
	}
	return out
}

// ---- tracked run: output block ---------------------------------------------------------

// Scenarios 16 and 17 (output side): one terminal event with the billing label,
// no replayable deltas afterwards, pair withdrawn.
func TestTrackedOutputBlockIsWithdrawnAndNotReplayable(t *testing.T) {
	e, sm := newSessEnv(t, nil)
	sm.blockOutput = true
	id := e.newConv(e.u1, "t", "gpt-4o")
	w := e.call("POST", "/conversations/"+id+"/messages/stream", e.u1, sendBody("tell me", map[string]any{"clientRunID": "blk1"}))
	lines := ndjson(t, w)
	var terminals []map[string]any
	for _, l := range lines {
		if isTerminalType(str(l, "type")) {
			terminals = append(terminals, l)
		}
	}
	if len(terminals) != 1 || str(terminals[0], "type") != evModerationBlocked {
		t.Fatalf("exactly one blocked terminal expected: %v", types(lines))
	}
	b := terminals[0]
	if str(b, "eventID") != "evt_9" || str(b, "direction") != "output" || str(b, "billedReason") != "moderation_blocked_upstream_usage" || b["data"] == nil {
		t.Fatalf("blocked event: %v", b)
	}
	if len(sm.begun) != 1 || sm.begun[0].RunID != "run_blk1" || !sm.begun[0].PaidReservation || sm.finished != 1 || sm.closed != 1 {
		t.Fatalf("session lifecycle: %+v finished=%d closed=%d", sm.begun, sm.finished, sm.closed)
	}

	rows := e.list(id)
	if len(rows) != 2 || str(rows[0], "status") != "success" || str(rows[1], "status") != "blocked" || str(rows[1], "content") != "" {
		t.Fatalf("pair: %v", rows)
	}
	var am Message
	e.DB.Where("role = ?", roleAssistant).First(&am)
	if am.ErrorCode != BlockedErrorCode || am.ErrorMessage != BlockedErrorMessage || am.ModerationEventID != "evt_9" || am.ContentType != "text" {
		t.Fatalf("blocked message columns: %+v", am)
	}
	if !strings.Contains(am.PricingSnapshot, "moderation_blocked_upstream_usage") {
		t.Fatalf("snapshot should carry the billing label: %s", am.PricingSnapshot)
	}
	var run Run
	e.DB.Where("run_id = ?", "run_blk1").First(&run)
	if run.Status != runBlocked || run.ModerationState != "blocked" || run.ModerationEventID != "evt_9" || run.ModerationCategories != `["violence"]` {
		t.Fatalf("run: %+v", run)
	}

	// A client reconnecting with an earlier cursor never sees the deltas.
	rw := e.call("GET", "/conversation-runs/run_blk1/stream?after=0", e.u1, nil)
	replay := ndjson(t, rw)
	for _, l := range replay {
		if str(l, "type") == "delta" || str(l, "type") == "upstream_think_delta" {
			t.Fatalf("withdrawn text replayed: %v", replay)
		}
	}
	if len(replay) == 0 || str(replay[len(replay)-1], "type") != evModerationBlocked {
		t.Fatalf("replay must end with the blocked event: %v", types(replay))
	}
	snap := ndjson(t, e.call("GET", "/conversation-runs/run_blk1/stream?snapshot=true", e.u1, nil))
	if str(snap[0], "type") == "snapshot" && str(snap[0], "content") != "" {
		t.Fatalf("snapshot leaks withdrawn text: %v", snap[0])
	}
}

// Scenarios 17 and 18 (input side): the generation is cancelled once, both
// messages end blocked, the live event has no label yet but the stored
// snapshot has.
func TestTrackedInputHitCancelsGenerationOnce(t *testing.T) {
	e, sm := newSessEnv(t, nil)
	sm.hitInputText = "bad input"
	cancelled := 0
	e.gen.set(func(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task == taskTitle || req.Task == taskLabels {
			return defaultGenerate(ctx, req, emit)
		}
		select {
		case <-ctx.Done():
			cancelled++
		case <-time.After(5 * time.Second):
		}
		return GenerateResult{Usage: Usage{Input: 3}}, ctx.Err()
	})
	id := e.newConv(e.u1, "t", "gpt-4o")
	w := e.call("POST", "/conversations/"+id+"/messages/stream", e.u1, sendBody("bad input", map[string]any{"clientRunID": "blk2"}))
	lines := ndjson(t, w)
	last := lines[len(lines)-1]
	if str(last, "type") != evModerationBlocked || str(last, "direction") != "input" {
		t.Fatalf("lines: %v", types(lines))
	}
	if _, has := last["billedReason"]; has {
		t.Fatalf("an input-side block is not labelled on the stream: %v", last)
	}
	if cancelled != 1 || sm.cancelCalls != 1 {
		t.Fatalf("generation cancelled %d times (hook %d)", cancelled, sm.cancelCalls)
	}
	rows := e.list(id)
	if str(rows[0], "status") != "blocked" || str(rows[1], "status") != "blocked" {
		t.Fatalf("both messages blocked: %v", rows)
	}
	var am Message
	e.DB.Where("role = ?", roleAssistant).First(&am)
	if !strings.Contains(am.PricingSnapshot, "moderation_blocked_upstream_usage") {
		t.Fatalf("settlement snapshot must carry the label: %s", am.PricingSnapshot)
	}
	var run Run
	e.DB.Where("run_id = ?", "run_blk2").First(&run)
	if run.Status != runBlocked || e.bill.settled != 1 {
		t.Fatalf("run %+v settled %d", run, e.bill.settled)
	}
}

func TestTrackedPassingTurnSettlesModerationOnce(t *testing.T) {
	e, sm := newSessEnv(t, nil)
	id := e.newConv(e.u1, "t", "gpt-4o")
	r := e.send(e.u1, id, "hello", nil)
	_, a := pair(r)
	if str(a, "status") != "success" {
		t.Fatalf("%v", a)
	}
	if sm.finished != 1 || sm.closed != 1 || len(sm.inputTexts) != 1 || sm.inputTexts[0] != "hello" {
		t.Fatalf("finished=%d closed=%d inputs=%v", sm.finished, sm.closed, sm.inputTexts)
	}
	if sm.outputs[0].Text == "" {
		t.Fatal("the reply text is checked")
	}
	if len(sm.fakeModeration.seen) != 0 {
		t.Fatal("the text-only Check must not run for a tracked turn")
	}
}

// A failed generation still settles the moderation state, with the partial reply.
func TestTrackedFailureStillFinishesModeration(t *testing.T) {
	e, sm := newSessEnv(t, nil)
	e.gen.set(func(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task == taskTitle || req.Task == taskLabels {
			return defaultGenerate(ctx, req, emit)
		}
		return GenerateResult{Content: "partial"}, ErrUpstream
	})
	id := e.newConv(e.u1, "t", "gpt-4o")
	w := e.call("POST", "/conversations/"+id+"/messages/stream", e.u1, sendBody("hi", nil))
	lines := ndjson(t, w)
	if str(lines[len(lines)-1], "type") != "error" {
		t.Fatalf("%v", types(lines))
	}
	if sm.finished != 1 || sm.outputs[0].Text != "partial" {
		t.Fatalf("finished=%d outputs=%+v", sm.finished, sm.outputs)
	}
}

// Section 2.1: attachments and generated images feed the checks.
func TestTrackedTurnPassesFilesAndOutputImages(t *testing.T) {
	e, sm := newSessEnv(t, nil)
	f := e.uploadOK(e.u1, "p.png", "image/png", pngBytes(30))["file"].(map[string]any)
	d := e.uploadOK(e.u1, "d.txt", "text/plain", []byte("doc"))["file"].(map[string]any)
	e.cat.available["img-model"] = true
	e.gen.set(func(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task == taskTitle || req.Task == taskLabels {
			return defaultGenerate(ctx, req, emit)
		}
		return GenerateResult{Content: "here", Usage: Usage{Input: 1, Output: 1}, Files: []GeneratedFile{
			{FileName: "ok.png", MimeType: "image/png", Kind: "image", Data: pngBytes(40)},
			{FileName: "gone.png", MimeType: "image/png", Kind: "image"},
			{FileName: "clip.mp4", MimeType: "video/mp4", Data: []byte("\x00\x00\x00\x18ftypmp42video")},
		}}, nil
	})
	id := e.newConv(e.u1, "t", "img-model")
	w := e.call("POST", "/conversations/"+id+"/messages/stream", e.u1, sendBody("draw", map[string]any{"fileIDs": []string{str(f, "fileID"), str(d, "fileID")}}))
	if lines := ndjson(t, w); str(lines[len(lines)-1], "type") != "completed" && str(lines[len(lines)-1], "type") != "error" {
		t.Fatalf("%v", types(lines))
	}
	if len(sm.inputFiles) != 1 || len(sm.inputFiles[0]) != 2 {
		t.Fatalf("input files: %+v", sm.inputFiles)
	}
	for _, mf := range sm.inputFiles[0] {
		if mf.ObjectID == 0 || len(mf.SHA256) != 64 {
			t.Fatalf("file facts: %+v", mf)
		}
	}
	if len(sm.outputs) != 1 || len(sm.outputs[0].Images) != 1 || sm.outFailures != 1 {
		t.Fatalf("output images %+v failures %d (empty bytes are reported, video is not checked)", sm.outputs, sm.outFailures)
	}
}

// The engine's own terminal event is held back so the area can publish the
// terminal once, with the result attached.
func TestBridgeHoldsBlockedEventForTheRunJob(t *testing.T) {
	e, _ := newSessEnv(t, nil)
	b := &ModerationBridge{s: e.svc}
	lr := &liveRun{runID: "hold-run", userID: 1}
	e.svc.hub.register(lr)
	lr.mu.Lock()
	lr.holdBlocked = true
	lr.mu.Unlock()
	b.Emit(context.Background(), "hold-run", "moderation_checking", nil)
	b.Emit(context.Background(), "hold-run", evModerationBlocked, map[string]any{"eventID": "x"})
	lr.mu.Lock()
	n, term := len(lr.events), lr.terminal
	lr.mu.Unlock()
	if n != 1 || term {
		t.Fatalf("events=%d terminal=%v: checking is published, blocked is held", n, term)
	}
	lr.mu.Lock()
	lr.holdBlocked = false
	lr.mu.Unlock()
	b.Emit(context.Background(), "hold-run", evModerationBlocked, map[string]any{"eventID": "x"})
	b.Emit(context.Background(), "hold-run", evModerationBlocked, map[string]any{"eventID": "y"})
	lr.mu.Lock()
	n = len(lr.events)
	lr.mu.Unlock()
	if n != 2 {
		t.Fatalf("the first terminal wins: %d events", n)
	}
	// A run this process does not own is dropped silently.
	b.Emit(context.Background(), "someone-elses", evModerationBlocked, nil)
	b.DiscardRetained(context.Background(), "someone-elses")
}

// ---- bridge: persistence -------------------------------------------------------------------

func (e *tenv) mediaPair(id, runID string) (Message, Message, FileObject) {
	e.t.Helper()
	e.cat.available["img-model"] = true
	e.gen.set(mediaGen([]GeneratedFile{{FileName: "out.png", MimeType: "image/png", Kind: "image", Data: pngBytes(40)}}))
	w := e.call("POST", "/conversations/"+id+"/media/images/generations/stream", e.u1, map[string]any{"prompt": "a cat", "clientRunID": runID})
	lines := ndjson(e.t, w)
	if str(lines[len(lines)-1], "type") != "completed" {
		e.t.Fatalf("%v", types(lines))
	}
	var um, am Message
	e.DB.Where("run_id = ? AND role = ?", "run_"+runID, roleUser).First(&um)
	e.DB.Where("run_id = ? AND role = ?", "run_"+runID, roleAssistant).First(&am)
	var fo FileObject
	if err := e.DB.First(&fo).Error; err != nil {
		e.t.Fatal(err)
	}
	return um, am, fo
}

func TestBridgeApplyBlockWithdrawsGeneratedFiles(t *testing.T) {
	store := newMemObjects()
	e, _ := newSessEnv(t, store)
	b := &ModerationBridge{s: e.svc}
	id := e.newConv(e.u1, "t", "img-model")
	um, am, fo := e.mediaPair(id, "late1")
	if fo.StoragePath == "" || store.count() != 1 {
		t.Fatalf("file bytes must live in the object store: %d", store.count())
	}
	if err := e.DB.Create(&RunEvent{RunID: "run_late1", EventScope: scopeTraceBlock, EventID: "tb1"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.DB.Create(&RunEvent{RunID: "run_late1", EventScope: scopeToolCall, EventID: "tc1"}).Error; err != nil {
		t.Fatal(err)
	}
	var quotaBefore StorageQuota
	e.DB.Where("user_id = ?", 1).First(&quotaBefore)

	ids, err := b.ApplyBlock(context.Background(), "run_late1", "evt_5", dirOutput, []string{"sexual"})
	if err != nil || len(ids) != 1 || ids[0] != fo.ID {
		t.Fatalf("ids %v err %v", ids, err)
	}
	var gotA, gotU Message
	e.DB.First(&gotA, am.ID)
	e.DB.First(&gotU, um.ID)
	if gotA.Status != msgBlocked || gotA.Content != "" || gotA.ReasoningContent != "" || gotA.ErrorCode != BlockedErrorCode ||
		gotA.ModerationEventID != "evt_5" || gotA.ModerationCategoriesJSON != `["sexual"]` {
		t.Fatalf("assistant: %+v", gotA)
	}
	if gotU.Status == msgBlocked {
		t.Fatal("an output block leaves the user message alone")
	}
	var atts []Attachment
	e.DB.Where("message_id = ?", am.ID).Find(&atts)
	if len(atts) != 1 || atts[0].Status != fileStatusDeleted {
		t.Fatalf("attachments: %+v", atts)
	}
	var f FileObject
	e.DB.First(&f, fo.ID)
	if f.Status != fileStatusModerationBlocked || f.UserID != 0 || f.StoragePath == "" {
		t.Fatalf("file row: %+v", f)
	}
	var q StorageQuota
	e.DB.Where("user_id = ?", 1).First(&q)
	if q.UsedBytes != quotaBefore.UsedBytes-fo.SizeBytes {
		t.Fatalf("quota %d -> %d", quotaBefore.UsedBytes, q.UsedBytes)
	}
	var events int64
	e.DB.Unscoped().Model(&RunEvent{}).Where("run_id = ?", "run_late1").Count(&events)
	if events != 1 {
		t.Fatalf("trace rows removed, tool calls kept: %d", events)
	}
	var run Run
	e.DB.Where("run_id = ?", "run_late1").First(&run)
	if run.Status != runBlocked || run.ModerationState != "blocked" || run.EndedAt == nil {
		t.Fatalf("run: %+v", run)
	}

	// Withdrawn files are unreachable at once, before the object is removed.
	e.expectErr("GET", "/files/"+fo.FileID+"/content", e.u1, nil, 404, "file.not_found")

	// Scenario 20: object delete fails -> still unreachable, retried until the path clears.
	store.failDel = true
	if err := b.DeleteFileObjects(context.Background(), ids); err == nil {
		t.Fatal("a failing delete is reported")
	}
	e.DB.First(&f, fo.ID)
	if f.StoragePath == "" || store.count() != 1 {
		t.Fatalf("path must stay until the object is gone: %+v", f)
	}
	if err := b.RetryBlockedDeletes(context.Background(), 200); err != nil {
		t.Fatal(err)
	}
	e.DB.First(&f, fo.ID)
	if f.StoragePath == "" {
		t.Fatal("still failing")
	}
	store.failDel = false
	if err := b.RetryBlockedDeletes(context.Background(), 200); err != nil {
		t.Fatal(err)
	}
	e.DB.First(&f, fo.ID)
	if f.StoragePath != "" || f.Status != fileStatusModerationBlocked || store.count() != 0 {
		t.Fatalf("after retry: %+v objects %d", f, store.count())
	}
	// A record that is gone counts as done.
	if err := b.DeleteFileObjects(context.Background(), []uint{9999}); err != nil {
		t.Fatal(err)
	}
}

func TestBridgeApplyBlockInputBlocksBothMessagesAndMissingRunFails(t *testing.T) {
	e, _ := newSessEnv(t, nil)
	b := &ModerationBridge{s: e.svc}
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.send(e.u1, id, "hello", map[string]any{"clientRunID": "in1"})
	if _, err := b.ApplyBlock(context.Background(), "run_in1", "evt_7", dirInput, nil); err != nil {
		t.Fatal(err)
	}
	var msgs []Message
	e.DB.Where("run_id = ?", "run_in1").Find(&msgs)
	if len(msgs) != 2 {
		t.Fatalf("%d", len(msgs))
	}
	for _, m := range msgs {
		if m.Status != msgBlocked || m.ModerationCategoriesJSON != "[]" {
			t.Fatalf("%+v", m)
		}
	}
	if _, err := b.ApplyBlock(context.Background(), "no-such-run", "evt", dirOutput, nil); err == nil {
		t.Fatal("a missing run row must fail the transaction")
	}
}

func TestBridgeRunStateAndStaleRuns(t *testing.T) {
	e, _ := newSessEnv(t, nil)
	b := &ModerationBridge{s: e.svc}
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.send(e.u1, id, "hello", map[string]any{"clientRunID": "st1"})
	ctx := context.Background()
	if err := b.SetRunState(ctx, "run_st1", "moderating", "evt_1", `["hate"]`); err != nil {
		t.Fatal(err)
	}
	// Empty values never clear what was stored.
	if err := b.SetRunState(ctx, "run_st1", "moderating", "", ""); err != nil {
		t.Fatal(err)
	}
	var run Run
	e.DB.Where("run_id = ?", "run_st1").First(&run)
	if run.ModerationState != "moderating" || run.ModerationEventID != "evt_1" || run.ModerationCategories != `["hate"]` {
		t.Fatalf("%+v", run)
	}
	// Fresh runs are not stale; old ones are, and carry their records.
	stale, err := b.StaleRuns(ctx, e.Clock.Now().Add(-2*time.Minute))
	if err != nil || len(stale) != 0 {
		t.Fatalf("%v %v", stale, err)
	}
	e.Clock.Advance(3 * time.Minute)
	e.DB.Model(&Run{}).Where("run_id = ?", "run_st1").UpdateColumn("updated_at", e.Clock.Now().Add(-3*time.Minute))
	stale, err = b.StaleRuns(ctx, e.Clock.Now().Add(-2*time.Minute))
	if err != nil || len(stale) != 1 || stale[0].RunID != "run_st1" || stale[0].UserID != 1 || stale[0].MessagePublicID == "" {
		t.Fatalf("%+v %v", stale, err)
	}
	if err := b.SetRunState(ctx, "run_st1", "failed_open", "", ""); err != nil {
		t.Fatal(err)
	}
	if stale, _ = b.StaleRuns(ctx, e.Clock.Now()); len(stale) != 0 {
		t.Fatalf("settled runs are not stale: %+v", stale)
	}
	if err := b.SetRunState(ctx, "run_st1", "blocked", "evt_2", `[]`); err != nil {
		t.Fatal(err)
	}
	e.DB.Where("run_id = ?", "run_st1").First(&run)
	if run.Status != runBlocked {
		t.Fatalf("blocked state marks the run blocked: %+v", run)
	}
}

func TestBridgeLoadFile(t *testing.T) {
	e, _ := newSessEnv(t, nil)
	b := &ModerationBridge{s: e.svc}
	ctx := context.Background()
	img := e.uploadOK(e.u1, "p.png", "image/png", pngBytes(30))["file"].(map[string]any)
	pdf := e.uploadOK(e.u1, "d.pdf", "application/pdf", []byte("%PDF-1.4 x"))["file"].(map[string]any)
	idOf := func(f map[string]any) uint {
		var fo FileObject
		e.DB.Where("file_id = ?", str(f, "fileID")).First(&fo)
		return fo.ID
	}
	data, mime, err := b.LoadFile(ctx, idOf(img))
	if err != nil || !strings.HasPrefix(mime, "image/") || len(data) == 0 {
		t.Fatalf("%d %q %v", len(data), mime, err)
	}
	if _, _, err := b.LoadFile(ctx, idOf(pdf)); !errors.Is(err, ErrModerationNotImage) {
		t.Fatalf("pdf: %v", err)
	}
	if _, _, err := b.LoadFile(ctx, 9999); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := b.RevokeFile(ctx, idOf(img)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.LoadFile(ctx, idOf(img)); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("withdrawn file must not load: %v", err)
	}
	e.DB.Model(&FileObject{}).Where("file_id = ?", str(pdf, "fileID")).UpdateColumn("size_bytes", moderationMaxImageBytes+1).
		UpdateColumn("detected_mime", "image/png")
	if _, _, err := b.LoadFile(ctx, idOf(pdf)); !errors.Is(err, ErrModerationFileTooLarge) {
		t.Fatalf("oversize: %v", err)
	}
}

// ---- object store port ---------------------------------------------------------------------------

// Scenario 24: identical uploads are reused only while the object is intact.
func TestUploadDedupVerifiesTheStoredObject(t *testing.T) {
	store := newMemObjects()
	e, _ := newSessEnv(t, store)
	first := e.uploadOK(e.u1, "a.txt", "text/plain", []byte("same bytes"))
	if first["reused"] != false || store.count() != 1 {
		t.Fatalf("%v objects=%d", first, store.count())
	}
	keys := store.keys()
	if !strings.HasPrefix(keys[0], "chat-files/") {
		t.Fatalf("object key layout: %v", keys)
	}
	second := e.uploadOK(e.u1, "b.txt", "text/plain", []byte("same bytes"))
	if second["reused"] != true || store.count() != 1 || store.puts != 1 {
		t.Fatalf("reuse: %v objects=%d puts=%d", second, store.count(), store.puts)
	}
	if str(second["file"].(map[string]any), "fileID") != str(first["file"].(map[string]any), "fileID") {
		t.Fatal("the existing record is returned")
	}

	// The object disappears out of band: the stale record is replaced.
	store.mu.Lock()
	store.data = map[string][]byte{}
	store.mu.Unlock()
	third := e.uploadOK(e.u1, "c.txt", "text/plain", []byte("same bytes"))
	if third["reused"] != false || store.count() != 1 {
		t.Fatalf("stale replaced: %v objects=%d", third, store.count())
	}
	if str(third["file"].(map[string]any), "fileID") == str(first["file"].(map[string]any), "fileID") {
		t.Fatal("a fresh record is expected")
	}
	var n int64
	e.DB.Model(&FileObject{}).Where("user_id = ?", 1).Count(&n)
	if n != 1 {
		t.Fatalf("one live record: %d", n)
	}
	var q StorageQuota
	e.DB.Where("user_id = ?", 1).First(&q)
	if q.UsedBytes != int64(len("same bytes")) {
		t.Fatalf("quota must be released and consumed once: %d", q.UsedBytes)
	}

	// Content that differs under the same key also counts as stale.
	store.mu.Lock()
	for k := range store.data {
		store.data[k] = []byte("tampered!!")
	}
	store.mu.Unlock()
	fourth := e.uploadOK(e.u1, "d.txt", "text/plain", []byte("same bytes"))
	if fourth["reused"] != false {
		t.Fatalf("%v", fourth)
	}
}

func TestChatFilesFollowTheObjectStoreForDownloadAndDelete(t *testing.T) {
	store := newMemObjects()
	e, _ := newSessEnv(t, store)
	f := e.uploadOK(e.u1, "a.txt", "text/plain", []byte("hello store"))["file"].(map[string]any)
	w := e.call("GET", "/files/"+str(f, "fileID")+"/content", e.u1, nil)
	if w.Code != 200 || w.Body.String() != "hello store" {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
	store.mu.Lock()
	store.data = map[string][]byte{}
	store.mu.Unlock()
	e.expectErr("GET", "/files/"+str(f, "fileID")+"/content", e.u1, nil, 404, "file.not_found")
	e.ok("DELETE", "/files/"+str(f, "fileID"), e.u1, nil)
}

// Section 2.1, temporary chats: image bytes travel with the request.
func TestTemporaryChatPassesImageBytesToModeration(t *testing.T) {
	e := newEnv(t)
	payload := tempBody([2]string{"user", "look"})
	payload["attachmentMessageIndexes"] = []int{0}
	buf, ct := multipartTemp(t, payload, []int{0}, [][2]string{{"p.png", string(pngBytes(20))}})
	code, body, _ := e.postMultipart(buf, ct)
	if code != 200 {
		t.Fatalf("%d %.200s", code, body)
	}
	var img int
	for _, r := range e.mod.seen {
		if r.Direction == dirInput && r.Ephemeral {
			img += len(r.Images)
		}
	}
	if img != 1 {
		t.Fatalf("input check images: %d (%+v)", img, e.mod.seen)
	}
}
