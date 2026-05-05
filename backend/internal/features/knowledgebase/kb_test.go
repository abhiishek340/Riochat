package knowledgebase

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

const api = "/api/v1"

func TestMigrationsAreIdempotent(t *testing.T) {
	te := newTestEnv(t)
	for i := 0; i < 2; i++ {
		for _, st := range Migrations() {
			if err := st.Up(t.Context(), te.DB); err != nil {
				t.Fatalf("step %s run %d: %v", st.Name, i, err)
			}
		}
	}
}

func TestRequiresAuthentication(t *testing.T) {
	te := newTestEnv(t)
	testkit.Expect(t, te.Do("GET", api+"/knowledge-bases", nil, nil), 401, "")
	testkit.Expect(t, te.Do("GET", api+"/admin/knowledge-bases", nil, nil), 401, "")
	// a normal user is stopped by the admin gate before reaching the area
	testkit.Expect(t, te.Do("GET", api+"/admin/knowledge-bases", te.user(5), nil), 403, "auth.admin_required")
	testkit.Expect(t, te.Do("POST", api+"/admin/knowledge-bases/files/embeddings", te.user(5), map[string]any{"fileIDs": []string{"a"}}), 403, "")
}

// Scenario 1.
func TestCreatePersonalTrimsAndNumbersSortOrder(t *testing.T) {
	te := newTestEnv(t)
	w := te.Do("POST", api+"/knowledge-bases/mine", te.user(11), map[string]any{"name": "  Notes  ", "description": "d"})
	testkit.Expect(t, w, 200, "")
	var out struct{ KnowledgeBase kbOut }
	testkit.Decode(t, w, &out)
	kb := out.KnowledgeBase
	if kb.Name != "Notes" || kb.Scope != "user" || !kb.Enabled || kb.Revision != 1 || kb.SortOrder != 1 {
		t.Fatalf("created: %+v", kb)
	}
	if kb.FileCount != 0 || kb.ReadyFileCount != 0 || kb.ProcessingFileCount != 0 {
		t.Fatalf("counts: %+v", kb)
	}
	if len(kb.PublicID) != 32 || strings.Contains(kb.PublicID, "-") {
		t.Fatalf("public id: %q", kb.PublicID)
	}
	if kb.CreatedAt.IsZero() || kb.UpdatedAt.IsZero() {
		t.Fatalf("timestamps: %+v", kb)
	}
	if second := te.createMine(11, "Second"); second.SortOrder != 2 {
		t.Fatalf("second sort order: %d", second.SortOrder)
	}
	// the stored row carries the acting user and owner
	row := te.reload(kb)
	if row.OwnerUserID != 11 || row.CreatedByUserID != 11 || row.UpdatedByUserID != 11 || row.Description != "d" {
		t.Fatalf("row: %+v", row)
	}
	// numeric ids are never exposed
	if strings.Contains(w.Body.String(), `"id"`) {
		t.Fatalf("body leaks numeric id: %s", w.Body)
	}
}

// Scenarios 2 and 27.
func TestCreateDuplicateNameIsPerOwner(t *testing.T) {
	te := newTestEnv(t)
	te.createMine(11, "Notes")
	w := te.Do("POST", api+"/knowledge-bases/mine", te.user(11), map[string]any{"name": "Notes"})
	testkit.Expect(t, w, 409, "knowledge_base.conflict")
	if te.Do("POST", api+"/knowledge-bases/mine", te.user(12), map[string]any{"name": "Notes"}).Code != 200 {
		t.Fatal("another user may reuse the name")
	}
	// names are compared exactly
	if te.Do("POST", api+"/knowledge-bases/mine", te.user(11), map[string]any{"name": "notes"}).Code != 200 {
		t.Fatal("different case is a different name")
	}
}

