package conversation

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func newProject(e *tenv, hdr map[string]string, body map[string]any) map[string]any {
	e.t.Helper()
	return e.ok("POST", "/conversation-projects", hdr, body)
}

func TestProjectCreateAndShape(t *testing.T) {
	e := newEnv(t)
	p := newProject(e, e.u1, map[string]any{"name": "  Alpha  ", "description": " d ", "systemPrompt": " sp ", "color": "red", "icon": "star"})
	if str(p, "name") != "Alpha" || str(p, "description") != "d" || str(p, "systemPrompt") != "sp" ||
		str(p, "status") != "active" || str(p, "mcpDefaultMode") != "inherit" || num(p, "sortOrder") != 0 {
		t.Fatalf("project: %v", p)
	}
	for _, k := range []string{"defaultMCPToolIDs", "defaultSkillIDs", "defaultKnowledgeBaseIDs"} {
		arr, ok := p[k].([]any)
		if !ok || len(arr) != 0 {
			t.Fatalf("%s must be a non-null empty array: %v", k, p[k])
		}
	}
	if len(str(p, "publicID")) != 32 {
		t.Fatalf("public id: %v", p)
	}
	if e.audit.find("create_conversation_project") == nil {
		t.Fatal("audit")
	}
}

func TestProjectValidation(t *testing.T) {
	e := newEnv(t)
	bad := func(body map[string]any, code string) {
		t.Helper()
		e.expectErr("POST", "/conversation-projects", e.u1, body, 400, code)
	}
	bad(map[string]any{}, "request.invalid_body")
	bad(map[string]any{"name": strings.Repeat("n", 81)}, "request.invalid_body")
	bad(map[string]any{"name": "x", "description": strings.Repeat("d", 256)}, "request.invalid_body")
	bad(map[string]any{"name": "x", "systemPrompt": strings.Repeat("d", 12001)}, "request.invalid_body")
	bad(map[string]any{"name": "x", "mcpDefaultMode": "weird"}, "request.invalid_body")
	bad(map[string]any{"name": "x", "defaultKnowledgeBaseIDs": make([]string, 9)}, "request.invalid_body")
	bad(map[string]any{"name": "x", "defaultKnowledgeBaseIDs": []string{""}}, "request.invalid_body")
	bad(map[string]any{"name": "x", "defaultMCPToolIDs": make([]uint, 129)}, "request.invalid_body")
	bad(map[string]any{"name": "x", "color": strings.Repeat("c", 33)}, "request.invalid_body")
	// service-level
	bad(map[string]any{"name": "   "}, "request.invalid_conversation_project")
	bad(map[string]any{"name": "x", "defaultKnowledgeBaseIDs": []string{"a", "a"}}, "request.invalid_conversation_project")
	bad(map[string]any{"name": "x", "defaultKnowledgeBaseIDs": []string{" "}}, "")
	if w := e.call("POST", "/conversation-projects", e.u1, map[string]any{"name": "x", "defaultModel": "unavailable-model"}); w.Code != 400 {
		t.Fatalf("unavailable default model: %d %s", w.Code, w.Body)
	}
	// rune counting: 80 Chinese characters is fine
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	newProject(e, e.u1, map[string]any{"name": strings.Repeat("\u540d", 80)})
}

