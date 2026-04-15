package settings_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/settings"
	"github.com/riochat/riochat/backend/internal/testkit"
)

const embBase = "/api/v1/admin/settings/embedding"

func TestEmbeddingStatus(t *testing.T) {
	emb := &fakeEmbedding{status: settings.EmbeddingStatus{
		ModelSignature: "abc@1536", ReadyCount: 10, StaleCount: 2, PendingCount: 5, StalledCount: 1, ActiveCount: 4,
		FailedCount: 3, EmptyCount: 6, UnsupportedCount: 7, NeedsReindex: true, ReindexRunning: true,
	}}
	h := newHarness(t, settings.Ports{Embedding: emb})
	w := h.do("GET", embBase+"/status", h.admin, nil)
	testkit.Expect(t, w, 200, "")
	m := decodeMap(t, w)
	want := map[string]any{"modelSignature": "abc@1536", "readyCount": 10.0, "staleCount": 2.0, "pendingCount": 5.0, "stalledCount": 1.0,
		"activeCount": 4.0, "failedCount": 3.0, "emptyCount": 6.0, "unsupportedCount": 7.0, "needsReindex": true, "reindexRunning": true}
	if len(m) != len(want) {
		t.Fatalf("fields: %v", m)
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v want %v", k, m[k], v)
		}
	}

	emb.statusErr = settings.ErrPortUnavailable
	testkit.Expect(t, h.do("GET", embBase+"/status", h.admin, nil), 503, "embedding.service_unavailable")
	emb.statusErr = errBoom
	testkit.Expect(t, h.do("GET", embBase+"/status", h.admin, nil), 500, "internal.error")

	h = newHarness(t, settings.Ports{})
	testkit.Expect(t, h.do("GET", embBase+"/status", h.admin, nil), 503, "embedding.service_unavailable")
}

func TestEmbeddingReindex(t *testing.T) {
	audit := &fakeAudit{}
	emb := &fakeEmbedding{reindex: settings.ReindexOutcome{Submitted: 4, Reprocessed: 2}}
	h := newHarness(t, settings.Ports{Embedding: emb, Audit: audit})

	w := h.do("POST", embBase+"/reindex", h.admin, nil)
	testkit.Expect(t, w, 200, "")
	m := decodeMap(t, w)
	if m["submitted"] != 6.0 || m["message"] != "reindex jobs submitted" {
		t.Fatalf("response: %v", m)
	}
	if emb.reindexFlag == nil || *emb.reindexFlag {
		t.Fatal("include_empty defaults to false")
	}
	e := audit.all()[0]
	if e.Action != "settings.embedding_reindex" || e.Resource != "system_settings" || e.ActorUserID != 1 ||
		e.Detail["include_empty"] != false || e.Detail["submitted"] != 6 || e.Detail["reprocessed"] != 2 {
		t.Fatalf("audit: %+v", e)
	}

	testkit.Expect(t, h.do("POST", embBase+"/reindex?include_empty=true", h.admin, nil), 200, "")
	if !*emb.reindexFlag {
		t.Fatal("include_empty flag not passed")
	}
	testkit.Expect(t, h.do("POST", embBase+"/reindex?include_empty=maybe", h.admin, nil), 400, "request.invalid_query")

	// a rebuild is already running
	emb.reindex, emb.reindexErr = settings.ReindexOutcome{}, settings.ErrReindexRunning
	testkit.Expect(t, h.do("POST", embBase+"/reindex", h.admin, nil), 409, "resource.conflict")
	// ... but files that were reprocessed still make the call succeed
	emb.reindex = settings.ReindexOutcome{Reprocessed: 3}
	w = h.do("POST", embBase+"/reindex", h.admin, nil)
	testkit.Expect(t, w, 200, "")
	if decodeMap(t, w)["submitted"] != 3.0 {
		t.Fatalf("submitted counts reprocessed files: %s", w.Body)
	}

	emb.reindex, emb.reindexErr = settings.ReindexOutcome{}, fmt.Errorf("wrapped: %w", settings.ErrNothingConfigured)
	testkit.Expect(t, h.do("POST", embBase+"/reindex", h.admin, nil), 400, "embedding.service_not_configured")
	emb.reindexErr = settings.ErrPortUnavailable
	testkit.Expect(t, h.do("POST", embBase+"/reindex", h.admin, nil), 503, "embedding.service_unavailable")
	emb.reindexErr = errBoom
	testkit.Expect(t, h.do("POST", embBase+"/reindex", h.admin, nil), 500, "internal.error")

	h = newHarness(t, settings.Ports{})
	testkit.Expect(t, h.do("POST", embBase+"/reindex", h.admin, nil), 503, "embedding.service_unavailable")
}

