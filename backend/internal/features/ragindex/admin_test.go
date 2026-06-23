package ragindex

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

func ago(te *tenv, d time.Duration) func(*File) {
	return func(f *File) { f.UpdatedAt = te.Clock.Now().Add(-d) }
}

func seedStates(te *tenv) {
	sig := te.sig()
	te.addFile(1, "s-ready", func(f *File) { f.EmbedStatus = StateReady; f.EmbedSignature = sig })
	te.addFile(1, "s-stale", func(f *File) { f.EmbedStatus = StateStale })
	te.addFile(1, "s-empty", func(f *File) { f.EmbedStatus = StateEmpty })
	te.addFile(1, "s-failed", func(f *File) { f.EmbedStatus = StateFailed })
	te.addFile(1, "s-never") // none + indexable
	te.addFile(1, "s-textfail", func(f *File) { f.ProcessingStatus = procFailed; f.ProcessingReady = false })
	te.addFile(1, "s-video", func(f *File) { f.FileCategory = "video"; f.MimeType = "video/mp4"; f.DetectedMIME = "video/mp4" })
	te.addFile(1, "s-image", func(f *File) { f.FileCategory = "image"; f.MimeType = "image/png"; f.DetectedMIME = "image/png" })
	te.addFile(1, "s-queued", func(f *File) { f.EmbedStatus = StateQueued; f.EmbedSignature = sig })
	te.addFile(1, "s-queued-old", func(f *File) { f.EmbedStatus = StateQueued; f.EmbedSignature = sig }, ago(te, 40*time.Minute))
	te.addFile(1, "s-proc", func(f *File) { f.EmbedStatus = StateProcessing; f.EmbedSignature = sig })
	te.addFile(1, "s-legacy", func(f *File) { f.EmbedStatus = "disabled" }) // legacy marker counts as never indexed
	te.addFile(1, "s-deleted", func(f *File) { f.Status = "deleted" })
}

func TestStatusCounts(t *testing.T) {
	te := newEnv(t)
	seedStates(te)
	st, err := te.svc.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := Status{
		ModelSignature: te.sig(), ReadyCount: 1, StaleCount: 1, EmptyCount: 1,
		FailedCount: 2, PendingCount: 1 + 1 + 3, StalledCount: 1, ActiveCount: 2, UnsupportedCount: 2, NeedsReindex: true,
	}
	if st != want {
		t.Fatalf("got  %+v\nwant %+v", st, want)
	}
}

func TestStatusImageRule(t *testing.T) {
	te := newEnv(t, func(c *Config) { c.ImageOCREnabled = true })
	te.addFile(1, "img", func(f *File) { f.FileCategory = "image"; f.MimeType = "image/png"; f.DetectedMIME = "image/png" })
	st, _ := te.svc.Status(context.Background())
	if st.PendingCount != 1 || st.UnsupportedCount != 0 {
		t.Fatalf("image with OCR must be pending: %+v", st)
	}
	te2 := newEnv(t, func(c *Config) { c.Protocol = "voyage" })
	te2.emb.set(func(e *fakeEmbedder) { e.images = true })
	te2.addFile(1, "gif", func(f *File) { f.FileCategory = "image"; f.MimeType = "image/gif"; f.DetectedMIME = "image/gif" })
	te2.addFile(1, "bmp", func(f *File) { f.FileCategory = "image"; f.MimeType = "image/bmp"; f.DetectedMIME = "image/bmp" })
	st, _ = te2.svc.Status(context.Background())
	if st.PendingCount != 1 || st.UnsupportedCount != 1 {
		t.Fatalf("pixel path accepts gif but not bmp: %+v", st)
	}
}

func TestStatusReindexRunningFlag(t *testing.T) {
	te := newEnv(t)
	te.svc.rebuilding.Store(true)
	st, _ := te.svc.Status(context.Background())
	if !st.ReindexRunning {
		t.Fatal("reindexRunning must reflect the local rebuild")
	}
}

func names(ts []Task) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.FileID
	}
	sort.Strings(out)
	return out
}