// Scenario 3 and the length edge cases.
func TestCreateValidation(t *testing.T) {
	te := newTestEnv(t)
	u := te.user(11)
	post := func(body any) int { return te.Do("POST", api+"/knowledge-bases/mine", u, body).Code }

	testkit.Expect(t, te.Do("POST", api+"/knowledge-bases/mine", u, map[string]any{"name": repeat("a", 81)}), 400, "request.invalid_body")
	testkit.Expect(t, te.Do("POST", api+"/knowledge-bases/mine", u, map[string]any{"name": "   "}), 400, "knowledge_base.invalid")
	testkit.Expect(t, te.Do("POST", api+"/knowledge-bases/mine", u, map[string]any{"name": ""}), 400, "request.invalid_body")
	testkit.Expect(t, te.Do("POST", api+"/knowledge-bases/mine", u, map[string]any{}), 400, "request.invalid_body")
	testkit.Expect(t, te.Do("POST", api+"/knowledge-bases/mine", u, ""), 400, "request.invalid_body")

	if c := post(map[string]any{"name": repeat("a", 80)}); c != 200 {
		t.Fatalf("80 chars: %d", c)
	}
	// multi-byte characters count once each
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	if c := post(map[string]any{"name": repeat("\u77e5", 80)}); c != 200 {
		t.Fatalf("80 multibyte chars: %d", c)
	}
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	if c := post(map[string]any{"name": repeat("\u77e5", 81)}); c != 400 {
		t.Fatalf("81 multibyte chars: %d", c)
	}
	if c := post(map[string]any{"name": "d255", "description": repeat("d", 255)}); c != 200 {
		t.Fatalf("description 255: %d", c)
	}
	if c := post(map[string]any{"name": "d256", "description": repeat("d", 256)}); c != 400 {
		t.Fatalf("description 256: %d", c)
	}
	// the service enforces the same limits after trimming
	if _, err := te.svc.createPersonal(t.Context(), 11, repeat("x", 81), ""); err == nil {
		t.Fatal("service must reject 81 chars")
	}
	if _, err := te.svc.createPersonal(t.Context(), 0, "x", ""); err == nil {
		t.Fatal("zero user id must be invalid")
	}
}

// Scenarios 4 and 5 plus the patch rules of section 6.3.
func TestPatchPersonal(t *testing.T) {
	te := newTestEnv(t)
	kb := te.createMine(11, "Notes")
	other := te.createMine(12, "Theirs")
	path := api + "/knowledge-bases/mine/" + kb.PublicID
	u := te.user(11)

	testkit.Expect(t, te.Do("PATCH", path, u, map[string]any{"enabled": false}), 400, "knowledge_base.invalid")
	testkit.Expect(t, te.Do("PATCH", path, u, map[string]any{"name": "x", "sortOrder": 9}), 400, "knowledge_base.invalid")
	testkit.Expect(t, te.Do("PATCH", path, u, map[string]any{}), 400, "knowledge_base.invalid")
	testkit.Expect(t, te.Do("PATCH", path, u, map[string]any{"name": "   "}), 400, "knowledge_base.invalid")
	testkit.Expect(t, te.Do("PATCH", path, u, map[string]any{"name": repeat("n", 81)}), 400, "request.invalid_body")
	testkit.Expect(t, te.Do("PATCH", path, u, map[string]any{"description": repeat("d", 256)}), 400, "request.invalid_body")
	testkit.Expect(t, te.Do("PATCH", api+"/knowledge-bases/mine/"+other.PublicID, u, map[string]any{"name": "mine now"}), 404, "knowledge_base.not_found")
	testkit.Expect(t, te.Do("PATCH", api+"/knowledge-bases/mine/does-not-exist", u, map[string]any{"name": "x"}), 404, "knowledge_base.not_found")
	testkit.Expect(t, te.Do("PATCH", api+"/knowledge-bases/mine/%20", u, map[string]any{"name": "x"}), 400, "knowledge_base.invalid")

	te.Clock.Advance(30 * time.Minute)
	u = te.user(11)
	w := te.Do("PATCH", path, u, map[string]any{"name": "  Renamed ", "description": "  hello  "})
	testkit.Expect(t, w, 200, "")
	var out struct{ KnowledgeBase kbOut }
	testkit.Decode(t, w, &out)
	got := out.KnowledgeBase
	if got.Name != "Renamed" || got.Description != "hello" || got.Revision != 1 || !got.Enabled {
		t.Fatalf("patched: %+v", got)
	}
	if !got.UpdatedAt.After(kb.UpdatedAt) {
		t.Fatalf("updatedAt must move: %v vs %v", got.UpdatedAt, kb.UpdatedAt)
	}

	// a whitespace-only description clears it
	w = te.Do("PATCH", path, u, map[string]any{"description": "   "})
	testkit.Expect(t, w, 200, "")
	testkit.Decode(t, w, &out)
	if out.KnowledgeBase.Description != "" {
		t.Fatalf("description not cleared: %q", out.KnowledgeBase.Description)
	}

	// renaming onto an existing name of the same owner conflicts
	te.createMine(11, "Taken")
	testkit.Expect(t, te.Do("PATCH", path, u, map[string]any{"name": "Taken"}), 409, "knowledge_base.conflict")
	// renaming to its own name is fine
	testkit.Expect(t, te.Do("PATCH", path, u, map[string]any{"name": "Renamed"}), 200, "")
	if row := te.reload(kb); row.UpdatedByUserID != 11 {
		t.Fatalf("updated_by: %d", row.UpdatedByUserID)
	}
}

