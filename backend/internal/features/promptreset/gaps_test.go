package promptreset

import (
	"math"
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/testkit"
)

// Unexpected storage failures are a generic 500 that never leaks the cause.
func TestStorageFailureIsGeneric500(t *testing.T) {
	env := newEnv(t)
	if err := env.DB.Migrator().DropTable(&PromptPreset{}); err != nil {
		t.Fatal(err)
	}
	a, r := env.Bearer(alice), env.Bearer(root)
	calls := []struct {
		method, url string
		payload     any
	}{
		{"GET", userBase, nil},
		{"GET", mineURL, nil},
		{"POST", mineURL, body("t", "x", "c", true)},
		{"PATCH", idURL(mineURL, 1), map[string]any{"title": "x"}},
		{"DELETE", idURL(mineURL, 1), nil},
		{"GET", adminBase, nil},
	}
	for _, c := range calls {
		who := a
		if strings.HasPrefix(c.url, adminBase) {
			who = r
		}
		w := env.Do(c.method, c.url, who, c.payload)
		testkit.Expect(t, w, 500, "internal.error")
		if s := w.Body.String(); strings.Contains(strings.ToLower(s), "prompt_presets") || strings.Contains(strings.ToLower(s), "no such table") {
			t.Fatalf("%s %s leaked the cause: %s", c.method, c.url, s)
		}
	}
}

// The column is a 32-bit integer, so out-of-range sort orders are rejected as
// a bad body instead of failing in storage.
func TestSortOrderStaysWithinColumnRange(t *testing.T) {
	env := newEnv(t)
	a := env.Bearer(alice)
	p := body("t", "a", "c", true)
	p["sortOrder"] = int64(math.MaxInt32) + 1
	testkit.Expect(t, env.Do("POST", mineURL, a, p), 400, "request.invalid_body")

	p["sortOrder"] = math.MaxInt32
	first := mk(t, env, mineURL, alice, p)
	if first.SortOrder != math.MaxInt32 {
		t.Fatalf("sortOrder %d", first.SortOrder)
	}
	// Auto-assignment after the maximum cannot overflow.
	next := mk(t, env, mineURL, alice, body("t", "b", "c", true))
	if next.SortOrder != math.MaxInt32 {
		t.Fatalf("auto sortOrder %d", next.SortOrder)
	}
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, next.ID), a, map[string]any{"sortOrder": int64(math.MinInt32) - 1}), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, next.ID), a, map[string]any{"sortOrder": math.MinInt32}), 200, "")
}

// Spec 3.1 / 4.3: a patch body that is only a type error on a foreign id is a
// body error (the body is parsed before ownership is looked up).
func TestBodyErrorsPrecedeOwnershipLookup(t *testing.T) {
	env := newEnv(t)
	bobs := mk(t, env, mineURL, bob, body("t", "b", "c", true))
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, bobs.ID), env.Bearer(alice), `{"enabled":"x"}`), 400, "request.invalid_body")
}
