package knowledgebase

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/testkit"
)

// Scenario 20.
func TestDeleteBaseKeepsFilesByDefault(t *testing.T) {
	te := newTestEnv(t)
	u := te.user(11)
	kb := te.createMine(11, "Notes")
	te.link(kb, te.addFile(11, "f1"), te.addFile(11, "f2"))
	path := api + "/knowledge-bases/mine/" + kb.PublicID

	// project defaults that point at the base disappear with it
	te.DB.Exec("CREATE TABLE chat_conversation_project_knowledge_bases (project_id integer, knowledge_base_id varchar(64), sort_order integer, PRIMARY KEY (project_id, knowledge_base_id))")
	te.DB.Exec("INSERT INTO chat_conversation_project_knowledge_bases VALUES (1, ?, 1), (2, ?, 1), (1, 'other', 2)", kb.PublicID, kb.PublicID)

	for _, q := range []string{"", "?delete_files=false", "?delete_files=TRUE", "?delete_files=1", "?delete_files=yes"} {
		k := te.createMine(11, "tmp"+q)
		te.link(k, te.addFile(11, "keep"+q))
		w := te.Do("DELETE", api+"/knowledge-bases/mine/"+k.PublicID+q, u, nil)
		testkit.Expect(t, w, 200, "")
		if strings.Contains(w.Body.String(), "deletedFileCount") || !strings.Contains(w.Body.String(), `"deleted":true`) {
			t.Fatalf("%q body: %s", q, w.Body)
		}
		if te.baseExists(k) {
			t.Fatalf("%q: base must be gone", q)
		}
		var n int64
		te.DB.Model(&fileObject{}).Where("file_id = ?", "keep"+q).Count(&n)
		if n != 1 {
			t.Fatalf("%q: file must be kept", q)
		}
	}

	testkit.Expect(t, te.Do("DELETE", path, u, nil), 200, "")
	var links, projects int64
	te.DB.Model(&KnowledgeBaseFile{}).Count(&links)
	te.DB.Table("chat_conversation_project_knowledge_bases").Count(&projects)
	if links != 0 {
		t.Fatalf("links remain: %d", links)
	}
	if projects != 1 {
		t.Fatalf("project defaults: %d remain, want only the unrelated one", projects)
	}
	var files int64
	te.DB.Model(&fileObject{}).Where("file_id IN ?", []string{"f1", "f2"}).Count(&files)
	if files != 2 {
		t.Fatal("file objects must survive")
	}
	testkit.Expect(t, te.Do("DELETE", path, u, nil), 404, "knowledge_base.not_found")
	testkit.Expect(t, te.Do("DELETE", api+"/knowledge-bases/mine/%20", u, nil), 400, "knowledge_base.invalid")
	other := te.createMine(12, "Theirs")
	testkit.Expect(t, te.Do("DELETE", api+"/knowledge-bases/mine/"+other.PublicID, u, nil), 404, "knowledge_base.not_found")
	if !te.baseExists(other) {
		t.Fatal("foreign base must survive")
	}
}

func TestDeleteBaseWithFileCleanup(t *testing.T) {
	te := newTestEnv(t)
	u := te.user(11)
	kb := te.createMine(11, "Notes")
	te.link(kb,
		te.addFile(11, "solo1"), te.addFile(11, "solo2"), te.addFile(11, "shared"), te.addFile(11, "flaky"),
		te.addFile(11, "inactive", func(o *fileObject) { o.Status = "deleted" }))
	te.files.kept["shared"] = true
	te.files.cleanErr["flaky"] = errors.New("storage unreachable /secret")

	w := te.Do("DELETE", api+"/knowledge-bases/mine/"+kb.PublicID+"?delete_files=true", u, nil)
	testkit.Expect(t, w, 200, "")
	var out struct {
		Deleted          bool
		DeletedFileCount int
	}
	testkit.Decode(t, w, &out)
	if !out.Deleted || out.DeletedFileCount != 2 {
		t.Fatalf("result: %s", w.Body)
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("cleanup errors must not leak")
	}
	if te.baseExists(kb) {
		t.Fatal("base must be deleted even though one file failed")
	}
	if len(te.files.deleted) != 2 {
		t.Fatalf("deleted: %v", te.files.deleted)
	}
	var n int64
	te.DB.Model(&fileObject{}).Where("file_id IN ?", []string{"shared", "flaky", "inactive"}).Count(&n)
	if n != 3 {
		t.Fatalf("kept files: %d", n)
	}
}