// Scenarios 6 and 27: admin create, explicit false, automatic sort order, audit.
func TestAdminCreateBuiltin(t *testing.T) {
	te := newTestEnv(t)
	a := te.admin(1)
	w := te.Do("POST", api+"/admin/knowledge-bases", a, map[string]any{"name": "Policies", "enabled": false, "sortOrder": 0})
	testkit.Expect(t, w, 200, "")
	var out struct{ KnowledgeBase kbOut }
	testkit.Decode(t, w, &out)
	kb := out.KnowledgeBase
	if kb.Enabled || kb.Scope != "builtin" || kb.SortOrder != 1 || kb.Revision != 1 {
		t.Fatalf("created: %+v", kb)
	}
	row := te.reload(kb)
	if row.Enabled || row.OwnerUserID != 0 || row.CreatedByUserID != 1 {
		t.Fatalf("stored: %+v", row)
	}
	if got := te.aud.byAction("knowledge_base.create_builtin"); len(got) != 1 ||
		got[0].ResourceID != kb.PublicID || got[0].Resource != "knowledge_bases" ||
		got[0].Detail["name"] != "Policies" || got[0].ActorUserID != 1 || got[0].RequestID == "" {
		t.Fatalf("audit: %+v", got)
	}

	// enabled defaults to true; explicit positive sort orders are kept; the
	// next automatic one follows the maximum
	w = te.Do("POST", api+"/admin/knowledge-bases", a, map[string]any{"name": "Handbook", "sortOrder": 40})
	testkit.Decode(t, w, &out)
	if !out.KnowledgeBase.Enabled || out.KnowledgeBase.SortOrder != 40 {
		t.Fatalf("handbook: %+v", out.KnowledgeBase)
	}
	w = te.Do("POST", api+"/admin/knowledge-bases", a, map[string]any{"name": "FAQ", "sortOrder": -3})
	testkit.Decode(t, w, &out)
	if out.KnowledgeBase.SortOrder != 41 {
		t.Fatalf("faq sort order: %d", out.KnowledgeBase.SortOrder)
	}

	// builtin names are unique among builtin bases only
	testkit.Expect(t, te.Do("POST", api+"/admin/knowledge-bases", a, map[string]any{"name": "Policies"}), 409, "knowledge_base.conflict")
	if te.Do("POST", api+"/knowledge-bases/mine", te.user(11), map[string]any{"name": "Policies"}).Code != 200 {
		t.Fatal("a personal base may reuse a builtin name")
	}
	testkit.Expect(t, te.Do("POST", api+"/admin/knowledge-bases", a, map[string]any{"name": " "}), 400, "knowledge_base.invalid")
}

// Scenario 27 at the storage level: a second user-scope base with the same
// owner and name is a duplicate even when created directly.
func TestUniqueIndexOnScopeOwnerName(t *testing.T) {
	te := newTestEnv(t)
	te.createMine(11, "Notes")
	err := te.DB.Create(&KnowledgeBase{PublicID: "dup", Scope: ScopeUser, OwnerUserID: 11, Name: "Notes", Revision: 1}).Error
	if err == nil {
		t.Fatal("unique index (scope, owner_user_id, name) missing")
	}
}

