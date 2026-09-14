package contentmoderation

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func outputHitOnly(h *harness, cats ...string) {
	h.prov.text = func(text string, scope Scope) (*Response, error) {
		if scope.Direction == DirectionOutput {
			return hitResp("mod-x", cats...), nil
		}
		return cleanResp("mod-x"), nil
	}
}

func TestPassedTurnRecordsEventAndStats(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.prov.text = func(text string, scope Scope) (*Response, error) {
		h.env.Clock.Advance(40 * time.Millisecond) // the fake clock stands in for latency
		return cleanResp("provider-model"), nil
	}
	run := h.begin("run-pass", nil)
	if !run.Active() {
		t.Fatal("run must be active")
	}
	out := run.Finish(context.Background(), Output{AssistantText: "hi there"})
	if out.State != StatePassed || out.Blocked || !out.Embeddable {
		t.Fatalf("outcome %+v", out)
	}
	if got := strings.Join(h.runs.stateLog, ","); got != "run-pass:pending,run-pass:moderating,run-pass:passed" {
		t.Fatalf("state transitions: %s", got)
	}
	if got := strings.Join(h.notifier.types(), ","); got != "moderation_checking" {
		t.Fatalf("stream events: %s", got)
	}
	events := h.eventsFor("run-pass")
	if len(events) != 1 {
		t.Fatalf("events: %d", len(events))
	}
	e := events[0]
	if e.Result != ResultPassed || e.EncryptedText != "" || e.CategoriesJSON != "[]" || e.CategoryScoresJSON != "{}" || e.ErrorCode != "" {
		t.Fatalf("event %+v", e)
	}
	if e.ModelName != "provider-model" || e.PolicyVersion != 1 || e.LatencyMS != 40 {
		t.Fatalf("model/policy/latency %+v", e)
	}
	if !strings.HasPrefix(e.PublicID, "cme_") || len(e.PublicID) != 28 {
		t.Fatalf("public id %q", e.PublicID)
	}
	if !e.ContentExpiresAt.Equal(e.CreatedAt) || !e.MetadataExpiresAt.Equal(e.CreatedAt.Add(90*24*time.Hour)) {
		t.Fatalf("expiry content %v metadata %v created %v", e.ContentExpiresAt, e.MetadataExpiresAt, e.CreatedAt)
	}
	if e.UserID != 5 || e.ConversationID != 9 || e.MessageID != 11 || e.MessagePublicID != "msg_run-pass" {
		t.Fatalf("run metadata %+v", e)
	}
	if e.ContentSummary != SummaryOutputTextPass || e.ContentLocationJSON != `{"field":"assistant_message"}` {
		t.Fatalf("summary %q location %s", e.ContentSummary, e.ContentLocationJSON)
	}
	s := h.stat(DirectionOutput, ModalityText, ResultPassed, "")
	if s.CheckCount != 1 || s.ContentItems != 1 || s.HitCount != 0 || s.FailureCount != 0 || s.LatencySumMS != 40 || s.LatencyCount != 1 {
		t.Fatalf("stat %+v", s)
	}
}

func TestInputPassSummaryAndLocation(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	run := h.begin("run-in", nil)
	run.CheckInputText("hello world")
	run.Finish(context.Background(), Output{})
	e := h.eventsFor("run-in")[0]
	if e.Direction != DirectionInput || e.ContentSummary != SummaryInputTextPass || e.ContentLocationJSON != `{"field":"user_message"}` {
		t.Fatalf("event %+v", e)
	}
	if s := h.stat(DirectionInput, ModalityText, ResultPassed, ""); s.CheckCount != 1 || s.LatencyCount != 0 {
		t.Fatalf("zero latency must not count: %+v", s)
	}
}

