package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestCreateDefaults(t *testing.T) {
	e := newEnv(t)
	m := e.ok("POST", "/conversations", e.u1, map[string]string{})
	if str(m, "title") != "Untitled chat" || str(m, "status") != "active" || str(m, "labelsJSON") != "[]" ||
		str(m, "shareStatus") != "none" || num(m, "messageCount") != 0 || str(m, "provider") != "internal" {
		t.Fatalf("defaults: %v", m)
	}
	if len(str(m, "publicID")) != 32 || strings.Contains(str(m, "publicID"), "-") {
		t.Fatalf("public id: %q", str(m, "publicID"))
	}
	if str(m, "sessionKey") == "" || str(m, "contextPolicyJSON") == "" {
		t.Fatalf("session key / policy missing: %v", m)
	}
	var pol map[string]any
	if err := json.Unmarshal([]byte(str(m, "contextPolicyJSON")), &pol); err != nil || len(pol) == 0 {
		t.Fatalf("policy snapshot: %v %v", err, pol)
	}
	for _, k := range []string{"compact_enabled", "compact_preserve_recent_turns", "compact_trigger_percent", "context_window_fallback_tokens", "max_turns"} {
		if _, ok := pol[k]; !ok {
			t.Fatalf("policy snapshot lacks %q: %v", k, pol)
		}
	}
	if len(pol) != 5 {
		t.Fatalf("policy snapshot has unexpected keys: %v", pol)
	}
	if e.audit.find("create_conversation") == nil {
		t.Fatalf("audit missing: %v", e.audit.actions())
	}
	// Empty body is accepted as all-optional? It must at least not 500.
	w := e.call("POST", "/conversations", e.u1, nil)
	if w.Code >= 500 {
		t.Fatalf("empty body: %d", w.Code)
	}
}

func TestCreateProviderInference(t *testing.T) {
	e := newEnv(t)
	cases := map[string]string{"gpt-4o": "openai", "GPT-5": "openai", "claude-sonnet": "anthropic", "llama": "internal"}
	for model, want := range cases {
		m := e.ok("POST", "/conversations", e.u1, map[string]string{"model": " " + model + " "})
		if str(m, "provider") != want || str(m, "model") != model {
			t.Fatalf("%s -> %v", model, m)
		}
	}
}

func TestCreateValidation(t *testing.T) {
	e := newEnv(t)
	e.expectErr("POST", "/conversations", e.u1, map[string]string{"title": strings.Repeat("x", 256)}, 400, "request.invalid_body")
	e.expectErr("POST", "/conversations", e.u1, map[string]string{"model": strings.Repeat("x", 129)}, 400, "request.invalid_body")
	e.expectErr("POST", "/conversations", e.u1, map[string]string{"projectID": strings.Repeat("x", 33)}, 400, "request.invalid_body")
	e.expectErr("POST", "/conversations", e.u1, map[string]string{"projectID": "nope"}, 404, "conversation_project.not_found")
	w := e.call("POST", "/conversations", nil, map[string]string{})
	testkit.Expect(t, w, 401, "")
}

func TestCreateInProjectUsesDefaultModel(t *testing.T) {
	e := newEnv(t)
	p := e.ok("POST", "/conversation-projects", e.u1, map[string]any{"name": "P", "defaultModel": "gpt-4o"})
	pid := str(p, "publicID")
	m := e.ok("POST", "/conversations", e.u1, map[string]string{"projectID": pid})
	if str(m, "model") != "gpt-4o" || str(m, "projectID") != pid || str(m, "projectName") != "P" {
		t.Fatalf("default model: %v", m)
	}
	// An explicit model wins.
	m = e.ok("POST", "/conversations", e.u1, map[string]string{"projectID": pid, "model": "claude-sonnet"})
	if str(m, "model") != "claude-sonnet" {
		t.Fatalf("explicit: %v", m)
	}
	// The default is dropped when it is no longer available.
	e.cat.mu.Lock()
	e.cat.available["gpt-4o"] = false
	e.cat.mu.Unlock()
	m = e.ok("POST", "/conversations", e.u1, map[string]string{"projectID": pid})
	if str(m, "model") != "" {
		t.Fatalf("unavailable default must be dropped: %v", m)
	}
	// Another user's project is not found.
	e.expectErr("POST", "/conversations", e.u2, map[string]string{"projectID": pid}, 404, "conversation_project.not_found")
}

