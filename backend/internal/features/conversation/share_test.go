package conversation

import (
	"fmt"
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func shareOf(t *testing.T, m map[string]any) string {
	t.Helper()
	id := str(m, "shareID")
	if len(id) != 32 {
		t.Fatalf("share id: %v", m)
	}
	return id
}

func TestShareLifecycle(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "Shared Title", "gpt-4o")
	// no share yet
	m := e.ok("GET", "/conversations/"+id+"/share", e.u1, nil)
	if str(m, "status") != "none" || str(m, "shareID") != "" || num(m, "messageCount") != 0 {
		t.Fatalf("none: %v", m)
	}
	// empty conversation cannot be shared
	e.expectErr("POST", "/conversations/"+id+"/share", e.u1, nil, 400, "conversation_share.invalid")
	_, a1 := pair(e.send(e.u1, id, "q1", nil))
	s1 := e.ok("POST", "/conversations/"+id+"/share", e.u1, nil)
	if str(s1, "status") != "active" || str(s1, "titleSnapshot") != "Shared Title" || str(s1, "modelSnapshot") != "plat-model" && str(s1, "modelSnapshot") != "gpt-4o" || num(s1, "messageCount") != 2 {
		t.Fatalf("create: %v", s1)
	}
	sid1 := shareOf(t, s1)
	// the conversation shows it
	c := e.ok("GET", "/conversations/"+id, e.u1, nil)
	if str(c, "shareStatus") != "active" || str(c, "shareID") != sid1 || c["sharedAt"] == nil {
		t.Fatalf("conversation share summary: %v", c)
	}
	// a second create revokes the first
	s2 := e.ok("POST", "/conversations/"+id+"/share", e.u1, map[string]any{"defaultMessagePublicIDs": []string{str(a1, "publicID")}})
	sid2 := shareOf(t, s2)
	if sid2 == sid1 {
		t.Fatal("new share id expected")
	}
	e.expectErr("GET", "/shared-conversations/"+sid1, nil, nil, 404, "conversation_share.not_found")
	e.ok("GET", "/shared-conversations/"+sid2, nil, nil)
	var n int64
	e.DB.Model(&Share{}).Where("conversation_id = ? AND status = ?", e.convRow(id).ID, "active").Count(&n)
	if n != 1 {
		t.Fatalf("only one active share: %d", n)
	}
	// regenerate marks regenerated_at and invalidates the old link
	s3 := e.ok("POST", "/conversations/"+id+"/share/regenerate", e.u1, nil)
	sid3 := shareOf(t, s3)
	e.expectErr("GET", "/shared-conversations/"+sid2, nil, nil, 404, "conversation_share.not_found")
	var row Share
	e.DB.Where("share_id = ?", sid3).First(&row)
	if row.RegeneratedAt == nil {
		t.Fatal("regenerated_at must be set")
	}
	// GET returns the latest share
	g := e.ok("GET", "/conversations/"+id+"/share", e.u1, nil)
	if str(g, "shareID") != sid3 || str(g, "status") != "active" {
		t.Fatalf("get: %v", g)
	}
	// revoke
	r := e.ok("DELETE", "/conversations/"+id+"/share", e.u1, nil)
	if str(r, "status") != "revoked" || r["revokedAt"] == nil {
		t.Fatalf("revoke: %v", r)
	}
	e.expectErr("GET", "/shared-conversations/"+sid3, nil, nil, 404, "conversation_share.not_found")
	c = e.ok("GET", "/conversations/"+id, e.u1, nil)
	if str(c, "shareStatus") != "revoked" {
		t.Fatalf("status after revoke: %v", c)
	}
	// revoking again is harmless; a conversation that was never shared yields none
	r = e.ok("DELETE", "/conversations/"+id+"/share", e.u1, nil)
	if str(r, "status") != "revoked" {
		t.Fatalf("second revoke: %v", r)
	}
	other := e.newConv(e.u1, "o", "")
	if r = e.ok("DELETE", "/conversations/"+other+"/share", e.u1, nil); str(r, "status") != "none" {
		t.Fatalf("revoke never-shared: %v", r)
	}
	// ownership
	e.expectErr("GET", "/conversations/"+id+"/share", e.u2, nil, 404, "conversation.not_found")
	e.expectErr("POST", "/conversations/"+id+"/share", e.u2, nil, 404, "conversation.not_found")
	e.expectErr("DELETE", "/conversations/"+id+"/share", e.u2, nil, 404, "conversation.not_found")
	for _, a := range []string{"create_conversation_share", "regenerate_conversation_share", "revoke_conversation_share"} {
		if e.audit.find(a) == nil {
			t.Errorf("audit %s missing: %v", a, e.audit.actions())
		}
	}
}