func TestOutputTextHitBlocksTheTurn(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	outputHitOnly(h, "violence")
	run := h.begin("run-hit", nil)
	run.CheckInputText("a harmless question")
	out := run.Finish(context.Background(), Output{AssistantText: "something violent", ReasoningText: "thinking it over", ThinkingText: "something violent"})
	if !out.Blocked || out.State != StateBlocked || out.Direction != DirectionOutput || out.Embeddable {
		t.Fatalf("outcome %+v", out)
	}
	if !strings.HasPrefix(out.EventID, "cme_") || len(out.Categories) != 1 || out.Categories[0] != "violence" {
		t.Fatalf("outcome event %+v", out)
	}
	if h.runs.state("run-hit") != StateBlocked || h.runs.blockCount() != 1 {
		t.Fatalf("block not persisted: %q %d", h.runs.state("run-hit"), h.runs.blockCount())
	}
	spec := h.runs.blocks[0]
	if spec.RunID != "run-hit" || spec.EventID != out.EventID || spec.Direction != DirectionOutput {
		t.Fatalf("spec %+v", spec)
	}
	if got := strings.Join(h.notifier.types(), ","); got != "moderation_checking,moderation_blocked" {
		t.Fatalf("stream: %s", got)
	}
	ev := h.notifier.last()
	if ev.EventID != out.EventID || ev.Direction != DirectionOutput || ev.BilledReason != "" {
		t.Fatalf("blocked event %+v", ev)
	}
	if len(h.notifier.discarded) != 1 || h.notifier.discarded[0] != "run-hit" {
		t.Fatalf("retained stream not discarded: %v", h.notifier.discarded)
	}

	// The stored event holds the checked text, encrypted: reasoning included, duplicates dropped.
	var e Event
	h.env.DB.Where("public_id = ?", out.EventID).First(&e)
	if e.Result != ResultHit || e.ContentSummary != SummaryOutputTextHit || e.EncryptedText == "" ||
		strings.Contains(e.EncryptedText, "violent") || e.CategoriesJSON != `["violence"]` {
		t.Fatalf("hit event %+v", e)
	}
	if e.ContentLocationJSON != `{"field":"assistant_message"}` || e.ModelName != "mod-x" {
		t.Fatalf("location/model: %s %s", e.ContentLocationJSON, e.ModelName)
	}
	if want := h.env.Clock.Now().Add(30 * 24 * time.Hour); e.ContentExpiresAt.Sub(want) > time.Second || want.Sub(e.ContentExpiresAt) > time.Second {
		t.Fatalf("content expiry %v", e.ContentExpiresAt)
	}
	var scores map[string]float64
	_ = json.Unmarshal([]byte(e.CategoryScoresJSON), &scores)
	if scores["violence"] != 0.9 {
		t.Fatalf("scores %v", scores)
	}

	// Super administrators can read it back, and the view is audited.
	w := h.do("GET", "/events/"+out.EventID, nil)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("detail %d %v", w.Code, w.Header())
	}
	var d struct {
		Event          EventItem          `json:"event"`
		CategoryScores map[string]float64 `json:"categoryScores"`
		DecryptedText  string             `json:"decryptedText"`
		TextAvailable  bool               `json:"textAvailable"`
		ImagesAvail    bool               `json:"imagesAvailable"`
		Images         []ImageInfo        `json:"images"`
	}
	decodeData(t, w.Body.Bytes(), &d)
	if !d.TextAvailable || d.DecryptedText != "something violent\n\nthinking it over" {
		t.Fatalf("decrypted %q (%v)", d.DecryptedText, d.TextAvailable)
	}
	if d.Event.PublicID != out.EventID || d.CategoryScores["violence"] != 0.9 || d.ImagesAvail || d.Images == nil {
		t.Fatalf("detail %+v", d)
	}
	entries := h.audit.list()
	if len(entries) != 1 {
		t.Fatalf("audit entries: %d", len(entries))
	}
	a := entries[0]
	if a.Action != "content_moderation.event.view" || a.ResourceType != "content_moderation_event" || a.ResourceID != out.EventID ||
		a.ActorID != 1 || a.Detail["retainedTextAvailable"] != true || a.RequestID == "" || a.ClientIP == "" {
		t.Fatalf("audit %+v", a)
	}

	// Statistics: aggregate row plus a per-category tally.
	agg := h.stat(DirectionOutput, ModalityText, ResultHit, "")
	cat := h.stat(DirectionOutput, ModalityText, ResultHit, "violence")
	if agg.CheckCount != 1 || agg.HitCount != 1 || agg.ContentItems != 1 || cat.HitCount != 1 || cat.CheckCount != 0 {
		t.Fatalf("stats %+v / %+v", agg, cat)
	}
}

func decodeData(t testing.TB, body []byte, out any) {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("envelope: %v: %s", err, body)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		t.Fatalf("data: %v: %s", err, env.Data)
	}
}

func TestInputHitTakesPrecedenceOverOutputHit(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.prov.text = func(text string, scope Scope) (*Response, error) {
		return hitResp("m", "hate"), nil
	}
	run := h.begin("run-both", nil)
	run.CheckInputText("input text")
	out := run.Finish(context.Background(), Output{AssistantText: "output text"})
	if !out.Blocked || out.Direction != DirectionInput {
		t.Fatalf("outcome %+v", out)
	}
	if h.runs.blockCount() != 1 || h.runs.blocks[0].Direction != DirectionInput {
		t.Fatalf("blocks %+v", h.runs.blocks)
	}
	if ev := h.notifier.last(); ev.Direction != DirectionInput {
		t.Fatalf("stream %+v", ev)
	}
	hits := 0
	for _, e := range h.eventsFor("run-both") {
		if e.Result == ResultHit {
			hits++
		}
	}
	if hits != 2 {
		t.Fatalf("both hits are recorded, got %d", hits)
	}
}

func TestInputHitCancelsGenerationOnce(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.prov.text = func(text string, scope Scope) (*Response, error) {
		return hitResp("m", "violence"), nil
	}
	var cancels atomic.Int32
	run := h.begin("run-cancel", func() { cancels.Add(1) })
	run.CheckInputText("bad input")
	// The model is still "streaming": the cancel must arrive before Finish.
	waitFor(t, "generation cancelled", func() bool { return cancels.Load() == 1 })
	out := run.Finish(context.Background(), Output{AssistantText: "partial answer"})
	if !out.Blocked || out.Direction != DirectionInput {
		t.Fatalf("outcome %+v", out)
	}
	if cancels.Load() != 1 {
		t.Fatalf("cancelled %d times", cancels.Load())
	}
}

