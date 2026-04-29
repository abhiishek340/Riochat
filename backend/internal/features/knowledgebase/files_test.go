package knowledgebase

import (
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func (te *tenv) filesOf(path string, hdr map[string]string) filePage {
	te.t.Helper()
	w := te.Do("GET", path, hdr, nil)
	testkit.Expect(te.t, w, 200, "")
	var p filePage
	testkit.Decode(te.t, w, &p)
	return p
}

func fileIDsOf(p filePage) []string {
	out := make([]string, len(p.Results))
	for i, f := range p.Results {
		out[i] = f.FileID
	}
	return out
}

// Scenario 11.
func TestAddFilesIsOrderedAndIdempotent(t *testing.T) {
	te := newTestEnv(t)
	u := te.user(11)
	kb := te.createMine(11, "Notes")
	te.addFile(11, "f1")
	te.addFile(11, "f2")
	te.addFile(11, "f3")
	add := api + "/knowledge-bases/mine/" + kb.PublicID + "/files"

	te.Clock.Advance(time.Minute)
	u = te.user(11)
	w := te.Do("POST", add, u, map[string]any{"fileIDs": []string{"f2", "f1"}})
	testkit.Expect(t, w, 200, "")
	var ok struct{ Updated bool }
	testkit.Decode(t, w, &ok)
	if !ok.Updated {
		t.Fatalf("body: %s", w.Body)
	}
	row := te.reload(kb)
	if row.Revision != 2 {
		t.Fatalf("revision: %d", row.Revision)
	}
	if !row.UpdatedAt.After(kb.UpdatedAt) {
		t.Fatal("updated_at must move when files change")
	}
	var links []KnowledgeBaseFile
	te.DB.Where("knowledge_base_id = ?", row.ID).Order("sort_order").Find(&links)
	if len(links) != 2 || links[0].SortOrder != 1 || links[1].SortOrder != 2 || links[0].AddedByUserID != 11 {
		t.Fatalf("links: %+v", links)
	}

	p := te.filesOf(api+"/knowledge-bases/"+kb.PublicID+"/files", u)
	if p.Total != 2 || !equalStrings(fileIDsOf(p), []string{"f2", "f1"}) {
		t.Fatalf("files: %v", fileIDsOf(p))
	}
	if got := te.listKB(api+"/knowledge-bases/mine", u).Results[0]; got.FileCount != 2 {
		t.Fatalf("fileCount: %+v", got)
	}

	// repeating the call (and mixing known with new files) is idempotent
	testkit.Expect(t, te.Do("POST", add, u, map[string]any{"fileIDs": []string{"f2", "f1"}}), 200, "")
	if rev := te.reload(kb).Revision; rev != 2 {
		t.Fatalf("revision after repeat: %d", rev)
	}
	testkit.Expect(t, te.Do("POST", add, u, map[string]any{"fileIDs": []string{"f1", "f3"}}), 200, "")
	if rev := te.reload(kb).Revision; rev != 3 {
		t.Fatalf("revision after partial add: %d", rev)
	}
	p = te.filesOf(api+"/knowledge-bases/"+kb.PublicID+"/files", u)
	if !equalStrings(fileIDsOf(p), []string{"f2", "f1", "f3"}) {
		t.Fatalf("order after partial add: %v", fileIDsOf(p))
	}
}

// Scenario 12.
func TestAddFilesIsAllOrNothing(t *testing.T) {
	te := newTestEnv(t)
	u := te.user(11)
	kb := te.createMine(11, "Notes")
	te.addFile(11, "mine1")
	te.addFile(12, "theirs")
	te.addFile(11, "inactive", func(o *fileObject) { o.Status = "deleted" })
	pool := te.addFile(0, "pool1")
	soft := te.addFile(11, "soft")
	te.DB.Delete(&soft)
	_ = pool
	add := api + "/knowledge-bases/mine/" + kb.PublicID + "/files"

	for _, bad := range []string{"theirs", "inactive", "missing", "pool1", "soft"} {
		testkit.Expect(t, te.Do("POST", add, u, map[string]any{"fileIDs": []string{"mine1", bad}}), 404, "knowledge_base.not_found")
		var n int64
		te.DB.Model(&KnowledgeBaseFile{}).Count(&n)
		if n != 0 || te.reload(kb).Revision != 1 {
			t.Fatalf("%s: partial add (%d links, revision %d)", bad, n, te.reload(kb).Revision)
		}
	}
	// another user's base is not addressable at all
	other := te.createMine(12, "Other")
	testkit.Expect(t, te.Do("POST", api+"/knowledge-bases/mine/"+other.PublicID+"/files", u, map[string]any{"fileIDs": []string{"mine1"}}), 404, "knowledge_base.not_found")
	// builtin bases are not reachable through the personal route
	b := te.createBuiltin("B", true)
	testkit.Expect(t, te.Do("POST", api+"/knowledge-bases/mine/"+b.PublicID+"/files", u, map[string]any{"fileIDs": []string{"mine1"}}), 404, "knowledge_base.not_found")
}

// Scenario 13.
func TestAddFilesValidation(t *testing.T) {
	te := newTestEnv(t)
	u := te.user(11)
	kb := te.createMine(11, "Notes")
	te.addFile(11, "a")
	te.addFile(11, "b")
	add := api + "/knowledge-bases/mine/" + kb.PublicID + "/files"
	post := func(ids []string) (int, string) {
		w := te.Do("POST", add, u, map[string]any{"fileIDs": ids})
		return w.Code, testkit.Envelope(t, w).ErrorCode
	}

	for _, ids := range [][]string{{"a", "a"}, {"a", " a "}, {"a", " "}, {"  "}} {
		if code, ec := post(ids); code != 400 || ec != "knowledge_base.invalid" {
			t.Errorf("%q: %d %s", ids, code, ec)
		}
	}
	if code, ec := post([]string{}); code != 400 || ec != "request.invalid_body" {
		t.Errorf("empty: %d %s", code, ec)
	}
	if code, ec := post([]string{""}); code != 400 || ec != "request.invalid_body" {
		t.Errorf("empty id: %d %s", code, ec)
	}
	many := make([]string, 101)
	for i := range many {
		many[i] = "f" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	if code, ec := post(many); code != 400 || ec != "request.invalid_body" {
		t.Errorf("101 ids: %d %s", code, ec)
	}
	if code, ec := post([]string{strings.Repeat("z", 65)}); code != 400 || ec != "request.invalid_body" {
		t.Errorf("65 chars: %d %s", code, ec)
	}
	testkit.Expect(t, te.Do("POST", add, u, map[string]any{}), 400, "request.invalid_body")
	testkit.Expect(t, te.Do("POST", add, u, ""), 400, "request.invalid_body")
	// surrounding blanks around a distinct id are tolerated... only when no
	// id was dropped; the count check is on the original input
	if code, _ := post([]string{" a ", "b"}); code != 200 {
		t.Errorf("padded distinct ids: %d", code)
	}
	// the service enforces the 100 limit itself
	if err := te.svc.addFiles(t.Context(), personalOf(11), 11, kb.PublicID, many); err == nil {
		t.Error("service must reject more than 100 ids")
	}
}

// Scenario 14.
func TestBuiltinAcceptsOnlyPoolFiles(t *testing.T) {
	te := newTestEnv(t)
	a := te.admin(1)
	b := te.createBuiltin("Policies", true)
	te.addFile(11, "personal")
	te.addFile(0, "pool1")
	te.addFile(0, "pool2")
	add := api + "/admin/knowledge-bases/" + b.PublicID + "/files"

	testkit.Expect(t, te.Do("POST", add, a, map[string]any{"fileIDs": []string{"personal"}}), 404, "knowledge_base.not_found")
	testkit.Expect(t, te.Do("POST", add, a, map[string]any{"fileIDs": []string{"pool1", "personal"}}), 404, "knowledge_base.not_found")
	testkit.Expect(t, te.Do("POST", add, a, map[string]any{"fileIDs": []string{"pool1", "pool2"}}), 200, "")
	if te.reload(b).Revision != 2 {
		t.Fatalf("revision: %d", te.reload(b).Revision)
	}
	got := te.aud.byAction("knowledge_base.add_files_builtin")
	if len(got) != 1 || got[0].ResourceID != b.PublicID || got[0].Detail["count"] != 2 {
		t.Fatalf("audit: %+v", got)
	}
	// a platform file can never go into a personal base
	kb := te.createMine(11, "Mine")
	testkit.Expect(t, te.Do("POST", api+"/knowledge-bases/mine/"+kb.PublicID+"/files", te.user(11), map[string]any{"fileIDs": []string{"pool1"}}), 404, "knowledge_base.not_found")
	// admin add to a personal base is 404
	testkit.Expect(t, te.Do("POST", api+"/admin/knowledge-bases/"+kb.PublicID+"/files", a, map[string]any{"fileIDs": []string{"pool1"}}), 404, "knowledge_base.not_found")

	// users see pool files through enabled builtin bases
	p := te.filesOf(api+"/knowledge-bases/"+b.PublicID+"/files", te.user(99))
	if p.Total != 2 {
		t.Fatalf("user sees builtin files: %+v", p)
	}
	// but cannot add to builtin bases
	testkit.Expect(t, te.Do("POST", api+"/knowledge-bases/mine/"+b.PublicID+"/files", te.user(99), map[string]any{"fileIDs": []string{"pool1"}}), 404, "knowledge_base.not_found")
}

// Scenario 15.
func TestRemoveFile(t *testing.T) {
	te := newTestEnv(t)
	u := te.user(11)
	kb := te.createMine(11, "Notes")
	te.addFile(11, "f1")
	te.addFile(11, "f2")
	te.Do("POST", api+"/knowledge-bases/mine/"+kb.PublicID+"/files", u, map[string]any{"fileIDs": []string{"f1", "f2"}})
	rev := te.reload(kb).Revision

	path := api + "/knowledge-bases/mine/" + kb.PublicID + "/files/f1"
	w := te.Do("DELETE", path, u, nil)
	testkit.Expect(t, w, 200, "")
	var ok struct{ Updated bool }
	testkit.Decode(t, w, &ok)
	if !ok.Updated || te.reload(kb).Revision != rev+1 {
		t.Fatalf("revision %d want %d", te.reload(kb).Revision, rev+1)
	}
	testkit.Expect(t, te.Do("DELETE", path, u, nil), 404, "knowledge_base.not_found")
	if te.reload(kb).Revision != rev+1 {
		t.Fatal("failed removal must not bump the revision")
	}
	// the file object itself is kept and can be added again
	var n int64
	te.DB.Model(&fileObject{}).Where("file_id = ?", "f1").Count(&n)
	if n != 1 {
		t.Fatal("file object must remain")
	}
	if got := te.filesOf(api+"/knowledge-bases/"+kb.PublicID+"/files", u); !equalStrings(fileIDsOf(got), []string{"f2"}) {
		t.Fatalf("files: %v", fileIDsOf(got))
	}
	// unknown, inactive, foreign and blank ids
	testkit.Expect(t, te.Do("DELETE", api+"/knowledge-bases/mine/"+kb.PublicID+"/files/ghost", u, nil), 404, "knowledge_base.not_found")
	testkit.Expect(t, te.Do("DELETE", api+"/knowledge-bases/mine/"+kb.PublicID+"/files/%20", u, nil), 400, "knowledge_base.invalid")
	other := te.createMine(12, "Other")
	testkit.Expect(t, te.Do("DELETE", api+"/knowledge-bases/mine/"+other.PublicID+"/files/f2", u, nil), 404, "knowledge_base.not_found")
	te.DB.Model(&fileObject{}).Where("file_id = ?", "f2").Update("status", "deleted")
	testkit.Expect(t, te.Do("DELETE", api+"/knowledge-bases/mine/"+kb.PublicID+"/files/f2", u, nil), 404, "knowledge_base.not_found")

	// admin removal is audited
	b := te.createBuiltin("B", true)
	te.addFile(0, "p1")
	te.Do("POST", api+"/admin/knowledge-bases/"+b.PublicID+"/files", te.admin(1), map[string]any{"fileIDs": []string{"p1"}})
	testkit.Expect(t, te.Do("DELETE", api+"/admin/knowledge-bases/"+b.PublicID+"/files/p1", te.admin(1), nil), 200, "")
	got := te.aud.byAction("knowledge_base.remove_file_builtin")
	if len(got) != 1 || got[0].ResourceID != b.PublicID || got[0].Detail["file_id"] != "p1" {
		t.Fatalf("audit: %+v", got)
	}
	testkit.Expect(t, te.Do("DELETE", api+"/admin/knowledge-bases/"+b.PublicID+"/files/p1", te.admin(1), nil), 404, "knowledge_base.not_found")
}

// Scenario 16.
func TestContentIsAttachmentAndUsesFileOwner(t *testing.T) {
	te := newTestEnv(t)
	u := te.user(11)
	kb := te.createMine(11, "Notes")
	f1 := te.addFile(11, "f1", func(o *fileObject) { o.FileName = "report final.txt"; o.MimeType = "text/plain" })
	te.addFile(11, "loose")
	te.link(kb, f1)

	w := te.Do("GET", api+"/knowledge-bases/"+kb.PublicID+"/files/f1/content", u, nil)
	testkit.Expect(t, w, 200, "")
	if w.Body.String() != "bytes-of-f1-owner-11" {
		t.Fatalf("body: %q", w.Body)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") || !strings.Contains(cd, "final.txt") {
		t.Fatalf("disposition: %q", cd)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content type: %q", ct)
	}
	if w.Header().Get("Content-Length") != "20" {
		t.Fatalf("content length: %q", w.Header().Get("Content-Length"))
	}
	// a file that is not linked is not reachable, even if it exists
	testkit.Expect(t, te.Do("GET", api+"/knowledge-bases/"+kb.PublicID+"/files/loose/content", u, nil), 404, "knowledge_base.not_found")
	testkit.Expect(t, te.Do("GET", api+"/knowledge-bases/"+kb.PublicID+"/files/%20/content", u, nil), 400, "knowledge_base.invalid")
	// another user cannot read it
	testkit.Expect(t, te.Do("GET", api+"/knowledge-bases/"+kb.PublicID+"/files/f1/content", te.user(12), nil), 404, "knowledge_base.not_found")
	// inactive files drop out of content access
	te.DB.Model(&fileObject{}).Where("file_id = ?", "f1").Update("status", "deleted")
	testkit.Expect(t, te.Do("GET", api+"/knowledge-bases/"+kb.PublicID+"/files/f1/content", u, nil), 404, "knowledge_base.not_found")

	// builtin bases stream the platform file on behalf of owner 0, for any user
	b := te.createBuiltin("B", true)
	pool := te.addFile(0, "p1")
	te.link(b, pool)
	w = te.Do("GET", api+"/knowledge-bases/"+b.PublicID+"/files/p1/content", te.user(77), nil)
	testkit.Expect(t, w, 200, "")
	if w.Body.String() != "bytes-of-p1-owner-0" {
		t.Fatalf("builtin body: %q", w.Body)
	}
	if got := te.files.readOwners[len(te.files.readOwners)-1]; got != 0 {
		t.Fatalf("read as owner %d", got)
	}
	// the admin per-base route
	testkit.Expect(t, te.Do("GET", api+"/admin/knowledge-bases/"+b.PublicID+"/files/p1/content", te.admin(1), nil), 200, "")
	// and refuses a personal base
	testkit.Expect(t, te.Do("GET", api+"/admin/knowledge-bases/"+kb.PublicID+"/files/f1/content", te.admin(1), nil), 404, "knowledge_base.not_found")
	// disabling the builtin base hides the content from users but not admins
	te.Do("PATCH", api+"/admin/knowledge-bases/"+b.PublicID, te.admin(1), map[string]any{"enabled": false})
	testkit.Expect(t, te.Do("GET", api+"/knowledge-bases/"+b.PublicID+"/files/p1/content", te.user(77), nil), 404, "knowledge_base.not_found")
	testkit.Expect(t, te.Do("GET", api+"/admin/knowledge-bases/"+b.PublicID+"/files/p1/content", te.admin(1), nil), 200, "")
}

func TestContentUnavailableAndErrors(t *testing.T) {
	te := newTestEnv(t, func(s *setup) { s.noReader = true })
	kb := te.createMine(11, "Notes")
	te.link(kb, te.addFile(11, "f1"))
	testkit.Expect(t, te.Do("GET", api+"/knowledge-bases/"+kb.PublicID+"/files/f1/content", te.user(11), nil), 500, "knowledge_base.internal")

	te2 := newTestEnv(t)
	kb2 := te2.createMine(11, "Notes")
	te2.link(kb2, te2.addFile(11, "f1"))
	te2.files.readErr = ErrFileNotFound
	testkit.Expect(t, te2.Do("GET", api+"/knowledge-bases/"+kb2.PublicID+"/files/f1/content", te2.user(11), nil), 404, "file.not_found")
	te2.files.readErr = testErr("disk exploded: /var/secret/path")
	w := te2.Do("GET", api+"/knowledge-bases/"+kb2.PublicID+"/files/f1/content", te2.user(11), nil)
	testkit.Expect(t, w, 500, "knowledge_base.internal")
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("leak: %s", w.Body)
	}
}

type testErr string

func (e testErr) Error() string { return string(e) }

// Scenario 17.
func TestStatuses(t *testing.T) {
	te := newTestEnv(t)
	u := te.user(11)
	kb := te.createMine(11, "Notes")
	pending := te.addFile(11, "f-pending", func(o *fileObject) { o.ProcessingStatus = "queued"; o.ExtractStatus = "processing" })
	te.addFile(11, "not-in-base")
	te.link(kb, pending)
	path := api + "/knowledge-bases/" + kb.PublicID + "/files/processing/statuses"

	w := te.Do("POST", path, u, map[string]any{"fileIDs": []string{"f-pending", "not-in-base", " f-pending "}})
	testkit.Expect(t, w, 200, "")
	var sts []struct {
		FileID           string
		ProcessingStatus string
		Processing       bool
		ExtractStatus    string
		EmbedStatus      string
		CanVectorize     bool
	}
	testkit.Decode(t, w, &sts)
	if len(sts) != 1 || sts[0].FileID != "f-pending" || sts[0].ProcessingStatus != "queued" ||
		!sts[0].Processing || sts[0].ExtractStatus != "processing" || sts[0].EmbedStatus != "idle" || !sts[0].CanVectorize {
		t.Fatalf("statuses: %+v", sts)
	}

	// nothing linked: an empty array, not null
	w = te.Do("POST", path, u, map[string]any{"fileIDs": []string{"zzz"}})
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Fatalf("body: %s", w.Body)
	}

	testkit.Expect(t, te.Do("POST", path, u, map[string]any{"fileIDs": []string{}}), 400, "request.invalid_body")
	testkit.Expect(t, te.Do("POST", path, u, map[string]any{}), 400, "request.invalid_body")
	testkit.Expect(t, te.Do("POST", path, u, map[string]any{"fileIDs": []string{"  ", " "}}), 400, "knowledge_base.invalid")
	many := make([]string, 101)
	for i := range many {
		many[i] = "id" + strings.Repeat("a", i)
	}
	testkit.Expect(t, te.Do("POST", path, u, map[string]any{"fileIDs": many}), 400, "request.invalid_body")
	// service-level limit after dedup
	if _, err := te.svc.statuses(t.Context(), visibleTo(11), kb.PublicID, many); err == nil {
		t.Fatal("service must reject 101 ids")
	}
	// visibility rules are the same as for the other reads
	testkit.Expect(t, te.Do("POST", path, te.user(12), map[string]any{"fileIDs": []string{"f-pending"}}), 404, "knowledge_base.not_found")
	testkit.Expect(t, te.Do("POST", api+"/knowledge-bases/nope/files/processing/statuses", u, map[string]any{"fileIDs": []string{"a"}}), 404, "knowledge_base.not_found")
}