func TestProjectModesAndLinks(t *testing.T) {
	e := newEnv(t)
	// inherit forces tools empty
	p := newProject(e, e.u1, map[string]any{"name": "a", "mcpDefaultMode": "inherit", "defaultMCPToolIDs": []uint{1, 2}})
	if str(p, "mcpDefaultMode") != "inherit" || len(p["defaultMCPToolIDs"].([]any)) != 0 {
		t.Fatalf("inherit: %v", p)
	}
	// custom keeps order and dedupes
	p = newProject(e, e.u1, map[string]any{"name": "b", "mcpDefaultMode": "custom", "defaultMCPToolIDs": []uint{3, 1, 3, 2}, "defaultSkillIDs": []uint{0, 5, 5, 4}, "defaultKnowledgeBaseIDs": []string{" kb1 ", "kb2"}})
	if fmt.Sprint(p["defaultMCPToolIDs"]) != "[3 1 2]" || fmt.Sprint(p["defaultSkillIDs"]) != "[5 4]" || fmt.Sprint(p["defaultKnowledgeBaseIDs"]) != "[kb1 kb2]" {
		t.Fatalf("links: %v", p)
	}
	pid := str(p, "publicID")
	// update to inherit clears tools
	p = e.ok("PATCH", "/conversation-projects/"+pid, e.u1, map[string]any{"mcpDefaultMode": "inherit"})
	if len(p["defaultMCPToolIDs"].([]any)) != 0 || fmt.Sprint(p["defaultSkillIDs"]) != "[5 4]" {
		t.Fatalf("mode switch: %v", p)
	}
	// capability validation failures are invalid projects
	e.caps.toolErr = ErrMultipleImageProcs
	e.expectErr("PATCH", "/conversation-projects/"+pid, e.u1, map[string]any{"mcpDefaultMode": "custom", "defaultMCPToolIDs": []uint{9}}, 400, "request.invalid_conversation_project")
	e.caps.toolErr = nil
	e.caps.skillErr = errors.New("nope")
	w := e.call("PATCH", "/conversation-projects/"+pid, e.u1, map[string]any{"defaultSkillIDs": []uint{77}})
	if w.Code == 200 {
		t.Fatal("skill validation must fail")
	}
	e.caps.skillErr = nil
	// KB disabled
	e.set.mutate(func(r *RuntimeSettings) { r.KnowledgeBaseEnabled = false })
	e.expectErr("PATCH", "/conversation-projects/"+pid, e.u1, map[string]any{"defaultKnowledgeBaseIDs": []string{"kb9"}}, 400, "request.invalid_conversation_project")
	e.set.mutate(func(r *RuntimeSettings) { r.KnowledgeBaseEnabled = true })
	// per-message maximum of tools
	e.set.mutate(func(r *RuntimeSettings) { r.MaxSelectedTools = 2 })
	e.expectErr("PATCH", "/conversation-projects/"+pid, e.u1, map[string]any{"mcpDefaultMode": "custom", "defaultMCPToolIDs": []uint{1, 2, 3}}, 400, "request.invalid_conversation_project")
	e.expectErr("PATCH", "/conversation-projects/"+pid, e.u1, map[string]any{"defaultSkillIDs": []uint{1, 2, 3}}, 400, "request.invalid_conversation_project")
}

func TestProjectUpdate(t *testing.T) {
	e := newEnv(t)
	p := newProject(e, e.u1, map[string]any{"name": "orig", "description": "keep"})
	pid := str(p, "publicID")
	p = e.ok("PATCH", "/conversation-projects/"+pid, e.u1, map[string]any{"name": " renamed ", "status": "archived", "defaultModel": "gpt-4o"})
	if str(p, "name") != "renamed" || str(p, "status") != "archived" || str(p, "description") != "keep" || str(p, "defaultModel") != "gpt-4o" {
		t.Fatalf("update: %v", p)
	}
	e.expectErr("PATCH", "/conversation-projects/"+pid, e.u1, map[string]any{}, 400, "request.invalid_conversation_project")
	e.expectErr("PATCH", "/conversation-projects/"+pid, e.u1, map[string]any{"name": "  "}, 400, "request.invalid_conversation_project")
	e.expectErr("PATCH", "/conversation-projects/"+pid, e.u1, map[string]any{"status": "weird"}, 400, "request.invalid_body")
	e.expectErr("PATCH", "/conversation-projects/"+pid, e.u2, map[string]any{"name": "x"}, 404, "conversation_project.not_found")
	e.expectErr("PATCH", "/conversation-projects/unknown", e.u1, map[string]any{"name": "x"}, 404, "conversation_project.not_found")
	e.expectErr("PATCH", "/conversation-projects/%20", e.u1, map[string]any{"name": "x"}, 400, "conversation_project.invalid_id")
	// default model unchanged is not rechecked
	e.cat.mu.Lock()
	e.cat.available["gpt-4o"] = false
	e.cat.mu.Unlock()
	e.ok("PATCH", "/conversation-projects/"+pid, e.u1, map[string]any{"description": "still fine"})
	e.expectErr("PATCH", "/conversation-projects/"+pid, e.u1, map[string]any{"defaultModel": "bad-model"}, 400, "request.invalid_conversation_project")
	// clearing the default model is allowed
	p = e.ok("PATCH", "/conversation-projects/"+pid, e.u1, map[string]any{"defaultModel": ""})
	if str(p, "defaultModel") != "" {
		t.Fatalf("clear model: %v", p)
	}
}