func TestProviderTimeoutFailsOpen(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.prov.text = func(string, Scope) (*Response, error) { return nil, &ProviderError{Code: CodeTimeout} }
	run := h.begin("run-timeout", nil)
	out := run.Finish(context.Background(), Output{AssistantText: "an answer"})
	if out.State != StateFailedOpen || out.Blocked || !out.Embeddable {
		t.Fatalf("outcome %+v", out)
	}
	if h.runs.state("run-timeout") != StateFailedOpen {
		t.Fatalf("state %q", h.runs.state("run-timeout"))
	}
	events := h.eventsFor("run-timeout")
	if len(events) != 1 {
		t.Fatalf("events %d", len(events))
	}
	e := events[0]
	if e.Result != ResultFailedOpen || e.ErrorCode != "timeout" || e.ErrorMessage != "content moderation timed out" ||
		e.ContentSummary != "" || e.ContentLocationJSON != "{}" || e.EncryptedText != "" {
		t.Fatalf("event %+v", e)
	}
	if e.ModelName != "m-test" || e.PolicyVersion != 1 {
		t.Fatalf("model/policy from current config: %+v", e)
	}
	if want := h.env.Clock.Now().Add(30 * 24 * time.Hour); e.ContentExpiresAt.Sub(want) > time.Second {
		t.Fatalf("expiry %v", e.ContentExpiresAt)
	}
	s := h.stat(DirectionOutput, ModalityText, ResultFailedOpen, "")
	if s.CheckCount != 1 || s.FailureCount != 1 || s.ContentItems != 1 {
		t.Fatalf("stat %+v", s)
	}
}

func TestEveryProviderErrorClassIsRecorded(t *testing.T) {
	for _, code := range []string{CodeTimeout, CodeRateLimited, CodeInvalidResponse, CodeNetworkError, CodeServiceError} {
		t.Run(code, func(t *testing.T) {
			h := newHarness(t)
			h.enable(nil)
			h.prov.text = func(string, Scope) (*Response, error) { return nil, &ProviderError{Code: code, Status: 500} }
			run := h.begin("r", nil)
			run.Finish(context.Background(), Output{AssistantText: "x"})
			e := h.eventsFor("r")[0]
			if e.ErrorCode != code || e.ErrorMessage != publicErrorText[code] {
				t.Fatalf("event %+v", e)
			}
		})
	}
	// An error that is not a provider error degrades to service_error.
	h := newHarness(t)
	h.enable(nil)
	h.prov.text = func(string, Scope) (*Response, error) { return nil, context.DeadlineExceeded }
	h.begin("r", nil).Finish(context.Background(), Output{AssistantText: "x"})
	if e := h.eventsFor("r")[0]; e.ErrorCode != CodeTimeout {
		t.Fatalf("deadline maps to timeout, got %q", e.ErrorCode)
	}
}

func TestQueueFullFailsOpen(t *testing.T) {
	h := newHarness(t)
	h.enable(map[string]any{"maxConcurrency": 1, "queueCapacity": 1})
	release := make(chan struct{})
	var started atomic.Int32
	h.prov.text = func(string, Scope) (*Response, error) {
		started.Add(1)
		<-release
		return cleanResp("m"), nil
	}
	run := h.begin("run-full", nil)
	run.CheckInputText("one") // runs
	waitFor(t, "first running", func() bool { return started.Load() == 1 })
	run.CheckInputText("two")   // waits
	run.CheckInputText("three") // refused
	done := make(chan Outcome, 1)
	go func() { done <- run.Finish(context.Background(), Output{}) }()
	waitFor(t, "queue_full recorded", func() bool {
		for _, e := range h.eventsFor("run-full") {
			if e.ErrorCode == CodeQueueFull {
				return true
			}
		}
		return false
	})
	close(release)
	out := <-done
	if out.State != StateFailedOpen {
		t.Fatalf("outcome %+v", out)
	}
	var full, passed int
	for _, e := range h.eventsFor("run-full") {
		switch {
		case e.ErrorCode == CodeQueueFull:
			full++
			if e.ErrorMessage != "content moderation queue full" || e.Result != ResultFailedOpen {
				t.Fatalf("queue_full event %+v", e)
			}
		case e.Result == ResultPassed:
			passed++
		}
	}
	if full != 1 || passed != 2 {
		t.Fatalf("queue_full %d passed %d", full, passed)
	}
}

func TestUnreadableConfigFailsOpenWithConfigMissing(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.env.DB.Model(&settingRow{}).Where(&settingRow{Namespace: "content_moderation", Key: "policy_json"}).Update("value", "{broken")
	h.svc.store.invalidate()
	run := h.begin("run-nocfg", nil)
	if run.Active() {
		t.Fatal("no moderation can run without a configuration")
	}
	run.CheckInputText("hello")
	out := run.Finish(context.Background(), Output{AssistantText: "hi"})
	if out.State != StateFailedOpen || out.Blocked {
		t.Fatalf("outcome %+v", out)
	}
	if h.runs.state("run-nocfg") != StateFailedOpen {
		t.Fatalf("persisted state %q", h.runs.state("run-nocfg"))
	}
	events := h.eventsFor("run-nocfg")
	if len(events) != 1 {
		t.Fatalf("events %d", len(events))
	}
	e := events[0]
	if e.ErrorCode != "config_missing" || e.Result != ResultFailedOpen || e.Direction != DirectionInput || e.Modality != ModalityText {
		t.Fatalf("event %+v", e)
	}
	if n, _ := h.prov.counts(); n != 0 {
		t.Fatal("provider must not be called")
	}
	s := h.stat(DirectionInput, ModalityText, ResultFailedOpen, "")
	if s.CheckCount != 1 || s.ContentItems != 1 || s.FailureCount != 1 || s.LatencySumMS != 0 || s.LatencyCount != 0 {
		t.Fatalf("stat %+v", s)
	}
}