// Scenario 18.
func TestSnapshot(t *testing.T) {
	te := newTestEnv(t)
	u := te.user(11)
	kb := te.createMine(11, "Notes")
	ready := te.addFile(11, "ready", ragReady)
	busy := te.addFile(11, "busy", func(o *fileObject) { o.EmbedStatus = "processing" })
	te.link(kb, ready, busy)
	path := api + "/knowledge-bases/" + kb.PublicID + "/files/processing/snapshot"

	type snap struct {
		KnowledgeBase kbOut
		Statuses      []struct{ FileID string }
	}
	for _, body := range []any{map[string]any{}, map[string]any{"fileIDs": []string{}}, nil, ""} {
		w := te.Do("POST", path, u, body)
		testkit.Expect(t, w, 200, "")
		var s snap
		testkit.Decode(t, w, &s)
		if s.KnowledgeBase.FileCount != 2 || s.KnowledgeBase.ReadyFileCount != 1 || s.KnowledgeBase.ProcessingFileCount != 1 {
			t.Fatalf("counts: %+v", s.KnowledgeBase)
		}
		if s.Statuses == nil || len(s.Statuses) != 0 {
			t.Fatalf("statuses must be an empty array: %s", w.Body)
		}
	}
	w := te.Do("POST", path, u, map[string]any{"fileIDs": []string{"busy", "nope"}})
	testkit.Expect(t, w, 200, "")
	var s snap
	testkit.Decode(t, w, &s)
	if len(s.Statuses) != 1 || s.Statuses[0].FileID != "busy" {
		t.Fatalf("statuses: %+v", s.Statuses)
	}
	testkit.Expect(t, te.Do("POST", path, u, map[string]any{"fileIDs": []string{"", ""}}), 400, "request.invalid_body")
	testkit.Expect(t, te.Do("POST", path, u, map[string]any{"fileIDs": []string{" ", "  "}}), 400, "knowledge_base.invalid")
	testkit.Expect(t, te.Do("POST", path, u, `{bad json`), 400, "request.invalid_body")
	// the admin variant works on builtin bases only
	b := te.createBuiltin("B", true)
	testkit.Expect(t, te.Do("POST", api+"/admin/knowledge-bases/"+b.PublicID+"/files/processing/snapshot", te.admin(1), map[string]any{}), 200, "")
	testkit.Expect(t, te.Do("POST", api+"/admin/knowledge-bases/"+b.PublicID+"/files/processing/statuses", te.admin(1), map[string]any{"fileIDs": []string{"x"}}), 200, "")
	testkit.Expect(t, te.Do("POST", api+"/admin/knowledge-bases/"+kb.PublicID+"/files/processing/snapshot", te.admin(1), map[string]any{}), 404, "knowledge_base.not_found")
	testkit.Expect(t, te.Do("POST", api+"/admin/knowledge-bases/"+kb.PublicID+"/files/processing/statuses", te.admin(1), map[string]any{"fileIDs": []string{"x"}}), 404, "knowledge_base.not_found")
}