func TestGetAndIsolation(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "mine", "")
	m := e.ok("GET", "/conversations/"+id, e.u1, nil)
	if str(m, "title") != "mine" || num(m, "userID") != 1 {
		t.Fatalf("get: %v", m)
	}
	e.expectErr("GET", "/conversations/"+id, e.u2, nil, 404, "conversation.not_found")
	e.expectErr("GET", "/conversations/unknown", e.u1, nil, 404, "conversation.not_found")
	e.expectErr("PATCH", "/conversations/"+id+"/title", e.u2, map[string]string{"title": "x"}, 404, "conversation.not_found")
	e.expectErr("DELETE", "/conversations/"+id, e.u2, nil, 404, "conversation.not_found")
	e.expectErr("PATCH", "/conversations/"+id+"/star", e.u2, map[string]any{"starred": true}, 404, "conversation.not_found")
	e.expectErr("PATCH", "/conversations/"+id+"/archive", e.u2, map[string]any{"archived": true}, 404, "conversation.not_found")
	e.expectErr("GET", "/conversations/"+id+"/messages", e.u2, nil, 404, "conversation.not_found")
}

func TestListFiltersAndOrder(t *testing.T) {
	e := newEnv(t)
	a := e.newConv(e.u1, "alpha", "")
	b := e.newConv(e.u1, "beta", "")
	c := e.newConv(e.u1, "gamma", "")
	e.newConv(e.u2, "other-user", "")

	// default hides archived
	e.ok("PATCH", "/conversations/"+c+"/archive", e.u1, map[string]any{"archived": true})
	w := e.call("GET", "/conversations", e.u1, nil)
	total, res := results(t, w)
	if total != 2 || len(res) != 2 {
		t.Fatalf("default list: %d %d", total, len(res))
	}
	w = e.call("GET", "/conversations?status=archived", e.u1, nil)
	total, res = results(t, w)
	if total != 1 || str(res[0], "publicID") != c {
		t.Fatalf("archived: %d %v", total, res)
	}
	w = e.call("GET", "/conversations?status=all", e.u1, nil)
	if total, _ = results(t, w); total != 3 {
		t.Fatalf("all: %d", total)
	}
	w = e.call("GET", "/conversations?status=bogus", e.u1, nil)
	if total, _ = results(t, w); total != 2 {
		t.Fatalf("unknown status falls back to active: %d", total)
	}

	// starred ordering: starred_at desc
	e.ok("PATCH", "/conversations/"+a+"/star", e.u1, map[string]any{"starred": true})
	e.Clock.Advance(5_000_000_000)
	e.ok("PATCH", "/conversations/"+b+"/star", e.u1, map[string]any{"starred": true})
	w = e.call("GET", "/conversations?starred=starred", e.u1, nil)
	total, res = results(t, w)
	if total != 2 || str(res[0], "publicID") != b || str(res[1], "publicID") != a {
		t.Fatalf("starred order: %v", res)
	}
	w = e.call("GET", "/conversations?starred=unstarred&status=all", e.u1, nil)
	if total, res = results(t, w); total != 1 || str(res[0], "publicID") != c {
		t.Fatalf("unstarred: %d %v", total, res)
	}

	// pagination
	w = e.call("GET", "/conversations?page=1&page_size=1&status=all", e.u1, nil)
	total, res = results(t, w)
	if total != 3 || len(res) != 1 {
		t.Fatalf("paging: %d %d", total, len(res))
	}
	w = e.call("GET", "/conversations?page=abc&page_size=-4", e.u1, nil)
	total, res = results(t, w)
	if total != 2 || len(res) != 2 {
		t.Fatalf("bad paging falls back: %d %d", total, len(res))
	}
}