func TestDisabledOrEmptyPolicyDoesNothing(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	testkitDisable := func() {
		h.do("PUT", "/config", map[string]any{"enabled": false})
	}
	testkitDisable()
	run := h.begin("r1", nil)
	run.CheckInputText("hello")
	if out := run.Finish(context.Background(), Output{AssistantText: "x"}); out.State != StateNotRequired || !out.Embeddable {
		t.Fatalf("disabled: %+v", out)
	}
	if n, _ := h.prov.counts(); n != 0 || len(h.allEvents()) != 0 || len(h.runs.stateLog) != 0 {
		t.Fatal("a disabled policy must leave no trace")
	}

	// Enabled in storage but with no category anywhere: skipped.
	h.do("PUT", "/config", map[string]any{"enabled": true})
	h.env.DB.Model(&settingRow{}).Where(&settingRow{Namespace: "content_moderation", Key: "policy_json"}).Update("value", "{}")
	h.svc.store.invalidate()
	if h.begin("r2", nil).Active() {
		t.Fatal("no category selected: run must be inert")
	}
}

func TestBlockPersistenceFailureIsRetried(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	outputHitOnly(h, "violence")
	h.runs.failApply = 1
	run := h.begin("run-retry", nil)
	out := run.Finish(context.Background(), Output{AssistantText: "bad"})
	if !out.Blocked {
		t.Fatalf("a failed write must not hide the block: %+v", out)
	}
	if ev := h.notifier.last(); ev.Type != EventTypeBlocked {
		t.Fatalf("stream must still receive the block: %+v", ev)
	}
	if h.runs.blockCount() != 0 || h.svc.PendingBlocks() != 1 {
		t.Fatalf("blocks %d pending %d", h.runs.blockCount(), h.svc.PendingBlocks())
	}
	h.svc.RetryPendingBlocks(context.Background())
	if h.runs.blockCount() != 1 || h.svc.PendingBlocks() != 0 || h.runs.state("run-retry") != StateBlocked {
		t.Fatalf("retry: blocks %d pending %d", h.runs.blockCount(), h.svc.PendingBlocks())
	}
	// The retry loop is part of the periodic maintenance.
	h.runs.failApply = 1
	outputHitOnly(h, "violence")
	h.begin("run-retry2", nil).Finish(context.Background(), Output{AssistantText: "bad"})
	h.svc.RunMaintenance(context.Background(), false)
	if h.svc.PendingBlocks() != 0 {
		t.Fatal("maintenance must retry pending blocks")
	}
}

func TestBlockedFilesAreDeletedAfterBlock(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	outputHitOnly(h, "violence")
	h.runs.fileIDs = []uint{41, 42}
	h.begin("run-files", nil).Finish(context.Background(), Output{AssistantText: "bad"})
	if len(h.files.deleted) != 2 {
		t.Fatalf("deleted %v", h.files.deleted)
	}
	h.svc.RunMaintenance(context.Background(), false)
	if h.files.retryRuns == 0 {
		t.Fatal("maintenance must retry blocked file deletes")
	}
}

func TestEphemeralRunsTouchNoConversationRecords(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	outputHitOnly(h, "violence")
	run := h.svc.BeginRun(context.Background(), RunInfo{UserID: 5, Ephemeral: true}, RunOptions{})
	out := run.Finish(context.Background(), Output{AssistantText: "bad"})
	if !out.Blocked {
		t.Fatalf("outcome %+v", out)
	}
	if len(h.runs.stateLog) != 0 || h.runs.blockCount() != 0 {
		t.Fatalf("ephemeral run touched conversation records: %v %d", h.runs.stateLog, h.runs.blockCount())
	}
	if h.notifier.last().Type != EventTypeBlocked {
		t.Fatalf("stream event expected: %v", h.notifier.types())
	}
	var hit Event
	if err := h.env.DB.Where("result = ?", ResultHit).First(&hit).Error; err != nil {
		t.Fatal(err)
	}
	if hit.ConversationID != 0 || hit.MessageID != 0 || hit.RunID != "" || hit.UserID != 5 {
		t.Fatalf("event ids %+v", hit)
	}
}

func TestOutputWithoutTextOrImagesQueuesNothing(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	run := h.begin("r", nil)
	run.CheckInputText("   \n ")
	out := run.Finish(context.Background(), Output{AssistantText: "  ", ReasoningText: "\n"})
	if out.State != StatePassed {
		t.Fatalf("%+v", out)
	}
	if n, _ := h.prov.counts(); n != 0 || len(h.allEvents()) != 0 {
		t.Fatalf("whitespace must not be checked: calls %d events %d", n, len(h.allEvents()))
	}
}

