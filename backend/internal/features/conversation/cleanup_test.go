package conversation

import (
	"testing"
)

func fileQuotaUsed(t *testing.T, e *tenv, hdr map[string]string) float64 {
	t.Helper()
	m := e.ok("GET", "/files", hdr, nil)
	return num(m["quota"].(map[string]any), "usedBytes")
}

func TestDeleteConversationWithFiles(t *testing.T) {
	e := newEnv(t)
	shared := e.uploadOK(e.u1, "shared.txt", "text/plain", []byte("shared-content"))["file"].(map[string]any)
	only := e.uploadOK(e.u1, "only.txt", "text/plain", []byte("only-here!"))["file"].(map[string]any)
	inUse := e.uploadOK(e.u1, "avatar.txt", "text/plain", []byte("avatar"))["file"].(map[string]any)
	c1 := e.newConv(e.u1, "c1", "gpt-4o")
	c2 := e.newConv(e.u1, "c2", "gpt-4o")
	e.send(e.u1, c1, "a", map[string]any{"fileIDs": []string{str(shared, "fileID")}})
	e.send(e.u1, c1, "b", map[string]any{"fileIDs": []string{str(only, "fileID")}})
	e.send(e.u1, c1, "c", map[string]any{"fileIDs": []string{str(inUse, "fileID")}})
	e.send(e.u1, c2, "d", map[string]any{"fileIDs": []string{str(shared, "fileID")}})
	e.caps.inUse[str(inUse, "fileID")] = true
	before := fileQuotaUsed(t, e, e.u1)

	// without the flag nothing is removed
	m := e.ok("DELETE", "/conversations/"+c1+"?delete_files=false", e.u1, nil)
	if m["deleted"] != true || m["deletedFileCount"] != nil || m["quota"] != nil {
		t.Fatalf("no file deletion: %v", m)
	}
	if fileQuotaUsed(t, e, e.u1) != before {
		t.Fatal("quota unchanged")
	}
	// only the literal "true" enables it
	c3 := e.newConv(e.u1, "c3", "gpt-4o")
	e.send(e.u1, c3, "e", map[string]any{"fileIDs": []string{str(only, "fileID")}})
	m = e.ok("DELETE", "/conversations/"+c3+"?delete_files=1", e.u1, nil)
	if m["deletedFileCount"] != nil {
		t.Fatalf("1 is not true: %v", m)
	}
	// the earlier conversation c1 is already deleted; its files stay referenced by c3 -> now delete c3 with files
	c4 := e.newConv(e.u1, "c4", "gpt-4o")
	more := e.uploadOK(e.u1, "more.txt", "text/plain", []byte("more-bytes"))["file"].(map[string]any)
	e.send(e.u1, c4, "f", map[string]any{"fileIDs": []string{str(more, "fileID"), str(shared, "fileID")}})
	m = e.ok("DELETE", "/conversations/"+c4+"?delete_files=true", e.u1, nil)
	if num(m, "deletedFileCount") != 1 || m["quota"] == nil {
		t.Fatalf("only the unshared file is removed: %v", m)
	}
	e.fileExists(str(more, "fileID"), false)
	e.fileExists(str(shared, "fileID"), true)
	// c3 and c1 were deleted without the flag, so `only` is still stored until removed explicitly
	e.fileExists(str(only, "fileID"), true)
	// in-use file survives
	c5 := e.newConv(e.u1, "c5", "gpt-4o")
	e.send(e.u1, c5, "g", map[string]any{"fileIDs": []string{str(inUse, "fileID")}})
	m = e.ok("DELETE", "/conversations/"+c5+"?delete_files=true", e.u1, nil)
	if m["deletedFileCount"] != nil {
		t.Fatalf("in-use file kept: %v", m)
	}
	e.fileExists(str(inUse, "fileID"), true)
}

func TestProjectDeleteWithConversationsAndFiles(t *testing.T) {
	e := newEnv(t)
	pid := str(newProject(e, e.u1, map[string]any{"name": "p"}), "publicID")
	f := e.uploadOK(e.u1, "p.txt", "text/plain", []byte("project-file"))["file"].(map[string]any)
	c := e.newConv(e.u1, "c", "gpt-4o")
	e.send(e.u1, c, "a", map[string]any{"fileIDs": []string{str(f, "fileID")}})
	e.ok("PATCH", "/conversations/"+c+"/project", e.u1, map[string]string{"projectID": pid})
	// delete_files without delete_conversations is ignored
	m := e.ok("DELETE", "/conversation-projects/"+pid+"?delete_files=true", e.u1, nil)
	if m["deletedFileCount"] != nil {
		t.Fatalf("ignored: %v", m)
	}
	e.fileExists(str(f, "fileID"), true)
	e.ok("GET", "/conversations/"+c, e.u1, nil)
	// with both flags
	pid2 := str(newProject(e, e.u1, map[string]any{"name": "p2"}), "publicID")
	e.ok("PATCH", "/conversations/"+c+"/project", e.u1, map[string]string{"projectID": pid2})
	m = e.ok("DELETE", "/conversation-projects/"+pid2+"?delete_conversations=true&delete_files=true", e.u1, nil)
	if num(m, "deletedFileCount") != 1 || m["quota"] == nil {
		t.Fatalf("project delete with files: %v", m)
	}
	e.fileExists(str(f, "fileID"), false)
}

func (e *tenv) fileExists(fileID string, want bool) {
	e.t.Helper()
	w := e.call("GET", "/files/"+fileID+"/content", e.u1, nil)
	if want && w.Code != 200 {
		e.t.Fatalf("file %s should exist: %d", fileID, w.Code)
	}
	if !want && w.Code != 404 {
		e.t.Fatalf("file %s should be gone: %d", fileID, w.Code)
	}
}
