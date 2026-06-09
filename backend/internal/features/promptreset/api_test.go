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