func TestAdminPatchBuiltin(t *testing.T) {
	te := newTestEnv(t)
	a := te.admin(7)
	kb := te.createBuiltin("Policies", true)
	mine := te.createMine(11, "Mine")
	path := api + "/admin/knowledge-bases/" + kb.PublicID

	testkit.Expect(t, te.Do("PATCH", path, a, map[string]any{}), 400, "knowledge_base.invalid")
	w := te.Do("PATCH", path, a, map[string]any{"enabled": false, "sortOrder": -5, "description": "x"})
	testkit.Expect(t, w, 200, "")
	var out struct{ KnowledgeBase kbOut }
	testkit.Decode(t, w, &out)
	if out.KnowledgeBase.Enabled || out.KnowledgeBase.SortOrder != -5 || out.KnowledgeBase.Description != "x" || out.KnowledgeBase.Revision != 1 {
		t.Fatalf("patched: %+v", out.KnowledgeBase)
	}
	if row := te.reload(kb); row.UpdatedByUserID != 7 {
		t.Fatalf("updated_by: %d", row.UpdatedByUserID)
	}
	if got := te.aud.byAction("knowledge_base.update_builtin"); len(got) != 1 || got[0].ResourceID != kb.PublicID {
		t.Fatalf("audit: %+v", got)
	}
	// admin operations on a personal base are 404
	testkit.Expect(t, te.Do("PATCH", api+"/admin/knowledge-bases/"+mine.PublicID, a, map[string]any{"name": "x"}), 404, "knowledge_base.not_found")
	testkit.Expect(t, te.Do("GET", api+"/admin/knowledge-bases/"+mine.PublicID, a, nil), 404, "knowledge_base.not_found")
	testkit.Expect(t, te.Do("DELETE", api+"/admin/knowledge-bases/"+mine.PublicID, a, nil), 404, "knowledge_base.not_found")
	if !te.baseExists(mine) {
		t.Fatal("personal base must survive")
	}
}

// Scenario 7.
func TestListVisibilityAndOrder(t *testing.T) {
	te := newTestEnv(t)
	b1 := te.createBuiltin("B1", true)
	b2 := te.createBuiltin("B2", false)
	m := te.createMine(11, "M")
	o := te.createMine(12, "O")
	u := te.user(11)

	p := te.listKB(api+"/knowledge-bases", u)
	if p.Total != 2 || !equalStrings(names(p), []string{"B1", "M"}) {
		t.Fatalf("visible list: %+v", p)
	}
	admin := te.listKB(api+"/admin/knowledge-bases", te.admin(1))
	if admin.Total != 2 || !equalStrings(names(admin), []string{"B1", "B2"}) {
		t.Fatalf("admin list: %v", names(admin))
	}
	mine := te.listKB(api+"/knowledge-bases/mine", u)
	if mine.Total != 1 || mine.Results[0].PublicID != m.PublicID {
		t.Fatalf("mine: %+v", mine)
	}

	testkit.Expect(t, te.Do("GET", api+"/knowledge-bases/"+b2.PublicID, u, nil), 404, "knowledge_base.not_found")
	testkit.Expect(t, te.Do("GET", api+"/admin/knowledge-bases/"+b2.PublicID, te.admin(1), nil), 200, "")
	testkit.Expect(t, te.Do("GET", api+"/knowledge-bases/"+o.PublicID, u, nil), 404, "knowledge_base.not_found")
	testkit.Expect(t, te.Do("GET", api+"/knowledge-bases/"+b1.PublicID, u, nil), 200, "")
	testkit.Expect(t, te.Do("GET", api+"/knowledge-bases/"+m.PublicID, u, nil), 200, "")
	testkit.Expect(t, te.Do("GET", api+"/knowledge-bases/nope", u, nil), 404, "knowledge_base.not_found")
	testkit.Expect(t, te.Do("GET", api+"/knowledge-bases/%20", u, nil), 400, "knowledge_base.invalid")
	// a user cannot read a builtin base through the admin route (admin gate) and
	// vice versa an admin reading a personal base through the user route sees
	// only their own
	testkit.Expect(t, te.Do("GET", api+"/knowledge-bases/"+m.PublicID, te.admin(1), nil), 404, "knowledge_base.not_found")
}