func TestTasksBuckets(t *testing.T) {
	te := newEnv(t)
	seedStates(te)
	ctx := context.Background()
	cases := map[string][]string{
		BucketReady:       {"s-ready"},
		BucketStale:       {"s-stale"},
		BucketEmpty:       {"s-empty"},
		BucketFailed:      {"s-failed", "s-textfail"},
		BucketPending:     {"s-legacy", "s-never", "s-proc", "s-queued", "s-queued-old"},
		BucketUnsupported: {"s-image", "s-video"},
	}
	for bucket, want := range cases {
		rows, total, err := te.svc.Tasks(ctx, TaskQuery{Bucket: bucket, Page: 1, PageSize: 50})
		if err != nil {
			t.Fatal(bucket, err)
		}
		got := names(rows)
		if int(total) != len(want) || len(got) != len(want) {
			t.Fatalf("%s: got %v (total %d), want %v", bucket, got, total, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: got %v, want %v", bucket, got, want)
			}
		}
	}
	if _, _, err := te.svc.Tasks(ctx, TaskQuery{Bucket: "nope"}); err == nil {
		t.Fatal("unknown bucket must fail")
	}
}

func TestTasksFieldsAndSearch(t *testing.T) {
	te := newEnv(t)
	te.addFile(5, "alpha-id", func(f *File) {
		f.FileName = "Quarterly_Report.pdf"
		f.EmbedStatus = StateFailed
		f.EmbedError = "vectorization failed, retry later"
		f.SizeBytes = 2048
		f.DetectedMIME = "text/plain"
		f.MimeType = "application/octet-stream"
	})
	te.addFile(5, "beta-id", func(f *File) { f.FileName = "50%_done.txt"; f.EmbedStatus = StateFailed }, ago(te, time.Hour))
	te.addFile(0, "gamma-id", func(f *File) {
		f.EmbedStatus = StateFailed
		f.ProcessingStatus = procFailed
		f.ProcessingErrMsg = "extract blew up"
		f.ProcessingReady = false
	})
	ctx := context.Background()
	rows, total, err := te.svc.Tasks(ctx, TaskQuery{Bucket: BucketFailed, Query: "  REPORT ", Page: 1, PageSize: 10})
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("name search: %v %d %v", err, total, rows)
	}
	r := rows[0]
	if r.FileID != "alpha-id" || r.MimeType != "text/plain" || r.SizeBytes != 2048 || r.UserID != 5 || r.UserLabel == "" ||
		r.EmbedStatus != StateFailed || r.EmbedError != "vectorization failed, retry later" || !r.Retryable || r.RetryBlockedReason != "" {
		t.Fatalf("row %+v", r)
	}
	rows, total, _ = te.svc.Tasks(ctx, TaskQuery{Bucket: BucketFailed, Query: "BETA-ID"})
	if total != 1 || rows[0].FileID != "beta-id" {
		t.Fatalf("id search: %v", rows)
	}
	rows, total, _ = te.svc.Tasks(ctx, TaskQuery{Bucket: BucketFailed, Query: "50%_"})
	if total != 1 {
		t.Fatalf("wildcards must match literally, got %d", total)
	}
	rows, total, _ = te.svc.Tasks(ctx, TaskQuery{Bucket: BucketFailed, PageSize: 2000})
	if total != 3 || len(rows) != 3 {
		t.Fatalf("page_size clamp: %d %d", total, len(rows))
	}
	if rows[0].FileID != "gamma-id" && rows[0].FileID != "alpha-id" {
		t.Fatalf("most recently updated first, got %s", rows[0].FileID)
	}
	if rows[2].FileID != "beta-id" {
		t.Fatalf("oldest last, got %s", rows[2].FileID)
	}
	for _, r := range rows {
		if r.FileID == "gamma-id" {
			if r.EmbedError != "extract blew up" || r.UserLabel != "" {
				t.Fatalf("text failure shows the processing error and unresolved owners are blank: %+v", r)
			}
		}
	}
	rows, _, _ = te.svc.Tasks(ctx, TaskQuery{Bucket: BucketFailed, Page: 2, PageSize: 2})
	if len(rows) != 1 {
		t.Fatalf("second page: %d", len(rows))
	}
}

