package conversation

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/testkit"
)

// pngBytes is a minimal valid PNG header followed by padding.
func pngBytes(extra int) []byte {
	b := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R'}
	return append(b, bytes.Repeat([]byte{1}, extra)...)
}

func (e *tenv) uploadOK(hdr map[string]string, name, mime string, data []byte) map[string]any {
	e.t.Helper()
	w := e.upload(hdr, name, mime, data)
	testkit.Expect(e.t, w, 200, "")
	return dataMap(e.t, w)
}

func TestUploadAndFileObject(t *testing.T) {
	e := newEnv(t)
	m := e.uploadOK(e.u1, "notes.txt", "text/plain", []byte("hello file world"))
	f := m["file"].(map[string]any)
	if !strings.HasPrefix(str(f, "fileID"), "file_") || len(str(f, "fileID")) != 37 {
		t.Fatalf("file id: %v", f["fileID"])
	}
	if str(f, "fileName") != "notes.txt" || num(f, "sizeBytes") != 16 || str(f, "status") != "active" || str(f, "sha256") == "" || str(f, "fileCategory") != "text" {
		t.Fatalf("file: %v", f)
	}
	for _, k := range []string{"purpose", "mimeType", "detectedMIME", "processingStatus", "processingReady", "processingErrorCode", "processingErrorMessage",
		"extractStatus", "embedStatus", "embedError", "chunkCount", "ragOptOut", "canVectorize", "vectorizationReason", "lastAccessedAt", "expiresAt", "createdAt", "updatedAt"} {
		if _, ok := f[k]; !ok {
			t.Errorf("missing file field %q", k)
		}
	}
	q := m["quota"].(map[string]any)
	if num(q, "usedBytes") != 16 || num(q, "userID") != 1 {
		t.Fatalf("quota: %v", q)
	}
	if m["reused"] != false {
		t.Fatalf("reused: %v", m["reused"])
	}
	// identical content by the same user is reused, no extra quota
	m2 := e.uploadOK(e.u1, "again.txt", "text/plain", []byte("hello file world"))
	if m2["reused"] != true || str(m2["file"].(map[string]any), "fileID") != str(f, "fileID") || num(m2["quota"].(map[string]any), "usedBytes") != 16 {
		t.Fatalf("reuse: %v", m2)
	}
	// another user gets their own file
	m3 := e.uploadOK(e.u2, "notes.txt", "text/plain", []byte("hello file world"))
	if m3["reused"] != false || str(m3["file"].(map[string]any), "fileID") == str(f, "fileID") {
		t.Fatalf("per-user: %v", m3)
	}
	if e.audit.find("upload_file") == nil {
		t.Fatal("audit")
	}
}

func TestUploadErrors(t *testing.T) {
	e := newEnv(t)
	// no file part
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("purpose", "x")
	_ = mw.Close()
	h := map[string]string{"Content-Type": mw.FormDataContentType()}
	for k, v := range e.u1 {
		h[k] = v
	}
	testkit.Expect(t, e.Do(http.MethodPost, "/api/v1/files", h, buf.Bytes()), 400, "file.required")
	testkit.Expect(t, e.call("POST", "/files", e.u1, "plain"), 400, "file.required")
	// blocked types
	testkit.Expect(t, e.upload(e.u1, "run.exe", "application/octet-stream", []byte("MZ....")), 400, "file.type_blocked")
	testkit.Expect(t, e.upload(e.u1, "a.txt", "application/x-msdownload", []byte("MZ\x90\x00\x03")), 400, "file.type_blocked")
	// too large (cap = max upload + 1 MiB)
	e.set.mutate(func(r *RuntimeSettings) { r.MaxUploadBytes = 1024 })
	testkit.Expect(t, e.upload(e.u1, "big.txt", "text/plain", bytes.Repeat([]byte("a"), 2*1024*1024)), 400, "file.required")
	testkit.Expect(t, e.upload(e.u1, "mid.txt", "text/plain", bytes.Repeat([]byte("a"), 2048)), 400, "file.required")
	e.set.mutate(func(r *RuntimeSettings) { r.MaxUploadBytes = 20 << 20; r.ImageMaxBytes = 100 })
	testkit.Expect(t, e.upload(e.u1, "p.png", "image/png", pngBytes(500)), 413, "file.too_large")
	// quota
	e.DB.Create(&StorageQuota{UserID: 2, QuotaBytes: 10})
	testkit.Expect(t, e.upload(e.u2, "q.txt", "text/plain", []byte("more than ten bytes")), 409, "quota.exceeded")
	testkit.Expect(t, e.call("POST", "/files", nil, nil), 401, "")
}