func TestDefaultOrdering(t *testing.T) {
	te := newTestEnv(t)
	a := te.admin(1)
	// builtin, ordered by sort order
	te.Do("POST", api+"/admin/knowledge-bases", a, map[string]any{"name": "B-late", "sortOrder": 9})
	te.Do("POST", api+"/admin/knowledge-bases", a, map[string]any{"name": "B-early", "sortOrder": 2})
	te.createMine(11, "U1")
	te.createMine(11, "U2")
	p := te.listKB(api+"/knowledge-bases", te.user(11))
	if !equalStrings(names(p), []string{"B-early", "B-late", "U1", "U2"}) {
		t.Fatalf("visible order: %v", names(p))
	}
	// same sort order: most recently updated first, then newest id
	te.Do("POST", api+"/admin/knowledge-bases", a, map[string]any{"name": "B-tie-1", "sortOrder": 2})
	te.Clock.Advance(time.Minute)
	te.Do("POST", api+"/admin/knowledge-bases", a, map[string]any{"name": "B-tie-2", "sortOrder": 2})
	p = te.listKB(api+"/admin/knowledge-bases", a)
	if !equalStrings(names(p), []string{"B-tie-2", "B-tie-1", "B-early", "B-late"}) {
		t.Fatalf("admin order: %v", names(p))
	}
	// disabled bases sort after enabled ones on the admin and mine lists
	kb := te.createBuiltin("Off", false)
	p = te.listKB(api+"/admin/knowledge-bases", a)
	if p.Results[len(p.Results)-1].PublicID != kb.PublicID {
		t.Fatalf("disabled must be last: %v", names(p))
	}
}

// Scenario 8 and the other sort keys.
func TestListSorting(t *testing.T) {
	te := newTestEnv(t)
	u := te.user(11)
	zeta := te.createMine(11, "zeta")
	alpha := te.createMine(11, "Alpha")
	mid := te.createMine(11, "mid")
	te.link(zeta, te.addFile(11, "z1"), te.addFile(11, "z2"), te.addFile(11, "z3"))
	te.link(mid, te.addFile(11, "m1"))
	// an inactive and a soft-deleted file do not count
	te.link(alpha, te.addFile(11, "a1", func(o *fileObject) { o.Status = "deleted" }))
	gone := te.addFile(11, "a2")
	te.link(alpha, gone)
	te.DB.Delete(&gone)

	if p := te.listKB(api+"/knowledge-bases/mine?sort=files", u); !equalStrings(names(p), []string{"zeta", "mid", "Alpha"}) {
		t.Fatalf("files: %v", names(p))
	}
	if p := te.listKB(api+"/knowledge-bases/mine?sort=name", u); !equalStrings(names(p), []string{"Alpha", "mid", "zeta"}) {
		t.Fatalf("name: %v", names(p))
	}
	if p := te.listKB(api+"/knowledge-bases/mine?sort=created", u); !equalStrings(names(p), []string{"mid", "Alpha", "zeta"}) {
		t.Fatalf("created: %v", names(p))
	}
	te.Clock.Advance(30 * time.Minute)
	u = te.user(11)
	te.Do("PATCH", api+"/knowledge-bases/mine/"+zeta.PublicID, u, map[string]any{"description": "touched"})
	if p := te.listKB(api+"/knowledge-bases/mine?sort=updated", u); p.Results[0].Name != "zeta" {
		t.Fatalf("updated: %v", names(p))
	}
	if p := te.listKB(api+"/knowledge-bases/mine?sort=%20name%20", u); !equalStrings(names(p), []string{"Alpha", "mid", "zeta"}) {
		t.Fatalf("sort is trimmed: %v", names(p))
	}
	// unknown sort keys fall back to the default order
	if p := te.listKB(api+"/knowledge-bases/mine?sort=bogus", u); !equalStrings(names(p), []string{"zeta", "Alpha", "mid"}) {
		t.Fatalf("default: %v", names(p))
	}
	// every item carries live counts
	p := te.listKB(api+"/knowledge-bases/mine?sort=files", u)
	if p.Results[0].FileCount != 3 || p.Results[2].FileCount != 0 {
		t.Fatalf("counts: %+v", p.Results)
	}
}

