package uicomponent

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestRoutesRequireAuthentication(t *testing.T) {
	env := newEnv(t)
	testkit.Expect(t, env.Do("GET", userBase, nil, nil), 401, "auth.invalid_token")
	testkit.Expect(t, env.Do("GET", mineURL, nil, nil), 401, "auth.invalid_token")
	user := env.Bearer(testkit.User(1))
	testkit.Expect(t, env.Do("GET", adminBase, user, nil), 403, "auth.admin_required")
	testkit.Expect(t, env.Do("POST", adminBase, user, validBody("x")), 403, "auth.admin_required")
	testkit.Expect(t, env.Do("PATCH", adminBase+"/1", user, map[string]any{}), 403, "auth.admin_required")
	testkit.Expect(t, env.Do("DELETE", adminBase+"/1", user, nil), 403, "auth.admin_required")
}

// Scenario 1.
func TestCreateRejectsBadNames(t *testing.T) {
	env := newEnv(t)
	alice := env.Bearer(testkit.User(1))
	w := env.Do("POST", mineURL, alice, validBody("Bad Name"))
	testkit.Expect(t, w, 400, "request.invalid_ui_component")
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "invalid ui component" {
		t.Fatalf("message: %q", msg)
	}
	for _, name := range []string{
		"panel", "widget", "slider", "row", "button", "formula", "scene", // reserved
		"a--b", "-a", "a-", "2a", "a_b", "Bad", "", "   ", "a b", "é",
		strings.Repeat("a", 65),
	} {
		w := env.Do("POST", mineURL, alice, validBody(name))
		if w.Code != 400 {
			t.Errorf("name %q: status %d, want 400: %s", name, w.Code, w.Body)
		}
	}
	// A whitespace-only name passes binding and fails validation.
	testkit.Expect(t, env.Do("POST", mineURL, alice, validBody("   ")), 400, "request.invalid_ui_component")
	for _, name := range []string{"my-chart2", "a", "a1-b2-c3", strings.Repeat("a", 64), "chart", "  padded  "} {
		w := env.Do("POST", mineURL, alice, validBody(name))
		if w.Code != 200 {
			t.Errorf("name %q: status %d, want 200: %s", name, w.Code, w.Body)
		}
	}
	// Names are stored trimmed.
	if got := list(t, env, mineURL+"?q=padded", testkit.User(1)); len(got.Results) != 1 || got.Results[0].Name != "padded" {
		t.Fatalf("trimmed name: %+v", got.Results)
	}
}

// Scenario 2.
func TestCreateSourceAndSchemaValidation(t *testing.T) {
	env := newEnv(t)
	alice := env.Bearer(testkit.User(1))
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("a1"), "rendererSource", "")), 400, "")
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("a2"), "rendererSource", "  \n\t ")), 400, "request.invalid_ui_component")
	for _, schema := range []string{"[1]", "not json", `"str"`, "12", "null", `{"a":`} {
		w := env.Do("POST", mineURL, alice, with(validBody("a3"), "propsSchema", schema))
		testkit.Expect(t, w, 400, "request.invalid_ui_component")
	}
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("a4"), "propsSchema", `{"type":"object"}`)), 200, "")
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("a5"), "propsSchema", "")), 200, "")

	// Size caps: schema 16 KiB, source 256 KiB, counted in bytes.
	big := `{"x":"` + strings.Repeat("a", 16384-8) + `"}`
	if len(big) != 16384 {
		t.Fatalf("test setup: %d", len(big))
	}
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("a6"), "propsSchema", big)), 200, "")
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("a7"), "propsSchema", `{"x":"`+strings.Repeat("a", 16384)+`"}`)), 400, "")
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("a8"), "rendererSource", strings.Repeat("x", 262144))), 200, "")
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("a9"), "rendererSource", strings.Repeat("x", 262145))), 400, "")
	// Multi-byte text: within the rune limit by binding, over the byte limit.
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("b1"), "rendererSource", strings.Repeat("\u5b57", 100000))), 400, "request.invalid_ui_component")
	// Source is stored untrimmed.
	c := mk(t, env, mineURL, testkit.User(1), with(validBody("b2"), "rendererSource", "  <p>x</p>\n"))
	if c.RendererSource != "  <p>x</p>\n" {
		t.Fatalf("source: %q", c.RendererSource)
	}
}

