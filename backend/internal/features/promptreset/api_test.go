package promptreset

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/testkit"
)

var (
	alice = testkit.User(1)
	bob   = testkit.User(2)
	root  = testkit.Admin(9)
)

func TestAuthRequiredEverywhere(t *testing.T) {
	env := newEnv(t)
	for _, c := range []struct{ method, url string }{
		{"GET", userBase}, {"GET", mineURL}, {"POST", mineURL}, {"PATCH", mineURL + "/1"}, {"DELETE", mineURL + "/1"},
		{"GET", adminBase}, {"POST", adminBase}, {"PATCH", adminBase + "/1"}, {"DELETE", adminBase + "/1"},
	} {
		testkit.Expect(t, env.Do(c.method, c.url, nil, nil), 401, "")
	}
}

func TestNonAdminForbiddenOnAdminRoutes(t *testing.T) {
	env := newEnv(t)
	u := env.Bearer(alice)
	testkit.Expect(t, env.Do("GET", adminBase, u, nil), 403, "auth.admin_required")
	testkit.Expect(t, env.Do("POST", adminBase, u, body("t", "t", "c", true)), 403, "auth.admin_required")
	testkit.Expect(t, env.Do("PATCH", adminBase+"/1", u, map[string]any{}), 403, "auth.admin_required")
	testkit.Expect(t, env.Do("DELETE", adminBase+"/1", u, nil), 403, "auth.admin_required")
}

// Scenario 1.
func TestCreateUserPreset(t *testing.T) {
	env := newEnv(t)
	w := env.Do("POST", mineURL, env.Bearer(alice), map[string]any{
		"title": "Polish", "trigger": "/polish", "content": "Improve my text", "enabled": true,
	})
	testkit.Expect(t, w, 200, "")
	if env.Do("POST", mineURL, env.Bearer(alice), body("x", "x", "x", true)).Code != 200 {
		t.Fatal("creates answer 200, not 201")
	}
	var out struct {
		PromptPreset map[string]any `json:"promptPreset"`
	}
	testkit.Decode(t, w, &out)
	p := out.PromptPreset
	if p["trigger"] != "polish" || p["scope"] != "user" || p["title"] != "Polish" || p["content"] != "Improve my text" {
		t.Fatalf("preset: %v", p)
	}
	if p["createdByUserID"] != float64(1) || p["updatedByUserID"] != float64(1) || p["sortOrder"] != float64(1) || p["enabled"] != true {
		t.Fatalf("preset: %v", p)
	}
	for _, key := range []string{"id", "description", "createdAt", "updatedAt"} {
		if _, ok := p[key]; !ok {
			t.Fatalf("missing %s in %v", key, p)
		}
	}
	for _, key := range []string{"ownerUserID", "ownerUserId", "owner_user_id", "deletedAt"} {
		if _, ok := p[key]; ok {
			t.Fatalf("owner must not be exposed: %v", p)
		}
	}
	if _, err := time.Parse(time.RFC3339, p["createdAt"].(string)); err != nil {
		t.Fatalf("createdAt not RFC 3339: %v", p["createdAt"])
	}
	if e := testkit.Envelope(t, w); e.ErrorMsg != "" {
		t.Fatalf("errorMsg: %q", e.ErrorMsg)
	}
}

// Scenario 2.
func TestSortOrderAssignment(t *testing.T) {
	env := newEnv(t)
	first := mk(t, env, mineURL, alice, body("a", "a", "c", true))
	second := mk(t, env, mineURL, alice, body("b", "b", "c", true))
	third := mk(t, env, mineURL, alice, map[string]any{"title": "c", "trigger": "c", "content": "c", "sortOrder": 10})
	fourth := mk(t, env, mineURL, alice, body("d", "d", "c", true))
	neg := mk(t, env, mineURL, alice, map[string]any{"title": "e", "trigger": "e", "content": "c", "sortOrder": -5})
	got := []int{first.SortOrder, second.SortOrder, third.SortOrder, fourth.SortOrder, neg.SortOrder}
	want := []int{1, 2, 10, 11, 12}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sort orders %v, want %v", got, want)
		}
	}
	// Another user and the builtin set count independently.
	if other := mk(t, env, mineURL, bob, body("a", "a", "c", true)); other.SortOrder != 1 {
		t.Fatalf("bob first: %d", other.SortOrder)
	}
	if b := mk(t, env, adminBase, root, body("a", "a", "c", true)); b.SortOrder != 1 {
		t.Fatalf("builtin first: %d", b.SortOrder)
	}
}

func TestConcurrentCreatesGetDistinctSortOrders(t *testing.T) {
	env := newEnv(t)
	const n = 12
	var wg sync.WaitGroup
	orders := make(chan int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := env.Do("POST", mineURL, env.Bearer(alice), body("t", fmt.Sprintf("t%d", i), "c", true))
			if w.Code != 200 {
				t.Errorf("create %d: %d %s", i, w.Code, w.Body)
				return
			}
			var out struct {
				PromptPreset presetJSON `json:"promptPreset"`
			}
			testkit.Decode(t, w, &out)
			orders <- out.PromptPreset.SortOrder
		}(i)
	}
	wg.Wait()
	close(orders)
	seen := map[int]bool{}
	for o := range orders {
		if seen[o] {
			t.Fatalf("duplicate sort order %d", o)
		}
		seen[o] = true
	}
	if len(seen) != n {
		t.Fatalf("got %d orders", len(seen))
	}
}

