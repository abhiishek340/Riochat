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

func TestPatchValidation(t *testing.T) {
	env := newEnv(t)
	c := mk(t, env, mineURL, testkit.User(1), validBody("pv"))
	other := mk(t, env, mineURL, testkit.User(1), validBody("pv-other"))
	alice := env.Bearer(testkit.User(1))
	url := idURL(mineURL, c.ID)

	for name, body := range map[string]map[string]any{
		"bad name":         {"name": "Bad Name"},
		"reserved name":    {"name": "grid"},
		"switch name":      {"name": "widget"},
		"empty name":       {"name": ""},
		"version zero":     {"version": 0},
		"version negative": {"version": -1},
		"empty desc":       {"description": ""},
		"blank desc":       {"description": "   "},
		"empty summary":    {"propsSummary": ""},
		"bad schema":       {"propsSchema": "[1]"},
		"blank source":     {"rendererSource": "  "},
		"empty source":     {"rendererSource": ""},
	} {
		w := env.Do("PATCH", url, alice, body)
		if w.Code != 400 {
			t.Errorf("%s: status %d: %s", name, w.Code, w.Body)
		}
	}
	testkit.Expect(t, env.Do("PATCH", url, alice, map[string]any{"name": "Bad Name"}), 400, "request.invalid_ui_component")
	testkit.Expect(t, env.Do("PATCH", url, alice, map[string]any{"description": strings.Repeat("a", 257)}), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("PATCH", url, alice, `{"enabled": "x"}`), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("PATCH", url, alice, "[]"), 400, "request.invalid_body")

	// Collision in the same bucket is 409; renaming to itself is fine.
	testkit.Expect(t, env.Do("PATCH", url, alice, map[string]any{"name": other.Name}), 409, "ui_component.already_exists")
	testkit.Expect(t, env.Do("PATCH", url, alice, map[string]any{"name": "pv"}), 200, "")
	// A failed patch changes nothing.
	got := list(t, env, mineURL+"?q=pv", testkit.User(1))
	if len(got.Results) != 2 {
		t.Fatalf("rows: %+v", got.Results)
	}
}

func TestInvalidIDs(t *testing.T) {
	env := newEnv(t)
	alice := env.Bearer(testkit.User(1))
	admin := env.Bearer(testkit.Admin(9))
	for _, id := range []string{"abc", "0", "-1", "1.5", "99999999999999999999999"} {
		for _, call := range []struct {
			method, base string
			who          map[string]string
			body         any
		}{
			{"PATCH", mineURL, alice, map[string]any{}},
			{"DELETE", mineURL, alice, nil},
			{"PATCH", adminBase, admin, map[string]any{}},
			{"DELETE", adminBase, admin, nil},
		} {
			w := env.Do(call.method, call.base+"/"+id, call.who, call.body)
			testkit.Expect(t, w, 400, "ui_component.invalid_id")
			if msg := testkit.Envelope(t, w).ErrorMsg; msg != "invalid ui component id" {
				t.Fatalf("message: %q", msg)
			}
		}
	}
}

func TestDeleteMine(t *testing.T) {
	env := newEnv(t)
	c := mk(t, env, mineURL, testkit.User(1), validBody("gone"))
	bob := env.Bearer(testkit.User(2))
	testkit.Expect(t, env.Do("DELETE", idURL(mineURL, c.ID), bob, nil), 404, "ui_component.not_found")
	plat := mk(t, env, adminBase, testkit.Admin(9), validBody("plat"))
	testkit.Expect(t, env.Do("DELETE", idURL(mineURL, plat.ID), env.Bearer(testkit.User(1)), nil), 404, "ui_component.not_found")
	testkit.Expect(t, env.Do("DELETE", idURL(mineURL, builtinID(t, env, "chart")), env.Bearer(testkit.User(1)), nil), 404, "ui_component.not_found")

	w := env.Do("DELETE", idURL(mineURL, c.ID), env.Bearer(testkit.User(1)), nil)
	testkit.Expect(t, w, 200, "")
	var out struct{ Deleted bool }
	testkit.Decode(t, w, &out)
	if !out.Deleted {
		t.Fatalf("deleted flag: %s", w.Body)
	}
	testkit.Expect(t, env.Do("DELETE", idURL(mineURL, c.ID), env.Bearer(testkit.User(1)), nil), 404, "ui_component.not_found")
	// The name is free again.
	mk(t, env, mineURL, testkit.User(1), validBody("gone"))
}