func TestTasksRetryBlockedReasons(t *testing.T) {
	te := newEnv(t)
	sig := te.sig()
	te.addFile(1, "t-ready", func(f *File) { f.EmbedStatus = StateReady; f.EmbedSignature = sig })
	te.addFile(1, "t-proc", func(f *File) { f.EmbedStatus = StateProcessing; f.EmbedSignature = sig })
	te.addFile(1, "t-stalled", func(f *File) { f.EmbedStatus = StateQueued; f.EmbedSignature = sig }, ago(te, time.Hour))
	te.addFile(1, "t-unsupported", func(f *File) { f.FileCategory = "video"; f.MimeType = "video/mp4"; f.DetectedMIME = "video/mp4" })
	te.addFile(1, "t-extracting", func(f *File) { f.ProcessingStatus = procExtracting; f.ProcessingReady = false })
	te.addFile(1, "t-textfail", func(f *File) { f.ProcessingStatus = procFailed; f.ProcessingReady = false })
	rows := map[string]Task{}
	for _, b := range []string{BucketReady, BucketPending, BucketFailed, BucketUnsupported} {
		ts, _, err := te.svc.Tasks(context.Background(), TaskQuery{Bucket: b})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range ts {
			rows[r.FileID] = r
		}
	}
	want := map[string]string{
		"t-ready": SkipAlreadyDone, "t-proc": SkipProcessing, "t-stalled": "", "t-unsupported": SkipUnsupported,
		"t-extracting": SkipNotReady, "t-textfail": "",
	}
	for id, reason := range want {
		r, ok := rows[id]
		if !ok {
			t.Fatalf("%s missing from the listing", id)
		}
		if r.RetryBlockedReason != reason || r.Retryable != (reason == "") {
			t.Fatalf("%s: %+v want reason %q", id, r, reason)
		}
	}
	if !rows["t-stalled"].Stalled || rows["t-proc"].Stalled {
		t.Fatal("stalled flag")
	}
}

func TestRetrySkipReasonsAndSubmission(t *testing.T) {
	te := newEnv(t)
	sig := te.sig()
	te.addFile(1, "ready", func(f *File) { f.EmbedStatus = StateReady; f.EmbedSignature = sig })
	te.addFile(1, "busy", func(f *File) { f.EmbedStatus = StateProcessing; f.EmbedSignature = sig })
	te.addFile(1, "video", func(f *File) { f.FileCategory = "video"; f.MimeType = "video/mp4"; f.DetectedMIME = "video/mp4" })
	te.addFile(1, "extracting", func(f *File) { f.ProcessingReady = false; f.ProcessingStatus = procExtracting })
	te.addFile(1, "ok")
	te.addFile(2, "theirs")
	te.addFile(1, "gone", func(f *File) { f.Status = "deleted" })
	ctx := context.Background()
	res, err := te.svc.Submit(ctx, 1, []string{"ready", "busy", "video", "extracting", "ok", "theirs", "gone", "missing", " ok ", "", "ready"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range res.Skipped {
		got[s.FileID] = s.Reason
	}
	want := map[string]string{
		"ready": SkipAlreadyDone, "busy": SkipProcessing, "video": SkipUnsupported, "extracting": SkipNotReady,
		"theirs": SkipNotFound, "gone": SkipNotFound, "missing": SkipNotFound,
	}
	if len(got) != len(want) || len(res.Skipped) != len(want) {
		t.Fatalf("skipped %v", res.Skipped)
	}
	for id, r := range want {
		if got[id] != r {
			t.Fatalf("%s: got %q want %q", id, got[id], r)
		}
	}
	if len(res.SubmittedFileIDs) != 1 || res.SubmittedFileIDs[0] != "ok" {
		t.Fatalf("submitted %v", res.SubmittedFileIDs)
	}
	var f File
	te.DB.Where("file_id = ?", "ok").First(&f)
	if f.EmbedStatus != StateQueued || f.EmbedSignature != sig {
		t.Fatalf("registered as %q/%q", f.EmbedStatus, f.EmbedSignature)
	}
	// The same file submitted again is in flight.
	res, _ = te.svc.Submit(ctx, 1, []string{"ok"})
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != SkipProcessing {
		t.Fatalf("resubmission: %+v", res)
	}
	// After the stall window it may be submitted again.
	te.Clock.Advance(31 * time.Minute)
	res, _ = te.svc.Submit(ctx, 1, []string{"ok"})
	if len(res.SubmittedFileIDs) != 1 {
		t.Fatalf("stalled task must be resubmittable: %+v", res)
	}
}

func TestSubmitWorksWithoutAutoTrigger(t *testing.T) {
	te := newEnv(t, func(c *Config) { c.TriggerOnUpload = false })
	f := te.addFile(1, "manual")
	if got := te.indexNow(f); got.EmbedStatus != StateReady {
		t.Fatalf("got %q", got.EmbedStatus)
	}
}

func TestSubmitNormalizationAndLimits(t *testing.T) {
	te := newEnv(t)
	ctx := context.Background()
	te.cfg.update(func(c *Config) { c.Enabled = false })
	res, err := te.svc.Submit(ctx, 1, []string{"  ", ""})
	if err != nil || len(res.SubmittedFileIDs) != 0 || len(res.Skipped) != 0 || res.SubmittedFileIDs == nil || res.Skipped == nil {
		t.Fatalf("blank-only request must succeed with empty lists even when unavailable: %+v %v", res, err)
	}
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = "id" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	te.cfg.update(func(c *Config) { c.Enabled = true })
	if _, err := te.svc.Submit(ctx, 1, ids); !errors.Is(err, ErrTooManyFiles) {
		t.Fatalf("got %v", err)
	}
}

func TestSubmitAvailabilityErrors(t *testing.T) {
	cases := map[string]struct {
		mod  func(te *tenv)
		want *apperr.Error
	}{
		"disabled":      {func(te *tenv) { te.cfg.update(func(c *Config) { c.Enabled = false }) }, ErrNotConfigured},
		"model missing": {func(te *tenv) { te.cfg.update(func(c *Config) { c.Model = "" }) }, ErrNotConfigured},
		"host missing":  {func(te *tenv) { te.cfg.update(func(c *Config) { c.Host = "" }) }, ErrNotConfigured},
		"no client":     {func(te *tenv) { te.svc.emb = nil }, ErrUnavailable},
		"no store": {func(te *tenv) {
			te.svc.storeMu.Lock()
			te.svc.storeKnown, te.svc.storeOK = true, false
			te.svc.storeMu.Unlock()
		}, ErrUnavailable},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			te := newEnv(t)
			te.addFile(1, "f")
			c.mod(te)
			_, err := te.svc.Submit(context.Background(), 1, []string{"f"})
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v want %v", err, c.want)
			}
		})
	}
}