// Scenario 3 and the per-scope uniqueness rules.
func TestTriggerUniqueness(t *testing.T) {
	env := newEnv(t)
	mk(t, env, mineURL, alice, body("Polish", "/polish", "x", true))
	a := env.Bearer(alice)
	testkit.Expect(t, env.Do("POST", mineURL, a, body("Other", "polish", "y", true)), 409, "prompt_preset_trigger.already_exists")
	testkit.Expect(t, env.Do("POST", mineURL, a, body("Other", "//polish", "y", true)), 409, "prompt_preset_trigger.already_exists")
	// Case-sensitive.
	testkit.Expect(t, env.Do("POST", mineURL, a, body("Other", "Polish", "y", true)), 200, "")
	// Another user, and the builtin set, may reuse the trigger.
	testkit.Expect(t, env.Do("POST", mineURL, env.Bearer(bob), body("P", "polish", "y", true)), 200, "")
	testkit.Expect(t, env.Do("POST", adminBase, env.Bearer(root), body("P", "polish", "y", true)), 200, "")
	testkit.Expect(t, env.Do("POST", adminBase, env.Bearer(root), body("P2", "polish", "y", true)), 409, "prompt_preset_trigger.already_exists")
	if msg := testkit.Envelope(t, env.Do("POST", mineURL, a, body("Other", "polish", "y", true))).ErrorMsg; msg != "prompt preset trigger already exists" {
		t.Fatalf("message %q", msg)
	}
}

// Scenario 4.
func TestUnicodeLimitsCountCodePoints(t *testing.T) {
	env := newEnv(t)
	a := env.Bearer(alice)
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	zh := func(n int) string { return strings.Repeat("\u4e2d", n) }

	p := mk(t, env, mineURL, alice, body(zh(64), "/"+zh(63), "c", true))
	if p.Trigger != zh(63) || p.Title != zh(64) {
		t.Fatalf("stored: %+v", p)
	}
	testkit.Expect(t, env.Do("POST", mineURL, a, body("t", zh(64), "c", true)), 200, "")
	testkit.Expect(t, env.Do("POST", mineURL, a, body("t", zh(65), "c", true)), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("POST", mineURL, a, body("t", "/"+zh(64), "c", true)), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("POST", mineURL, a, body(zh(65), "x", "c", true)), 400, "request.invalid_body")
	// Surrounding spaces count toward the raw limit.
	testkit.Expect(t, env.Do("POST", mineURL, a, body(" "+zh(64)+" ", "y", "c", true)), 400, "request.invalid_body")
	// Description and content bounds.
	d := body("t", "d1", "c", true)
	d["description"] = zh(256)
	testkit.Expect(t, env.Do("POST", mineURL, a, d), 200, "")
	d = body("t", "d2", "c", true)
	d["description"] = zh(257)
	testkit.Expect(t, env.Do("POST", mineURL, a, d), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("POST", mineURL, a, body("t", "c1", zh(10000), true)), 200, "")
	testkit.Expect(t, env.Do("POST", mineURL, a, body("t", "c2", zh(10001), true)), 400, "request.invalid_body")
}

// Scenario 5.
func TestCreateValidation(t *testing.T) {
	env := newEnv(t)
	a := env.Bearer(alice)
	testkit.Expect(t, env.Do("POST", mineURL, a, body("t", "x", "   ", true)), 400, "request.invalid_prompt_preset")
	testkit.Expect(t, env.Do("POST", mineURL, a, body("   ", "x", "c", true)), 400, "request.invalid_prompt_preset")
	testkit.Expect(t, env.Do("POST", mineURL, a, body("t", "   ", "c", true)), 400, "request.invalid_prompt_preset")
	testkit.Expect(t, env.Do("POST", mineURL, a, body("t", "/", "c", true)), 400, "request.invalid_prompt_preset")
	testkit.Expect(t, env.Do("POST", mineURL, a, body("t", "  /  ", "c", true)), 400, "request.invalid_prompt_preset")
	testkit.Expect(t, env.Do("POST", mineURL, a, map[string]any{"title": "t", "trigger": "x"}), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("POST", mineURL, a, body("t", "x", strings.Repeat("a", 10001), true)), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("POST", mineURL, a, map[string]any{"trigger": "x", "content": "c"}), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("POST", mineURL, a, map[string]any{"title": "", "trigger": "x", "content": "c"}), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("POST", mineURL, a, "{not json"), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("POST", mineURL, a, ""), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("POST", mineURL, a, `{"title": 5, "trigger": "x", "content": "c"}`), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("POST", mineURL, a, `{"title":"t","trigger":"x","content":"c","enabled":"yes"}`), 400, "request.invalid_body")
	if msg := testkit.Envelope(t, env.Do("POST", mineURL, a, body("t", "/", "c", true))).ErrorMsg; msg != "invalid prompt preset" {
		t.Fatalf("message %q", msg)
	}
	var n int64
	env.DB.Model(&PromptPreset{}).Count(&n)
	if n != 0 {
		t.Fatalf("%d rows stored by rejected creates", n)
	}
}