func TestListMineFiltersAndOrder(t *testing.T) {
	env := newEnv(t)
	u := testkit.User(1)
	mk(t, env, mineURL, u, with(validBody("off-one"), "sortOrder", 1))
	env.Clock.Advance(1e9)
	mk(t, env, mineURL, u, with(validBody("on-late"), "sortOrder", 2, "enabled", true))
	env.Clock.Advance(1e9)
	mk(t, env, mineURL, u, with(validBody("on-early"), "sortOrder", 1, "enabled", true))
	env.Clock.Advance(1e9)
	mk(t, env, mineURL, u, with(validBody("on-early-new"), "sortOrder", 1, "enabled", true))
	mk(t, env, mineURL, testkit.User(2), with(validBody("others"), "enabled", true))

	got := names(list(t, env, mineURL, u))
	want := []string{"on-early-new", "on-early", "on-late", "off-one"}
	if !sameStrings(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
	if got := names(list(t, env, mineURL+"?enabled=true", u)); len(got) != 3 {
		t.Fatalf("enabled=true: %v", got)
	}
	if got := names(list(t, env, mineURL+"?enabled=0", u)); !sameStrings(got, []string{"off-one"}) {
		t.Fatalf("enabled=0: %v", got)
	}
	// Unparsable values (including padded ones) mean no filter.
	for _, v := range []string{"", "maybe", "%20true"} {
		if got := list(t, env, mineURL+"?enabled="+v, u); got.Total != 4 {
			t.Fatalf("enabled=%q: total %d", v, got.Total)
		}
	}
	if got := names(list(t, env, mineURL+"?q=EARLY", u)); len(got) != 2 {
		t.Fatalf("q: %v", got)
	}
	// q matches description too; wildcards are literal.
	if got := list(t, env, mineURL+"?q=test%20component", u); got.Total != 4 {
		t.Fatalf("q description: %d", got.Total)
	}
	if got := list(t, env, mineURL+"?q=%25", u); got.Total != 0 {
		t.Fatalf("q wildcard: %d", got.Total)
	}
	if got := list(t, env, mineURL+"?q=_", u); got.Total != 0 {
		t.Fatalf("q underscore: %d", got.Total)
	}
	// Pagination.
	p := list(t, env, mineURL+"?page_size=2&page=2", u)
	if p.Total != 4 || len(p.Results) != 2 || p.Results[0].Name != "on-late" {
		t.Fatalf("page 2: %+v", p)
	}
	p = list(t, env, mineURL+"?page_size=2&page=99999999999999", u)
	if p.Total != 4 || len(p.Results) != 0 {
		t.Fatalf("far page: %+v", p)
	}
	// Mine ignores the global switch.
	env2, sw := newEnvWithSwitch(t)
	mk(t, env2, mineURL, u, validBody("kept"))
	sw.on.Store(false)
	if got := list(t, env2, mineURL, u); got.Total != 1 {
		t.Fatalf("mine with switch off: %+v", got)
	}
}

// Scenario 6.
func TestAdminPatchBuiltin(t *testing.T) {
	env := newEnv(t)
	admin := env.Bearer(testkit.Admin(9))
	id := builtinID(t, env, "chart")

	before := list(t, env, userBase, testkit.User(1))
	found := false
	for _, n := range names(before) {
		found = found || n == "chart"
	}
	if !found {
		t.Fatalf("chart missing from catalog: %v", names(before))
	}

	for _, body := range []map[string]any{
		{"description": "x"}, {"name": "x"}, {"version": 2}, {"propsSummary": "x"},
		{"propsSchema": "{}"}, {"rendererSource": "<b>"},
		{"enabled": false, "name": "x"}, // mixed: still rejected, nothing applied
	} {
		w := env.Do("PATCH", idURL(adminBase, id), admin, body)
		testkit.Expect(t, w, 400, "ui_component.builtin_protected")
		if msg := testkit.Envelope(t, w).ErrorMsg; msg != "builtin ui component is protected" {
			t.Fatalf("message: %q", msg)
		}
	}
	var row UIComponent
	env.DB.Take(&row, id)
	if !row.Enabled || row.Name != "chart" {
		t.Fatalf("protected patch applied changes: %+v", row)
	}

	w := env.Do("PATCH", idURL(adminBase, id), admin, map[string]any{"enabled": false, "sortOrder": 77})
	testkit.Expect(t, w, 200, "")
	var out struct {
		Component compJSON `json:"component"`
	}
	testkit.Decode(t, w, &out)
	if out.Component.Enabled || out.Component.SortOrder != 77 || out.Component.Scope != "builtin" || out.Component.UpdatedByUserID != 9 {
		t.Fatalf("patched builtin: %+v", out.Component)
	}
	for _, who := range []testkit.Claims{testkit.User(1), testkit.User(2), testkit.Admin(9)} {
		for _, n := range names(list(t, env, userBase, who)) {
			if n == "chart" {
				t.Fatalf("disabled chart still visible to %v", who)
			}
		}
	}
	// Re-enabling brings it back.
	testkit.Expect(t, env.Do("PATCH", idURL(adminBase, id), admin, map[string]any{"enabled": true}), 200, "")
	if n := names(list(t, env, userBase+"?q=radar", testkit.User(1))); len(n) != 1 {
		t.Fatalf("re-enabled: %v", n)
	}
}

func TestAdminPatchPlatformAndReach(t *testing.T) {
	env := newEnv(t)
	admin := env.Bearer(testkit.Admin(9))
	plat := mk(t, env, adminBase, testkit.Admin(9), validBody("plat"))
	userComp := mk(t, env, mineURL, testkit.User(1), validBody("users"))

	w := env.Do("PATCH", idURL(adminBase, plat.ID), admin, map[string]any{"description": "changed", "name": "plat2", "rendererSource": "<i/>", "version": 2})
	testkit.Expect(t, w, 200, "")
	var out struct {
		Component compJSON `json:"component"`
	}
	testkit.Decode(t, w, &out)
	if out.Component.Description != "changed" || out.Component.Name != "plat2" || out.Component.Version != 2 || out.Component.Scope != "platform" {
		t.Fatalf("platform patch: %+v", out.Component)
	}
	// A user-scoped row addressed through admin is 404 and untouched.
	testkit.Expect(t, env.Do("PATCH", idURL(adminBase, userComp.ID), admin, map[string]any{"enabled": true}), 404, "ui_component.not_found")
	testkit.Expect(t, env.Do("PATCH", idURL(adminBase, 424242), admin, map[string]any{"enabled": true}), 404, "ui_component.not_found")
	testkit.Expect(t, env.Do("PATCH", idURL(adminBase, plat.ID), admin, map[string]any{"name": "chart"}), 200, "") // chart is builtin bucket, platform bucket is free
	other := mk(t, env, adminBase, testkit.Admin(9), validBody("second"))
	testkit.Expect(t, env.Do("PATCH", idURL(adminBase, other.ID), admin, map[string]any{"name": "chart"}), 409, "ui_component.already_exists")
}

// Scenario 7.
func TestAdminDelete(t *testing.T) {
	env := newAuditedEnv(t)
	admin := env.Bearer(testkit.Admin(9))
	testkit.Expect(t, env.Do("DELETE", idURL(adminBase, builtinID(t, env, "chart")), admin, nil), 400, "ui_component.builtin_protected")

	plat := mk(t, env, adminBase, testkit.Admin(9), validBody("plat"))
	userComp := mk(t, env, mineURL, testkit.User(1), validBody("users"))
	testkit.Expect(t, env.Do("DELETE", idURL(adminBase, userComp.ID), admin, nil), 404, "ui_component.not_found")
	testkit.Expect(t, env.Do("DELETE", idURL(adminBase, 424242), admin, nil), 404, "ui_component.not_found")

	w := env.Do("DELETE", idURL(adminBase, plat.ID), admin, nil)
	testkit.Expect(t, w, 200, "")
	var out struct{ Deleted bool }
	testkit.Decode(t, w, &out)
	if !out.Deleted {
		t.Fatalf("body: %s", w.Body)
	}
	testkit.Expect(t, env.Do("DELETE", idURL(adminBase, plat.ID), admin, nil), 404, "ui_component.not_found")

	var logs []audit.Log
	env.DB.Where("action = ?", "ui_component.delete_platform").Find(&logs)
	if len(logs) != 1 || logs[0].Resource != "ui_components" || logs[0].ResourceID != itoa(plat.ID) || logs[0].ActorUserID != 9 {
		t.Fatalf("audit: %+v", logs)
	}
	if d := strings.TrimSpace(logs[0].DetailJSON); d != "" && d != "{}" {
		t.Fatalf("delete audit must have no detail: %q", d)
	}
	// The failed attempts above wrote nothing.
	var n int64
	env.DB.Model(&audit.Log{}).Where("action = ?", "ui_component.delete_platform").Count(&n)
	if n != 1 {
		t.Fatalf("audit rows: %d", n)
	}
	// The user's component survived.
	if got := list(t, env, mineURL, testkit.User(1)); got.Total != 1 {
		t.Fatalf("user component: %+v", got)
	}
}

func itoa(id uint) string { return strconv.FormatUint(uint64(id), 10) }

func TestAdminAudit(t *testing.T) {
	env := newAuditedEnv(t)
	admin := env.Bearer(testkit.Admin(9))
	plat := mk(t, env, adminBase, testkit.Admin(9), validBody("plat"))
	testkit.Expect(t, env.Do("PATCH", idURL(adminBase, plat.ID), admin, map[string]any{"enabled": true}), 200, "")
	// Failed admin operations are not audited.
	testkit.Expect(t, env.Do("PATCH", idURL(adminBase, plat.ID), admin, map[string]any{"name": "Bad"}), 400, "request.invalid_ui_component")
	testkit.Expect(t, env.Do("POST", adminBase, admin, validBody("plat")), 409, "ui_component.already_exists")
	// User operations are not audited.
	c := mk(t, env, mineURL, testkit.User(1), validBody("mine"))
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, c.ID), env.Bearer(testkit.User(1)), map[string]any{"enabled": true}), 200, "")
	testkit.Expect(t, env.Do("DELETE", idURL(mineURL, c.ID), env.Bearer(testkit.User(1)), nil), 200, "")

	var logs []audit.Log
	env.DB.Order("id ASC").Find(&logs)
	if len(logs) != 2 {
		t.Fatalf("audit rows: %+v", logs)
	}
	if logs[0].Action != "ui_component.create_platform" || logs[0].ResourceID != itoa(plat.ID) || logs[0].Resource != "ui_components" {
		t.Fatalf("create audit: %+v", logs[0])
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(logs[0].DetailJSON), &d); err != nil || d["name"] != "plat" || len(d) != 1 {
		t.Fatalf("create detail: %q", logs[0].DetailJSON)
	}
	if logs[1].Action != "ui_component.update" {
		t.Fatalf("update audit: %+v", logs[1])
	}
	d = nil
	if err := json.Unmarshal([]byte(logs[1].DetailJSON), &d); err != nil || d["name"] != "plat" || d["scope"] != "platform" || len(d) != 2 {
		t.Fatalf("update detail: %q", logs[1].DetailJSON)
	}
	if logs[1].RequestID == "" || logs[1].IP == "" || logs[1].ActorUserID != 9 {
		t.Fatalf("request metadata: %+v", logs[1])
	}
	// Built-in updates are audited with scope builtin.
	testkit.Expect(t, env.Do("PATCH", idURL(adminBase, builtinID(t, env, "diff")), admin, map[string]any{"enabled": false}), 200, "")
	var last audit.Log
	env.DB.Order("id DESC").Take(&last)
	if !strings.Contains(last.DetailJSON, `"scope":"builtin"`) || !strings.Contains(last.DetailJSON, `"name":"diff"`) {
		t.Fatalf("builtin audit: %q", last.DetailJSON)
	}
}