// Scenario 19.
func TestLiveCounts(t *testing.T) {
	te := newTestEnv(t)
	kb := te.createMine(11, "Notes")
	te.link(kb,
		te.addFile(11, "ready", ragReady),
		te.addFile(11, "embedding", func(o *fileObject) { o.EmbedStatus = "processing"; o.ProcessingReady = true }),
		te.addFile(11, "plain"),
	)
	get := func() kbOut {
		w := te.Do("GET", api+"/knowledge-bases/"+kb.PublicID, te.user(11), nil)
		testkit.Expect(t, w, 200, "")
		var out struct{ KnowledgeBase kbOut }
		testkit.Decode(t, w, &out)
		return out.KnowledgeBase
	}
	if c := get(); c.FileCount != 3 || c.ReadyFileCount != 1 || c.ProcessingFileCount != 1 {
		t.Fatalf("counts: %+v", c)
	}

	// each processing_status value that means "in progress"
	for _, st := range []string{"uploaded", "queued", "extracting", "embedding"} {
		te.DB.Model(&fileObject{}).Where("file_id = ?", "plain").Update("processing_status", st)
		if c := get(); c.ProcessingFileCount != 2 {
			t.Errorf("status %s must count as processing: %+v", st, c)
		}
	}
	te.DB.Model(&fileObject{}).Where("file_id = ?", "plain").Updates(map[string]any{"processing_status": "ready", "extract_status": "processing"})
	if c := get(); c.ProcessingFileCount != 2 {
		t.Errorf("extract_status processing: %+v", c)
	}
	te.DB.Model(&fileObject{}).Where("file_id = ?", "plain").Update("extract_status", "ready")

	// ready requires every condition
	for name, upd := range map[string]map[string]any{
		"opt-out":      {"rag_opt_out": true},
		"no chunks":    {"chunk_count": 0},
		"not ready":    {"processing_ready": false},
		"embed failed": {"embed_status": "failed"},
	} {
		te.DB.Model(&fileObject{}).Where("file_id = ?", "ready").Updates(upd)
		if c := get(); c.ReadyFileCount != 0 {
			t.Errorf("%s must not be ready: %+v", name, c)
		}
		te.DB.Model(&fileObject{}).Where("file_id = ?", "ready").Updates(map[string]any{
			"rag_opt_out": false, "chunk_count": 2, "processing_ready": true, "embed_status": "ready"})
		if c := get(); c.ReadyFileCount != 1 {
			t.Errorf("restored: %+v", c)
		}
	}

	// inactive and soft-deleted files are not counted, and no write happens
	te.DB.Model(&fileObject{}).Where("file_id = ?", "plain").Update("status", "deleted")
	if c := get(); c.FileCount != 2 {
		t.Errorf("inactive: %+v", c)
	}
	var emb fileObject
	te.DB.Where("file_id = ?", "embedding").Take(&emb)
	te.DB.Delete(&emb)
	if c := get(); c.FileCount != 1 || c.ProcessingFileCount != 0 {
		t.Errorf("soft-deleted: %+v", c)
	}
	before := te.reload(kb)
	get()
	if after := te.reload(kb); after.Revision != before.Revision || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatal("reads must not write")
	}
	// a base without links reports zeros
	empty := te.createMine(11, "Empty")
	if empty.FileCount != 0 || empty.ReadyFileCount != 0 || empty.ProcessingFileCount != 0 {
		t.Fatalf("empty: %+v", empty)
	}
}