func TestScopesWithoutCategoriesAreSkipped(t *testing.T) {
	h := newHarness(t)
	h.enable(map[string]any{"policy": map[string]any{"outputTextCategories": []string{"violence"}}})
	run := h.begin("r", nil)
	run.CheckInputText("hello")                                  // input text not enforced
	run.CheckInputImages([]ImageRef{{Bytes: pngBytes(t, 4, 4)}}) // input images not enforced
	run.Finish(context.Background(), Output{AssistantText: "x", Images: []ImageRef{{Bytes: pngBytes(t, 4, 4)}}})
	texts, images := h.prov.counts()
	if texts != 1 || images != 0 {
		t.Fatalf("texts %d images %d", texts, images)
	}
}

func TestWaitBudgetExpiryFailsOpenButLateHitStillBlocks(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.svc.budgetFn = func(int) time.Duration { return 40 * time.Millisecond }
	release := make(chan struct{})
	h.prov.text = func(text string, scope Scope) (*Response, error) {
		<-release
		return hitResp("m", "violence"), nil
	}
	run := h.begin("run-late", nil)
	out := run.Finish(context.Background(), Output{AssistantText: "slow answer"})
	if out.State != StateFailedOpen || out.Blocked {
		t.Fatalf("budget expiry must fail open: %+v", out)
	}
	if h.runs.state("run-late") != StateFailedOpen {
		t.Fatalf("state %q", h.runs.state("run-late"))
	}
	close(release)
	waitFor(t, "late block applied", func() bool { return h.runs.blockCount() == 1 })
	waitFor(t, "late block announced", func() bool { return h.notifier.last().Type == EventTypeBlocked })
	if h.runs.state("run-late") != StateBlocked {
		t.Fatalf("state %q", h.runs.state("run-late"))
	}
}

func TestCallerContextEndFailsOpenAfterGrace(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	release := make(chan struct{})
	defer close(release)
	h.prov.text = func(string, Scope) (*Response, error) { <-release; return cleanResp("m"), nil }
	ctx, cancel := context.WithCancel(context.Background())
	run := h.begin("run-ctx", nil)
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	start := time.Now()
	out := run.Finish(ctx, Output{AssistantText: "x"})
	if out.State != StateFailedOpen || time.Since(start) > 2*time.Second {
		t.Fatalf("outcome %+v after %v", out, time.Since(start))
	}
}

func TestLateInputHitUpgradesOutputBlock(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.svc.budgetFn = func(int) time.Duration { return 60 * time.Millisecond }
	release := make(chan struct{})
	h.prov.text = func(text string, scope Scope) (*Response, error) {
		if scope.Direction == DirectionInput {
			<-release
			return hitResp("m", "hate"), nil
		}
		return hitResp("m", "violence"), nil
	}
	run := h.begin("run-upgrade", nil)
	run.CheckInputText("slow input")
	out := run.Finish(context.Background(), Output{AssistantText: "output"})
	if !out.Blocked || out.Direction != DirectionOutput {
		t.Fatalf("first outcome %+v", out)
	}
	close(release)
	waitFor(t, "upgrade", func() bool { return h.runs.blockCount() == 2 })
	if last := h.runs.blocks[1]; last.Direction != DirectionInput {
		t.Fatalf("upgraded block %+v", last)
	}
	if ev := h.notifier.last(); ev.Direction != DirectionInput {
		t.Fatalf("announced %+v", ev)
	}
	// A further late output hit must not downgrade the block.
	h.svc.applyBlock(context.Background(), run, blockInfo{EventID: "x", Direction: DirectionOutput})
	if h.runs.blockCount() != 2 {
		t.Fatal("block was downgraded")
	}
}

func TestBilledReasonForPaidReservations(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	outputHitOnly(h, "violence")
	run := h.svc.BeginRun(context.Background(), RunInfo{RunID: "paid-out", PaidReservation: true}, RunOptions{})
	out := run.Finish(context.Background(), Output{AssistantText: "bad"})
	if out.BilledReason != BilledReasonUpstreamUsage || h.notifier.last().BilledReason != "moderation_blocked_upstream_usage" {
		t.Fatalf("output block: %+v / %+v", out, h.notifier.last())
	}
	// Input-side blocks are labelled at settlement, not in the stream event.
	h.prov.text = func(string, Scope) (*Response, error) { return hitResp("m", "hate"), nil }
	run = h.svc.BeginRun(context.Background(), RunInfo{RunID: "paid-in", PaidReservation: true}, RunOptions{})
	run.CheckInputText("bad")
	out = run.Finish(context.Background(), Output{})
	if out.BilledReason != BilledReasonUpstreamUsage || h.notifier.last().BilledReason != "" {
		t.Fatalf("input block: %+v / %+v", out, h.notifier.last())
	}
	// Unpaid turns carry no label.
	run = h.svc.BeginRun(context.Background(), RunInfo{RunID: "free"}, RunOptions{})
	run.CheckInputText("bad")
	if out = run.Finish(context.Background(), Output{}); out.BilledReason != "" {
		t.Fatalf("free turn: %+v", out)
	}
}