func TestNormalizationOnCreate(t *testing.T) {
	env := newEnv(t)
	p := mk(t, env, mineURL, alice, map[string]any{
		"title": "  Spaced  ", "trigger": "  ///x y  ", "content": "\n body \n", "description": "  desc  ", "enabled": true,
	})
	if p.Title != "Spaced" || p.Trigger != "x y" || p.Content != "body" || p.Description != "desc" {
		t.Fatalf("not normalized: %+v", p)
	}
	q := mk(t, env, mineURL, alice, body("t", " / /abc", "c", true))
	if q.Trigger != "/abc" {
		// "/ /abc": strip leading slashes, trim, the remaining text starts with a space-free "/abc".
		t.Fatalf("trigger %q", q.Trigger)
	}
	if r := mk(t, env, mineURL, alice, body("t", "///x", "c", true)); r.Trigger != "x" {
		t.Fatalf("trigger %q", r.Trigger)
	}
	if r := mk(t, env, mineURL, alice, body("t", "cmd/with-slash", "c", true)); r.Trigger != "cmd/with-slash" {
		t.Fatalf("inner slashes kept: %q", r.Trigger)
	}
}

func TestCreateAlwaysEnabled(t *testing.T) {
	env := newEnv(t)
	for i, raw := range []string{
		`{"title":"t","trigger":"a","content":"c"}`,
		`{"title":"t","trigger":"b","content":"c","enabled":false}`,
		`{"title":"t","trigger":"c","content":"c","enabled":null}`,
		`{"title":"t","trigger":"d","content":"c","enabled":true}`,
	} {
		w := env.Do("POST", mineURL, env.Bearer(alice), raw)
		testkit.Expect(t, w, 200, "")
		var out struct {
			PromptPreset presetJSON `json:"promptPreset"`
		}
		testkit.Decode(t, w, &out)
		if !out.PromptPreset.Enabled {
			t.Fatalf("case %d: create must yield enabled=true", i)
		}
	}
	if l := list(t, env, userBase, alice); l.Total != 4 {
		t.Fatalf("created presets must be pickable immediately: %d", l.Total)
	}
	b := env.Do("POST", adminBase, env.Bearer(root), `{"title":"t","trigger":"g","content":"c","enabled":false}`)
	testkit.Expect(t, b, 200, "")
	var ob struct {
		PromptPreset presetJSON `json:"promptPreset"`
	}
	testkit.Decode(t, b, &ob)
	if !ob.PromptPreset.Enabled {
		t.Fatal("builtin create must also ignore enabled=false")
	}
	// PATCH still disables.
	w := env.Do("PATCH", idURL(adminBase, ob.PromptPreset.ID), env.Bearer(root), map[string]any{"enabled": false})
	testkit.Expect(t, w, 200, "")
	testkit.Decode(t, w, &ob)
	if ob.PromptPreset.Enabled {
		t.Fatal("patch must disable")
	}
}

func TestClientCannotChooseScopeOrOwner(t *testing.T) {
	env := newEnv(t)
	payload := body("t", "x", "c", true)
	payload["scope"] = "builtin"
	payload["ownerUserID"] = 99
	payload["owner_user_id"] = 99
	payload["createdByUserID"] = 99
	p := mk(t, env, mineURL, alice, payload)
	if p.Scope != "user" || p.CreatedByUserID != 1 || p.UpdatedByUserID != 1 {
		t.Fatalf("%+v", p)
	}
	var row PromptPreset
	env.DB.First(&row, p.ID)
	if row.OwnerUserID != 1 || row.Scope != ScopeUser {
		t.Fatalf("stored %+v", row)
	}
	// Admin route: builtin, owner 0, creator is the acting admin.
	b := mk(t, env, adminBase, root, payload)
	row = PromptPreset{}
	env.DB.First(&row, b.ID)
	if b.Scope != "builtin" || row.OwnerUserID != 0 || b.CreatedByUserID != 9 || b.UpdatedByUserID != 9 {
		t.Fatalf("%+v %+v", b, row)
	}
	// A patch cannot move a preset between scopes either.
	w := env.Do("PATCH", idURL(mineURL, p.ID), env.Bearer(alice), map[string]any{"scope": "builtin", "ownerUserID": 5, "title": "kept"})
	testkit.Expect(t, w, 200, "")
	row = PromptPreset{}
	env.DB.First(&row, p.ID)
	if row.Scope != ScopeUser || row.OwnerUserID != 1 || row.Title != "kept" {
		t.Fatalf("%+v", row)
	}
}

// Scenarios 6 and 7.
func TestOwnershipLooksLikeNotFound(t *testing.T) {
	env := newEnv(t)
	bobs := mk(t, env, mineURL, bob, body("t", "b", "c", true))
	builtin := mk(t, env, adminBase, root, body("t", "g", "c", true))
	a := env.Bearer(alice)
	for _, id := range []uint{bobs.ID, builtin.ID, 9999} {
		testkit.Expect(t, env.Do("PATCH", idURL(mineURL, id), a, map[string]any{"title": "x"}), 404, "prompt_preset.not_found")
		testkit.Expect(t, env.Do("DELETE", idURL(mineURL, id), a, nil), 404, "prompt_preset.not_found")
	}
	// Ownership is decided before body validation.
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, bobs.ID), a, map[string]any{"title": "  "}), 404, "prompt_preset.not_found")
	// Admin routes only reach builtin presets.
	r := env.Bearer(root)
	testkit.Expect(t, env.Do("PATCH", idURL(adminBase, bobs.ID), r, map[string]any{"title": "x"}), 404, "prompt_preset.not_found")
	testkit.Expect(t, env.Do("DELETE", idURL(adminBase, bobs.ID), r, nil), 404, "prompt_preset.not_found")
	if msg := testkit.Envelope(t, env.Do("DELETE", idURL(adminBase, bobs.ID), r, nil)).ErrorMsg; msg != "prompt preset not found" {
		t.Fatalf("message %q", msg)
	}
	// Nothing changed.
	var n int64
	env.DB.Model(&PromptPreset{}).Where("title = ?", "t").Count(&n)
	if n != 2 {
		t.Fatalf("rows touched by foreign operations: %d", n)
	}
}