func TestFileViewFields(t *testing.T) {
	te := newTestEnv(t)
	kb := te.createMine(11, "Notes")
	te.link(kb,
		te.addFile(11, "plain", func(o *fileObject) { o.EmbedError = "boom"; o.ProcessingStatus = "embedding"; o.RagOptOut = true }),
		te.addFile(11, "done", ragReady),
	)
	w := te.Do("GET", api+"/knowledge-bases/"+kb.PublicID+"/files", te.user(11), nil)
	testkit.Expect(t, w, 200, "")
	var raw struct {
		Results []map[string]any
	}
	testkit.Decode(t, w, &raw)
	first := raw.Results[0]
	for _, key := range []string{"fileID", "fileName", "mimeType", "detectedMIME", "fileCategory", "sizeBytes", "processingStatus",
		"processing", "processingReady", "extractStatus", "embedStatus", "embedError", "chunkCount", "ragOptOut",
		"canVectorize", "vectorizationReason", "createdAt", "updatedAt"} {
		if _, ok := first[key]; !ok {
			t.Errorf("missing field %s in %v", key, first)
		}
	}
	if len(first) != 18 {
		t.Errorf("unexpected fields: %v", first)
	}
	if first["processing"] != true || first["embedError"] != "boom" || first["ragOptOut"] != true || first["canVectorize"] != true || first["vectorizationReason"] != "ok" {
		t.Errorf("values: %v", first)
	}
	if second := raw.Results[1]; second["canVectorize"] != false || second["vectorizationReason"] != "already_embedded" {
		t.Errorf("second: %v", second)
	}

	// without an embedding subsystem every file reports false and no reason
	te2 := newTestEnv(t, func(s *setup) { s.noVectorizer = true })
	kb2 := te2.createMine(11, "Notes")
	te2.link(kb2, te2.addFile(11, "plain"))
	w = te2.Do("GET", api+"/knowledge-bases/"+kb2.PublicID+"/files", te2.user(11), nil)
	var p filePage
	testkit.Decode(t, w, &p)
	if p.Results[0].CanVectorize || p.Results[0].VectorizationReason != "" {
		t.Fatalf("no vectorizer: %+v", p.Results[0])
	}
}