func TestFileListGetPatchDelete(t *testing.T) {
	e := newEnv(t)
	a := str(e.uploadOK(e.u1, "alpha.txt", "text/plain", []byte("aaaa"))["file"].(map[string]any), "fileID")
	b := str(e.uploadOK(e.u1, "beta.png", "image/png", pngBytes(50))["file"].(map[string]any), "fileID")
	c := str(e.uploadOK(e.u1, "gamma.go", "text/plain", []byte("package main"))["file"].(map[string]any), "fileID")
	e.uploadOK(e.u2, "other.txt", "text/plain", []byte("zzz"))

	m := e.ok("GET", "/files", e.u1, nil)
	if num(m, "total") != 3 || len(m["results"].([]any)) != 3 || m["quota"] == nil {
		t.Fatalf("list: %v", m)
	}
	cases := []struct {
		q    string
		want int
	}{{"kind=image", 1}, {"kind=image,code", 3}, {"kind=all", 3}, {"kind=", 3}, {"kind=bogus", 3}, {"q=alp", 1}, {"q=ALPHA", 1}, {"q=nomatch", 0}, {"kind=document", 0}, {"kind=code", 2}}
	for _, c := range cases {
		m := e.ok("GET", "/files?"+c.q, e.u1, nil)
		if int(num(m, "total")) != c.want {
			t.Errorf("%s -> %v want %d", c.q, m["total"], c.want)
		}
	}
	// sorting
	m = e.ok("GET", "/files?sort=name", e.u1, nil)
	rs := m["results"].([]any)
	if rs[0].(map[string]any)["fileName"] != "alpha.txt" || rs[2].(map[string]any)["fileName"] != "gamma.go" {
		t.Fatalf("sort name: %v", rs)
	}
	m = e.ok("GET", "/files?sort=size", e.u1, nil)
	if rs = m["results"].([]any); rs[0].(map[string]any)["fileName"] != "beta.png" && rs[0].(map[string]any)["fileName"] != "alpha.txt" {
		t.Fatalf("sort size: %v", rs)
	}
	m = e.ok("GET", "/files?sort=bogus&page_size=2", e.u1, nil)
	if len(m["results"].([]any)) != 2 || num(m, "total") != 3 {
		t.Fatalf("paging: %v", m)
	}

	// content
	w := e.call("GET", "/files/"+a+"/content", e.u1, nil)
	if w.Code != 200 || w.Body.String() != "aaaa" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("content: %d %q %v", w.Code, w.Body.String(), w.Header())
	}
	e.expectErr("GET", "/files/"+a+"/content", e.u2, nil, 404, "file.not_found")
	e.expectErr("GET", "/files/file_nope/content", e.u1, nil, 404, "file.not_found")
	e.expectErr("GET", "/files/%20/content", e.u1, nil, 400, "file.invalid_id")
	// extract / processing
	x := e.ok("GET", "/files/"+a+"/extract", e.u1, nil)
	if str(x, "fileID") != a || str(x, "extractText") != "aaaa" || num(x, "extractChars") != 4 {
		t.Fatalf("extract: %v", x)
	}
	for _, k := range []string{"previewText", "extractPages", "ocrUsed"} {
		if _, ok := x[k]; !ok {
			t.Errorf("extract missing %s", k)
		}
	}
	p := e.ok("GET", "/files/"+a+"/processing", e.u1, nil)
	for _, k := range []string{"fileID", "detectedMIME", "fileCategory", "processingStatus", "processingReady", "extractStatus", "embedStatus", "previewText", "ocrUsed", "ragReady",
		"ragReason", "errorCode", "errorMessage", "extractChars", "extractPages", "chunkCount", "embedError", "canVectorize", "vectorizationReason", "startedAt", "completedAt", "updatedAt"} {
		if _, ok := p[k]; !ok {
			t.Errorf("processing missing %s", k)
		}
	}
	e.expectErr("GET", "/files/"+a+"/processing", e.u2, nil, 500, "internal.error")
	// not ready extraction -> 409
	e.DB.Model(&FileObject{}).Where("file_id = ?", c).Updates(map[string]any{"processing_ready": false, "extract_status": "processing", "processing_status": "processing"})
	e.expectErr("GET", "/files/"+c+"/extract", e.u1, nil, 409, "file.extract_not_ready")

	// statuses
	l := dataList(t, e.call("POST", "/files/processing/statuses", e.u1, map[string]any{"fileIDs": []string{a, b, "file_unknown"}}))
	if len(l) != 2 {
		t.Fatalf("statuses: %v", l)
	}
	e.expectErr("POST", "/files/processing/statuses", e.u1, map[string]any{"fileIDs": []string{}}, 400, "request.invalid_body")
	e.expectErr("POST", "/files/processing/statuses", e.u1, map[string]any{"fileIDs": make([]string, 101)}, 400, "request.invalid_body")
	e.expectErr("POST", "/files/processing/statuses", e.u1, map[string]any{"fileIDs": []string{strings.Repeat("f", 65)}}, 400, "request.invalid_body")

	// patch
	pm := e.ok("PATCH", "/files/"+a, e.u1, map[string]any{"fileName": " renamed.txt ", "ragOptOut": true})
	if str(pm, "fileName") != "renamed.txt" || pm["ragOptOut"] != true {
		t.Fatalf("patch: %v", pm)
	}
	pm = e.ok("PATCH", "/files/"+a, e.u1, map[string]any{"ragOptOut": false})
	if str(pm, "fileName") != "renamed.txt" || pm["ragOptOut"] != false {
		t.Fatalf("partial patch: %v", pm)
	}
	w = e.call("PATCH", "/files/"+a, e.u1, map[string]any{})
	testkit.Expect(t, w, 400, "request.required")
	if testkit.Envelope(t, w).ErrorMsg != "at least one of file_name or rag_opt_out is required" {
		t.Fatalf("message: %q", testkit.Envelope(t, w).ErrorMsg)
	}
	e.expectErr("PATCH", "/files/"+a, e.u1, map[string]any{"fileName": "   "}, 400, "file.invalid_name")
	if m := e.ok("PATCH", "/files/"+a, e.u1, map[string]any{"fileName": "a/b c.txt"}); str(m, "fileName") != "b_c.txt" {
		t.Fatalf("path and space name: %v", m["fileName"])
	}
	e.expectErr("PATCH", "/files/"+a, e.u2, map[string]any{"fileName": "x.txt"}, 404, "file.not_found")

	// delete
	d := e.ok("DELETE", "/files/"+b, e.u1, nil)
	if d["deleted"] != true || str(d, "fileID") != b || d["quota"] == nil {
		t.Fatalf("delete: %v", d)
	}
	e.expectErr("GET", "/files/"+b+"/content", e.u1, nil, 404, "file.not_found")
	e.expectErr("DELETE", "/files/"+b, e.u1, nil, 404, "file.not_found")
	e.expectErr("DELETE", "/files/"+a, e.u2, nil, 404, "file.not_found")
	m = e.ok("GET", "/files", e.u1, nil)
	if num(m, "total") != 2 {
		t.Fatalf("after delete: %v", m["total"])
	}
	q := m["quota"].(map[string]any)
	if num(q, "usedBytes") != 4+12 {
		t.Fatalf("quota released: %v", q)
	}
	// in-use
	e.caps.inUse[c] = true
	e.expectErr("DELETE", "/files/"+c, e.u1, nil, 409, "file.in_use")
	if e.audit.find("update_file") == nil || e.audit.find("delete_file") == nil {
		t.Fatal("audits")
	}
}