func TestDeleteBaseWithoutCleanupCapability(t *testing.T) {
	te := newTestEnv(t, func(s *setup) { s.noCleaner = true })
	u := te.user(11)
	kb := te.createMine(11, "Notes")
	te.link(kb, te.addFile(11, "f1"))
	w := te.Do("DELETE", api+"/knowledge-bases/mine/"+kb.PublicID+"?delete_files=true", u, nil)
	testkit.Expect(t, w, 503, "knowledge_base.file_cleanup_unavailable")
	if !te.baseExists(kb) {
		t.Fatal("nothing may be deleted on a refusal")
	}
	var n int64
	te.DB.Model(&KnowledgeBaseFile{}).Count(&n)
	if n != 1 {
		t.Fatal("links must remain")
	}
	// without delete_files the capability is not needed
	testkit.Expect(t, te.Do("DELETE", api+"/knowledge-bases/mine/"+kb.PublicID, u, nil), 200, "")
	// a missing base is still 404 even when cleanup is unavailable
	testkit.Expect(t, te.Do("DELETE", api+"/knowledge-bases/mine/ghost?delete_files=true", u, nil), 404, "knowledge_base.not_found")
}

func TestAdminDeleteBuiltin(t *testing.T) {
	te := newTestEnv(t)
	a := te.admin(1)
	b := te.createBuiltin("Policies", true)
	te.link(b, te.addFile(0, "p1"), te.addFile(0, "p2"))
	w := te.Do("DELETE", api+"/admin/knowledge-bases/"+b.PublicID+"?delete_files=true", a, nil)
	testkit.Expect(t, w, 200, "")
	var out struct{ DeletedFileCount int }
	testkit.Decode(t, w, &out)
	if out.DeletedFileCount != 2 || te.baseExists(b) {
		t.Fatalf("result: %s", w.Body)
	}
	got := te.aud.byAction("knowledge_base.delete_builtin")
	if len(got) != 1 || got[0].ResourceID != b.PublicID ||
		got[0].Detail["delete_files"] != true || got[0].Detail["deleted_file_count"] != 2 {
		t.Fatalf("audit: %+v", got)
	}
	testkit.Expect(t, te.Do("DELETE", api+"/admin/knowledge-bases/"+b.PublicID, a, nil), 404, "knowledge_base.not_found")
	// a refused delete is not audited
	if n := len(te.aud.byAction("knowledge_base.delete_builtin")); n != 1 {
		t.Fatalf("audit entries: %d", n)
	}
	// files are cleaned under their own owner id
	b2 := te.createBuiltin("Second", true)
	te.link(b2, te.addFile(11, "legacy"))
	testkit.Expect(t, te.Do("DELETE", api+"/admin/knowledge-bases/"+b2.PublicID+"?delete_files=true", a, nil), 200, "")
	if len(te.files.deleted) != 3 {
		t.Fatalf("deleted: %v", te.files.deleted)
	}
}