func TestListProjectAndShareFilters(t *testing.T) {
	e := newEnv(t)
	p := e.ok("POST", "/conversation-projects", e.u1, map[string]any{"name": "Work Folder", "description": "docs"})
	pid := str(p, "publicID")
	in := e.newConv(e.u1, "in-project", "")
	out := e.newConv(e.u1, "loose", "")
	e.ok("PATCH", "/conversations/"+in+"/project", e.u1, map[string]string{"projectID": pid})

	w := e.call("GET", "/conversations?project=unassigned", e.u1, nil)
	total, res := results(t, w)
	if total != 1 || str(res[0], "publicID") != out {
		t.Fatalf("unassigned: %v", res)
	}
	w = e.call("GET", "/conversations?project="+pid, e.u1, nil)
	total, res = results(t, w)
	if total != 1 || str(res[0], "publicID") != in || str(res[0], "projectName") != "Work Folder" {
		t.Fatalf("by project: %v", res)
	}
	w = e.call("GET", "/conversations?project=doesnotexist", e.u1, nil)
	total, res = results(t, w)
	if total != 0 || res == nil || len(res) != 0 {
		t.Fatalf("unknown project: %d %v", total, res)
	}
	w = e.call("GET", "/conversations?project=all", e.u1, nil)
	if total, _ = results(t, w); total != 2 {
		t.Fatalf("project=all: %d", total)
	}

	// share filter
	e.send(e.u1, in, "hello", nil)
	e.ok("POST", "/conversations/"+in+"/share", e.u1, nil)
	w = e.call("GET", "/conversations?share=shared", e.u1, nil)
	total, res = results(t, w)
	if total != 1 || str(res[0], "publicID") != in || str(res[0], "shareStatus") != "active" || str(res[0], "shareID") == "" {
		t.Fatalf("shared: %v", res)
	}
	w = e.call("GET", "/conversations?share=unshared", e.u1, nil)
	if total, res = results(t, w); total != 1 || str(res[0], "publicID") != out || str(res[0], "shareStatus") != "none" {
		t.Fatalf("unshared: %v", res)
	}
	// after revoke the status is revoked and the id is not exposed
	e.ok("DELETE", "/conversations/"+in+"/share", e.u1, nil)
	w = e.call("GET", "/conversations?project="+pid, e.u1, nil)
	_, res = results(t, w)
	if str(res[0], "shareStatus") != "revoked" || str(res[0], "shareID") != "" || res[0]["sharedAt"] != nil {
		t.Fatalf("revoked summary: %v", res[0])
	}
}

func TestListKeywordSearch(t *testing.T) {
	e := newEnv(t)
	p := e.ok("POST", "/conversation-projects", e.u1, map[string]any{"name": "Foo_Bar project"})
	pid := str(p, "publicID")
	byTitle := e.newConv(e.u1, "Foo_Bar title", "")
	decoy := e.newConv(e.u1, "FooXBar decoy", "")
	byMsg := e.newConv(e.u1, "plain", "")
	byProject := e.newConv(e.u1, "other", "")
	e.ok("PATCH", "/conversations/"+byProject+"/project", e.u1, map[string]string{"projectID": pid})
	conv := e.convRow(byMsg)
	e.seedMsg(conv, roleUser, "this has foo_bar inside", msgSuccess, nil)
	deleted := e.newConv(e.u1, "plain2", "")
	dc := e.convRow(deleted)
	dm := e.seedMsg(dc, roleUser, "foo_bar hidden", msgSuccess, nil)
	e.DB.Delete(dm)
	sysOnly := e.newConv(e.u1, "plain3", "")
	e.seedMsg(e.convRow(sysOnly), roleSystem, "foo_bar system", msgSuccess, nil)

	w := e.call("GET", "/conversations?q=Foo_Bar", e.u1, nil)
	total, res := results(t, w)
	got := map[string]bool{}
	for _, r := range res {
		got[str(r, "publicID")] = true
	}
	if total != 3 || !got[byTitle] || !got[byMsg] || !got[byProject] || got[decoy] {
		t.Fatalf("keyword results total=%d got=%v", total, got)
	}
	// percent is literal
	w = e.call("GET", "/conversations?q=%25", e.u1, nil)
	if total, _ = results(t, w); total != 0 {
		t.Fatalf("percent must be literal: %d", total)
	}
	// matches model and public id
	m := e.newConv(e.u1, "t", "claude-sonnet")
	w = e.call("GET", "/conversations?q=CLAUDE", e.u1, nil)
	if total, res = results(t, w); total != 1 || str(res[0], "publicID") != m {
		t.Fatalf("model match: %v", res)
	}
	w = e.call("GET", "/conversations?q="+m[:12], e.u1, nil)
	if total, _ = results(t, w); total != 1 {
		t.Fatalf("public id match: %d", total)
	}
}