func TestLongTextHitRecordsChunkLocation(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.prov.text = func(text string, scope Scope) (*Response, error) {
		r := cleanResp("m")
		r.Results = append(r.Results, cleanResp("m").Results[0], hitResp("m", "violence").Results[0])
		return r, nil
	}
	long := strings.Repeat("a", 2*maxTextChunkBytes+10)
	run := h.begin("run-chunks", nil)
	run.CheckInputText(long)
	run.Finish(context.Background(), Output{})
	for _, e := range h.eventsFor("run-chunks") {
		if e.Result == ResultHit {
			if e.ContentLocationJSON != `{"field":"user_message","chunkIndex":2,"chunkCount":3}` {
				t.Fatalf("location %s", e.ContentLocationJSON)
			}
			return
		}
	}
	t.Fatal("no hit event")
}

func TestAbandonedRunCanBeClosed(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	run := h.begin("abandon", nil)
	if !h.svc.isTracked("abandon") {
		t.Fatal("run should be tracked")
	}
	run.Close()
	if h.svc.isTracked("abandon") {
		t.Fatal("Close must untrack")
	}
	var nilRun *Run
	nilRun.Close()
	nilRun.CheckInputText("x")
	if out := nilRun.Finish(context.Background(), Output{}); out.State != StateNotRequired {
		t.Fatalf("nil run: %+v", out)
	}
}

func TestConfigChangesApplyToNewRuns(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	if !h.begin("a", nil).Active() {
		t.Fatal("active expected")
	}
	h.do("PUT", "/config", map[string]any{"enabled": false})
	if h.begin("b", nil).Active() {
		t.Fatal("config change must be visible immediately")
	}
}

func TestConfigIsCachedBrieflyBetweenRuns(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.begin("a", nil).Close()
	h.env.DB.Model(&settingRow{}).Where(&settingRow{Namespace: "content_moderation", Key: "enabled"}).Update("value", "false")
	if !h.begin("b", nil).Active() {
		t.Fatal("cached config expected within the TTL")
	}
	h.env.Clock.Advance(3 * time.Second)
	if h.begin("c", nil).Active() {
		t.Fatal("cache must expire after two seconds")
	}
}

// ---- images --------------------------------------------------------------------------