func TestInvalidPathID(t *testing.T) {
	env := newEnv(t)
	for _, base := range []string{mineURL, adminBase} {
		who := env.Bearer(alice)
		if base == adminBase {
			who = env.Bearer(root)
		}
		for _, id := range []string{"abc", "0", "-1", "1.5", "99999999999999999999999", "9223372036854775808"} {
			testkit.Expect(t, env.Do("PATCH", base+"/"+id, who, map[string]any{"title": "x"}), 400, "prompt_preset.invalid_id")
			testkit.Expect(t, env.Do("DELETE", base+"/"+id, who, nil), 400, "prompt_preset.invalid_id")
		}
		// The id is checked before the body.
		testkit.Expect(t, env.Do("PATCH", base+"/abc", who, "{bad json"), 400, "prompt_preset.invalid_id")
	}
	if msg := testkit.Envelope(t, env.Do("PATCH", mineURL+"/0", env.Bearer(alice), map[string]any{})).ErrorMsg; msg != "invalid prompt preset id" {
		t.Fatalf("message %q", msg)
	}
}

// Scenario 8 and the other patch rules.
func TestPatch(t *testing.T) {
	env := newEnv(t)
	p := mk(t, env, mineURL, alice, map[string]any{"title": "T", "trigger": "t", "content": "C", "description": "D", "enabled": true, "sortOrder": 4})
	url := idURL(mineURL, p.ID)
	env.Clock.Advance(time.Hour)
	a := env.Bearer(alice)

	w := env.Do("PATCH", url, a, map[string]any{"enabled": false})
	testkit.Expect(t, w, 200, "")
	var out struct {
		PromptPreset presetJSON `json:"promptPreset"`
	}
	testkit.Decode(t, w, &out)
	g := out.PromptPreset
	if g.Enabled || g.Title != "T" || g.Trigger != "t" || g.Content != "C" || g.Description != "D" || g.SortOrder != 4 ||
		g.UpdatedByUserID != 1 || g.CreatedByUserID != 1 || g.Scope != "user" || g.ID != p.ID {
		t.Fatalf("patched: %+v", g)
	}
	if g.CreatedAt != p.CreatedAt || g.UpdatedAt == p.UpdatedAt {
		t.Fatalf("timestamps: created %s->%s updated %s->%s", p.CreatedAt, g.CreatedAt, p.UpdatedAt, g.UpdatedAt)
	}

	// Several fields, normalized; sortOrder stored verbatim, even zero/negative.
	w = env.Do("PATCH", url, a, map[string]any{"title": " New ", "trigger": "//new", "content": " body ", "enabled": true, "sortOrder": -3, "description": ""})
	testkit.Expect(t, w, 200, "")
	testkit.Decode(t, w, &out)
	g = out.PromptPreset
	if g.Title != "New" || g.Trigger != "new" || g.Content != "body" || !g.Enabled || g.SortOrder != -3 || g.Description != "" {
		t.Fatalf("patched: %+v", g)
	}
	testkit.Expect(t, env.Do("PATCH", url, a, map[string]any{"sortOrder": 0}), 200, "")
	var row PromptPreset
	env.DB.First(&row, p.ID)
	if row.SortOrder != 0 {
		t.Fatalf("sort order %d", row.SortOrder)
	}

	// Whitespace clears the description.
	testkit.Expect(t, env.Do("PATCH", url, a, map[string]any{"description": "again"}), 200, "")
	testkit.Expect(t, env.Do("PATCH", url, a, map[string]any{"description": "   "}), 200, "")
	env.DB.First(&row, p.ID)
	if row.Description != "" {
		t.Fatalf("description %q", row.Description)
	}

	// An empty patch is accepted and only refreshes updater and time.
	env.Clock.Advance(time.Hour)
	a = env.Bearer(alice)
	before := row
	w = env.Do("PATCH", url, a, map[string]any{})
	testkit.Expect(t, w, 200, "")
	env.DB.First(&row, p.ID)
	if row.Title != before.Title || row.Trigger != before.Trigger || row.Content != before.Content || !row.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("empty patch: before %+v after %+v", before, row)
	}

	// Null fields are treated as absent.
	testkit.Expect(t, env.Do("PATCH", url, a, `{"title": null, "enabled": null}`), 200, "")
	env.DB.First(&row, p.ID)
	if row.Title != "New" {
		t.Fatalf("title %q", row.Title)
	}
}