func TestMountWithoutAuditWriterStillWorks(t *testing.T) {
	env := newEnv(t) // MountWith(nil, ...)
	plat := mk(t, env, adminBase, testkit.Admin(9), validBody("plat"))
	testkit.Expect(t, env.Do("DELETE", idURL(adminBase, plat.ID), env.Bearer(testkit.Admin(9)), nil), 200, "")
}

// Scenario 8.
func TestAdminList(t *testing.T) {
	env := newEnv(t)
	admin := testkit.Admin(9)
	mk(t, env, adminBase, admin, with(validBody("plat-on"), "enabled", true, "sortOrder", 1))
	mk(t, env, adminBase, admin, with(validBody("plat-off"), "enabled", false, "sortOrder", 1))
	mk(t, env, mineURL, testkit.User(1), with(validBody("users-comp"), "enabled", true))
	testkit.Expect(t, env.Do("PATCH", idURL(adminBase, builtinID(t, env, "quiz")), env.Bearer(admin), map[string]any{"enabled": false}), 200, "")

	testkit.Expect(t, env.Do("GET", adminBase+"?scope=user", env.Bearer(admin), nil), 400, "request.invalid_ui_component")
	testkit.Expect(t, env.Do("GET", adminBase+"?scope=Builtin", env.Bearer(admin), nil), 400, "request.invalid_ui_component")
	testkit.Expect(t, env.Do("GET", adminBase+"?scope=other", env.Bearer(admin), nil), 400, "request.invalid_ui_component")

	all := list(t, env, adminBase+"?page_size=100", admin)
	if all.Total != 13+2 {
		t.Fatalf("total %d: %v", all.Total, names(all))
	}
	for _, r := range all.Results {
		if r.Scope == "user" || r.Name == "users-comp" {
			t.Fatalf("user row leaked: %+v", r)
		}
	}
	// Enabled first, disabled last.
	seenDisabled := false
	for _, r := range all.Results {
		if !r.Enabled {
			seenDisabled = true
		} else if seenDisabled {
			t.Fatalf("enabled row after disabled one: %v", names(all))
		}
	}
	if !seenDisabled {
		t.Fatal("disabled rows should be listed for admins")
	}
	if last := all.Results[len(all.Results)-1]; last.Enabled {
		t.Fatalf("last row should be disabled: %+v", last)
	}

	if b := list(t, env, adminBase+"?scope=builtin&page_size=100", admin); b.Total != 13 {
		t.Fatalf("builtin total %d", b.Total)
	}
	p := list(t, env, adminBase+"?scope=platform", admin)
	if p.Total != 2 || p.Results[0].Name != "plat-on" || p.Results[1].Name != "plat-off" {
		t.Fatalf("platform: %v", names(p))
	}
	if e := list(t, env, adminBase+"?enabled=false&page_size=100", admin); e.Total != 2 {
		t.Fatalf("disabled filter: %v", names(e))
	}
	if q := list(t, env, adminBase+"?q=PLAT-", admin); q.Total != 2 {
		t.Fatalf("q: %v", names(q))
	}
}