func TestEmbeddingTasks(t *testing.T) {
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	emb := &fakeEmbedding{total: 41, tasks: []settings.EmbeddingTask{{
		FileID: "f1", FileName: "a.pdf", MimeType: "application/pdf", SizeBytes: 12, UserID: 3, UserLabel: "Alice (alice)",
		EmbedStatus: "failed", EmbedError: "boom", Stalled: false, Retryable: true, UpdatedAt: now,
	}}}
	h := newHarness(t, settings.Ports{Embedding: emb})

	testkit.Expect(t, h.do("GET", embBase+"/tasks", h.admin, nil), 400, "request.invalid_query")
	testkit.Expect(t, h.do("GET", embBase+"/tasks?bucket=x", h.admin, nil), 400, "request.invalid_query")
	testkit.Expect(t, h.do("GET", embBase+"/tasks?bucket=", h.admin, nil), 400, "request.invalid_query")
	// the bucket is validated even without a service
	bare := newHarness(t, settings.Ports{})
	testkit.Expect(t, bare.do("GET", embBase+"/tasks?bucket=x", bare.admin, nil), 400, "request.invalid_query")
	testkit.Expect(t, bare.do("GET", embBase+"/tasks?bucket=failed", bare.admin, nil), 503, "embedding.service_unavailable")

	w := h.do("GET", embBase+"/tasks?bucket=failed&query=%20a.pdf%20&page=2&page_size=5", h.admin, nil)
	testkit.Expect(t, w, 200, "")
	var page struct {
		Total   int64            `json:"total"`
		Results []map[string]any `json:"results"`
	}
	testkit.Decode(t, w, &page)
	if page.Total != 41 || len(page.Results) != 1 {
		t.Fatalf("page: %+v", page)
	}
	r := page.Results[0]
	for _, k := range []string{"fileID", "fileName", "mimeType", "sizeBytes", "userID", "userLabel", "embedStatus", "embedError", "stalled", "retryable", "retryBlockedReason", "updatedAt"} {
		if _, ok := r[k]; !ok {
			t.Errorf("result lacks %s", k)
		}
	}
	if emb.lastQuery.Bucket != "failed" || emb.lastQuery.Query != "a.pdf" || emb.lastQuery.Page != 2 || emb.lastQuery.PageSize != 5 {
		t.Fatalf("query passed to the service: %+v", emb.lastQuery)
	}
	for _, b := range []string{"ready", "pending", "failed", "stale", "empty", "unsupported"} {
		testkit.Expect(t, h.do("GET", embBase+"/tasks?bucket="+b, h.admin, nil), 200, "")
	}
	// empty result renders as an array
	emb.tasks, emb.total = nil, 0
	w = h.do("GET", embBase+"/tasks?bucket=ready", h.admin, nil)
	if !strings.Contains(w.Body.String(), `"results":[]`) {
		t.Fatalf("empty page: %s", w.Body)
	}
	emb.tasksErr = settings.ErrPortUnavailable
	testkit.Expect(t, h.do("GET", embBase+"/tasks?bucket=ready", h.admin, nil), 503, "embedding.service_unavailable")
}

func ids(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("file-%d", i)
	}
	return out
}