func TestPatchValidation(t *testing.T) {
	env := newEnv(t)
	p := mk(t, env, mineURL, alice, body("T", "t", "C", true))
	a := env.Bearer(alice)
	url := idURL(mineURL, p.ID)
	for _, f := range []string{"title", "trigger", "content"} {
		testkit.Expect(t, env.Do("PATCH", url, a, map[string]any{f: ""}), 400, "request.invalid_prompt_preset")
		testkit.Expect(t, env.Do("PATCH", url, a, map[string]any{f: "   "}), 400, "request.invalid_prompt_preset")
	}
	testkit.Expect(t, env.Do("PATCH", url, a, map[string]any{"trigger": "///"}), 400, "request.invalid_prompt_preset")
	testkit.Expect(t, env.Do("PATCH", url, a, map[string]any{"title": strings.Repeat("a", 65)}), 400, "request.invalid_body")
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	testkit.Expect(t, env.Do("PATCH", url, a, map[string]any{"trigger": "/" + strings.Repeat("\u4e2d", 64)}), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("PATCH", url, a, map[string]any{"trigger": "/" + strings.Repeat("\u4e2d", 63)}), 200, "")
	testkit.Expect(t, env.Do("PATCH", url, a, map[string]any{"content": strings.Repeat("a", 10001)}), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("PATCH", url, a, map[string]any{"description": strings.Repeat("a", 257)}), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("PATCH", url, a, `{"enabled": "no"}`), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("PATCH", url, a, "{bad"), 400, "request.invalid_body")
	// A rejected patch changes nothing.
	var row PromptPreset
	env.DB.First(&row, p.ID)
	if row.Title != "T" || row.Content != "C" {
		t.Fatalf("%+v", row)
	}
}

func TestPatchTriggerConflict(t *testing.T) {
	env := newEnv(t)
	one := mk(t, env, mineURL, alice, body("A", "one", "c", true))
	two := mk(t, env, mineURL, alice, body("B", "two", "c", true))
	a := env.Bearer(alice)
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, two.ID), a, map[string]any{"trigger": "/one"}), 409, "prompt_preset_trigger.already_exists")
	// Keeping the same trigger on the same row is not a conflict.
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, one.ID), a, map[string]any{"trigger": "/one", "title": "A2"}), 200, "")
	// Case differs: allowed.
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, two.ID), a, map[string]any{"trigger": "One"}), 200, "")
	// Another owner's trigger is not a conflict.
	testkit.Expect(t, env.Do("POST", mineURL, env.Bearer(bob), body("x", "zzz", "c", true)), 200, "")
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, two.ID), a, map[string]any{"trigger": "zzz"}), 200, "")
	// A failed patch leaves the row as it was.
	var row PromptPreset
	env.DB.First(&row, one.ID)
	if row.Trigger != "one" || row.Title != "A2" {
		t.Fatalf("%+v", row)
	}
}

// Scenario 9, 11 and the picker rules.
func TestPickerList(t *testing.T) {
	env := newEnv(t)
	mk(t, env, adminBase, root, body("Musk", "musk", "be elon", true))
	mk(t, env, adminBase, root, body("Old", "old", "legacy", false))
	mk(t, env, mineURL, alice, body("Mine one", "mine1", "x", true))
	mk(t, env, mineURL, alice, body("Mine two", "mine2", "x", false))
	mk(t, env, mineURL, bob, body("Other", "other", "x", true))

	l := list(t, env, userBase, alice)
	if !sameStrings(triggers(l), []string{"mine1", "musk"}) || l.Total != 2 {
		t.Fatalf("alice sees %v total %d", triggers(l), l.Total)
	}
	l = list(t, env, userBase, bob)
	if !sameStrings(triggers(l), []string{"other", "musk"}) || l.Total != 2 {
		t.Fatalf("bob sees %v", triggers(l))
	}
	// The enabled parameter is ignored on the picker.
	l = list(t, env, userBase+"?enabled=false", alice)
	if !sameStrings(triggers(l), []string{"mine1", "musk"}) {
		t.Fatalf("enabled filter must be ignored: %v", triggers(l))
	}
	// Admins see only what any user sees.
	l = list(t, env, userBase, root)
	if !sameStrings(triggers(l), []string{"musk"}) {
		t.Fatalf("admin picker: %v", triggers(l))
	}
}

func TestPickerKeyword(t *testing.T) {
	env := newEnv(t)
	mk(t, env, adminBase, root, map[string]any{"title": "Elon", "trigger": "tweet", "content": "post a message", "description": "social", "enabled": true})
	mk(t, env, adminBase, root, map[string]any{"title": "Other", "trigger": "MUSKY", "content": "c", "enabled": true})
	mk(t, env, adminBase, root, map[string]any{"title": "Third", "trigger": "third", "content": "c", "description": "about Musk here", "enabled": true})
	mk(t, env, adminBase, root, map[string]any{"title": "Fourth", "trigger": "fourth", "content": "Quoting MUSK", "enabled": true})
	mk(t, env, adminBase, root, map[string]any{"title": "MuskTitle", "trigger": "fifth", "content": "c", "enabled": true})
	mk(t, env, adminBase, root, map[string]any{"title": "Nope", "trigger": "nope", "content": "c", "enabled": true})
	mk(t, env, mineURL, bob, map[string]any{"title": "Bobs musk", "trigger": "bobmusk", "content": "c", "enabled": true})
	mk(t, env, mineURL, alice, map[string]any{"title": "Disabled musk", "trigger": "offmusk", "content": "c", "enabled": false})

	got := triggers(list(t, env, userBase+"?q=MUSK", alice))
	if len(got) != 4 {
		t.Fatalf("q=MUSK: %v", got)
	}
	for _, g := range got {
		if g == "bobmusk" || g == "offmusk" || g == "tweet" || g == "nope" {
			t.Fatalf("keyword widened visibility or over-matched: %v", got)
		}
	}
	if n := len(triggers(list(t, env, userBase+"?q=%20%20", alice))); n != 6 {
		t.Fatalf("blank q means no search: %d", n)
	}
	if n := len(triggers(list(t, env, userBase+"?q=%20musk%20", alice))); n != 4 {
		t.Fatalf("q is trimmed: %d", n)
	}
	// SQL wildcards are literal.
	if l := list(t, env, userBase+"?q=%25", alice); l.Total != 0 {
		t.Fatalf("%% matched %d rows", l.Total)
	}
	if l := list(t, env, userBase+"?q=_", alice); l.Total != 0 {
		t.Fatalf("_ matched %d rows", l.Total)
	}
	if l := list(t, env, userBase+"?q=zzz-none", alice); l.Total != 0 || l.Results == nil {
		t.Fatalf("no match: %+v", l)
	}
}