// Scenario 21.
func TestPlatformUpload(t *testing.T) {
	te := newTestEnv(t)
	a := te.admin(5)

	testkit.Expect(t, te.upload(a, "", "", nil), 400, "file.required")
	testkit.Expect(t, te.upload(a, "document", "a.txt", []byte("x")), 400, "file.required")
	testkit.Expect(t, te.Do("POST", api+"/admin/knowledge-bases/files", a, map[string]any{"a": 1}), 400, "file.required")

	w := te.upload(a, "file", "../../handbook.txt", []byte("hello world"))
	testkit.Expect(t, w, 200, "")
	var out struct{ File fileOut }
	testkit.Decode(t, w, &out)
	if out.File.FileID != "pool-upload-1" || out.File.FileName != "handbook.txt" {
		t.Fatalf("file: %+v", out.File)
	}
	if len(te.files.uploads) != 1 {
		t.Fatal("no upload recorded")
	}
	req := te.files.uploads[0]
	if req.OwnerUserID != 0 || req.OperatorUserID != 5 || req.Purpose != "knowledge_base" || string(te.files.uploaded) != "hello world" || req.FileName != "handbook.txt" {
		t.Fatalf("upload request: %+v", req)
	}
	var row fileObject
	te.DB.Where("file_id = ?", "pool-upload-1").Take(&row)
	if row.UserID != 0 || row.Purpose != "knowledge_base" {
		t.Fatalf("row: %+v", row)
	}
	got := te.aud.byAction("knowledge_base.upload_builtin_file")
	if len(got) != 1 || got[0].ResourceID != "pool-upload-1" || got[0].Detail["file_name"] != "handbook.txt" || got[0].Detail["size_bytes"] != int64(11) {
		t.Fatalf("audit: %+v", got)
	}
	// it shows up in the pool and nowhere else
	p := te.filesOf(api+"/admin/knowledge-bases/files", a)
	if p.Total != 1 || p.Results[0].FileID != "pool-upload-1" {
		t.Fatalf("pool: %+v", p)
	}
}

func TestPlatformUploadErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{ErrQuotaExceeded, 409, "quota.exceeded"},
		{ErrInvalidFile, 400, "request.invalid_file"},
		{apperr.New("file.type_blocked", "file type is not allowed"), 400, "request.invalid_file"},
		{ErrEmbeddingUnavailableForFile, 400, "file.embedding_unavailable"},
		{ErrFileTooLarge, 413, "file.too_large"},
		{errors.New("disk full at /var/lib/secret"), 500, "knowledge_base.internal"},
	}
	for _, c := range cases {
		te := newTestEnv(t)
		te.files.uploadErr = c.err
		w := te.upload(te.admin(1), "file", "a.txt", []byte("x"))
		testkit.Expect(t, w, c.status, c.code)
		if strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("leak: %s", w.Body)
		}
		if n := len(te.aud.byAction("knowledge_base.upload_builtin_file")); n != 0 {
			t.Fatalf("failed upload must not be audited")
		}
	}
	// no upload pipeline in this deployment
	te := newTestEnv(t, func(s *setup) { s.noUploader = true })
	testkit.Expect(t, te.upload(te.admin(1), "file", "a.txt", []byte("x")), 400, "knowledge_base.invalid")
}

func TestPlatformUploadSizeCap(t *testing.T) {
	te := newTestEnv(t)
	te.settings.maxBytes = 1024
	a := te.admin(1)
	// the cap is the setting plus 1 MiB of framing
	if got := te.svc.uploadLimit(t.Context()); got != 1024+(1<<20) {
		t.Fatalf("limit: %d", got)
	}
	testkit.Expect(t, te.upload(a, "file", "small.bin", make([]byte, 2000)), 200, "")
	testkit.Expect(t, te.upload(a, "file", "big.bin", make([]byte, 1024+(1<<20)+10)), 400, "file.required")
	if len(te.files.uploads) != 1 {
		t.Fatalf("oversized upload must not reach the pipeline: %d", len(te.files.uploads))
	}
	// unset or non-positive settings fall back to 20 MiB
	te.settings.maxBytes = 0
	if got := te.svc.uploadLimit(t.Context()); got != 21<<20 {
		t.Fatalf("default limit: %d", got)
	}
	te.settings.maxBytes = -5
	if got := te.svc.uploadLimit(t.Context()); got != 21<<20 {
		t.Fatalf("negative limit: %d", got)
	}
}