func TestListFilters(t *testing.T) {
	te := newTestEnv(t)
	u := te.user(11)
	a := te.admin(1)
	x := te.createMine(11, "Quarterly Reports")
	te.Do("PATCH", api+"/knowledge-bases/mine/"+x.PublicID, u, map[string]any{"description": "finance 100%_done"})
	te.createMine(11, "Recipes")
	te.createBuiltin("Company Handbook", true)
	te.createBuiltin("Archive", false)

	if p := te.listKB(api+"/knowledge-bases?q=QUARTERLY", u); p.Total != 1 || p.Results[0].Name != "Quarterly Reports" {
		t.Fatalf("q on name: %+v", p)
	}
	if p := te.listKB(api+"/knowledge-bases?q=%20FINANCE%20", u); p.Total != 1 {
		t.Fatalf("q on description: %+v", p)
	}
	// LIKE wildcards in the keyword are literal
	if p := te.listKB(api+"/knowledge-bases?q=100%25_", u); p.Total != 1 {
		t.Fatalf("literal wildcard: %+v", p)
	}
	if p := te.listKB(api+"/knowledge-bases?q=%25", u); p.Total != 1 {
		t.Fatalf("lone percent matches only literal: %+v", p)
	}
	if p := te.listKB(api+"/knowledge-bases?q=nomatch", u); p.Total != 0 || p.Results == nil {
		t.Fatalf("no match: %+v", p)
	}

	// enabled filter applies to the admin list, ignored on the visible list
	if p := te.listKB(api+"/admin/knowledge-bases?enabled=false", a); p.Total != 1 || p.Results[0].Name != "Archive" {
		t.Fatalf("admin enabled=false: %+v", p)
	}
	if p := te.listKB(api+"/admin/knowledge-bases?enabled=true", a); p.Total != 1 || p.Results[0].Name != "Company Handbook" {
		t.Fatalf("admin enabled=true: %+v", p)
	}
	if p := te.listKB(api+"/admin/knowledge-bases?enabled=maybe", a); p.Total != 2 {
		t.Fatalf("unparseable enabled means no filter: %+v", p)
	}
	if p := te.listKB(api+"/admin/knowledge-bases?enabled=", a); p.Total != 2 {
		t.Fatalf("empty enabled means no filter: %+v", p)
	}
	if p := te.listKB(api+"/knowledge-bases?enabled=false", u); p.Total != 3 {
		t.Fatalf("visible list ignores enabled: %+v", p)
	}
	if p := te.listKB(api+"/knowledge-bases/mine?enabled=false", u); p.Total != 0 {
		t.Fatalf("personal bases are always enabled: %+v", p)
	}
}

// Scenario 9 and the id filter rules of 6.5.
func TestListIDFilter(t *testing.T) {
	te := newTestEnv(t)
	u := te.user(11)
	a := te.createMine(11, "A")
	b := te.createMine(11, "B")
	te.createMine(11, "C")

	if p := te.listKB(api+"/knowledge-bases?id="+a.PublicID+"&id="+b.PublicID, u); p.Total != 2 {
		t.Fatalf("two ids: %+v", p)
	}
	if p := te.listKB(api+"/knowledge-bases?id=%20"+a.PublicID+"%20&id="+a.PublicID, u); p.Total != 1 {
		t.Fatalf("duplicates collapse: %+v", p)
	}
	if p := te.listKB(api+"/knowledge-bases?id=%20&id=", u); p.Total != 0 || p.Results == nil {
		t.Fatalf("all blank: %+v", p)
	}
	if p := te.listKB(api+"/knowledge-bases?id="+a.PublicID+"&id=%20", u); p.Total != 1 {
		t.Fatalf("blank ignored: %+v", p)
	}
	var q []string
	for i := 0; i < 9; i++ {
		q = append(q, fmt.Sprintf("id=id%d", i))
	}
	w := te.Do("GET", api+"/knowledge-bases?"+strings.Join(q, "&"), u, nil)
	testkit.Expect(t, w, 200, "")
	var p kbPage
	testkit.Decode(t, w, &p)
	if p.Total != 0 || p.Results == nil || len(p.Results) != 0 {
		t.Fatalf("9 ids: %s", w.Body)
	}
	// 8 distinct ids are fine
	if p := te.listKB(api+"/knowledge-bases?"+strings.Join(q[:8], "&"), u); p.Total != 0 {
		t.Fatalf("8 unknown ids: %+v", p)
	}
	// the admin list honors the id filter as well
	bl := te.createBuiltin("Policies", true)
	if p := te.listKB(api+"/admin/knowledge-bases?id="+bl.PublicID, te.admin(1)); p.Total != 1 {
		t.Fatalf("admin id filter: %+v", p)
	}
}