func TestAdminRetryReextractsFailedText(t *testing.T) {
	te := newEnv(t)
	ctx := context.Background()
	failed := te.addFile(4, "textfail", func(f *File) {
		f.ProcessingStatus = procFailed
		f.ProcessingReady = false
		f.ProcessingErrMsg = "boom"
		f.ExtractText = ""
		f.ExtractStatus = "failed"
	})
	te.content.text[failed.ID] = "fresh text recovered by the administrator action"
	res, err := te.svc.Submit(ctx, 4, []string{"textfail"})
	if err != nil || len(res.Skipped) != 1 || res.Skipped[0].Reason != SkipNotReady {
		t.Fatalf("user submit reports not_ready: %+v %v", res, err)
	}
	res, err = te.svc.Retry(ctx, []string{"textfail"})
	if err != nil || len(res.SubmittedFileIDs) != 1 {
		t.Fatalf("admin retry: %+v %v", res, err)
	}
	got := te.reload(failed)
	if got.ProcessingStatus != procQueued || got.EmbedStatus != StateQueued || got.ProcessingErrMsg != "" || got.EmbedSignature != te.sig() {
		t.Fatalf("after reset: %+v", got)
	}
	te.svc.DrainQueue(ctx)
	got = te.reload(failed)
	if got.EmbedStatus != StateReady || got.ProcessingStatus != procReady || !got.ProcessingReady || got.ExtractText == "" || got.UserID != 4 {
		t.Fatalf("after extraction and index: %+v", got)
	}
	var chunk Chunk
	te.DB.Where("file_obj_id = ?", failed.ID).First(&chunk)
	if chunk.UserID != 4 {
		t.Fatalf("tasks stay attributed to the owner, chunk owner %d", chunk.UserID)
	}
}