func TestEmbeddingRetry(t *testing.T) {
	audit := &fakeAudit{}
	emb := &fakeEmbedding{retry: settings.RetryOutcome{
		Submitted: []string{"a"}, Skipped: []settings.SkippedFile{{FileID: "b", Reason: "file is not ready"}},
	}}
	h := newHarness(t, settings.Ports{Embedding: emb, Audit: audit})
	post := func(body any) int {
		return h.do("POST", embBase+"/tasks/retry", h.admin, body).Code
	}

	w := h.do("POST", embBase+"/tasks/retry", h.admin, map[string]any{"fileIDs": []string{"a", "b"}})
	testkit.Expect(t, w, 200, "")
	var out struct {
		Submitted []string               `json:"submittedFileIDs"`
		Skipped   []settings.SkippedFile `json:"skipped"`
	}
	testkit.Decode(t, w, &out)
	if len(out.Submitted) != 1 || out.Submitted[0] != "a" || len(out.Skipped) != 1 || out.Skipped[0].FileID != "b" || out.Skipped[0].Reason != "file is not ready" {
		t.Fatalf("outcome: %+v", out)
	}
	if strings.Join(emb.lastRetry, ",") != "a,b" {
		t.Fatalf("ids passed: %v", emb.lastRetry)
	}
	e := audit.all()[0]
	if e.Action != "settings.embedding_retry" || e.Detail["skipped_count"] != 1 {
		t.Fatalf("audit: %+v", e)
	}

	// results are never null
	emb.retry = settings.RetryOutcome{}
	w = h.do("POST", embBase+"/tasks/retry", h.admin, map[string]any{"fileIDs": []string{"a"}})
	if !strings.Contains(w.Body.String(), `"submittedFileIDs":[]`) || !strings.Contains(w.Body.String(), `"skipped":[]`) {
		t.Fatalf("empty outcome: %s", w.Body)
	}

	testkit.Expect(t, h.do("POST", embBase+"/tasks/retry", h.admin, map[string]any{"fileIDs": ids(101)}), 400, "embedding.too_many_files")
	if post(map[string]any{"fileIDs": ids(100)}) != 200 {
		t.Fatal("100 ids are allowed")
	}
	for name, body := range map[string]any{
		"zero ids": map[string]any{"fileIDs": []string{}}, "missing": map[string]any{}, "null": `{"fileIDs":null}`,
		"blank id": map[string]any{"fileIDs": []string{""}}, "long id": map[string]any{"fileIDs": []string{strings.Repeat("x", 65)}},
		"not json": "{", "empty body": "",
	} {
		w := h.do("POST", embBase+"/tasks/retry", h.admin, body)
		if w.Code != 400 || testkit.Envelope(t, w).ErrorCode != "request.invalid_body" {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if post(map[string]any{"fileIDs": []string{strings.Repeat("x", 64)}}) != 200 {
		t.Fatal("64 character ids are allowed")
	}

	emb.retryErr = settings.ErrNothingConfigured
	testkit.Expect(t, h.do("POST", embBase+"/tasks/retry", h.admin, map[string]any{"fileIDs": []string{"a"}}), 503, "embedding.service_not_configured")
	emb.retryErr = settings.ErrPortUnavailable
	testkit.Expect(t, h.do("POST", embBase+"/tasks/retry", h.admin, map[string]any{"fileIDs": []string{"a"}}), 503, "embedding.service_unavailable")
	emb.retryErr = errors.New("db")
	testkit.Expect(t, h.do("POST", embBase+"/tasks/retry", h.admin, map[string]any{"fileIDs": []string{"a"}}), 500, "internal.error")

	h = newHarness(t, settings.Ports{})
	testkit.Expect(t, h.do("POST", embBase+"/tasks/retry", h.admin, map[string]any{"fileIDs": []string{"a"}}), 503, "embedding.service_unavailable")
}

func runtimeStatus(t *testing.T, h *harness) map[string]any {
	t.Helper()
	w := h.do("GET", embBase+"/runtime", h.admin, nil)
	testkit.Expect(t, w, 200, "")
	return decodeMap(t, w)
}

func TestEmbeddingRuntime(t *testing.T) {
	emb := &fakeEmbedding{}
	h := newHarness(t, settings.Ports{Embedding: emb})

	m := runtimeStatus(t, h)
	if m["status"] != "unconfigured" || m["message"] != "Embedding is not enabled" || m["source"] != "external" || m["reachable"] != false {
		t.Fatalf("disabled: %v", m)
	}
	for _, k := range []string{"source", "baseURL", "containerName", "image", "network", "status", "reachable", "message"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing field %s", k)
		}
	}
	if emb.probeCalls != 0 {
		t.Fatal("no probe while unconfigured")
	}

	h.setRaw("file", "embedding_enabled", "true")
	_ = h.svc.Reload(ctxBG())
	if m := runtimeStatus(t, h); m["status"] != "unconfigured" || m["message"] != "The Embedding endpoint URL has not been set" {
		t.Fatalf("no host: %v", m)
	}

	h.setRaw("file", "embedding_host", " http://emb.internal:8080 ")
	h.setRaw("file", "rag_model", "")
	_ = h.svc.Reload(ctxBG())
	m = runtimeStatus(t, h)
	if m["status"] != "unconfigured" || m["message"] != "No model has been specified for Embedding calls" || m["baseURL"] != "http://emb.internal:8080" {
		t.Fatalf("no model: %v", m)
	}

	h.setRaw("file", "rag_model", "m")
	h.setRaw("file", "embedding_timeout_seconds", "600")
	_ = h.svc.Reload(ctxBG())
	m = runtimeStatus(t, h)
	if m["status"] != "running" || m["reachable"] != true || m["message"] != "Service is reachable" {
		t.Fatalf("healthy: %v", m)
	}
	if emb.probeDeadline > 10*time.Second || emb.probeDeadline < 5*time.Second {
		t.Fatalf("probe timeout must be capped at 10s: %v", emb.probeDeadline)
	}
	h.setRaw("file", "embedding_timeout_seconds", "3")
	_ = h.svc.Reload(ctxBG())
	runtimeStatus(t, h)
	if emb.probeDeadline > 3*time.Second {
		t.Fatalf("probe timeout should follow the configured 3s: %v", emb.probeDeadline)
	}

	emb.probeErr = errBoom
	m = runtimeStatus(t, h)
	if m["status"] != "unhealthy" || m["reachable"] != false || m["message"] != "Cannot reach the Embedding service; check the configuration and make sure the service is running" {
		t.Fatalf("failing probe: %v", m)
	}
	if strings.Contains(toJSON(t, m), "boom") {
		t.Fatal("probe error text must not leak")
	}

	// not wired
	h = newHarness(t, settings.Ports{})
	h.setRaw("file", "embedding_enabled", "true")
	h.setRaw("file", "embedding_host", "http://e")
	_ = h.svc.Reload(ctxBG())
	if m := runtimeStatus(t, h); m["status"] != "unavailable" || m["message"] != "embedding service not available" {
		t.Fatalf("not wired: %v", m)
	}
}

func TestManagedRuntimes(t *testing.T) {
	tika := &fakeRuntime{status: settings.RuntimeStatus{Source: "managed", BaseURL: "http://riochat-tika:9998", ContainerName: "riochat-tika",
		Image: "apache/tika", Network: "riochat", Status: "running", Reachable: true, Message: "ok"}}
	rapid := &fakeRuntime{}
	h := newHarness(t, settings.Ports{Runtimes: map[string]settings.ManagedRuntime{settings.RuntimeTika: tika, settings.RuntimeRapidOCR: rapid}})

	w := h.do("GET", "/api/v1/admin/settings/tika/runtime", h.admin, nil)
	testkit.Expect(t, w, 200, "")
	m := decodeMap(t, w)
	if m["source"] != "managed" || m["containerName"] != "riochat-tika" || m["reachable"] != true || m["image"] != "apache/tika" || m["network"] != "riochat" {
		t.Fatalf("status: %v", m)
	}
	for _, action := range []string{"start", "stop", "restart"} {
		w := h.do("POST", "/api/v1/admin/settings/tika/runtime/"+action, h.admin, nil)
		testkit.Expect(t, w, 200, "")
		if decodeMap(t, w)["status"] != action {
			t.Errorf("%s: %s", action, w.Body)
		}
		testkit.Expect(t, h.do("POST", "/api/v1/admin/settings/rapidocr/runtime/"+action, h.admin, nil), 200, "")
	}
	if strings.Join(tika.actions, ",") != "start,stop,restart" || len(rapid.actions) != 3 {
		t.Fatalf("actions: %v %v", tika.actions, rapid.actions)
	}

	// helpers that are not wired report the configured service as unreachable
	// instead of failing, so the admin page can still render them
	for name, want := range map[string]struct{ source, container string }{
		"docling": {"external", "riochat-docling"}, "tesseract": {"external", "riochat-tesseract"}, "mineru": {"cloud", ""}} {
		w := h.do("GET", "/api/v1/admin/settings/"+name+"/runtime", h.admin, nil)
		testkit.Expect(t, w, 200, "")
		m := decodeMap(t, w)
		if m["status"] != "unhealthy" || m["reachable"] != false || m["source"] != want.source || m["containerName"] != want.container {
			t.Errorf("%s offline status: %v", name, m)
		}
		if msg, _ := m["message"].(string); msg == "" {
			t.Errorf("%s offline status has no message", name)
		}
	}
	bare := newHarness(t, settings.Ports{})
	w = bare.do("GET", "/api/v1/admin/settings/tika/runtime", bare.admin, nil)
	testkit.Expect(t, w, 200, "")
	if m := decodeMap(t, w); m["status"] != "unhealthy" || m["image"] == "" || m["network"] == "" {
		t.Errorf("tika offline status: %v", m)
	}
	// control actions without a manager still fail with the service's own code
	testkit.Expect(t, bare.do("POST", "/api/v1/admin/settings/tika/runtime/start", bare.admin, nil), 500, "runtime.tika_unavailable")
	testkit.Expect(t, bare.do("POST", "/api/v1/admin/settings/rapidocr/runtime/restart", bare.admin, nil), 500, "runtime.rapidocr_unavailable")

	// failures of the underlying runtime are 500s without leaking details
	tika.err = errors.New("docker socket secret-path")
	w = h.do("POST", "/api/v1/admin/settings/tika/runtime/start", h.admin, nil)
	testkit.Expect(t, w, 500, "internal.error")
	if strings.Contains(w.Body.String(), "secret-path") {
		t.Fatal("error text must not leak")
	}
	// a status check that errors degrades to the offline description, still 200
	w = h.do("GET", "/api/v1/admin/settings/tika/runtime", h.admin, nil)
	testkit.Expect(t, w, 200, "")
	if decodeMap(t, w)["status"] != "unhealthy" || strings.Contains(w.Body.String(), "secret-path") {
		t.Fatalf("failing status check: %s", w.Body)
	}

	// docling has no control actions
	testkit.Expect(t, h.do("POST", "/api/v1/admin/settings/docling/runtime/start", h.admin, nil), 404, "")
}