func TestSearchEndpoint(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 20; i++ {
		e.newConv(e.u1, fmt.Sprintf("conv %02d", i), "")
	}
	w := e.call("GET", "/conversations/search?page=1&page_size=10", e.u1, nil)
	testkit.Expect(t, w, 200, "")
	m := dataMap(t, w)
	if m["hasMore"] != true || len(m["results"].([]any)) != 10 {
		t.Fatalf("page1: %v", m)
	}
	first := m["results"].([]any)[0].(map[string]any)
	for _, k := range []string{"publicID", "projectID", "projectName", "title", "labelsJSON", "isStarred", "messageCount", "status", "updatedAt"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("missing field %s in %v", k, first)
		}
	}
	m = e.ok("GET", "/conversations/search?page=2&page_size=10", e.u1, nil)
	if m["hasMore"] != false || len(m["results"].([]any)) != 10 {
		t.Fatalf("page2: %v", m)
	}
	// too long (runes, not bytes)
	e.expectErr("GET", "/conversations/search?q="+strings.Repeat("a", 201), e.u1, nil, 400, "request.invalid_query")
	if msg := testkit.Envelope(e.t, e.call("GET", "/conversations/search?q="+strings.Repeat("a", 201), e.u1, nil)).ErrorMsg; msg != "invalid query parameter" {
		t.Fatalf("message %q", msg)
	}
	e.expectErr("GET", "/conversations/search?q="+strings.Repeat("%E4%B8%AD", 201), e.u1, nil, 400, "request.invalid_query")
	w = e.call("GET", "/conversations/search?q="+strings.Repeat("%E4%B8%AD", 200), e.u1, nil)
	testkit.Expect(t, w, 200, "")
	// matches
	m = e.ok("GET", "/conversations/search?q=conv%2007", e.u1, nil)
	if len(m["results"].([]any)) != 1 {
		t.Fatalf("search match: %v", m)
	}
	// archived conversations are included in search
	id := e.newConv(e.u1, "zebra archived", "")
	e.ok("PATCH", "/conversations/"+id+"/archive", e.u1, map[string]any{"archived": true})
	m = e.ok("GET", "/conversations/search?q=zebra", e.u1, nil)
	if len(m["results"].([]any)) != 1 {
		t.Fatalf("archived search: %v", m)
	}
}

func TestRename(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "old", "")
	m := e.ok("PATCH", "/conversations/"+id+"/title", e.u1, map[string]string{"title": "  New Name "})
	if str(m, "title") != "New Name" {
		t.Fatalf("rename: %v", m)
	}
	e.expectErr("PATCH", "/conversations/"+id+"/title", e.u1, map[string]string{"title": "   "}, 400, "conversation.invalid_title")
	e.expectErr("PATCH", "/conversations/"+id+"/title", e.u1, map[string]string{"title": strings.Repeat("x", 256)}, 400, "request.invalid_body")
	e.expectErr("PATCH", "/conversations/"+id+"/title", e.u1, map[string]string{}, 400, "request.invalid_body")
	if e.audit.find("rename_conversation") == nil {
		t.Fatal("audit")
	}
}