func TestCreateTextLimits(t *testing.T) {
	env := newEnv(t)
	alice := env.Bearer(testkit.User(1))
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("d1"), "description", strings.Repeat("\u5b57", 256))), 200, "")
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("d2"), "description", strings.Repeat("\u5b57", 257))), 400, "")
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("d3"), "propsSummary", strings.Repeat("\u5b57", 1024))), 200, "")
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("d4"), "propsSummary", strings.Repeat("\u5b57", 1025))), 400, "")
	// Missing or blank required text.
	for _, field := range []string{"name", "description", "propsSummary", "rendererSource"} {
		b := with(validBody("d5"))
		delete(b, field)
		testkit.Expect(t, env.Do("POST", mineURL, alice, b), 400, "request.invalid_body")
	}
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("d6"), "description", "   ")), 400, "request.invalid_ui_component")
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("d7"), "propsSummary", " ")), 400, "request.invalid_ui_component")
	// Malformed JSON and wrong types.
	testkit.Expect(t, env.Do("POST", mineURL, alice, "{"), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("POST", mineURL, alice, ""), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("d8"), "enabled", "yes")), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("POST", mineURL, alice, with(validBody("d9"), "sortOrder", "x")), 400, "request.invalid_body")
}

// Scenario 3.
func TestCreateDefaults(t *testing.T) {
	env := newEnv(t)
	first := mk(t, env, mineURL, testkit.User(7), validBody("first"))
	if first.Scope != "user" || first.RendererKind != "sandbox" || first.Version != 1 || first.Enabled ||
		first.SortOrder != 1 || first.CreatedByUserID != 7 || first.UpdatedByUserID != 7 || first.ID == 0 {
		t.Fatalf("first: %+v", first)
	}
	if first.CreatedAt == "" || first.UpdatedAt == "" {
		t.Fatalf("timestamps: %+v", first)
	}
	second := mk(t, env, mineURL, testkit.User(7), validBody("second"))
	if second.SortOrder != 2 {
		t.Fatalf("second sort: %d", second.SortOrder)
	}
	// Another user starts their own bucket at 1.
	if other := mk(t, env, mineURL, testkit.User(8), validBody("first")); other.SortOrder != 1 {
		t.Fatalf("other sort: %d", other.SortOrder)
	}
	// Explicit positive sort order is kept; zero and negative become max+1.
	if c := mk(t, env, mineURL, testkit.User(7), with(validBody("explicit"), "sortOrder", 50)); c.SortOrder != 50 {
		t.Fatalf("explicit: %d", c.SortOrder)
	}
	if c := mk(t, env, mineURL, testkit.User(7), with(validBody("zero"), "sortOrder", 0)); c.SortOrder != 51 {
		t.Fatalf("zero: %d", c.SortOrder)
	}
	if c := mk(t, env, mineURL, testkit.User(7), with(validBody("neg"), "sortOrder", -4)); c.SortOrder != 52 {
		t.Fatalf("neg: %d", c.SortOrder)
	}
	// Version and enabled.
	if c := mk(t, env, mineURL, testkit.User(7), with(validBody("v3"), "version", 3, "enabled", true)); c.Version != 3 || !c.Enabled {
		t.Fatalf("v3: %+v", c)
	}
	if c := mk(t, env, mineURL, testkit.User(7), with(validBody("v0"), "version", 0)); c.Version != 1 {
		t.Fatalf("v0: %+v", c)
	}
	if c := mk(t, env, mineURL, testkit.User(7), with(validBody("vneg"), "version", 2)); c.Version != 2 {
		t.Fatalf("vneg: %+v", c)
	}
	testkit.Expect(t, env.Do("POST", mineURL, env.Bearer(testkit.User(7)), with(validBody("vneg2"), "version", -2)), 400, "request.invalid_body")
	// Text fields are stored trimmed.
	c := mk(t, env, mineURL, testkit.User(7), with(validBody("trim"), "description", "  hi  ", "propsSummary", " {x} ", "propsSchema", ` {"a":1} `))
	if c.Description != "hi" || c.PropsSummary != "{x}" || c.PropsSchema != `{"a":1}` {
		t.Fatalf("trim: %+v", c)
	}
}