func TestShareDefaultMessageValidation(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.send(e.u1, id, "q", nil)
	other := e.newConv(e.u1, "other", "gpt-4o")
	_, oa := pair(e.send(e.u1, other, "q", nil))
	e.expectErr("POST", "/conversations/"+id+"/share", e.u1, map[string]any{"defaultMessagePublicIDs": []string{"unknown"}}, 400, "conversation_share.invalid")
	e.expectErr("POST", "/conversations/"+id+"/share", e.u1, map[string]any{"defaultMessagePublicIDs": []string{str(oa, "publicID")}}, 400, "conversation_share.invalid")
	e.expectErr("POST", "/conversations/"+id+"/share", e.u1, map[string]any{"defaultMessagePublicIDs": make([]string, 1001)}, 400, "request.invalid_body")
	// blanks and duplicates are tolerated
	rows := e.list(id)
	ids := []string{" " + str(rows[0], "publicID") + " ", str(rows[0], "publicID"), ""}
	s := e.ok("POST", "/conversations/"+id+"/share", e.u1, map[string]any{"defaultMessagePublicIDs": ids})
	pub := e.ok("GET", "/shared-conversations/"+str(s, "shareID"), nil, nil)
	def := pub["defaultMessagePublicIDs"].([]any)
	if len(def) != 1 || def[0] != str(rows[0], "publicID") {
		t.Fatalf("explicit default branch: %v", def)
	}
	// explicit body with empty object / no body both work
	e.ok("POST", "/conversations/"+id+"/share", e.u1, map[string]any{})
	e.ok("POST", "/conversations/"+id+"/share", e.u1, "")
	e.expectErr("POST", "/conversations/"+id+"/share", e.u1, "{bad", 400, "request.invalid_body")
}

func TestPublicShareView(t *testing.T) {
	e := newEnv(t)
	e.gen.set(traceGenerator())
	id := e.newConv(e.u1, "Public", "gpt-4o")
	u, a := pair(e.send(e.u1, id, "question", nil))
	e.ok("PUT", "/messages/"+str(a, "publicID")+"/feedback", e.u1, map[string]string{"feedback": "up"})
	e.ok("PUT", uiPath(id, str(a, "publicID")), e.u1, map[string]any{"blocks": map[string]any{"b": map[string]any{"x": 1}}})
	s := e.ok("POST", "/conversations/"+id+"/share", e.u1, nil)
	sid := shareOf(t, s)
	// message added after the snapshot is not visible
	e.send(e.u1, id, "later", nil)

	w := e.call("GET", "/shared-conversations/"+sid, nil, nil)
	testkit.Expect(t, w, 200, "")
	m := dataMap(t, w)
	if str(m, "title") != "Public" || str(m, "shareID") != sid || m["uiComponentsEnabled"] != true {
		t.Fatalf("share page: %v", m)
	}
	msgs := m["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("only snapshot messages: %d", len(msgs))
	}
	first := msgs[0].(map[string]any)
	second := msgs[1].(map[string]any)
	if str(first, "publicID") != str(u, "publicID") || str(second, "publicID") != str(a, "publicID") || str(second, "parentPublicID") != str(u, "publicID") {
		t.Fatalf("order/links: %v", msgs)
	}
	for _, banned := range []string{"billingCost", "myFeedback", "thumbsUpCount", "thumbsDownCount", "uiState", "knowledgeSources", "id", "userID", "conversationID", "errorRequestID"} {
		if _, has := second[banned]; has {
			t.Errorf("public message must not carry %q", banned)
		}
	}
	// Only the bare "not required" state is shown, never a verdict.
	if m, _ := second["moderation"].(map[string]any); len(m) != 1 || m["state"] != "not_required" {
		t.Errorf("public moderation: %v", second["moderation"])
	}
	for _, k := range []string{"publicID", "parentPublicID", "sourcePublicID", "runID", "role", "contentType", "content", "branchReason", "tokenUsage", "inputTokens", "outputTokens",
		"reasoningEffort", "latencyMS", "status", "errorCode", "errorMessage", "attachments", "platformModelName", "upstreamModelName", "modelVendor", "modelIcon", "processTrace", "editedAt", "createdAt", "updatedAt"} {
		if _, has := second[k]; !has {
			t.Errorf("public message missing %q", k)
		}
	}
	body := w.Body.String()
	for _, secret := range []string{"Bearer zzz", "apiKey", "secret-up", `"authorization"`} {
		if strings.Contains(body, secret) {
			t.Errorf("public body leaks %q", secret)
		}
	}
	pt := second["processTrace"].(map[string]any)
	if pt["detailOmitted"] != nil {
		t.Fatalf("public traces never set detailOmitted: %v", pt)
	}
	// lastAccessedAt updates on each read, visible in the owner's view
	e.Clock.Advance(3_000_000_000)
	e.ok("GET", "/shared-conversations/"+sid, nil, nil)
	c := e.ok("GET", "/conversations/"+id, e.u1, nil)
	if c["lastShareAccessedAt"] == nil {
		t.Fatalf("lastShareAccessedAt: %v", c)
	}
	sh := e.ok("GET", "/conversations/"+id+"/share", e.u1, nil)
	if sh["lastAccessedAt"] == nil || num(sh, "messageCount") != 2 {
		t.Fatalf("share summary: %v", sh)
	}
	// blank id; deleted conversation
	e.expectErr("GET", "/shared-conversations/%20", nil, nil, 400, "share.invalid_id")
	e.expectErr("GET", "/shared-conversations/unknown", nil, nil, 404, "conversation_share.not_found")
	e.ok("DELETE", "/conversations/"+id, e.u1, nil)
	e.expectErr("GET", "/shared-conversations/"+sid, nil, nil, 404, "conversation_share.not_found")
}