func TestLabels(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "")
	m := e.ok("PATCH", "/conversations/"+id+"/labels", e.u1, map[string]any{"labels": []string{"#AI", "ai", " Work  Notes "}})
	if str(m, "labelsJSON") != `["AI","Work Notes"]` {
		t.Fatalf("labels: %v", m)
	}
	if !e.convRow(id).LabelsManuallyManaged {
		t.Fatal("manual flag must be set")
	}
	e.expectErr("PATCH", "/conversations/"+id+"/labels", e.u1, map[string]any{"labels": []string{"1", "2", "3", "4", "5", "6", "7"}}, 400, "request.invalid_body")
	e.expectErr("PATCH", "/conversations/"+id+"/labels", e.u1, map[string]any{"labels": []string{strings.Repeat("a", 25)}}, 400, "request.invalid_body")
	e.expectErr("PATCH", "/conversations/"+id+"/labels", e.u1, map[string]any{}, 400, "request.required")
	e.expectErr("PATCH", "/conversations/"+id+"/labels", e.u1, map[string]any{"labels": []string{"  ", "ok"}}, 400, "request.invalid_conversation_labels")
	e.expectErr("PATCH", "/conversations/"+id+"/labels", e.u1, map[string]any{"labels": []string{"#"}}, 400, "request.invalid_conversation_labels")
	// 6 labels where duplicates reduce below the cap are fine
	m = e.ok("PATCH", "/conversations/"+id+"/labels", e.u1, map[string]any{"labels": []string{"a", "A", "b", "c", "d", "e"}})
	if str(m, "labelsJSON") != `["a","b","c","d","e"]` {
		t.Fatalf("dedupe: %v", m)
	}
	// empty array clears
	m = e.ok("PATCH", "/conversations/"+id+"/labels", e.u1, map[string]any{"labels": []string{}})
	if str(m, "labelsJSON") != "[]" {
		t.Fatalf("clear: %v", m)
	}
	// Chinese characters count as runes, not bytes
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	m = e.ok("PATCH", "/conversations/"+id+"/labels", e.u1, map[string]any{"labels": []string{strings.Repeat("\u4e2d", 24)}})
	if !strings.Contains(str(m, "labelsJSON"), "\u4e2d") {
		t.Fatalf("rune labels: %v", m)
	}
}

func TestStarIdempotent(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "")
	m1 := e.ok("PATCH", "/conversations/"+id+"/star", e.u1, map[string]any{"starred": true})
	if m1["starredAt"] == nil || m1["isStarred"] != true {
		t.Fatalf("star: %v", m1)
	}
	e.Clock.Advance(60_000_000_000)
	m2 := e.ok("PATCH", "/conversations/"+id+"/star", e.u1, map[string]any{"starred": true})
	if m1["starredAt"] != m2["starredAt"] {
		t.Fatalf("starredAt changed: %v vs %v", m1["starredAt"], m2["starredAt"])
	}
	m3 := e.ok("PATCH", "/conversations/"+id+"/star", e.u1, map[string]any{"starred": false})
	if m3["starredAt"] != nil || m3["isStarred"] != false {
		t.Fatalf("unstar: %v", m3)
	}
	e.expectErr("PATCH", "/conversations/"+id+"/star", e.u1, map[string]any{}, 400, "request.invalid_body")
}

func TestArchiveToggle(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "")
	if m := e.ok("PATCH", "/conversations/"+id+"/archive", e.u1, map[string]any{"archived": true}); str(m, "status") != "archived" {
		t.Fatalf("archive: %v", m)
	}
	if m := e.ok("PATCH", "/conversations/"+id+"/archive", e.u1, map[string]any{"archived": false}); str(m, "status") != "active" {
		t.Fatalf("unarchive: %v", m)
	}
	e.expectErr("PATCH", "/conversations/"+id+"/archive", e.u1, map[string]any{}, 400, "request.invalid_body")
}

func TestDeleteConversation(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "")
	m := e.ok("DELETE", "/conversations/"+id, e.u1, nil)
	if m["deleted"] != true {
		t.Fatalf("delete: %v", m)
	}
	if _, has := m["deletedFileCount"]; has {
		t.Fatalf("deletedFileCount must be omitted when 0: %v", m)
	}
	e.expectErr("GET", "/conversations/"+id, e.u1, nil, 404, "conversation.not_found")
	e.expectErr("DELETE", "/conversations/"+id, e.u1, nil, 404, "conversation.not_found")
	w := e.call("GET", "/conversations?status=all", e.u1, nil)
	if total, _ := results(t, w); total != 0 {
		t.Fatalf("deleted must vanish: %d", total)
	}
	a := e.audit.find("delete_conversation")
	if a == nil || a.Detail["delete_files"] != false {
		t.Fatalf("audit: %+v", a)
	}
}