func TestPickerOrdering(t *testing.T) {
	env := newEnv(t)
	// Builtin presets: orders 5, 1, 1 (the last two tie, newer wins).
	mk(t, env, adminBase, root, map[string]any{"title": "b5", "trigger": "b5", "content": "c", "enabled": true, "sortOrder": 5})
	mk(t, env, adminBase, root, map[string]any{"title": "b1a", "trigger": "b1a", "content": "c", "enabled": true, "sortOrder": 1})
	env.Clock.Advance(time.Minute)
	mk(t, env, adminBase, root, map[string]any{"title": "b1b", "trigger": "b1b", "content": "c", "enabled": true, "sortOrder": 1})
	// User presets have a higher sort order but still come first.
	mk(t, env, mineURL, alice, map[string]any{"title": "u9", "trigger": "u9", "content": "c", "enabled": true, "sortOrder": 9})
	mk(t, env, mineURL, alice, map[string]any{"title": "u2", "trigger": "u2", "content": "c", "enabled": true, "sortOrder": 2})
	got := triggers(list(t, env, userBase, alice))
	want := []string{"u2", "u9", "b1b", "b1a", "b5"}
	if !sameStrings(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
	// Equal sort order and equal time: higher id first.
	mk(t, env, mineURL, bob, map[string]any{"title": "x", "trigger": "x1", "content": "c", "enabled": true, "sortOrder": 3})
	mk(t, env, mineURL, bob, map[string]any{"title": "x", "trigger": "x2", "content": "c", "enabled": true, "sortOrder": 3})
	got = triggers(list(t, env, userBase, bob))
	if !sameStrings(got[:2], []string{"x2", "x1"}) {
		t.Fatalf("tie order %v", got)
	}
}

// Scenario 10.
func TestMineList(t *testing.T) {
	env := newEnv(t)
	mk(t, env, mineURL, alice, map[string]any{"title": "on2", "trigger": "on2", "content": "c", "enabled": true, "sortOrder": 2})
	mk(t, env, mineURL, alice, map[string]any{"title": "off1", "trigger": "off1", "content": "c", "enabled": false, "sortOrder": 1})
	mk(t, env, mineURL, alice, map[string]any{"title": "on1", "trigger": "on1", "content": "c", "enabled": true, "sortOrder": 1})
	mk(t, env, mineURL, bob, body("bob", "bob", "c", true))
	mk(t, env, adminBase, root, body("g", "g", "c", true))

	got := triggers(list(t, env, mineURL, alice))
	if !sameStrings(got, []string{"on1", "on2", "off1"}) {
		t.Fatalf("mine order %v", got)
	}
	if got := triggers(list(t, env, mineURL+"?enabled=false", alice)); !sameStrings(got, []string{"off1"}) {
		t.Fatalf("enabled=false: %v", got)
	}
	if got := triggers(list(t, env, mineURL+"?enabled=true", alice)); !sameStrings(got, []string{"on1", "on2"}) {
		t.Fatalf("enabled=true: %v", got)
	}
	for _, v := range []string{"bogus", "", "%20true", "true%20", "yes", "2"} {
		if l := list(t, env, mineURL+"?enabled="+v, alice); l.Total != 3 {
			t.Fatalf("enabled=%q should not filter: total %d", v, l.Total)
		}
	}
	for _, v := range []string{"1", "t", "T", "TRUE", "True", "true"} {
		if l := list(t, env, mineURL+"?enabled="+v, alice); l.Total != 2 {
			t.Fatalf("enabled=%q should select enabled: total %d", v, l.Total)
		}
	}
	for _, v := range []string{"0", "f", "F", "FALSE", "False", "false"} {
		if l := list(t, env, mineURL+"?enabled="+v, alice); l.Total != 1 {
			t.Fatalf("enabled=%q should select disabled: total %d", v, l.Total)
		}
	}
	if l := list(t, env, mineURL+"?q=ON", alice); l.Total != 2 {
		t.Fatalf("keyword on mine: %d", l.Total)
	}
	if l := list(t, env, mineURL+"?q=bob", alice); l.Total != 0 {
		t.Fatalf("mine leaked: %d", l.Total)
	}
	if l := list(t, env, mineURL, root); l.Total != 0 {
		t.Fatalf("admin's own presets: %d", l.Total)
	}
}

// Scenario 12.
func TestAdminList(t *testing.T) {
	env := newEnv(t)
	mk(t, env, adminBase, root, map[string]any{"title": "off", "trigger": "off", "content": "c", "enabled": false, "sortOrder": 1})
	mk(t, env, adminBase, root, map[string]any{"title": "on", "trigger": "on", "content": "c", "enabled": true, "sortOrder": 2})
	mk(t, env, mineURL, alice, body("mine", "mine", "c", true))

	l := list(t, env, adminBase, root)
	if !sameStrings(triggers(l), []string{"on", "off"}) || l.Total != 2 {
		t.Fatalf("admin list %v", triggers(l))
	}
	for _, r := range l.Results {
		if r.Scope != "builtin" {
			t.Fatalf("scope %s", r.Scope)
		}
	}
	if got := triggers(list(t, env, adminBase+"?enabled=false", root)); !sameStrings(got, []string{"off"}) {
		t.Fatalf("%v", got)
	}
	if got := triggers(list(t, env, adminBase+"?q=OFF", root)); !sameStrings(got, []string{"off"}) {
		t.Fatalf("%v", got)
	}
	testkit.Expect(t, env.Do("GET", adminBase, env.Bearer(alice), nil), 403, "auth.admin_required")
}

// Scenarios 13 and 14.
func TestAdminAuditTrail(t *testing.T) {
	env := newEnv(t)
	r := env.Bearer(root)
	created := mk(t, env, adminBase, root, body("G", "/greet", "hi", true))

	w := env.Do("PATCH", idURL(adminBase, created.ID), r, map[string]any{"trigger": "/hello"})
	testkit.Expect(t, w, 200, "")
	var out struct {
		PromptPreset presetJSON `json:"promptPreset"`
	}
	testkit.Decode(t, w, &out)
	if out.PromptPreset.Trigger != "hello" || out.PromptPreset.UpdatedByUserID != 9 || out.PromptPreset.Scope != "builtin" {
		t.Fatalf("%+v", out.PromptPreset)
	}

	w = env.Do("DELETE", idURL(adminBase, created.ID), r, nil)
	testkit.Expect(t, w, 200, "")
	var del struct {
		Deleted bool `json:"deleted"`
	}
	testkit.Decode(t, w, &del)
	if !del.Deleted {
		t.Fatal("deleted flag")
	}
	testkit.Expect(t, env.Do("DELETE", idURL(adminBase, created.ID), r, nil), 404, "prompt_preset.not_found")

	var logs []audit.Log
	env.DB.Order("id").Find(&logs)
	if len(logs) != 3 {
		t.Fatalf("expected 3 audit rows (create, update, delete), got %d: %+v", len(logs), logs)
	}
	id := fmt.Sprint(created.ID)
	wants := []struct{ action, detail string }{
		{"prompt_preset.create_builtin", `{"trigger":"greet"}`},
		{"prompt_preset.update_builtin", `{"trigger":"hello"}`},
		{"prompt_preset.delete_builtin", `{}`},
	}
	for i, wnt := range wants {
		l := logs[i]
		if l.Action != wnt.action || l.Resource != "prompt_presets" || l.ResourceID != id || l.ActorUserID != 9 {
			t.Fatalf("log %d: %+v", i, l)
		}
		if !jsonEqual(l.DetailJSON, wnt.detail) {
			t.Fatalf("log %d detail %s, want %s", i, l.DetailJSON, wnt.detail)
		}
		if l.RequestID == "" || l.IP == "" {
			t.Fatalf("log %d lacks request metadata: %+v", i, l)
		}
	}
}

func jsonEqual(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return string(xa) == string(ya)
}

func TestFailedAndPersonalOperationsAreNotAudited(t *testing.T) {
	env := newEnv(t)
	r := env.Bearer(root)
	// Failures.
	testkit.Expect(t, env.Do("POST", adminBase, r, body("t", "/", "c", true)), 400, "request.invalid_prompt_preset")
	testkit.Expect(t, env.Do("PATCH", adminBase+"/77", r, map[string]any{"title": "x"}), 404, "")
	testkit.Expect(t, env.Do("DELETE", adminBase+"/77", r, nil), 404, "")
	g := mk(t, env, adminBase, root, body("t", "dup", "c", true))
	testkit.Expect(t, env.Do("POST", adminBase, r, body("t", "dup", "c", true)), 409, "")
	testkit.Expect(t, env.Do("PATCH", idURL(adminBase, g.ID), r, map[string]any{"title": ""}), 400, "")
	// Personal operations.
	a := env.Bearer(alice)
	p := mk(t, env, mineURL, alice, body("t", "mine", "c", true))
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, p.ID), a, map[string]any{"title": "n"}), 200, "")
	testkit.Expect(t, env.Do("DELETE", idURL(mineURL, p.ID), a, nil), 200, "")

	var n int64
	env.DB.Model(&audit.Log{}).Count(&n)
	if n != 1 { // only the successful builtin create
		t.Fatalf("audit rows: %d", n)
	}
}

