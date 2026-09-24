package contentmoderation

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"strings"
	"testing"
	"time"
)

// notImageFiles answers like a loader that refuses non-image files.
type notImageFiles struct{ *memFiles }

func (n notImageFiles) Load(ctx context.Context, id uint) ([]byte, string, error) {
	if id == 50 {
		return nil, "application/pdf", ErrNotImage
	}
	return n.memFiles.Load(ctx, id)
}

func TestNotImageFilesAreSkippedSilently(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.svc.files = notImageFiles{h.files}
	run := h.begin("r", nil)
	run.CheckInputImages([]ImageRef{{FileID: 50}})
	out := run.Finish(context.Background(), Output{})
	if out.State != StatePassed || len(h.allEvents()) != 0 {
		t.Fatalf("%+v events %d", out, len(h.allEvents()))
	}
}

// Scenario 1 and 8: evidence is the prepared image under the documented key.
func TestEvidenceIsPreparedImageUnderDocumentedKey(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.files.put(3, pngBytes(t, 2000, 1000), "image/png")
	h.prov.image = func([]Image, Scope) (*Response, error) { return hitResp("m", "sexual"), nil }
	hash := strings.Repeat("ab", 32)
	run := h.begin("r2", nil)
	run.CheckInputImages([]ImageRef{{FileID: 3, SHA256: hash}})
	res := run.Finish(context.Background(), Output{})
	if !res.Blocked {
		t.Fatalf("%+v", res)
	}
	var e Event
	h.env.DB.Where("public_id = ?", res.EventID).First(&e)
	metas := imagesFromJSON(e.ImageMetaJSON)
	want := fmt.Sprintf("moderation-isolated/%s/0_%s.bin", res.EventID, hash[:16])
	if len(metas) != 1 || metas[0].StoragePath != want || metas[0].SHA256 != hash || metas[0].MIMEType != "image/png" {
		t.Fatalf("meta %+v want key %s", metas, want)
	}
	// The sent image and the evidence are the same downscaled picture.
	w := h.do("GET", "/events/"+res.EventID+"/images/0", nil)
	if w.Code != 200 {
		t.Fatalf("download %d", w.Code)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
	if err != nil || cfg.Width != 1024 || cfg.Height != 512 {
		t.Fatalf("evidence %dx%d %v", cfg.Width, cfg.Height, err)
	}
	if metas[0].SizeBytes != int64(w.Body.Len()) {
		t.Fatalf("recorded size %d, served %d", metas[0].SizeBytes, w.Body.Len())
	}
	if !bytes.Equal(h.prov.imageCalls[len(h.prov.imageCalls)-1][0].Data, w.Body.Bytes()) {
		t.Fatal("served evidence differs from what the provider saw")
	}
}

// Indexes are positions in the task even when an earlier image could not be isolated.
func TestEvidenceIndexIsPositionInTask(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.files.put(1, pngBytes(t, 8, 8), "image/png")
	h.files.put(2, pngBytes(t, 9, 9), "image/png")
	h.prov.image = func([]Image, Scope) (*Response, error) { return hitResp("m", "sexual"), nil }
	h.objects.failPut = func(key string) bool { return strings.Contains(key, "/0_") }
	run := h.begin("r", nil)
	run.CheckInputImages([]ImageRef{{FileID: 1, SHA256: "aa"}, {FileID: 2, SHA256: "bb"}})
	res := run.Finish(context.Background(), Output{})
	var e Event
	h.env.DB.Where("public_id = ?", res.EventID).First(&e)
	metas := imagesFromJSON(e.ImageMetaJSON)
	if e.ImageCount != 1 || len(metas) != 1 || metas[0].Index != 1 || metas[0].SourceFileID != "2" {
		t.Fatalf("count %d meta %+v", e.ImageCount, metas)
	}
	if !res.Blocked {
		t.Fatal("an evidence failure must not stop the block")
	}
}

// Scenario 9: output hit with every evidence write failing.
func TestOutputHitWithEvidenceFailureStillRevokes(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.files.put(7, pngBytes(t, 8, 8), "image/png")
	h.prov.image = func([]Image, Scope) (*Response, error) { return hitResp("m", "sexual"), nil }
	h.objects.failPut = func(string) bool { return true }
	res := h.begin("r", nil).Finish(context.Background(), Output{Images: []ImageRef{{FileID: 7}}})
	var e Event
	h.env.DB.Where("public_id = ?", res.EventID).First(&e)
	if !res.Blocked || e.ImageCount != 0 || e.ImageMetaJSON != "[]" {
		t.Fatalf("%+v %+v", res, e)
	}
	if len(h.files.revoked) != 1 || len(h.files.deleted) != 1 {
		t.Fatalf("revoked %v deleted %v", h.files.revoked, h.files.deleted)
	}
}

// Section 2.5: unloadable output images are failures, never passes.
func TestOutputImageLoadFailureIsReported(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	run := h.begin("r", nil)
	run.ReportOutputImageFailure(context.Background())
	out := run.Finish(context.Background(), Output{AssistantText: "fine"})
	if out.State != StateFailedOpen {
		t.Fatalf("%+v", out)
	}
	var found bool
	for _, e := range h.eventsFor("r") {
		if e.Direction == DirectionOutput && e.Modality == ModalityImage && e.Result == ResultFailedOpen && e.ErrorCode == "service_error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("events %+v", h.eventsFor("r"))
	}
	if s := h.stat(DirectionOutput, ModalityImage, ResultFailedOpen, ""); s.CheckCount != 1 || s.FailureCount != 1 || s.ContentItems != 1 {
		t.Fatalf("stat %+v", s)
	}

	// Late reports (after settlement) are discarded.
	n := len(h.allEvents())
	run.ReportOutputImageFailure(context.Background())
	if len(h.allEvents()) != n {
		t.Fatal("a report after settlement must be discarded")
	}

	// No selected output-image category: nothing is recorded.
	h.enable(map[string]any{"policy": map[string]any{
		"inputTextCategories": []string{"violence"}, "outputTextCategories": []string{"violence"},
		"inputImageCategories": []string{}, "outputImageCategories": []string{},
	}})
	run2 := h.begin("r-off", nil)
	run2.ReportOutputImageFailure(context.Background())
	if out := run2.Finish(context.Background(), Output{AssistantText: "fine"}); out.State != StatePassed {
		t.Fatalf("%+v", out)
	}
}

func TestOutputImagesWithoutBytesAreDropped(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	run := h.begin("r", nil)
	out := run.Finish(context.Background(), Output{Images: []ImageRef{{}, {Bytes: pngBytes(t, 8, 8)}}})
	if out.State != StatePassed {
		t.Fatalf("an empty output image is the caller's report, not the engine's: %+v", out)
	}
	if _, n := h.prov.counts(); n != 1 || len(h.prov.imageCalls[0]) != 1 {
		t.Fatalf("calls %d", n)
	}
}

// Scenario 15: a late hit whose persistence fails leaves the run findable.
func TestLateHitPersistFailureSetsModeratingWithEvent(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	h.svc.budgetFn = func(int) time.Duration { return 30 * time.Millisecond }
	release := make(chan struct{})
	h.prov.text = func(string, Scope) (*Response, error) {
		<-release
		return hitResp("m", "violence"), nil
	}
	h.runs.failApply = 1
	out := h.begin("late", nil).Finish(context.Background(), Output{AssistantText: "slow"})
	if out.State != StateFailedOpen {
		t.Fatalf("%+v", out)
	}
	close(release)
	waitFor(t, "late block remembered", func() bool { return h.svc.PendingBlocks() == 1 })
	waitFor(t, "state moderating", func() bool { return h.runs.state("late") == StateModerating })
	if id := h.runs.eventID("late"); !strings.HasPrefix(id, "cme_") {
		t.Fatalf("event id %q must be saved with the state", id)
	}
	if h.notifier.last().Type != EventTypeBlocked {
		t.Fatal("the client still sees the block")
	}
}

// Scenario 14: a remembered block suppresses a second terminal event.
func TestRecoveryAfterRememberedBlockDoesNotNotifyAgain(t *testing.T) {
	h := newHarness(t)
	h.enable(nil)
	outputHitOnly(h, "violence")
	h.runs.failApply = 2
	out := h.begin("again", nil).Finish(context.Background(), Output{AssistantText: "bad"})
	if !out.Blocked {
		t.Fatal("expected block")
	}
	before := len(h.notifier.types())
	h.runs.stale = []RunRef{{RunID: "again"}}
	if err := h.svc.RecoverStaleRuns(context.Background()); err == nil {
		t.Fatal("persistence still failing: recovery reports it")
	}
	if h.runs.state("again") == StateFailedOpen {
		t.Fatal("must never downgrade")
	}
	if err := h.svc.RecoverStaleRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.runs.state("again") != StateBlocked || len(h.notifier.types()) != before {
		t.Fatalf("state %q events %v", h.runs.state("again"), h.notifier.types())
	}
}

// Scenario 19 and the cleanup rules of 3.5.
func TestCleanupSkipsUnreadableMetadataAndMissingStore(t *testing.T) {
	h := newHarness(t)
	bad := h.insertEvent(Event{Direction: "input", Modality: "image", Result: "hit", ImageCount: 1, ImageMetaJSON: `{not json`})
	nostore := h.insertEvent(Event{Direction: "input", Modality: "image", Result: "hit", ImageCount: 2, ImageMetaJSON: `[]`})
	text := h.insertEvent(Event{Direction: "input", Modality: "text", Result: "hit", EncryptedText: "cipher"})
	h.env.Clock.Advance(31 * 24 * time.Hour)
	if err := h.svc.CleanupExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	var b, n, tx Event
	h.env.DB.First(&b, bad.ID)
	h.env.DB.First(&n, nostore.ID)
	h.env.DB.First(&tx, text.ID)
	if b.ImageCount != 1 || b.ImageMetaJSON != `{not json` {
		t.Fatalf("unparsable metadata must stay: %+v", b)
	}
	if tx.EncryptedText != "" {
		t.Fatal("text-only events are always clearable")
	}
	// With images counted but a store that works, an empty list is clearable.
	if n.ImageCount != 0 {
		t.Fatalf("images counted without metadata are clearable when a store exists: %+v", n)
	}
	h.svc.objects = nil
	again := h.insertEvent(Event{Direction: "input", Modality: "image", Result: "hit", ImageCount: 2, ImageMetaJSON: `[]`})
	h.env.Clock.Advance(31 * 24 * time.Hour)
	if err := h.svc.CleanupExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	var kept Event
	h.env.DB.First(&kept, again.ID)
	if kept.ImageCount != 2 {
		t.Fatalf("without an object store events with images are never cleared: %+v", kept)
	}
}