func TestSetProjectSingleAndBatch(t *testing.T) {
	e := newEnv(t)
	p := e.ok("POST", "/conversation-projects", e.u1, map[string]any{"name": "P"})
	pid := str(p, "publicID")
	a, b, c := e.newConv(e.u1, "a", ""), e.newConv(e.u1, "b", ""), e.newConv(e.u1, "c", "")
	m := e.ok("PATCH", "/conversations/"+a+"/project", e.u1, map[string]string{"projectID": pid})
	if str(m, "projectID") != pid {
		t.Fatalf("assign: %v", m)
	}
	m = e.ok("PATCH", "/conversations/"+a+"/project", e.u1, map[string]string{"projectID": "unassigned"})
	if str(m, "projectID") != "" {
		t.Fatalf("unassign: %v", m)
	}
	e.ok("PATCH", "/conversations/"+a+"/project", e.u1, map[string]string{"projectID": pid})
	m = e.ok("PATCH", "/conversations/"+a+"/project", e.u1, map[string]string{})
	if str(m, "projectID") != "" {
		t.Fatalf("empty clears: %v", m)
	}
	e.expectErr("PATCH", "/conversations/"+a+"/project", e.u1, map[string]string{"projectID": "nope"}, 404, "conversation_project.not_found")
	e.expectErr("PATCH", "/conversations/zzz/project", e.u1, map[string]string{"projectID": pid}, 404, "conversation.not_found")
	e.expectErr("PATCH", "/conversations/"+a+"/project", e.u2, map[string]string{"projectID": pid}, 404, "")

	// batch
	m = e.ok("POST", "/conversations/project", e.u1, map[string]any{"conversationPublicIDs": []string{a, b, c}, "projectID": pid})
	if num(m, "updated") != 3 {
		t.Fatalf("batch: %v", m)
	}
	e.expectErr("POST", "/conversations/project", e.u1, map[string]any{"conversationPublicIDs": []string{a, a}, "projectID": pid}, 400, "request.invalid_conversation_project")
	e.expectErr("POST", "/conversations/project", e.u1, map[string]any{"conversationPublicIDs": []string{a, " "}, "projectID": pid}, 400, "request.invalid_conversation_project")
	e.expectErr("POST", "/conversations/project", e.u1, map[string]any{"conversationPublicIDs": []string{}, "projectID": pid}, 400, "")
	e.expectErr("POST", "/conversations/project", e.u1, map[string]any{"conversationPublicIDs": []string{a}, "projectID": "nope"}, 404, "conversation_project.not_found")
	// partial match -> 404 but applied updates remain
	other := e.newConv(e.u2, "o", "")
	e.ok("POST", "/conversations/project", e.u1, map[string]any{"conversationPublicIDs": []string{a}, "projectID": "unassigned"})
	e.expectErr("POST", "/conversations/project", e.u1, map[string]any{"conversationPublicIDs": []string{a, other}, "projectID": pid}, 404, "conversation.not_found")
	if got := e.convRow(a); got.ProjectID == nil {
		t.Fatal("applied update must remain after partial failure")
	}
	if got := e.convRow(other); got.ProjectID != nil {
		t.Fatal("other user's conversation must not be touched")
	}
	e.expectErr("POST", "/conversations/project", e.u1, map[string]any{"conversationPublicIDs": make([]string, 1001), "projectID": pid}, 400, "request.invalid_body")
}

func TestDefaultModelCandidate(t *testing.T) {
	e := newEnv(t)
	m := e.ok("GET", "/conversations/default-model-candidate", e.u1, nil)
	if str(m, "platformModelName") != "" || str(m, "source") != "" || m["usedAt"] != nil {
		t.Fatalf("empty candidate: %v", m)
	}
	e.set.mutate(func(r *RuntimeSettings) { r.DefaultNewConversationModel = "gpt-4o" })
	m = e.ok("GET", "/conversations/default-model-candidate", e.u1, nil)
	if str(m, "platformModelName") != "gpt-4o" || str(m, "source") != "system_default" || m["usedAt"] != nil {
		t.Fatalf("candidate: %v", m)
	}
}

