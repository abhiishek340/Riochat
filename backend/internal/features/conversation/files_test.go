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