func TestProjectListAndOrder(t *testing.T) {
	e := newEnv(t)
	a := str(newProject(e, e.u1, map[string]any{"name": "a"}), "publicID")
	b := str(newProject(e, e.u1, map[string]any{"name": "b"}), "publicID")
	c := str(newProject(e, e.u1, map[string]any{"name": "c"}), "publicID")
	newProject(e, e.u2, map[string]any{"name": "theirs"})
	// all sort_order 0 -> id desc
	w := e.call("GET", "/conversation-projects", e.u1, nil)
	l := dataList(t, w)
	if len(l) != 3 || str(l[0], "publicID") != c || str(l[2], "publicID") != a {
		t.Fatalf("default order: %v", l)
	}
	// reorder
	w = e.call("POST", "/conversation-projects/reorder", e.u1, map[string]any{"projectIDs": []string{b, c, a}})
	l = dataList(t, w)
	if len(l) != 3 || str(l[0], "publicID") != b || num(l[0], "sortOrder") != 1 || str(l[2], "publicID") != a || num(l[2], "sortOrder") != 3 {
		t.Fatalf("reordered: %v", l)
	}
	// archived filter
	e.ok("PATCH", "/conversation-projects/"+a, e.u1, map[string]any{"status": "archived"})
	if l = dataList(t, e.call("GET", "/conversation-projects", e.u1, nil)); len(l) != 2 {
		t.Fatalf("active: %v", l)
	}
	if l = dataList(t, e.call("GET", "/conversation-projects?status=archived", e.u1, nil)); len(l) != 1 || str(l[0], "publicID") != a {
		t.Fatalf("archived: %v", l)
	}
	if l = dataList(t, e.call("GET", "/conversation-projects?status=all", e.u1, nil)); len(l) != 3 {
		t.Fatalf("all: %v", l)
	}
	if l = dataList(t, e.call("GET", "/conversation-projects?status=zzz", e.u1, nil)); len(l) != 2 {
		t.Fatalf("unknown -> active: %v", l)
	}
	// empty list is [] not null
	w = e.call("GET", "/conversation-projects", e.Bearer(userClaims(77)), nil)
	if !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Fatalf("must be empty array: %s", w.Body)
	}
}

func TestProjectReorderValidation(t *testing.T) {
	e := newEnv(t)
	a := str(newProject(e, e.u1, map[string]any{"name": "a"}), "publicID")
	b := str(newProject(e, e.u1, map[string]any{"name": "b"}), "publicID")
	theirs := str(newProject(e, e.u2, map[string]any{"name": "t"}), "publicID")
	e.expectErr("POST", "/conversation-projects/reorder", e.u1, map[string]any{"projectIDs": []string{a, a}}, 400, "request.invalid_conversation_project")
	e.expectErr("POST", "/conversation-projects/reorder", e.u1, map[string]any{"projectIDs": []string{a, " "}}, 400, "request.invalid_conversation_project")
	e.expectErr("POST", "/conversation-projects/reorder", e.u1, map[string]any{"projectIDs": []string{}}, 400, "")
	e.expectErr("POST", "/conversation-projects/reorder", e.u1, map[string]any{}, 400, "request.invalid_body")
	e.expectErr("POST", "/conversation-projects/reorder", e.u1, map[string]any{"projectIDs": make([]string, 201)}, 400, "request.invalid_body")
	// not owned -> 404 and nothing changes
	e.expectErr("POST", "/conversation-projects/reorder", e.u1, map[string]any{"projectIDs": []string{b, a, theirs}}, 404, "conversation_project.not_found")
	var rows []Project
	e.DB.Where("user_id = 1").Find(&rows)
	for _, r := range rows {
		if r.SortOrder != 0 {
			t.Fatalf("transaction must roll back: %+v", r)
		}
	}
}