func TestPublicShareDefaultBranchFallsBackWhenMessagesGone(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	_, a := pair(e.send(e.u1, id, "q", nil))
	s := e.ok("POST", "/conversations/"+id+"/share", e.u1, map[string]any{"defaultMessagePublicIDs": []string{str(a, "publicID")}})
	e.DB.Where("public_id = ?", str(a, "publicID")).Delete(&Message{})
	m := e.ok("GET", "/shared-conversations/"+str(s, "shareID"), nil, nil)
	def := m["defaultMessagePublicIDs"].([]any)
	msgs := m["messages"].([]any)
	if len(msgs) != 1 || len(def) != 1 || def[0] != msgs[0].(map[string]any)["publicID"] {
		t.Fatalf("fallback: def=%v msgs=%v", def, msgs)
	}
}

func TestPublicShareBlockedAssistantContentHidden(t *testing.T) {
	e := newEnv(t)
	e.mod.block[dirOutput] = true
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.send(e.u1, id, "hi", nil)
	s := e.ok("POST", "/conversations/"+id+"/share", e.u1, nil)
	m := e.ok("GET", "/shared-conversations/"+str(s, "shareID"), nil, nil)
	msgs := m["messages"].([]any)
	if str(msgs[1].(map[string]any), "content") != "" {
		t.Fatalf("blocked content must be hidden: %v", msgs[1])
	}
}

func TestSharingFeatureFlag(t *testing.T) {
	e := newEnv(t, localOpt())
	id := e.newConv(e.u1, "t", "gpt-4o")
	for _, c := range []struct{ method, path string }{
		{"GET", "/conversations/" + id + "/share"},
		{"POST", "/conversations/" + id + "/share"},
		{"POST", "/conversations/" + id + "/share/regenerate"},
		{"DELETE", "/conversations/" + id + "/share"},
		{"POST", "/conversations/shares/revoke"},
		{"POST", "/shared-conversations/x/clone"},
	} {
		w := e.call(c.method, c.path, e.u1, map[string]any{"conversationPublicIDs": []string{}})
		testkit.Expect(t, w, 404, "feature.disabled")
	}
	testkit.Expect(t, e.call("GET", "/shared-conversations/x", nil, nil), 404, "feature.disabled")
	testkit.Expect(t, e.call("GET", "/shared-conversations/x/files/y/content", nil, nil), 404, "feature.disabled")
}