func TestInputImageHandling(t *testing.T) {
	t.Run("non-images are skipped silently", func(t *testing.T) {
		h := newHarness(t)
		h.enable(nil)
		h.files.put(1, []byte("%PDF-1.4 fake pdf"), "application/pdf")
		run := h.begin("r", nil)
		run.CheckInputImages([]ImageRef{{FileID: 1}})
		out := run.Finish(context.Background(), Output{})
		if out.State != StatePassed {
			t.Fatalf("%+v", out)
		}
		if _, n := h.prov.counts(); n != 0 || len(h.allEvents()) != 0 {
			t.Fatalf("calls %d events %d", n, len(h.allEvents()))
		}
	})
	t.Run("missing file fails open", func(t *testing.T) {
		h := newHarness(t)
		h.enable(nil)
		run := h.begin("r", nil)
		run.CheckInputImages([]ImageRef{{FileID: 99}})
		out := run.Finish(context.Background(), Output{})
		if out.State != StateFailedOpen {
			t.Fatalf("%+v", out)
		}
		e := h.eventsFor("r")[0]
		if e.ErrorCode != "service_error" || e.Modality != ModalityImage || e.Direction != DirectionInput {
			t.Fatalf("event %+v", e)
		}
		s := h.stat(DirectionInput, ModalityImage, ResultFailedOpen, "")
		if s.CheckCount != 1 || s.ContentItems != 1 || s.FailureCount != 1 {
			t.Fatalf("stat %+v", s)
		}
	})
	t.Run("unusable files fail open", func(t *testing.T) {
		h := newHarness(t)
		h.enable(nil)
		h.files.put(1, nil, "image/png")
		h.files.put(2, append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 21<<20)...), "image/png")
		h.files.put(3, []byte("\x89PNG\r\n\x1a\ncorrupt"), "image/png")
		run := h.begin("r", nil)
		run.CheckInputImages([]ImageRef{{FileID: 1}, {FileID: 2}, {FileID: 3}, {}})
		run.Finish(context.Background(), Output{})
		failed := 0
		for _, e := range h.eventsFor("r") {
			if e.Result == ResultFailedOpen && e.ErrorCode == "service_error" {
				failed++
			}
		}
		if failed != 4 {
			t.Fatalf("failed-open events: %d", failed)
		}
		if _, n := h.prov.counts(); n != 0 {
			t.Fatal("nothing loadable, provider must not be called")
		}
	})
	t.Run("no loader configured", func(t *testing.T) {
		h := newHarness(t)
		h.enable(nil)
		h.svc.files = nil
		run := h.begin("r", nil)
		run.CheckInputImages([]ImageRef{{FileID: 5}})
		if out := run.Finish(context.Background(), Output{}); out.State != StateFailedOpen {
			t.Fatalf("%+v", out)
		}
	})
	t.Run("temporary chat bytes and duplicates", func(t *testing.T) {
		h := newHarness(t)
		h.enable(nil)
		same := pngBytes(t, 8, 8)
		run := h.svc.BeginRun(context.Background(), RunInfo{UserID: 5, Ephemeral: true}, RunOptions{})
		// Images lacking a hash are never duplicates of each other; a repeated
		// stored hash is checked once.
		run.CheckInputImages([]ImageRef{{Bytes: same}, {Bytes: same}, {Bytes: pngBytes(t, 9, 9), SHA256: "AB"}, {Bytes: pngBytes(t, 9, 9), SHA256: "ab"}})
		run.Finish(context.Background(), Output{})
		_, n := h.prov.counts()
		if n != 1 || len(h.prov.imageCalls[0]) != 3 {
			t.Fatalf("duplicates must be moderated once: %d calls, %d images", n, len(h.prov.imageCalls[0]))
		}
		s := h.stat(DirectionInput, ModalityImage, ResultPassed, "")
		if s.ContentItems != 3 || s.CheckCount != 1 {
			t.Fatalf("stat %+v", s)
		}
		// Empty bytes with no file id are a failed-open.
		run = h.svc.BeginRun(context.Background(), RunInfo{UserID: 5, Ephemeral: true}, RunOptions{})
		run.CheckInputImages([]ImageRef{{}})
		if out := run.Finish(context.Background(), Output{}); out.State != StateFailedOpen {
			t.Fatalf("%+v", out)
		}
	})
	t.Run("large images are resized and mime is detected", func(t *testing.T) {
		h := newHarness(t)
		h.enable(nil)
		h.files.put(1, pngBytes(t, 2000, 1000), "") // blank MIME is sniffed
		run := h.begin("r", nil)
		run.CheckInputImages([]ImageRef{{FileID: 1}})
		run.Finish(context.Background(), Output{})
		if _, n := h.prov.counts(); n != 1 {
			t.Fatal("image not sent")
		}
		sent := h.prov.imageCalls[0][0]
		cfg, err := png.DecodeConfig(bytes.NewReader(sent.Data))
		if err != nil || cfg.Width != 1024 || cfg.Height != 512 || sent.MIME != "image/png" {
			t.Fatalf("sent %dx%d %s (%v)", cfg.Width, cfg.Height, sent.MIME, err)
		}
	})
	t.Run("input image hit isolates a copy but keeps the user's file", func(t *testing.T) {
		h := newHarness(t)
		h.enable(nil)
		orig := pngBytes(t, 16, 16)
		h.files.put(3, orig, "image/png")
		h.prov.image = func([]Image, Scope) (*Response, error) { return hitResp("m", "sexual"), nil }
		run := h.begin("r", nil)
		run.CheckInputImages([]ImageRef{{FileID: 3}})
		out := run.Finish(context.Background(), Output{})
		if !out.Blocked || out.Direction != DirectionInput {
			t.Fatalf("%+v", out)
		}
		if len(h.files.revoked) != 0 {
			t.Fatal("the user's own file must not be revoked")
		}
		if h.objects.count() != 1 {
			t.Fatalf("isolated objects: %d", h.objects.count())
		}
		var e Event
		h.env.DB.Where("result = ?", ResultHit).First(&e)
		if e.ImageCount != 1 || e.ContentSummary != SummaryImageHit {
			t.Fatalf("event %+v", e)
		}
		if e.ContentLocationJSON != `{"field":"user_attachments","fileID":3}` {
			t.Fatalf("location %s", e.ContentLocationJSON)
		}
	})
}

func TestOutputImageHitIsolatesRevokesAndServesOriginal(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	orig := pngBytes(t, 20, 10)
	h.files.put(7, orig, "image/png")
	h.prov.image = func([]Image, Scope) (*Response, error) { return hitResp("img-model", "sexual"), nil }
	run := h.begin("run-img", nil)
	out := run.Finish(context.Background(), Output{Images: []ImageRef{{FileID: 7}, {FileID: 8 /* bytes are gone */}}})
	if !out.Blocked || out.Direction != DirectionOutput {
		t.Fatalf("%+v", out)
	}
	if len(h.files.revoked) != 2 {
		t.Fatalf("revoked %v: files must be revoked even when their bytes are missing", h.files.revoked)
	}
	deleted := map[uint]bool{}
	for _, id := range h.files.deleted {
		deleted[id] = true
	}
	if !deleted[7] || !deleted[8] {
		t.Fatalf("stored objects deleted: %v", h.files.deleted)
	}

	var e Event
	h.env.DB.Where("public_id = ?", out.EventID).First(&e)
	if e.Modality != ModalityImage || e.ImageCount != 1 || e.EncryptedText != "" || e.ContentSummary != SummaryImageHit {
		t.Fatalf("event %+v", e)
	}
	metas := imagesFromJSON(e.ImageMetaJSON)
	if len(metas) != 1 || metas[0].StoragePath == "" || metas[0].SizeBytes != int64(len(orig)) ||
		metas[0].MIMEType != "image/png" || metas[0].SourceFileID != "7" || len(metas[0].SHA256) != 64 {
		t.Fatalf("meta %+v", metas)
	}
	if stored, _ := h.objects.Get(context.Background(), metas[0].StoragePath); len(stored) == 0 || bytes.Contains(stored, orig) {
		t.Fatal("isolated copy must be stored encrypted")
	}
	if h.objects.types[metas[0].StoragePath] != "application/octet-stream" {
		t.Fatalf("content type %q", h.objects.types[metas[0].StoragePath])
	}

	// The detail view shows image metadata but never storage paths.
	w := h.do("GET", "/events/"+out.EventID, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "storagePath") || strings.Contains(w.Body.String(), metas[0].StoragePath) {
		t.Fatalf("detail %d %s", w.Code, w.Body)
	}
	var d struct {
		ImagesAvailable bool        `json:"imagesAvailable"`
		TextAvailable   bool        `json:"textAvailable"`
		Images          []ImageInfo `json:"images"`
	}
	decodeData(t, w.Body.Bytes(), &d)
	if !d.ImagesAvailable || d.TextAvailable || len(d.Images) != 1 || d.Images[0].SourceFileID != "7" || d.Images[0].Index != 0 {
		t.Fatalf("detail data %+v", d)
	}

	// Downloading returns the original bytes with the stored type.
	w = h.do("GET", "/events/"+out.EventID+"/images/0", nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), orig) || w.Header().Get("Content-Type") != "image/png" ||
		w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("image %d %v", w.Code, w.Header())
	}
	entries := h.audit.list()
	if got := entries[len(entries)-1]; got.Action != "content_moderation.event_image.view" || got.Detail["imageIndex"] != 0 || got.ResourceID != out.EventID {
		t.Fatalf("audit %+v", got)
	}
}