func TestSubmitEmbeddings(t *testing.T) {
	e := newEnv(t)
	a := str(e.uploadOK(e.u1, "a.txt", "text/plain", []byte("abc"))["file"].(map[string]any), "fileID")
	e.expectErr("POST", "/files/embeddings", e.u1, map[string]any{"fileIDs": []string{}}, 400, "request.invalid_body")
	e.expectErr("POST", "/files/embeddings", e.u1, map[string]any{}, 400, "request.invalid_body")
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = fmt.Sprintf("file_%d", i)
	}
	w := e.call("POST", "/files/embeddings", e.u1, map[string]any{"fileIDs": ids})
	if w.Code != 400 {
		t.Fatalf("too many: %d %s", w.Code, w.Body)
	}
	// embeddings are not configured by default
	w = e.call("POST", "/files/embeddings", e.u1, map[string]any{"fileIDs": []string{a}})
	if w.Code != 503 {
		t.Fatalf("not configured: %d %s", w.Code, w.Body)
	}
	if code := testkit.Envelope(t, w).ErrorCode; code != "embedding.service_not_configured" && code != "embedding.service_unavailable" {
		t.Fatalf("code: %s", code)
	}
	e.set.mutate(func(r *RuntimeSettings) { r.RAGAvailable = true; r.RAGAvailabilityReason = "" })
	m := e.ok("POST", "/files/embeddings", e.u1, map[string]any{"fileIDs": []string{a, "file_unknown"}})
	sub, _ := m["submittedFileIDs"].([]any)
	sk, _ := m["skipped"].([]any)
	if len(sub)+len(sk) != 2 {
		t.Fatalf("submission accounting: %v", m)
	}
	if e.audit.find("submit_file_embeddings") == nil {
		t.Fatal("audit")
	}
}

