package uicomponent

import (
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/testkit"
)

// A zero-length PATCH body is treated like {}.
func TestPatchWithZeroLengthBody(t *testing.T) {
	env := newEnv(t)
	alice := env.Bearer(testkit.User(1))
	c := mk(t, env, mineURL, testkit.User(1), validBody("zl"))
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, c.ID), alice, ""), 200, "")
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, c.ID), alice, "  \n"), 200, "")
	// Malformed JSON is still rejected.
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, c.ID), alice, "{"), 400, "request.invalid_body")
	// And so are create bodies that are empty.
	testkit.Expect(t, env.Do("POST", mineURL, alice, ""), 400, "request.invalid_body")
	// Admin PATCH accepts the same.
	admin := env.Bearer(testkit.Admin(9))
	testkit.Expect(t, env.Do("PATCH", idURL(adminBase, builtinID(t, env, "chart")), admin, ""), 200, "")
}

// Numbers outside the 32-bit column range are body errors, never a 500.
func TestOutOfRangeNumbersAreBodyErrors(t *testing.T) {
	env := newEnv(t)
	alice := env.Bearer(testkit.User(1))
	c := mk(t, env, mineURL, testkit.User(1), validBody("rng"))
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("rng2"), "sortOrder", 3000000000)), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("rng3"), "version", 3000000000)), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, c.ID), alice, map[string]any{"sortOrder": 3000000000}), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, c.ID), alice, map[string]any{"version": 3000000000}), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("rng4"), "sortOrder", 2147483647, "version", 2147483647)), 200, "")
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, c.ID), alice, map[string]any{"sortOrder": -2147483648}), 200, "")
}

// NUL characters cannot be stored in PostgreSQL text, so they are invalid input.
func TestNulCharactersRejected(t *testing.T) {
	env := newEnv(t)
	alice := env.Bearer(testkit.User(1))
	c := mk(t, env, mineURL, testkit.User(1), validBody("nul"))
	for _, f := range []string{"description", "propsSummary", "rendererSource"} {
		testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("nul-"+strings.ToLower(f)), f, "a\u0000b")), 400, "request.invalid_ui_component")
		testkit.Expect(t, env.Do("PATCH", idURL(mineURL, c.ID), alice, map[string]any{f: "a\u0000b"}), 400, "request.invalid_ui_component")
	}
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("nul-schema"), "propsSchema", "{\"a\":\"\u0000\"}")), 400, "request.invalid_ui_component")
}

func TestUsageNeverLeaksInteractionText(t *testing.T) {
	if got := usageText(". Interaction: only interactions"); got != "" {
		t.Fatalf("usage: %q", got)
	}
	if got := usageText("what. Use when: when. Interaction: how"); got != "what. Use when: when" {
		t.Fatalf("usage: %q", got)
	}
	if got := usageText("  plain  "); got != "plain" {
		t.Fatalf("usage: %q", got)
	}
}

func TestValidBlockVersion(t *testing.T) {
	for v, want := range map[int]bool{-1: false, 0: true, 1: true, 2: true, 3: false} {
		if ValidBlockVersion(v) != want {
			t.Errorf("ValidBlockVersion(%d) != %v", v, want)
		}
	}
}