func TestAdminRetryReextractionFailure(t *testing.T) {
	te := newEnv(t)
	f := te.addFile(1, "bad", func(f *File) { f.ProcessingStatus = procFailed; f.ProcessingReady = false; f.ExtractText = "" })
	te.content.textErr = errors.New("parser crashed")
	te.svc.Retry(context.Background(), []string{"bad"})
	te.svc.DrainQueue(context.Background())
	got := te.reload(f)
	if got.ProcessingStatus != procFailed || got.EmbedStatus != StateNone || got.ProcessingErrCod != "extract_failed" {
		t.Fatalf("got %+v", got)
	}
}

func TestAdminRetryEmptyExtractionAndLostMessage(t *testing.T) {
	te := newEnv(t)
	te.addFile(1, "empty", func(f *File) { f.ExtractStatus = extractEmpty; f.ProcessingReady = false; f.ExtractText = "" })
	te.addFile(1, "lost", func(f *File) { f.ProcessingStatus = procUploaded; f.ProcessingReady = false }, ago(te, 31*time.Minute))
	te.addFile(1, "fresh", func(f *File) { f.ProcessingStatus = procUploaded; f.ProcessingReady = false })
	res, err := te.svc.Retry(context.Background(), []string{"empty", "lost", "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SubmittedFileIDs) != 2 || len(res.Skipped) != 1 || res.Skipped[0].FileID != "fresh" || res.Skipped[0].Reason != SkipNotReady {
		t.Fatalf("%+v", res)
	}
}

func TestTwoAdminsRetrySameFile(t *testing.T) {
	te := newEnv(t)
	te.addFile(1, "race", func(f *File) { f.ProcessingStatus = procFailed; f.ProcessingReady = false })
	a, _ := te.svc.Retry(context.Background(), []string{"race"})
	b, _ := te.svc.Retry(context.Background(), []string{"race"})
	if len(a.SubmittedFileIDs) != 1 || len(b.SubmittedFileIDs) != 0 || len(b.Skipped) != 1 || b.Skipped[0].Reason != SkipProcessing {
		t.Fatalf("first %+v second %+v", a, b)
	}
}

func TestReextractQueueFailure(t *testing.T) {
	te := newEnv(t)
	te.svc.enqueueFn = func(context.Context, *Job) error { return errors.New("down") }
	f := te.addFile(1, "q", func(f *File) { f.ProcessingStatus = procFailed; f.ProcessingReady = false })
	res, _ := te.svc.Retry(context.Background(), []string{"q"})
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != SkipQueueBusy {
		t.Fatalf("%+v", res)
	}
	if got := te.reload(f); got.ProcessingStatus != procFailed || got.ProcessingErrCod != "queue_unavailable" || got.EmbedStatus != StateNone {
		t.Fatalf("got %+v", got)
	}
}

func TestVectorization(t *testing.T) {
	te := newEnv(t)
	ctx := context.Background()
	sig := te.sig()
	check := func(f File, can bool, reason string) {
		t.Helper()
		c, r := te.svc.Vectorization(ctx, f)
		if c != can || r != reason {
			t.Fatalf("%s: got %v/%q want %v/%q", f.FileID, c, r, can, reason)
		}
	}
	check(te.addFile(1, "a"), true, "")
	check(te.addFile(1, "b", func(f *File) { f.EmbedStatus = StateReady; f.EmbedSignature = sig }), false, SkipAlreadyDone)
	check(te.addFile(1, "c", func(f *File) { f.EmbedStatus = StateReady; f.EmbedSignature = "old@16" }), true, ReasonOutdatedIndex)
	check(te.addFile(1, "d", func(f *File) { f.EmbedStatus = StateStale }), true, ReasonOutdatedIndex)
	check(te.addFile(1, "e", func(f *File) { f.ProcessingReady = false }), false, SkipNotReady)
	check(te.addFile(1, "f", func(f *File) { f.FileCategory = "audio"; f.MimeType = "audio/mp3"; f.DetectedMIME = "audio/mp3" }), false, SkipUnsupported)
	te.cfg.update(func(c *Config) { c.Enabled = false })
	check(te.addFile(1, "g"), false, ReasonDisabled)
	te.cfg.update(func(c *Config) { c.Enabled = true; c.Model = "" })
	check(te.addFile(1, "h"), false, ReasonModelMissing)
	te.cfg.update(func(c *Config) { c.Model = "m"; c.Host = "" })
	check(te.addFile(1, "i"), false, ReasonHostMissing)
	te.cfg.update(func(c *Config) { c.Host = "https://x" })
	te.svc.emb = nil
	check(te.addFile(1, "j"), false, ReasonClientMissing)
}

func TestAvailabilityStoreMissing(t *testing.T) {
	te := newEnv(t)
	te.svc.storeMu.Lock()
	te.svc.storeKnown, te.svc.storeOK = true, false
	te.svc.storeMu.Unlock()
	if av := te.svc.Availability(context.Background()); av.OK || av.Reason != ReasonStoreUnavailable || av.NotConfigured {
		t.Fatalf("%+v", av)
	}
	te2 := newEnv(t)
	te2.DB.Migrator().DropTable(&ChunkVector{})
	if ok, _ := te2.svc.VectorStoreAvailable(context.Background()); ok {
		t.Fatal("missing vector table means the store is unavailable")
	}
	te2.DB.AutoMigrate(&ChunkVector{})
	if ok, _ := te2.svc.VectorStoreAvailable(context.Background()); ok {
		t.Fatal("a definite answer is cached for the life of the process")
	}
}

func TestProbe(t *testing.T) {
	te := newEnv(t)
	ctx := context.Background()
	if err := te.svc.Probe(ctx, ""); err != nil {
		t.Fatal(err)
	}
	reqs := te.emb.requests()
	if reqs[0].Inputs[0].Text != "health check" {
		t.Fatalf("default probe text: %q", reqs[0].Inputs[0].Text)
	}
	te.emb.set(func(e *fakeEmbedder) { e.err = ErrProviderUnavailable })
	if err := te.svc.Probe(ctx, "x"); err == nil {
		t.Fatal("an unreachable provider must fail the probe")
	}
	te.cfg.update(func(c *Config) { c.Enabled = false })
	if err := te.svc.Probe(ctx, "x"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("got %v", err)
	}
}

func TestReindexNothingToDo(t *testing.T) {
	te := newEnv(t)
	te.addFile(1, "r", func(f *File) { f.EmbedStatus = StateReady; f.EmbedSignature = te.sig() })
	res, err := te.svc.Reindex(context.Background(), false)
	if err != nil || res.Submitted != 0 || res.Reprocessed != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	te.svc.WaitRebuild()
}

func TestReindexBuildsMissingIndexes(t *testing.T) {
	te := newEnv(t)
	sig := te.sig()
	var stale []File
	for _, id := range []string{"a", "b", "c"} {
		stale = append(stale, te.addFile(1, "stale-"+id, func(f *File) { f.EmbedStatus = StateStale }))
	}
	failed := te.addFile(1, "failed", func(f *File) { f.EmbedStatus = StateFailed })
	never := te.addFile(1, "never")
	stalledQueued := te.addFile(1, "queued-old", func(f *File) { f.EmbedStatus = StateQueued; f.EmbedSignature = sig }, ago(te, time.Hour))
	freshQueued := te.addFile(1, "queued-new", func(f *File) { f.EmbedStatus = StateQueued; f.EmbedSignature = sig })
	ready := te.addFile(1, "ready", func(f *File) { f.EmbedStatus = StateReady; f.EmbedSignature = sig; f.ChunkCount = 9 })
	empty := te.addFile(1, "empty", func(f *File) { f.EmbedStatus = StateNone; f.ExtractStatus = extractEmpty })
	video := te.addFile(1, "video", func(f *File) { f.FileCategory = "video"; f.MimeType = "video/mp4"; f.DetectedMIME = "video/mp4" })
	res, err := te.svc.Reindex(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Submitted != 6 || res.Reprocessed != 0 {
		t.Fatalf("%+v", res)
	}
	te.svc.WaitRebuild()
	for _, f := range append(stale, failed, never, stalledQueued) {
		if g := te.reload(f); g.EmbedStatus != StateReady || g.EmbedSignature != sig || g.ChunkCount == 0 {
			t.Fatalf("%s: %q chunks %d", f.FileID, g.EmbedStatus, g.ChunkCount)
		}
	}
	if g := te.reload(ready); g.ChunkCount != 9 || te.chunkCount(ready) != 0 {
		t.Fatal("ready files must not be touched")
	}
	if te.reload(freshQueued).EmbedStatus != StateQueued || te.reload(empty).EmbedStatus != StateNone || te.reload(video).EmbedStatus != StateNone {
		t.Fatal("fresh queued, empty and unsupported files are left alone")
	}
	st, _ := te.svc.Status(context.Background())
	if st.ReindexRunning {
		t.Fatal("rebuild finished")
	}
}

func TestReindexBusy(t *testing.T) {
	te := newEnv(t)
	te.addFile(1, "x", func(f *File) { f.EmbedStatus = StateStale })
	release := make(chan struct{})
	entered := make(chan struct{}, 8)
	te.emb.set(func(e *fakeEmbedder) { e.hook = func(EmbedRequest) { entered <- struct{}{}; <-release } })
	res, err := te.svc.Reindex(context.Background(), false)
	if err != nil || res.Submitted != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	<-entered
	if st, _ := te.svc.Status(context.Background()); !st.ReindexRunning {
		t.Fatal("reindexRunning must be true while rebuilding")
	}
	res, err = te.svc.Reindex(context.Background(), false)
	if !errors.Is(err, ErrReindexRunning) || res.Submitted != 0 {
		t.Fatalf("second call: %+v %v", res, err)
	}
	close(release)
	te.svc.WaitRebuild()
	res, err = te.svc.Reindex(context.Background(), false)
	if err != nil || res.Submitted != 0 {
		t.Fatalf("after completion: %+v %v", res, err)
	}
}

func TestReindexBusyWithReprocessing(t *testing.T) {
	te := newEnv(t)
	te.addFile(1, "x", func(f *File) { f.EmbedStatus = StateStale })
	te.addFile(1, "bad", func(f *File) { f.ProcessingStatus = procFailed; f.ProcessingReady = false })
	te.svc.rebuilding.Store(true)
	res, err := te.svc.Reindex(context.Background(), false)
	if !errors.Is(err, ErrReindexRunning) || res.Reprocessed != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	te.svc.rebuilding.Store(false)
}

func TestReindexUnavailable(t *testing.T) {
	te := newEnv(t)
	te.addFile(1, "x", func(f *File) { f.EmbedStatus = StateStale })
	te.cfg.update(func(c *Config) { c.Enabled = false })
	if _, err := te.svc.Reindex(context.Background(), false); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("got %v", err)
	}
	te.cfg.update(func(c *Config) { c.Enabled = true })
	te.svc.storeMu.Lock()
	te.svc.storeKnown, te.svc.storeOK = true, false
	te.svc.storeMu.Unlock()
	if _, err := te.svc.Reindex(context.Background(), false); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("a missing vector store is reported as not configured here, got %v", err)
	}
}