func TestRegenerateTitle(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "old", "gpt-4o")
	// no content -> 400
	e.expectErr("POST", "/conversations/"+id+"/title/regenerate", e.u1, nil, 400, "conversation.no_titleable_content")

	conv := e.convRow(id)
	u := e.seedMsg(conv, roleUser, "Tell me about Go generics please", msgSuccess, nil)
	e.seedMsg(conv, roleAssistant, "Sure, here it is", msgSuccess, u)
	e.seedMsg(conv, roleUser, "pending one", msgPending, nil)

	e.gen.set(func(_ context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		return GenerateResult{Content: "```json\n{'title': \"  Go   Generics \"}\n```", Usage: Usage{Input: 3, Output: 2}}, nil
	})
	m := e.ok("POST", "/conversations/"+id+"/title/regenerate", e.u1, nil)
	if str(m, "title") != "Go Generics" {
		t.Fatalf("title: %v", m)
	}
	reqs := e.gen.requests()
	if len(reqs) != 1 || reqs[0].Task != taskTitle {
		t.Fatalf("task: %v", e.gen.tasks())
	}
	if len(e.bill.charges()) != 1 || e.bill.charges()[0].Service != "title" {
		t.Fatalf("title must be billed as a basic service: %+v", e.bill.charges())
	}
	if e.audit.find("regenerate_conversation_title") == nil {
		t.Fatal("audit")
	}

	// generation failure -> fallback to first 15 chars of most recent non-pending user message
	e.gen.set(func(_ context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		return GenerateResult{}, ErrUpstream
	})
	m = e.ok("POST", "/conversations/"+id+"/title/regenerate", e.u1, nil)
	if str(m, "title") != "Tell me about G" {
		t.Fatalf("fallback title: %q", str(m, "title"))
	}
	// empty result -> fallback too
	e.gen.set(func(_ context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		return GenerateResult{Content: "   "}, nil
	})
	m = e.ok("POST", "/conversations/"+id+"/title/regenerate", e.u1, nil)
	if !strings.HasPrefix(str(m, "title"), "Tell me about G") {
		t.Fatalf("empty fallback: %q", str(m, "title"))
	}
	// a long generated title is capped at 80 characters
	e.gen.set(func(_ context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		return GenerateResult{Content: `{"title":"` + strings.Repeat("a", 200) + `"}`}, nil
	})
	m = e.ok("POST", "/conversations/"+id+"/title/regenerate", e.u1, nil)
	if len([]rune(str(m, "title"))) != 80 {
		t.Fatalf("cap: %d", len([]rune(str(m, "title"))))
	}
}

func TestRegenerateTitleCustomPromptAndTaskModel(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "old", "gpt-4o")
	conv := e.convRow(id)
	e.seedMsg(conv, roleUser, "alpha beta", msgSuccess, nil)
	e.set.mutate(func(r *RuntimeSettings) {
		r.TitlePrompt = "Name it: {{MESSAGES}} -- end"
		r.TaskModel = "claude-sonnet"
	})
	e.ok("POST", "/conversations/"+id+"/title/regenerate", e.u1, nil)
	req := e.gen.requests()[0]
	if req.Model != "claude-sonnet" {
		t.Fatalf("task model: %q", req.Model)
	}
	joined := lastUserText(req) + req.System
	if !strings.Contains(joined, "Name it:") || !strings.Contains(joined, "alpha beta") || strings.Contains(joined, "{{MESSAGES}}") {
		t.Fatalf("prompt template not applied: %q", joined)
	}
	// template without placeholder: messages appended after two newlines
	e.set.mutate(func(r *RuntimeSettings) { r.TitlePrompt = "Just a title" })
	e.ok("POST", "/conversations/"+id+"/title/regenerate", e.u1, nil)
	req = e.gen.requests()[1]
	joined = lastUserText(req) + req.System
	if !strings.Contains(joined, "Just a title\n\n") || !strings.Contains(joined, "alpha beta") {
		t.Fatalf("append: %q", joined)
	}
}

func TestRateLimitedGroupsDoNotBreakNormalUse(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 30; i++ {
		testkit.Expect(t, e.call("GET", "/conversations", e.u1, nil), 200, "")
	}
}