// Scenario 22.
func TestPlatformFileDelete(t *testing.T) {
	te := newTestEnv(t)
	a := te.admin(1)
	b := te.createBuiltin("B", true)
	linked := te.addFile(0, "linked")
	te.addFile(0, "free")
	te.addFile(11, "personal")
	te.addFile(0, "shared-elsewhere")
	te.link(b, linked)
	te.files.kept["shared-elsewhere"] = true
	del := func(id string) *httptest.ResponseRecorder {
		return te.Do("DELETE", api+"/admin/knowledge-bases/files/"+id, a, nil)
	}

	testkit.Expect(t, del("linked"), 409, "knowledge_base.platform_file_in_use")
	testkit.Expect(t, del("personal"), 409, "knowledge_base.platform_file_in_use")
	testkit.Expect(t, del("shared-elsewhere"), 409, "knowledge_base.platform_file_in_use")
	testkit.Expect(t, del("missing"), 404, "file.not_found")
	testkit.Expect(t, del("%20"), 400, "knowledge_base.invalid")
	var n int64
	te.DB.Model(&fileObject{}).Where("file_id IN ?", []string{"linked", "personal", "shared-elsewhere"}).Count(&n)
	if n != 3 {
		t.Fatal("refused deletes must leave files alone")
	}
	if len(te.aud.byAction("knowledge_base.delete_platform_file")) != 0 {
		t.Fatal("refusals are not audited")
	}

	w := del("free")
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"deleted":true`) {
		t.Fatalf("body: %s", w.Body)
	}
	// the second delete finds nothing
	testkit.Expect(t, del("free"), 404, "file.not_found")
	got := te.aud.byAction("knowledge_base.delete_platform_file")
	if len(got) != 1 || got[0].ResourceID != "free" || got[0].Detail["deleted"] != true {
		t.Fatalf("audit: %+v", got)
	}

	// after unlinking, the file can go
	testkit.Expect(t, te.Do("DELETE", api+"/admin/knowledge-bases/"+b.PublicID+"/files/linked", a, nil), 200, "")
	testkit.Expect(t, del("linked"), 200, "")

	// capability missing
	te2 := newTestEnv(t, func(s *setup) { s.noCleaner = true })
	te2.addFile(0, "free")
	testkit.Expect(t, te2.Do("DELETE", api+"/admin/knowledge-bases/files/free", te2.admin(1), nil), 503, "knowledge_base.file_cleanup_unavailable")
	testkit.Expect(t, te2.Do("DELETE", api+"/admin/knowledge-bases/files/%20", te2.admin(1), nil), 400, "knowledge_base.invalid")

	// cleaner failure
	te3 := newTestEnv(t)
	te3.addFile(0, "x")
	te3.files.cleanErr["x"] = errors.New("boom /secret")
	w = te3.Do("DELETE", api+"/admin/knowledge-bases/files/x", te3.admin(1), nil)
	testkit.Expect(t, w, 500, "knowledge_base.internal")
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("leak")
	}
}

func TestPlatformPoolListAndContent(t *testing.T) {
	te := newTestEnv(t)
	a := te.admin(1)
	te.addFile(0, "p-old", func(o *fileObject) { o.FileName = "Guide.pdf" })
	te.addFile(0, "p-new", func(o *fileObject) { o.FileName = "faq.md" })
	te.addFile(0, "p-gone", func(o *fileObject) { o.Status = "deleted" })
	te.addFile(11, "personal")
	p := te.filesOf(api+"/admin/knowledge-bases/files", a)
	if p.Total != 2 || !equalStrings(fileIDsOf(p), []string{"p-new", "p-old"}) {
		t.Fatalf("pool: %v", fileIDsOf(p))
	}
	if p = te.filesOf(api+"/admin/knowledge-bases/files?q=GUIDE", a); p.Total != 1 || p.Results[0].FileID != "p-old" {
		t.Fatalf("q: %+v", p)
	}
	w := te.Do("GET", api+"/admin/knowledge-bases/files/p-new/content", a, nil)
	testkit.Expect(t, w, 200, "")
	if w.Body.String() != "bytes-of-p-new-owner-0" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("content: %q %q", w.Body, w.Header().Get("Content-Disposition"))
	}
	// only owner 0 is readable through the pool route
	testkit.Expect(t, te.Do("GET", api+"/admin/knowledge-bases/files/personal/content", a, nil), 404, "file.not_found")
	testkit.Expect(t, te.Do("GET", api+"/admin/knowledge-bases/files/p-gone/content", a, nil), 404, "file.not_found")
	testkit.Expect(t, te.Do("GET", api+"/admin/knowledge-bases/files/%20/content", a, nil), 400, "knowledge_base.invalid")
}

// Scenario 23.
func TestSubmitEmbeddings(t *testing.T) {
	te := newTestEnv(t)
	a := te.admin(1)
	te.embed.skip = map[string]string{"done": "already_embedded"}
	path := api + "/admin/knowledge-bases/files/embeddings"

	w := te.Do("POST", path, a, map[string]any{"fileIDs": []string{"p1", "done", " p1 "}})
	testkit.Expect(t, w, 200, "")
	var out struct {
		SubmittedFileIDs []string
		Skipped          []struct{ FileID, Reason string }
	}
	testkit.Decode(t, w, &out)
	if !equalStrings(out.SubmittedFileIDs, []string{"p1"}) || len(out.Skipped) != 1 || out.Skipped[0].FileID != "done" || out.Skipped[0].Reason != "already_embedded" {
		t.Fatalf("result: %s", w.Body)
	}
	if te.embed.owners[0] != 0 || !equalStrings(te.embed.seen[0], []string{"p1", "done"}) {
		t.Fatalf("queue call: %v %v", te.embed.owners, te.embed.seen)
	}
	got := te.aud.byAction("knowledge_base.submit_platform_file_embeddings")
	if len(got) != 1 || got[0].ResourceID != "" || got[0].Detail["skipped_count"] != 1 {
		t.Fatalf("audit: %+v", got)
	}
	if req, _ := got[0].Detail["requested_file_ids"].([]string); !equalStrings(req, []string{"p1", "done"}) {
		t.Fatalf("requested: %v", got[0].Detail["requested_file_ids"])
	}
	if sub, _ := got[0].Detail["submitted_file_ids"].([]string); !equalStrings(sub, []string{"p1"}) {
		t.Fatalf("submitted: %v", got[0].Detail["submitted_file_ids"])
	}

	// nothing submitted still yields arrays, never null
	w = te.Do("POST", path, a, map[string]any{"fileIDs": []string{"done"}})
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"submittedFileIDs":[]`) {
		t.Fatalf("null array: %s", w.Body)
	}

	// 101 ids and malformed bodies
	many := make([]string, 101)
	for i := range many {
		many[i] = "id" + strings.Repeat("a", i)
	}
	testkit.Expect(t, te.Do("POST", path, a, map[string]any{"fileIDs": many}), 400, "request.invalid_body")
	testkit.Expect(t, te.Do("POST", path, a, map[string]any{"fileIDs": []string{}}), 400, "request.invalid_body")
	testkit.Expect(t, te.Do("POST", path, a, map[string]any{"fileIDs": []string{"  "}}), 400, "knowledge_base.invalid")
	testkit.Expect(t, te.Do("POST", path, a, map[string]any{}), 400, "request.invalid_body")
}

func TestSubmitEmbeddingsErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{ErrTooManyEmbeddingFiles, 400, "embedding.too_many_files"},
		{ErrEmbeddingNotConfigured, 503, "embedding.service_not_configured"},
		{ErrEmbeddingUnavailable, 503, "embedding.service_unavailable"},
		{errors.New("queue exploded /secret"), 500, "knowledge_base.internal"},
	}
	for _, c := range cases {
		te := newTestEnv(t)
		te.embed.err = c.err
		w := te.Do("POST", api+"/admin/knowledge-bases/files/embeddings", te.admin(1), map[string]any{"fileIDs": []string{"a"}})
		testkit.Expect(t, w, c.status, c.code)
		if strings.Contains(w.Body.String(), "secret") {
			t.Fatal("leak")
		}
		if len(te.aud.byAction("knowledge_base.submit_platform_file_embeddings")) != 0 {
			t.Fatal("failures are not audited")
		}
	}
	te := newTestEnv(t, func(s *setup) { s.noEmbedder = true })
	testkit.Expect(t, te.Do("POST", api+"/admin/knowledge-bases/files/embeddings", te.admin(1), map[string]any{"fileIDs": []string{"a"}}), 503, "embedding.service_not_configured")
}