func TestFilePolicy(t *testing.T) {
	e := newEnv(t)
	m := e.ok("GET", "/runtime/chat-file-policy", e.u1, nil)
	if num(m, "maxMessageFiles") != 10 || num(m, "maxUploadFileBytes") != 20971520 || m["ragAvailable"] != false ||
		str(m, "capabilityMode") != "full_context_only" || str(m, "fileMode") != "auto" {
		t.Fatalf("policy: %v", m)
	}
	mimes := m["allowedMIMETypes"].([]any)
	for i := 1; i < len(mimes); i++ {
		if mimes[i-1].(string) > mimes[i].(string) {
			t.Fatalf("allowed mime must be sorted: %v", mimes)
		}
	}
	for _, k := range []string{"imageMaxBytes", "docMaxBytes", "effectiveImageMaxBytes", "effectiveDocMaxBytes", "fullContextMaxBytes", "fullContextMaxTokens", "fullContextPDFMaxPages", "ragAvailabilityReason"} {
		if _, ok := m[k]; !ok {
			t.Errorf("policy missing %s", k)
		}
	}
	e.set.mutate(func(r *RuntimeSettings) {
		r.RAGAvailable = true
		r.MaxUploadBytes = 1000
		r.ImageMaxBytes = 5000
		r.DocMaxBytes = 500
	})
	e.set.users["1:chat.file_mode"] = "rag"
	m = e.ok("GET", "/runtime/chat-file-policy", e.u1, nil)
	if str(m, "capabilityMode") != "full_context_and_rag" || str(m, "fileMode") != "rag" ||
		num(m, "effectiveImageMaxBytes") != 1000 || num(m, "effectiveDocMaxBytes") != 500 {
		t.Fatalf("policy 2: %v", m)
	}
	// the other user keeps the default mode
	if m = e.ok("GET", "/runtime/chat-file-policy", e.u2, nil); str(m, "fileMode") != "auto" {
		t.Fatalf("per-user: %v", m)
	}
}