func TestProjectLimit(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 200; i++ {
		e.DB.Create(&Project{UserID: 1, PublicID: newPublicID(), Name: fmt.Sprintf("p%d", i), MCPDefaultMode: "inherit", Status: "active"})
	}
	e.expectErr("POST", "/conversation-projects", e.u1, map[string]any{"name": "201st"}, 400, "conversation.project_limit_exceeded")
	// other users are unaffected
	newProject(e, e.u2, map[string]any{"name": "fine"})
}

func TestProjectDeleteDetachesOrDeletes(t *testing.T) {
	e := newEnv(t)
	pid := str(newProject(e, e.u1, map[string]any{"name": "p", "mcpDefaultMode": "custom", "defaultMCPToolIDs": []uint{1}, "defaultSkillIDs": []uint{2}, "defaultKnowledgeBaseIDs": []string{"kb"}}), "publicID")
	c1 := e.newConv(e.u1, "c1", "")
	c2 := e.newConv(e.u1, "c2", "")
	e.ok("POST", "/conversations/project", e.u1, map[string]any{"conversationPublicIDs": []string{c1, c2}, "projectID": pid})

	m := e.ok("DELETE", "/conversation-projects/"+pid, e.u1, nil)
	if m["deleted"] != true {
		t.Fatalf("delete: %v", m)
	}
	g := e.ok("GET", "/conversations/"+c1, e.u1, nil)
	if str(g, "projectID") != "" {
		t.Fatalf("conversation must be detached: %v", g)
	}
	var n int64
	e.DB.Model(&ProjectMCPTool{}).Count(&n)
	e.DB.Model(&ProjectSkill{}).Count(&n)
	if n != 0 {
		t.Fatalf("link rows must be removed: %d", n)
	}
	var links int64
	e.DB.Model(&ProjectKnowledgeBase{}).Count(&links)
	if links != 0 {
		t.Fatalf("kb links: %d", links)
	}
	e.expectErr("DELETE", "/conversation-projects/"+pid, e.u1, nil, 404, "conversation_project.not_found")

	// delete with conversations
	p2 := str(newProject(e, e.u1, map[string]any{"name": "p2"}), "publicID")
	c3 := e.newConv(e.u1, "c3", "")
	e.ok("PATCH", "/conversations/"+c3+"/project", e.u1, map[string]string{"projectID": p2})
	e.ok("DELETE", "/conversation-projects/"+p2+"?delete_conversations=true", e.u1, nil)
	e.expectErr("GET", "/conversations/"+c3, e.u1, nil, 404, "conversation.not_found")
	e.ok("GET", "/conversations/"+c2, e.u1, nil)
	// not owned
	p3 := str(newProject(e, e.u1, map[string]any{"name": "p3"}), "publicID")
	e.expectErr("DELETE", "/conversation-projects/"+p3, e.u2, nil, 404, "conversation_project.not_found")
	// delete_files is ignored without delete_conversations
	a := e.audit.find("delete_conversation_project")
	if a == nil {
		t.Fatal("audit")
	}
	e.ok("DELETE", "/conversation-projects/"+p3+"?delete_files=true", e.u1, nil)
}