func TestPagination(t *testing.T) {
	te := newTestEnv(t)
	u := te.user(11)
	for i := 1; i <= 25; i++ {
		te.createMine(11, fmt.Sprintf("kb-%02d", i))
	}
	p := te.listKB(api+"/knowledge-bases/mine?sort=name", u)
	if p.Total != 25 || len(p.Results) != 20 {
		t.Fatalf("default page: total=%d len=%d", p.Total, len(p.Results))
	}
	p = te.listKB(api+"/knowledge-bases/mine?sort=name&page=2&page_size=20", u)
	if p.Total != 25 || len(p.Results) != 5 || p.Results[0].Name != "kb-21" {
		t.Fatalf("page 2: %+v", names(p))
	}
	if p = te.listKB(api+"/knowledge-bases/mine?page=0&page_size=-1", u); len(p.Results) != 20 {
		t.Fatalf("invalid values fall back: %d", len(p.Results))
	}
	if p = te.listKB(api+"/knowledge-bases/mine?page_size=100000", u); len(p.Results) != 25 {
		t.Fatalf("oversize page size is clamped, not rejected: %d", len(p.Results))
	}
	if p = te.listKB(api+"/knowledge-bases/mine?page=9", u); p.Total != 25 || len(p.Results) != 0 || p.Results == nil {
		t.Fatalf("beyond last page: %+v", p)
	}
}

// Scenario 10.
func TestFeatureSwitch(t *testing.T) {
	te := newTestEnv(t)
	kb := te.createMine(11, "Notes")
	builtin := te.createBuiltin("B", true)
	te.settings.set(true)
	u := te.user(11)

	userRoutes := []struct{ method, path string }{
		{"GET", "/knowledge-bases"},
		{"GET", "/knowledge-bases/mine"},
		{"POST", "/knowledge-bases/mine"},
		{"PATCH", "/knowledge-bases/mine/" + kb.PublicID},
		{"DELETE", "/knowledge-bases/mine/" + kb.PublicID},
		{"GET", "/knowledge-bases/" + builtin.PublicID},
		{"GET", "/knowledge-bases/" + builtin.PublicID + "/files"},
		{"POST", "/knowledge-bases/" + builtin.PublicID + "/files/processing/statuses"},
		{"POST", "/knowledge-bases/" + builtin.PublicID + "/files/processing/snapshot"},
		{"GET", "/knowledge-bases/" + builtin.PublicID + "/files/f/content"},
		{"GET", "/knowledge-bases/mine/" + kb.PublicID + "/available-files"},
		{"POST", "/knowledge-bases/mine/" + kb.PublicID + "/files"},
		{"DELETE", "/knowledge-bases/mine/" + kb.PublicID + "/files/f"},
	}
	for _, r := range userRoutes {
		w := te.Do(r.method, api+r.path, u, map[string]any{"name": "x", "fileIDs": []string{"f"}})
		testkit.Expect(t, w, 403, "knowledge_base.disabled")
		if msg := testkit.Envelope(t, w).ErrorMsg; msg != "knowledge base feature is disabled" {
			t.Fatalf("%s %s message: %q", r.method, r.path, msg)
		}
	}
	if !te.baseExists(kb) {
		t.Fatal("a refused delete must not delete")
	}
	// administrator routes are not gated
	a := te.admin(1)
	testkit.Expect(t, te.Do("GET", api+"/admin/knowledge-bases", a, nil), 200, "")
	testkit.Expect(t, te.Do("GET", api+"/admin/knowledge-bases/"+builtin.PublicID, a, nil), 200, "")
	testkit.Expect(t, te.Do("GET", api+"/admin/knowledge-bases/files", a, nil), 200, "")

	te.settings.set(false)
	testkit.Expect(t, te.Do("GET", api+"/knowledge-bases", u, nil), 200, "")
}