func TestAuditDisabledDoesNotAffectResponses(t *testing.T) {
	// The recorder is optional: without one nothing is written and the API works.
	env := newEnvWithoutAudit(t)
	g := mk(t, env, adminBase, root, body("t", "x", "c", true))
	testkit.Expect(t, env.Do("PATCH", idURL(adminBase, g.ID), env.Bearer(root), map[string]any{"title": "y"}), 200, "")
	testkit.Expect(t, env.Do("DELETE", idURL(adminBase, g.ID), env.Bearer(root), nil), 200, "")
}

// Audit persistence failing (no audit table) must not break the request.
func TestAuditFailureDoesNotAffectResponses(t *testing.T) {
	env := newEnvWithoutAuditTable(t)
	g := mk(t, env, adminBase, root, body("t", "x", "c", true))
	testkit.Expect(t, env.Do("DELETE", idURL(adminBase, g.ID), env.Bearer(root), nil), 200, "")
}

// Scenario 14.
func TestUserDeleteOwnPreset(t *testing.T) {
	env := newEnv(t)
	p := mk(t, env, mineURL, alice, body("t", "gone", "c", true))
	w := env.Do("DELETE", idURL(mineURL, p.ID), env.Bearer(alice), nil)
	testkit.Expect(t, w, 200, "")
	var del struct {
		Deleted bool `json:"deleted"`
	}
	testkit.Decode(t, w, &del)
	if !del.Deleted {
		t.Fatal("deleted flag")
	}
	if l := list(t, env, mineURL, alice); l.Total != 0 {
		t.Fatalf("still listed: %+v", l)
	}
	var n int64
	env.DB.Model(&PromptPreset{}).Count(&n)
	if n != 0 {
		t.Fatalf("row not hard-deleted: %d", n)
	}
	testkit.Expect(t, env.Do("DELETE", idURL(mineURL, p.ID), env.Bearer(alice), nil), 404, "prompt_preset.not_found")
	// The trigger can be reused afterwards.
	testkit.Expect(t, env.Do("POST", mineURL, env.Bearer(alice), body("t", "gone", "c", true)), 200, "")
	var logs int64
	env.DB.Model(&audit.Log{}).Count(&logs)
	if logs != 0 {
		t.Fatalf("personal operations audited: %d", logs)
	}
}