func TestAdminListIgnoresGlobalSwitch(t *testing.T) {
	env, sw := newEnvWithSwitch(t)
	sw.on.Store(false)
	if got := list(t, env, adminBase+"?page_size=100", testkit.Admin(9)); got.Total != 13 {
		t.Fatalf("admin list with switch off: %d", got.Total)
	}
	// Management writes keep working.
	mk(t, env, adminBase, testkit.Admin(9), validBody("still-works"))
	mk(t, env, mineURL, testkit.User(1), validBody("still-works"))
}

// Scenario 9.
func TestVisibleCatalog(t *testing.T) {
	env, sw := newEnvWithSwitch(t)
	admin := testkit.Admin(9)
	a, b := testkit.User(1), testkit.User(2)
	mk(t, env, adminBase, admin, with(validBody("plat-2"), "enabled", true, "sortOrder", 2))
	mk(t, env, adminBase, admin, with(validBody("plat-1"), "enabled", true, "sortOrder", 1))
	mk(t, env, adminBase, admin, with(validBody("plat-off"), "enabled", false))
	mk(t, env, mineURL, a, with(validBody("a-comp"), "enabled", true))
	mk(t, env, mineURL, a, with(validBody("a-off"), "enabled", false))
	mk(t, env, mineURL, b, with(validBody("b-comp"), "enabled", true))

	got := names(list(t, env, userBase+"?page_size=100", a))
	wantPrefix := []string{"card-grid", "chart", "function-plot", "stat-grid", "image-gallery", "gantt", "data-table", "calculator", "decision-tree", "diff", "quiz", "panel", "widget"}
	want := append(append([]string{}, wantPrefix...), "plat-1", "plat-2", "a-comp")
	if !sameStrings(got, want) {
		t.Fatalf("catalog for A:\n got %v\nwant %v", got, want)
	}
	gotB := names(list(t, env, userBase+"?page_size=100", b))
	if gotB[len(gotB)-1] != "b-comp" || len(gotB) != len(want) {
		t.Fatalf("catalog for B: %v", gotB)
	}
	for _, n := range gotB {
		if n == "a-comp" {
			t.Fatal("B sees A's component")
		}
	}
	// Catalog rows include the renderer source and are not the owner's view.
	for _, r := range list(t, env, userBase+"?q=plat-1", a).Results {
		if r.RendererSource == "" || r.Scope != "platform" {
			t.Fatalf("platform row: %+v", r)
		}
	}
	// Search.
	if got := names(list(t, env, userBase+"?q=CALC", a)); !sameStrings(got, []string{"calculator"}) {
		t.Fatalf("q: %v", got)
	}
	if got := list(t, env, userBase+"?q=Use%20when", a); got.Total != 16 {
		t.Fatalf("q on description: %d", got.Total)
	}
	if got := list(t, env, userBase+"?q=++", a); got.Total != int64(len(want)) {
		t.Fatalf("blank q: %d", got.Total)
	}

	// Switch off: empty page, still 200 and a non-null array.
	sw.on.Store(false)
	w := env.Do("GET", userBase, env.Bearer(a), nil)
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"results":[]`) || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatalf("switch off body: %s", w.Body)
	}
	sw.on.Store(true)
	if list(t, env, userBase, a).Total == 0 {
		t.Fatal("switch on again should restore the catalog")
	}
}

// Scenario 10.
func TestPagination(t *testing.T) {
	env := newEnv(t)
	u := testkit.User(1)
	if got := list(t, env, userBase, u); got.Total != 13 || len(got.Results) != 13 {
		t.Fatalf("default: total %d len %d", got.Total, len(got.Results))
	}
	for _, q := range []string{"page_size=abc", "page_size=0", "page_size=-3", "page=abc&page_size=5000", "page=0"} {
		got := list(t, env, userBase+"?"+q, u)
		if got.Total != 13 {
			t.Fatalf("%s: total %d", q, got.Total)
		}
	}
	for i := 0; i < 25; i++ {
		mk(t, env, mineURL, u, with(validBody("bulk-"+strings.Repeat("a", i+1)), "enabled", true))
	}
	if got := list(t, env, userBase+"?page_size=abc", u); len(got.Results) != 20 || got.Total != 38 {
		t.Fatalf("size abc: %d/%d", len(got.Results), got.Total)
	}
	if got := list(t, env, userBase+"?page_size=5000", u); len(got.Results) != 38 {
		t.Fatalf("size 5000: %d", len(got.Results))
	}
	if got := list(t, env, userBase+"?page_size=5", u); len(got.Results) != 5 {
		t.Fatalf("size 5: %d", len(got.Results))
	}
	if got := list(t, env, userBase+"?page_size=5&page=8", u); len(got.Results) != 3 || got.Total != 38 {
		t.Fatalf("last page: %d/%d", len(got.Results), got.Total)
	}
	if got := list(t, env, userBase+"?page_size=5&page=9", u); len(got.Results) != 0 || got.Total != 38 {
		t.Fatalf("beyond: %d/%d", len(got.Results), got.Total)
	}
	if got := list(t, env, userBase+"?page_size=1000&page=9223372036854775807", u); len(got.Results) != 0 || got.Total != 38 {
		t.Fatalf("huge page: %d/%d", len(got.Results), got.Total)
	}
}

func TestDisabledOwnComponentHiddenFromCatalogOnly(t *testing.T) {
	env := newEnv(t)
	u := testkit.User(1)
	c := mk(t, env, mineURL, u, with(validBody("quiet"), "enabled", false))
	if got := list(t, env, userBase+"?q=quiet", u); got.Total != 0 {
		t.Fatalf("catalog: %+v", got)
	}
	if got := list(t, env, mineURL+"?q=quiet", u); got.Total != 1 {
		t.Fatalf("mine: %+v", got)
	}
	testkit.Expect(t, env.Do("PATCH", idURL(mineURL, c.ID), env.Bearer(u), map[string]any{"enabled": true}), 200, "")
	if got := list(t, env, userBase+"?q=quiet", u); got.Total != 1 {
		t.Fatalf("catalog after enable: %+v", got)
	}
}