func TestRevokeSharesBatch(t *testing.T) {
	e := newEnv(t)
	var ids, sids []string
	for i := 0; i < 3; i++ {
		id := e.newConv(e.u1, fmt.Sprintf("c%d", i), "gpt-4o")
		e.send(e.u1, id, "q", nil)
		s := e.ok("POST", "/conversations/"+id+"/share", e.u1, nil)
		ids, sids = append(ids, id), append(sids, str(s, "shareID"))
	}
	m := e.ok("POST", "/conversations/shares/revoke", e.u1, map[string]any{"conversationPublicIDs": []string{ids[0], ids[1], ids[0], " "}})
	if m["revoked"] != true {
		t.Fatalf("revoke: %v", m)
	}
	e.expectErr("GET", "/shared-conversations/"+sids[0], nil, nil, 404, "conversation_share.not_found")
	e.expectErr("GET", "/shared-conversations/"+sids[1], nil, nil, 404, "conversation_share.not_found")
	e.ok("GET", "/shared-conversations/"+sids[2], nil, nil)
	// empty is a no-op
	e.ok("POST", "/conversations/shares/revoke", e.u1, map[string]any{"conversationPublicIDs": []string{}})
	e.ok("POST", "/conversations/shares/revoke", e.u1, map[string]any{})
	// unknown or foreign -> 404, nothing revoked
	e.expectErr("POST", "/conversations/shares/revoke", e.u1, map[string]any{"conversationPublicIDs": []string{ids[2], "unknown"}}, 404, "conversation.not_found")
	e.expectErr("POST", "/conversations/shares/revoke", e.u2, map[string]any{"conversationPublicIDs": []string{ids[2]}}, 404, "conversation.not_found")
	e.ok("GET", "/shared-conversations/"+sids[2], nil, nil)
	e.expectErr("POST", "/conversations/shares/revoke", e.u1, map[string]any{"conversationPublicIDs": make([]string, 1001)}, 400, "request.invalid_body")
	if e.audit.find("revoke_conversation_shares") == nil {
		t.Fatal("audit")
	}
}

func TestSharedFileContent(t *testing.T) {
	e := newEnv(t)
	f := e.uploadOK(e.u1, "pic.png", "image/png", pngBytes(30))["file"].(map[string]any)
	secret := e.uploadOK(e.u1, "secret.txt", "text/plain", []byte("not in share"))["file"].(map[string]any)
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.send(e.u1, id, "look", map[string]any{"fileIDs": []string{str(f, "fileID")}})
	s := e.ok("POST", "/conversations/"+id+"/share", e.u1, nil)
	sid := str(s, "shareID")
	w := e.call("GET", "/shared-conversations/"+sid+"/files/"+str(f, "fileID")+"/content", nil, nil)
	if w.Code != 200 || w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline") {
		t.Fatalf("shared file: %d %v", w.Code, w.Header())
	}
	e.expectErr("GET", "/shared-conversations/"+sid+"/files/"+str(secret, "fileID")+"/content", nil, nil, 404, "file.not_found")
	e.expectErr("GET", "/shared-conversations/"+sid+"/files/%20/content", nil, nil, 400, "file.invalid_id")
	e.expectErr("GET", "/shared-conversations/nope/files/"+str(f, "fileID")+"/content", nil, nil, 404, "conversation_share.not_found")
	e.ok("DELETE", "/conversations/"+id+"/share", e.u1, nil)
	e.expectErr("GET", "/shared-conversations/"+sid+"/files/"+str(f, "fileID")+"/content", nil, nil, 404, "conversation_share.not_found")
}