func TestMessageWithFiles(t *testing.T) {
	e := newEnv(t)
	f := e.uploadOK(e.u1, "doc.txt", "text/plain", []byte("file body text"))["file"].(map[string]any)
	fid := str(f, "fileID")
	id := e.newConv(e.u1, "t", "gpt-4o")
	u, _ := pair(e.send(e.u1, id, "summarise", map[string]any{"fileIDs": []string{fid}}))
	var snaps []map[string]any
	if err := jsonUnmarshal(str(u, "attachments"), &snaps); err != nil || len(snaps) != 1 {
		t.Fatalf("attachment snapshot: %v %v", u["attachments"], err)
	}
	s := snaps[0]
	for _, k := range []string{"file_id", "kind", "file_name", "mime_type", "detected_mime", "file_category", "file_size", "processing_status", "processing_ready", "processing_error_code", "processing_error_message"} {
		if _, ok := s[k]; !ok {
			t.Errorf("snapshot missing %s: %v", k, s)
		}
	}
	if s["file_id"] != fid || s["file_name"] != "doc.txt" {
		t.Fatalf("snapshot: %v", s)
	}
	if _, has := s["duration_seconds"]; has {
		t.Fatal("duration only when > 0")
	}
	req := e.gen.requests()[0]
	last := req.Messages[len(req.Messages)-1]
	if len(last.Files) != 1 || last.Files[0].Text != "file body text" {
		t.Fatalf("file reaches the model: %+v", last.Files)
	}
	// someone else's file
	e.expectErr("POST", "/conversations/"+e.newConv(e.u2, "x", "gpt-4o")+"/messages", e.u2, sendBody("x", map[string]any{"fileIDs": []string{fid}}), 400, "file.invalid_reference")
	// not ready
	e.DB.Model(&FileObject{}).Where("file_id = ?", fid).Updates(map[string]any{"processing_ready": false, "processing_status": "processing"})
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", map[string]any{"fileIDs": []string{fid}}), 400, "file.not_ready")
	// deleted attachments disappear from snapshots
	e.DB.Model(&FileObject{}).Where("file_id = ?", fid).Updates(map[string]any{"processing_ready": true, "processing_status": "ready"})
	e.DB.Model(&Attachment{}).Where("file_id = ?", fid).Update("status", "deleted")
	rows := e.list(id)
	if str(rows[0], "attachments") != "[]" {
		t.Fatalf("deleted attachment must be excluded: %v", rows[0]["attachments"])
	}
}

func TestFullContextTooLarge(t *testing.T) {
	e := newEnv(t)
	f := e.uploadOK(e.u1, "big.txt", "text/plain", []byte(strings.Repeat("word ", 400)))["file"].(map[string]any)
	e.set.mutate(func(r *RuntimeSettings) { r.FullContextMaxTokens = 10 })
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", map[string]any{"fileIDs": []string{str(f, "fileID")}}), 400, "file.too_large_for_context")
}

func TestUploadRespectsConfiguredStorageDir(t *testing.T) {
	dir := t.TempDir()
	e := newEnv(t, testkit.WithConfig(func(c *config.Config) { c.Storage.Local.RootDir = dir }))
	e.uploadOK(e.u1, "a.txt", "text/plain", []byte("x"))
	var fo FileObject
	if err := e.DB.First(&fo).Error; err != nil {
		t.Fatal(err)
	}
	if fo.StoragePath == "" || strings.HasPrefix(fo.StoragePath, "/") || strings.Contains(fo.StoragePath, "..") {
		t.Fatalf("storage path must be relative and safe: %q", fo.StoragePath)
	}
}

func TestDefaultStorageQuota(t *testing.T) {
	if DefaultRuntimeSettings().UserStorageQuotaBytes != 104857600 {
		t.Fatalf("default quota = %d", DefaultRuntimeSettings().UserStorageQuotaBytes)
	}
	e := newEnv(t)
	e.set.mutate(func(r *RuntimeSettings) { r.UserStorageQuotaBytes = 100 })
	m := e.uploadOK(e.u1, "a.txt", "text/plain", bytes.Repeat([]byte("a"), 60))
	if q := m["quota"].(map[string]any); num(q, "quotaBytes") != 100 || num(q, "usedBytes") != 60 {
		t.Fatalf("quota: %v", q)
	}
	// 60 + 50 > 100
	testkit.Expect(t, e.upload(e.u1, "b.txt", "text/plain", bytes.Repeat([]byte("b"), 50)), 409, "quota.exceeded")
	// other user has an own allowance
	e.uploadOK(e.u2, "b.txt", "text/plain", bytes.Repeat([]byte("b"), 50))
	// a legacy record with a 0 allowance follows the default
	e.DB.Model(&StorageQuota{}).Where("user_id = ?", 1).UpdateColumn("quota_bytes", 0)
	testkit.Expect(t, e.upload(e.u1, "c.txt", "text/plain", bytes.Repeat([]byte("c"), 50)), 409, "quota.exceeded")
	e.uploadOK(e.u1, "d.txt", "text/plain", bytes.Repeat([]byte("d"), 40))
	// listing reports the default allowance
	w := e.call("GET", "/files", e.u1, nil)
	testkit.Expect(t, w, 200, "")
	if q := dataMap(t, w)["quota"].(map[string]any); num(q, "quotaBytes") != 100 || num(q, "usedBytes") != 100 {
		t.Fatalf("list quota: %v", q)
	}
	// 0 means unlimited
	e.set.mutate(func(r *RuntimeSettings) { r.UserStorageQuotaBytes = 0 })
	e.uploadOK(e.u1, "e.txt", "text/plain", bytes.Repeat([]byte("e"), 5000))
}