func TestIsolatedObjectsAreRemovedWhenTheEventCannotBeWritten(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.files.put(7, pngBytes(t, 8, 8), "image/png")
	h.prov.image = func([]Image, Scope) (*Response, error) { return hitResp("m", "sexual"), nil }
	if err := h.env.DB.Migrator().DropTable(&Event{}); err != nil {
		t.Fatal(err)
	}
	out := h.begin("run-noevent", nil).Finish(context.Background(), Output{Images: []ImageRef{{FileID: 7}}})
	if !out.Blocked || out.EventID != "" {
		t.Fatalf("a hit without an event is still a block: %+v", out)
	}
	if h.objects.count() != 0 {
		t.Fatalf("%d isolated objects leaked", h.objects.count())
	}
	if len(h.files.revoked) != 1 {
		t.Fatalf("file must still be revoked: %v", h.files.revoked)
	}
}

func TestImageDownloadErrors(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.files.put(7, pngBytes(t, 8, 8), "image/png")
	h.prov.image = func([]Image, Scope) (*Response, error) { return hitResp("m", "sexual"), nil }
	out := h.begin("r", nil).Finish(context.Background(), Output{Images: []ImageRef{{FileID: 7}}})
	base := "/events/" + out.EventID + "/images/"
	for _, idx := range []string{"-1", "x", "1.5", "", "99999999999999999999999"} {
		w := h.do("GET", base+idx, nil)
		if idx == "" {
			if w.Code != 404 { // no route
				t.Fatalf("empty index: %d", w.Code)
			}
			continue
		}
		testkitExpect(t, w, 400, "request.invalid_image_index")
	}
	testkitExpect(t, h.do("GET", base+"1", nil), 404, "content_moderation.event_not_found")
	// Well-formed but huge: parses as an index, finds no image.
	testkitExpect(t, h.do("GET", base+"99999999999", nil), 404, "content_moderation.event_not_found")
	testkitExpect(t, h.do("GET", "/events/cme_nope/images/0", nil), 404, "content_moderation.event_not_found")

	// No object store: the same 404.
	h.svc.objects = nil
	testkitExpect(t, h.do("GET", base+"0", nil), 404, "content_moderation.event_not_found")
	h.svc.objects = h.objects

	// Storage failure is a 500, never a 404.
	h.objects.mu.Lock()
	for k := range h.objects.objects {
		h.objects.objects[k] = []byte("corrupt")
	}
	h.objects.mu.Unlock()
	testkitExpect(t, h.do("GET", base+"0", nil), 500, "internal.error")

	// Past content retention: 404 for the image, 200 without images for the detail.
	h.env.Clock.Advance(31 * 24 * time.Hour)
	testkitExpect(t, h.do("GET", base+"0", nil), 404, "content_moderation.event_not_found")
	w := h.do("GET", "/events/"+out.EventID, nil)
	var d struct {
		ImagesAvailable bool `json:"imagesAvailable"`
	}
	decodeData(t, w.Body.Bytes(), &d)
	if w.Code != 200 || d.ImagesAvailable {
		t.Fatalf("expired detail: %d %s", w.Code, w.Body)
	}
}

func TestSlowChecksDoNotBlockOnAnotherRun(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	var mu sync.Mutex
	seen := map[string]bool{}
	h.prov.text = func(text string, scope Scope) (*Response, error) {
		mu.Lock()
		seen[text] = true
		mu.Unlock()
		return cleanResp("m"), nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			run := h.begin("par-"+string(rune('a'+i)), nil)
			run.CheckInputText("in-" + string(rune('a'+i)))
			if out := run.Finish(context.Background(), Output{AssistantText: "out-" + string(rune('a'+i))}); out.State != StatePassed {
				t.Errorf("run %d: %+v", i, out)
			}
		}(i)
	}
	wg.Wait()
	if len(seen) != 24 {
		t.Fatalf("checked %d texts", len(seen))
	}
}