func TestFileListSoftDeletedAndInactiveHidden(t *testing.T) {
	te := newTestEnv(t)
	kb := te.createMine(11, "Notes")
	keep := te.addFile(11, "keep")
	inactive := te.addFile(11, "inactive", func(o *fileObject) { o.Status = "deleted" })
	soft := te.addFile(11, "soft")
	te.link(kb, keep, inactive, soft)
	te.DB.Delete(&soft)
	p := te.filesOf(api+"/knowledge-bases/"+kb.PublicID+"/files", te.user(11))
	if p.Total != 1 || p.Results[0].FileID != "keep" {
		t.Fatalf("files: %+v", p)
	}
	// pagination of files
	for _, id := range []string{"x1", "x2", "x3"} {
		te.link(kb, te.addFile(11, id))
	}
	p = te.filesOf(api+"/knowledge-bases/"+kb.PublicID+"/files?page_size=2&page=2", te.user(11))
	if p.Total != 4 || len(p.Results) != 2 {
		t.Fatalf("page 2: %+v", p)
	}
}

func TestAvailableFiles(t *testing.T) {
	te := newTestEnv(t)
	u := te.user(11)
	kb := te.createMine(11, "Notes")
	in := te.addFile(11, "in-base", func(o *fileObject) { o.FileName = "report.pdf" })
	te.addFile(11, "old", func(o *fileObject) { o.FileName = "Annual REPORT.pdf" })
	te.addFile(11, "new", func(o *fileObject) { o.FileName = "slides_100%.pdf" })
	te.addFile(11, "gone", func(o *fileObject) { o.Status = "deleted" })
	te.addFile(12, "theirs")
	te.addFile(0, "pool")
	te.link(kb, in)
	path := api + "/knowledge-bases/mine/" + kb.PublicID + "/available-files"

	p := te.filesOf(path, u)
	if p.Total != 2 || !equalStrings(fileIDsOf(p), []string{"new", "old"}) {
		t.Fatalf("available (newest first): %v", fileIDsOf(p))
	}
	if p = te.filesOf(path+"?q=report", u); !equalStrings(fileIDsOf(p), []string{"old"}) {
		t.Fatalf("q: %v", fileIDsOf(p))
	}
	if p = te.filesOf(path+"?q=%20REPORT%20", u); p.Total != 1 {
		t.Fatalf("q is trimmed and case-insensitive: %+v", p)
	}
	if p = te.filesOf(path+"?q=%25", u); p.Total != 1 || p.Results[0].FileID != "new" {
		t.Fatalf("literal percent: %+v", p)
	}
	if p = te.filesOf(path+"?page_size=1", u); p.Total != 2 || len(p.Results) != 1 {
		t.Fatalf("paged: %+v", p)
	}
	testkit.Expect(t, te.Do("GET", api+"/knowledge-bases/mine/"+kb.PublicID+"/available-files", te.user(12), nil), 404, "knowledge_base.not_found")

	// admin: pool files not yet in the builtin base
	b := te.createBuiltin("B", true)
	pa := te.addFile(0, "pool-a", func(o *fileObject) { o.FileName = "handbook.md" })
	pb := te.addFile(0, "pool-b")
	te.link(b, pa)
	_ = pb
	p = te.filesOf(api+"/admin/knowledge-bases/"+b.PublicID+"/available-files", te.admin(1))
	if p.Total != 2 || !equalStrings(fileIDsOf(p), []string{"pool-b", "pool"}) {
		t.Fatalf("admin available: %v", fileIDsOf(p))
	}
	testkit.Expect(t, te.Do("GET", api+"/admin/knowledge-bases/"+kb.PublicID+"/available-files", te.admin(1), nil), 404, "knowledge_base.not_found")
}