// Scenario 15 and the paging rules.
func TestPagination(t *testing.T) {
	env := newEnv(t)
	for i := 1; i <= 25; i++ {
		mk(t, env, mineURL, alice, body("t", fmt.Sprintf("p%02d", i), "c", true))
	}
	l := list(t, env, mineURL, alice)
	if l.Total != 25 || len(l.Results) != 20 {
		t.Fatalf("default page: total %d len %d", l.Total, len(l.Results))
	}
	for _, q := range []string{"page_size=abc", "page_size=0", "page_size=-4", "page=-3", "page=abc", "page=0"} {
		if l := list(t, env, mineURL+"?"+q, alice); len(l.Results) != 20 || l.Results[0].Trigger != "p01" {
			t.Fatalf("%s: len %d first %v", q, len(l.Results), triggers(l)[:1])
		}
	}
	if l := list(t, env, mineURL+"?page_size=5000", alice); len(l.Results) != 25 {
		t.Fatalf("page_size 5000 should clamp to 1000: %d", len(l.Results))
	}
	l = list(t, env, mineURL+"?page=2&page_size=10", alice)
	if len(l.Results) != 10 || l.Results[0].Trigger != "p11" {
		t.Fatalf("page 2: %v", triggers(l))
	}
	l = list(t, env, mineURL+"?page=3&page_size=10", alice)
	if len(l.Results) != 5 || l.Total != 25 {
		t.Fatalf("page 3: %v", triggers(l))
	}
	w := env.Do("GET", mineURL+"?page=99&page_size=10", env.Bearer(alice), nil)
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"results":[]`) {
		t.Fatalf("beyond the last page must be an empty array: %s", w.Body)
	}
	l = list(t, env, mineURL+"?page=99&page_size=10", alice)
	if l.Total != 25 || len(l.Results) != 0 {
		t.Fatalf("%+v", l)
	}
	// Huge page numbers do not overflow.
	testkit.Expect(t, env.Do("GET", mineURL+"?page=9223372036854775807&page_size=1000", env.Bearer(alice), nil), 200, "")
	// Empty lists are arrays, never null.
	w = env.Do("GET", userBase, env.Bearer(bob), nil)
	if !strings.Contains(w.Body.String(), `"results":[]`) || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatalf("%s", w.Body)
	}
}

func TestPatchRaceKeepsRowConsistent(t *testing.T) {
	env := newEnv(t)
	p := mk(t, env, mineURL, alice, body("t", "r", "c", true))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := env.Do("PATCH", idURL(mineURL, p.ID), env.Bearer(alice), map[string]any{"title": fmt.Sprintf("v%d", i), "sortOrder": i})
			if w.Code != 200 {
				t.Errorf("patch %d: %d %s", i, w.Code, w.Body)
			}
		}(i)
	}
	wg.Wait()
	var row PromptPreset
	env.DB.First(&row, p.ID)
	if row.Title != fmt.Sprintf("v%d", row.SortOrder) {
		t.Fatalf("fields from different patches mixed: %+v", row)
	}
}

func TestRoutesShareNoPrefixConflictWithMine(t *testing.T) {
	// "mine" is a literal segment on the collection; it must not be read as an id.
	env := newEnv(t)
	testkit.Expect(t, env.Do("GET", mineURL, env.Bearer(alice), nil), 200, "")
	testkit.Expect(t, env.Do("PUT", mineURL, env.Bearer(alice), body("t", "x", "c", true)), 404, "")
}