func TestCloneShared(t *testing.T) {
	e := newEnv(t)
	e.gen.set(traceGenerator())
	f := e.uploadOK(e.u1, "pic.png", "image/png", pngBytes(30))["file"].(map[string]any)
	id := e.newConv(e.u1, "Original", "gpt-4o")
	u1, a1 := pair(e.send(e.u1, id, "look", map[string]any{"fileIDs": []string{str(f, "fileID")}}))
	// a retry branch: the default branch must come last under the parent
	_, a2 := pair(e.send(e.u1, id, "look", map[string]any{"sourceMessagePublicID": str(a1, "publicID"), "parentMessagePublicID": str(u1, "publicID"), "branchReason": "retry"}))
	s := e.ok("POST", "/conversations/"+id+"/share", e.u1, nil)
	sid := str(s, "shareID")
	m := e.ok("POST", "/shared-conversations/"+sid+"/clone", e.u2, nil)
	nid := str(m, "publicID")
	if nid == id || str(m, "title") != "Original" || num(m, "userID") != 2 || str(m, "status") != "active" || str(m, "labelsJSON") != "[]" || num(m, "messageCount") != 3 || str(m, "shareStatus") != "none" {
		t.Fatalf("clone: %v", m)
	}
	rows := func() []map[string]any {
		w := e.call("GET", "/conversations/"+nid+"/messages", e.u2, nil)
		_, r := results(t, w)
		return r
	}()
	if len(rows) != 3 {
		t.Fatalf("cloned messages: %d", len(rows))
	}
	// parent-before-child; the default (retry) branch is the newest child
	if rows[0]["role"] != "user" || rows[1]["role"] != "assistant" || rows[2]["role"] != "assistant" {
		t.Fatalf("roles: %v", rows)
	}
	if rows[2]["parentMessageID"] != rows[0]["id"] || rows[1]["parentMessageID"] != rows[0]["id"] {
		t.Fatalf("parents remapped: %v", rows)
	}
	if str(rows[2], "content") != str(a2, "content") || str(rows[2], "branchReason") != "retry" || rows[2]["sourceMessageID"] != rows[1]["id"] {
		t.Fatalf("default branch last / source remapped: %v", rows[2])
	}
	if str(rows[0], "publicID") == str(u1, "publicID") {
		t.Fatal("new public ids")
	}
	for _, r := range rows {
		if _, has := r["billingCost"]; has {
			t.Fatalf("billing reset: %v", r["billingCost"])
		}
		if str(r, "runID") != "" && str(r, "runID") == str(a1, "runID") {
			t.Fatal("run ids must be new")
		}
	}
	var snaps []map[string]any
	_ = jsonUnmarshal(str(rows[0], "attachments"), &snaps)
	if len(snaps) != 1 || snaps[0]["file_id"] == str(f, "fileID") {
		t.Fatalf("attachment file copied with a new id: %v", snaps)
	}
	// the new owner sees the copied file and consumed quota
	lf := e.ok("GET", "/files", e.u2, nil)
	if num(lf, "total") != 1 || num(lf["quota"].(map[string]any), "usedBytes") != num(f, "sizeBytes") {
		t.Fatalf("copied file: %v", lf)
	}
	w := e.call("GET", "/files/"+snaps[0]["file_id"].(string)+"/content", e.u2, nil)
	if w.Code != 200 {
		t.Fatalf("cloned content: %d", w.Code)
	}
	// runs are copied under new ids owned by the caller; display trace is preserved but sanitised
	rw := e.call("GET", "/conversations/"+nid+"/runs", e.u2, nil)
	if total, rr := results(t, rw); total == 0 || num(rr[0], "userID") != 2 {
		t.Fatalf("runs: %v", rr)
	}
	if rows[1]["processTrace"] == nil {
		t.Fatalf("display trace copied: %v", rows[1])
	}
	if strings.Contains(fmt.Sprint(rows[1]["processTrace"]), "Bearer zzz") {
		t.Fatal("credentials must not survive cloning")
	}
	var tc int64
	e.DB.Model(&RunEvent{}).Where("event_scope = ? AND run_id IN (?)", scopeToolCall, e.DB.Model(&Run{}).Select("run_id").Where("user_id = 2")).Count(&tc)
	if tc != 0 {
		t.Fatalf("tool call audit rows must not be cloned: %d", tc)
	}
	// source untouched
	if len(e.list(id)) != 3 {
		t.Fatalf("source changed")
	}
	if e.audit.find("clone_shared_conversation") == nil {
		t.Fatal("audit")
	}
	// the owner can clone too, and clones are independent
	m2 := e.ok("POST", "/shared-conversations/"+sid+"/clone", e.u1, nil)
	if str(m2, "publicID") == nid {
		t.Fatal("independent clones")
	}
	// auth required and unknown share
	testkit.Expect(t, e.call("POST", "/shared-conversations/"+sid+"/clone", nil, nil), 401, "")
	e.expectErr("POST", "/shared-conversations/nope/clone", e.u2, nil, 404, "conversation_share.not_found")
}

func TestCloneQuotaExceededAndMissingFile(t *testing.T) {
	e := newEnv(t)
	f := e.uploadOK(e.u1, "pic.png", "image/png", pngBytes(30))["file"].(map[string]any)
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.send(e.u1, id, "look", map[string]any{"fileIDs": []string{str(f, "fileID")}})
	s := e.ok("POST", "/conversations/"+id+"/share", e.u1, nil)
	sid := str(s, "shareID")
	e.DB.Create(&StorageQuota{UserID: 2, QuotaBytes: 5})
	e.expectErr("POST", "/shared-conversations/"+sid+"/clone", e.u2, nil, 400, "quota.exceeded")
	var n int64
	e.DB.Model(&Conversation{}).Where("user_id = 2").Count(&n)
	if n != 0 {
		t.Fatalf("failed clone must leave nothing behind: %d", n)
	}
	e.DB.Model(&StorageQuota{}).Where("user_id = 2").Update("quota_bytes", 0)
	e.DB.Where("file_id = ?", str(f, "fileID")).Delete(&FileObject{})
	e.expectErr("POST", "/shared-conversations/"+sid+"/clone", e.u3(), nil, 404, "shared_file.not_found")
}

func (e *tenv) u3() map[string]string { return e.Bearer(testkit.User(3)) }