func TestReindexIncludeEmpty(t *testing.T) {
	te := newEnv(t)
	empty := te.addFile(1, "empty", func(f *File) { f.ExtractStatus = extractEmpty; f.ProcessingReady = false; f.EmbedStatus = StateEmpty })
	te.addFile(1, "failed", func(f *File) { f.ProcessingStatus = procFailed; f.ProcessingReady = false })
	te.addFile(1, "lost", func(f *File) { f.ProcessingStatus = procQueued; f.ProcessingReady = false }, ago(te, time.Hour))
	res, err := te.svc.Reindex(context.Background(), false)
	if err != nil || res.Reprocessed != 2 {
		t.Fatalf("without include_empty: %+v %v", res, err)
	}
	res, err = te.svc.Reindex(context.Background(), true)
	if err != nil || res.Reprocessed != 1 {
		t.Fatalf("with include_empty only the empty file is new: %+v %v", res, err)
	}
	if g := te.reload(empty); g.ProcessingStatus != procQueued || g.EmbedStatus != StateQueued || g.ExtractStatus != extractNone {
		t.Fatalf("empty file after reset: %+v", g)
	}
	// Sending them through extraction and indexing completes the cycle.
	te.content.text[empty.ID] = "now there is text"
	te.svc.DrainQueue(context.Background())
	if g := te.reload(empty); g.EmbedStatus != StateReady {
		t.Fatalf("after drain: %q", g.EmbedStatus)
	}
}