func TestUploadStartsQueuedAndFinishesInBackground(t *testing.T) {
	inlineProcessing.Store(false)
	defer inlineProcessing.Store(true)
	e := newEnv(t)
	m := e.uploadOK(e.u1, "q.txt", "text/plain", []byte("hello content world\nline two\n"))
	f := m["file"].(map[string]any)
	if str(f, "processingStatus") != "queued" || f["processingReady"] != false || str(f, "extractStatus") != "none" || str(f, "embedStatus") != "none" {
		t.Fatalf("upload response: %v", f)
	}
	id := str(f, "fileID")
	deadline := time.Now().Add(3 * time.Second)
	for {
		p := e.ok("GET", "/files/"+id+"/processing", e.u1, nil)
		if str(p, "processingStatus") == "ready" {
			if str(p, "extractStatus") != "ready" || num(p, "extractChars") != 28 || str(p, "ragReason") != "embedding_not_configured" && str(p, "ragReason") != "rag_disabled" {
				t.Fatalf("processed: %v", p)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never finished: %v", p)
		}
		time.Sleep(10 * time.Millisecond)
	}
	x := e.ok("GET", "/files/"+id+"/extract", e.u1, nil)
	if str(x, "extractText") != "hello content world\nline two" || str(x, "previewText") != "hello content world line two" {
		t.Fatalf("extract: %v", x)
	}
}

func TestProcessingOutcomes(t *testing.T) {
	e := newEnv(t)
	status := func(name, mime string, data []byte) map[string]any {
		return e.uploadOK(e.u1, name, mime, data)["file"].(map[string]any)
	}
	if f := status("empty.txt", "text/plain", nil); str(f, "processingStatus") != "failed" || str(f, "processingErrorCode") != "extract_failed" || f["processingReady"] != false {
		t.Fatalf("empty: %v", f)
	}
	if f := status("fake.pdf", "application/pdf", []byte("%PDF-1.4 fake")); str(f, "processingStatus") != "failed" || str(f, "fileCategory") != "pdf" {
		t.Fatalf("fake pdf: %v", f)
	}
	if f := status("real.png", "image/png", pngBytes(20)); str(f, "processingStatus") != "ready" || str(f, "extractStatus") != "none" || str(f, "fileCategory") != "image" {
		t.Fatalf("png: %v", f)
	}
	cats := map[string]string{"a.csv": "excel", "a.docx": "word", "a.md": "text", "a.json": "text"}
	for name, want := range cats {
		if f := status(name, "text/plain", []byte("x,y\n1,2\n"+name)); str(f, "fileCategory") != want {
			t.Fatalf("%s category %v want %s", name, f["fileCategory"], want)
		}
	}
	if f := status("a.md", "text/plain", []byte("# t\n")); str(f, "detectedMIME") != "text/markdown" {
		t.Fatalf("md: %v", f)
	}
	if f := e.uploadOK(e.u1, "my notes.txt", "text/plain", []byte("n"))["file"].(map[string]any); str(f, "fileName") != "my_notes.txt" {
		t.Fatalf("spaces: %v", f["fileName"])
	}
}

func TestDeletingAFileKeepsEarlierAttachmentMetadata(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	f := e.uploadOK(e.u1, "keep.txt", "text/plain", []byte("abc"))["file"].(map[string]any)
	e.send(e.u1, id, "with file", map[string]any{"fileIDs": []string{str(f, "fileID")}})
	e.ok("DELETE", "/files/"+str(f, "fileID"), e.u1, nil)
	m := e.ok("GET", "/conversations/"+id+"/messages", e.u1, nil)
	var found bool
	for _, raw := range m["results"].([]any) {
		msg := raw.(map[string]any)
		if msg["role"] == "user" && strings.Contains(str(msg, "attachments"), `"file_name":"keep.txt"`) && strings.Contains(str(msg, "attachments"), `"kind":"file"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("attachment metadata lost: %v", m)
	}
}