func TestOwnerIsNotExposed(t *testing.T) {
	env := newEnv(t)
	w := env.Do("POST", mineURL, env.Bearer(testkit.User(7)), validBody("hidden"))
	testkit.Expect(t, w, 200, "")
	var raw struct {
		Data struct {
			Component map[string]json.RawMessage `json:"component"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	want := []string{"id", "scope", "name", "version", "description", "propsSummary", "propsSchema", "rendererKind",
		"rendererSource", "enabled", "sortOrder", "createdByUserID", "updatedByUserID", "createdAt", "updatedAt"}
	if len(raw.Data.Component) != len(want) {
		t.Fatalf("fields: %v", raw.Data.Component)
	}
	for _, k := range want {
		if _, ok := raw.Data.Component[k]; !ok {
			t.Errorf("missing field %s", k)
		}
	}
	for k := range raw.Data.Component {
		if strings.Contains(strings.ToLower(k), "owner") {
			t.Errorf("owner leaked: %s", k)
		}
	}
}

// Scenario 4.
func TestDuplicateNamesPerBucket(t *testing.T) {
	env := newEnv(t)
	mk(t, env, mineURL, testkit.User(1), validBody("dup"))
	w := env.Do("POST", mineURL, env.Bearer(testkit.User(1)), validBody("dup"))
	testkit.Expect(t, w, 409, "ui_component.already_exists")
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "ui component name already exists" {
		t.Fatalf("message: %q", msg)
	}
	mk(t, env, mineURL, testkit.User(2), validBody("dup"))
	// Platform bucket is separate from every user bucket.
	mk(t, env, adminBase, testkit.Admin(9), validBody("dup"))
	testkit.Expect(t, env.Do("POST", adminBase, env.Bearer(testkit.Admin(9)), validBody("dup")), 409, "ui_component.already_exists")
	// A user may reuse a built-in name.
	mk(t, env, mineURL, testkit.User(1), validBody("chart"))
}

// Scenario 5.
func TestPatchMineOwnership(t *testing.T) {
	env := newEnv(t)
	bobs := mk(t, env, mineURL, testkit.User(2), validBody("bobs"))
	platform := mk(t, env, adminBase, testkit.Admin(9), validBody("plat"))
	alice := env.Bearer(testkit.User(1))
	body := map[string]any{"enabled": true}
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, bobs.ID), alice, body), 404, "ui_component.not_found")
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, platform.ID), alice, body), 404, "ui_component.not_found")
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, builtinID(t, env, "chart")), alice, body), 404, "ui_component.not_found")
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, 99999), alice, body), 404, "ui_component.not_found")
	// An unauthorised target is 404 even when the body is invalid.
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, bobs.ID), alice, map[string]any{"name": "Bad Name"}), 404, "ui_component.not_found")
	// Nothing changed.
	got := list(t, env, mineURL, testkit.User(2))
	if got.Results[0].Enabled {
		t.Fatalf("bob's component was modified")
	}
}

func TestPatchMineFieldsAndAbsence(t *testing.T) {
	env := newEnv(t)
	c := mk(t, env, mineURL, testkit.User(1), with(validBody("p1"), "enabled", true, "sortOrder", 5, "propsSchema", `{"a":1}`))
	alice := env.Bearer(testkit.User(1))

	// Empty body succeeds and only touches updated-by / updated-at.
	env.Clock.Advance(60e9)
	w := env.Do("PATCH", idURL(mineURL, c.ID), alice, map[string]any{})
	testkit.Expect(t, w, 200, "")
	var out struct {
		Component compJSON `json:"component"`
	}
	testkit.Decode(t, w, &out)
	same := out.Component
	if same.Name != "p1" || !same.Enabled || same.SortOrder != 5 || same.PropsSchema != `{"a":1}` || same.UpdatedByUserID != 1 {
		t.Fatalf("empty patch changed content: %+v", same)
	}
	if same.UpdatedAt == c.UpdatedAt {
		t.Fatalf("updatedAt not bumped: %s", same.UpdatedAt)
	}
	if same.CreatedAt != c.CreatedAt {
		t.Fatalf("createdAt changed")
	}

	// Zero values are real updates, not absence.
	w = env.Do("PATCH", idURL(mineURL, c.ID), alice, map[string]any{"enabled": false, "sortOrder": 0, "propsSchema": ""})
	testkit.Expect(t, w, 200, "")
	testkit.Decode(t, w, &out)
	if out.Component.Enabled || out.Component.SortOrder != 0 || out.Component.PropsSchema != "" || out.Component.Name != "p1" {
		t.Fatalf("zero patch: %+v", out.Component)
	}

	// Content fields.
	w = env.Do("PATCH", idURL(mineURL, c.ID), alice, map[string]any{
		"name": " p2 ", "version": 4, "description": " new desc ", "propsSummary": "{z}", "rendererSource": " <b>x</b> ",
		"propsSchema": `{"type":"object"}`,
	})
	testkit.Expect(t, w, 200, "")
	testkit.Decode(t, w, &out)
	got := out.Component
	if got.Name != "p2" || got.Version != 4 || got.Description != "new desc" || got.PropsSummary != "{z}" ||
		got.RendererSource != " <b>x</b> " || got.PropsSchema != `{"type":"object"}` || got.RendererKind != "sandbox" || got.Scope != "user" {
		t.Fatalf("patched: %+v", got)
	}
}