// The default settings reader looks at system_settings.
func TestDefaultSettingsReadSystemSettings(t *testing.T) {
	te := newTestEnv(t)
	s := tableSettings{db: te.DB}
	ctx := t.Context()
	if !s.Enabled(ctx) || s.MaxUploadBytes(ctx) != 0 {
		t.Fatal("without the table: enabled, no cap")
	}
	if err := te.DB.Exec(`CREATE TABLE system_settings (id integer primary key autoincrement, namespace varchar(32) not null, "key" varchar(128) not null, value text not null, value_type varchar(16) not null, description varchar(255) not null default '', created_at datetime, updated_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	if !s.Enabled(ctx) {
		t.Fatal("without the row: enabled")
	}
	te.DB.Exec(`INSERT INTO system_settings (namespace, "key", value, value_type) VALUES ('knowledgebase','enabled','false','bool'), ('storage','max_upload_file_bytes','1234','int')`)
	if s.Enabled(ctx) {
		t.Fatal("row says false")
	}
	if s.MaxUploadBytes(ctx) != 1234 {
		t.Fatalf("max upload: %d", s.MaxUploadBytes(ctx))
	}
	te.DB.Exec(`UPDATE system_settings SET value = 'true' WHERE "key" = 'enabled'`)
	if !s.Enabled(ctx) {
		t.Fatal("row says true")
	}
	te.DB.Exec(`UPDATE system_settings SET value = 'x' WHERE "key" = 'max_upload_file_bytes'`)
	if s.MaxUploadBytes(ctx) != 0 {
		t.Fatal("garbage means unset")
	}

	// and the HTTP gate follows it with the real default settings
	te2 := newTestEnv(t)
	te2.DB.Exec(`CREATE TABLE system_settings (id integer primary key autoincrement, namespace varchar(32) not null, "key" varchar(128) not null, value text not null, value_type varchar(16) not null, description varchar(255) not null default '', created_at datetime, updated_at datetime)`)
	te2.svc.ports.Settings = tableSettings{db: te2.DB}
	testkit.Expect(t, te2.Do("GET", api+"/knowledge-bases", te2.user(1), nil), 200, "")
	te2.DB.Exec(`INSERT INTO system_settings (namespace, "key", value, value_type) VALUES ('knowledgebase','enabled','false','bool')`)
	testkit.Expect(t, te2.Do("GET", api+"/knowledge-bases", te2.user(1), nil), 403, "knowledge_base.disabled")
}

func TestRouteDisambiguation(t *testing.T) {
	te := newTestEnv(t)
	a := te.admin(1)
	// "mine" and "files" are literal segments, never captured as ids
	testkit.Expect(t, te.Do("GET", api+"/knowledge-bases/mine", te.user(1), nil), 200, "")
	p := te.listKB(api+"/admin/knowledge-bases/files", a)
	if p.Total != 0 {
		t.Fatalf("pool list: %+v", p)
	}
	testkit.Expect(t, te.Do("GET", api+"/admin/knowledge-bases/files/nope/content", a, nil), 404, "file.not_found")
	testkit.Expect(t, te.Do("DELETE", api+"/admin/knowledge-bases/files/nope", a, nil), 404, "file.not_found")
	// real ids still route to the :id handlers
	kb := te.createBuiltin("Real", true)
	testkit.Expect(t, te.Do("GET", api+"/admin/knowledge-bases/"+kb.PublicID+"/files", a, nil), 200, "")
	testkit.Expect(t, te.Do("GET", api+"/admin/knowledge-bases/"+kb.PublicID+"/available-files", a, nil), 200, "")
}

func TestStorageErrorsNeverLeak(t *testing.T) {
	te := newTestEnv(t)
	te.createMine(11, "Notes")
	if err := te.DB.Exec("DROP TABLE knowledge_bases").Error; err != nil {
		t.Fatal(err)
	}
	w := te.Do("GET", api+"/knowledge-bases/mine", te.user(11), nil)
	testkit.Expect(t, w, 500, "knowledge_base.internal")
	env := testkit.Envelope(t, w)
	if env.ErrorMsg != "knowledge base operation failed" || strings.Contains(w.Body.String(), "knowledge_bases") {
		t.Fatalf("leaked: %s", w.Body)
	}
}

func TestStatusForUnknownErrors(t *testing.T) {
	if statusFor(fmt.Errorf("boom")) != 500 {
		t.Fatal("untyped is 500")
	}
	for code, want := range map[string]int{
		"knowledge_base.disabled": 403, "knowledge_base.invalid": 400, "knowledge_base.not_found": 404,
		"knowledge_base.conflict": 409, "knowledge_base.platform_file_in_use": 409,
		"knowledge_base.file_cleanup_unavailable": 503, "knowledge_base.internal": 500,
		"knowledge_base.owner_file_reference": 409, "file.not_found": 404, "file.too_large": 413,
		"quota.exceeded": 409, "embedding.too_many_files": 400, "embedding.service_not_configured": 503,
	} {
		if statusByCode[code] != want {
			t.Errorf("%s: %d want %d", code, statusByCode[code], want)
		}
	}
}
