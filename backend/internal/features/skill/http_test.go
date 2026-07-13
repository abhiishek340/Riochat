package skill_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestCreateNormalizesAndStamps(t *testing.T) {
	s := newSuite(t)
	w := s.do("POST", "/skills/mine", s.as(7), map[string]any{"title": " Plan ", "trigger": "/plan", "markdown": " body "})
	got := s.one(w)
	if got.Title != "Plan" || got.Trigger != "plan" || got.Markdown != "body" || got.Description != "" {
		t.Fatalf("normalized: %+v", got)
	}
	if got.Scope != "user" || got.SortOrder != 1 || got.CreatedByUserID != 7 || got.UpdatedByUserID != 7 || !got.Enabled || got.ID == 0 {
		t.Fatalf("stamps: %+v", got)
	}
	if got.CreatedAt == "" || got.UpdatedAt == "" {
		t.Fatalf("timestamps missing: %+v", got)
	}
}

func TestSortOrderSequencesAreSeparate(t *testing.T) {
	s := newSuite(t)
	a := s.mine(1, "a", "a")
	b := s.mine(1, "b", "b")
	if a.SortOrder != 1 || b.SortOrder != 2 {
		t.Fatalf("orders %d %d", a.SortOrder, b.SortOrder)
	}
	bi := s.builtin("builtin", "x")
	if bi.SortOrder != 1 || bi.Scope != "builtin" || bi.CreatedByUserID != 1 {
		t.Fatalf("builtin: %+v", bi)
	}
	other := s.mine(2, "c", "c")
	if other.SortOrder != 1 {
		t.Fatalf("other user's sequence: %d", other.SortOrder)
	}
	// An explicit positive value is kept; zero and negative are replaced.
	w := s.do("POST", "/skills/mine", s.as(1), map[string]any{"title": "d", "trigger": "d", "markdown": "m", "sortOrder": 40})
	if got := s.one(w); got.SortOrder != 40 {
		t.Fatalf("explicit: %d", got.SortOrder)
	}
	w = s.do("POST", "/skills/mine", s.as(1), map[string]any{"title": "e", "trigger": "e", "markdown": "m", "sortOrder": -5})
	if got := s.one(w); got.SortOrder != 41 {
		t.Fatalf("negative: %d", got.SortOrder)
	}
}

func TestCreateValidation(t *testing.T) {
	s := newSuite(t)
	post := func(body any) int {
		return s.do("POST", "/skills/mine", s.as(1), body).Code
	}
	base := func(over map[string]any) map[string]any {
		m := map[string]any{"title": "t", "trigger": "t" + fmt.Sprint(time.Now().UnixNano()), "markdown": "m"}
		for k, v := range over {
			m[k] = v
		}
		return m
	}
	// Body-level validation: generic invalid body.
	for name, body := range map[string]map[string]any{
		"empty markdown":   base(map[string]any{"markdown": ""}),
		"missing markdown": {"title": "t", "trigger": "x", "markdown": nil},
		"markdown 10001":   base(map[string]any{"markdown": repeat("a", 10001)}),
		"title 65":         base(map[string]any{"title": repeat("a", 65)}),
		"trigger 65":       base(map[string]any{"trigger": repeat("a", 65)}),
		"description 257":  base(map[string]any{"description": repeat("a", 257)}),
		"empty title":      base(map[string]any{"title": ""}),
		"empty trigger":    base(map[string]any{"trigger": ""}),
		"wrong type":       base(map[string]any{"title": 5}),
		"wrong enabled":    base(map[string]any{"enabled": "yes"}),
		"wrong sort":       base(map[string]any{"sortOrder": "x"}),
	} {
		w := s.do("POST", "/skills/mine", s.as(1), body)
		if w.Code != 400 || testkit.Envelope(t, w).ErrorCode != "request.invalid_body" {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	// Business validation after normalization.
	for name, body := range map[string]map[string]any{
		"blank title":        base(map[string]any{"title": "   "}),
		"slash trigger":      base(map[string]any{"trigger": "/"}),
		"many slashes":       base(map[string]any{"trigger": "///"}),
		"slash space":        base(map[string]any{"trigger": " / "}),
		"blank markdown":     base(map[string]any{"markdown": "  \n "}),
		"normalized too big": base(map[string]any{"markdown": repeat("a", 5), "trigger": "/" + repeat("b", 65)}),
	} {
		w := s.do("POST", "/skills/mine", s.as(1), body)
		if name == "normalized too big" {
			// 66 raw characters fail the raw bound first.
			testkit.Expect(t, w, 400, "request.invalid_body")
			continue
		}
		testkit.Expect(t, w, 400, "request.invalid_skill")
		if msg := testkit.Envelope(t, w).ErrorMsg; msg != "invalid skill" {
			t.Errorf("%s message: %q", name, msg)
		}
	}
	// Boundaries that are valid.
	if c := post(base(map[string]any{"markdown": repeat("a", 10000)})); c != 200 {
		t.Fatalf("10000 chars: %d", c)
	}
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	multibyte := repeat("\u754c", 64)
	if c := post(base(map[string]any{"title": multibyte})); c != 200 {
		t.Fatalf("64 multibyte chars: %d", c)
	}
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	if c := post(base(map[string]any{"title": repeat("\u754c", 65)})); c != 400 {
		t.Fatalf("65 multibyte chars: %d", c)
	}
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	if c := post(base(map[string]any{"description": repeat("\u754c", 256)})); c != 200 {
		t.Fatalf("256 description: %d", c)
	}
	// A 64 character trigger behind a slash is valid after stripping.
	if c := post(base(map[string]any{"trigger": "/" + repeat("t", 63)})); c != 200 {
		t.Fatalf("slash + 63: %d", c)
	}
	// Unknown fields are ignored.
	if c := post(base(map[string]any{"bogus": []int{1}})); c != 200 {
		t.Fatalf("unknown field: %d", c)
	}
	testkit.Expect(t, s.do("POST", "/skills/mine", s.as(1), "{not json"), 400, "request.invalid_body")
	testkit.Expect(t, s.do("POST", "/skills/mine", s.as(1), ""), 400, "request.invalid_body")
	testkit.Expect(t, s.do("POST", "/skills/mine", nil, map[string]any{}), 401, "")
}

func TestTriggerNormalizationVariants(t *testing.T) {
	s := newSuite(t)
	for i, trig := range []string{"/plan", "//plan", " /plan ", "/ plan", "plan"} {
		w := s.do("POST", "/skills/mine", s.as(uint(10+i)), map[string]any{"title": "t", "trigger": trig, "markdown": "m"})
		if got := s.one(w); got.Trigger != "plan" {
			t.Errorf("%q -> %q", trig, got.Trigger)
		}
	}
	// The same normalized trigger collides for one user.
	s.mine(50, "a", "/same")
	w := s.do("POST", "/skills/mine", s.as(50), map[string]any{"title": "b", "trigger": "//same", "markdown": "m"})
	testkit.Expect(t, w, 409, "skill_trigger.already_exists")
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "skill trigger already exists" {
		t.Fatalf("message %q", msg)
	}
}

func TestDuplicateTriggerRules(t *testing.T) {
	s := newSuite(t)
	first := s.mine(1, "one", "plan")
	body := map[string]any{"title": "two", "trigger": "plan", "markdown": "m"}
	testkit.Expect(t, s.do("POST", "/skills/mine", s.as(1), body), 409, "skill_trigger.already_exists")
	// Another user and a built-in may reuse it.
	testkit.Expect(t, s.do("POST", "/skills/mine", s.as(2), body), 200, "")
	s.builtin("bi", "plan")
	// Case-sensitive comparison.
	testkit.Expect(t, s.do("POST", "/skills/mine", s.as(1), map[string]any{"title": "x", "trigger": "Plan", "markdown": "m"}), 200, "")
	// Disabled skills still occupy their trigger.
	s.setEnabled(1, first.ID, false)
	testkit.Expect(t, s.do("POST", "/skills/mine", s.as(1), body), 409, "skill_trigger.already_exists")
	// A second built-in with the same trigger collides.
	testkit.Expect(t, s.do("POST", "/admin/skills", s.admin(1), body), 409, "skill_trigger.already_exists")
}

func TestCreateAlwaysEnabled(t *testing.T) {
	s := newSuite(t)
	for i, enabled := range []any{false, true, nil} {
		body := map[string]any{"title": "t", "trigger": fmt.Sprintf("t%d", i), "markdown": "m"}
		if enabled != nil {
			body["enabled"] = enabled
		}
		if got := s.one(s.do("POST", "/skills/mine", s.as(1), body)); !got.Enabled {
			t.Fatalf("enabled=%v produced a disabled skill", enabled)
		}
	}
}

func TestDiscoveryVisibilityAndOrder(t *testing.T) {
	s := newSuite(t)
	aOn := s.mine(1, "a on", "aon")
	aOff := s.mine(1, "a off", "aoff")
	s.setEnabled(1, aOff.ID, false)
	bOn := s.mine(2, "b on", "bon")
	biOn := s.builtin("bi on", "bion")
	biOff := s.builtin("bi off", "bioff")
	s.setBuiltinEnabled(biOff.ID, false)

	w := s.do("GET", "/skills", s.as(1), nil)
	total, list := s.page(w)
	if total != 2 || !eqIDs(ids(list), []uint{aOn.ID, biOn.ID}) {
		t.Fatalf("total %d ids %v (aOn=%d biOn=%d bOn=%d)", total, ids(list), aOn.ID, biOn.ID, bOn.ID)
	}
	if strings.Contains(w.Body.String(), `"markdown"`) {
		t.Fatalf("discovery leaked markdown: %s", w.Body)
	}
	for _, k := range []string{"createdByUserID", "updatedByUserID"} {
		if strings.Contains(w.Body.String(), k) {
			t.Fatalf("summary has %s", k)
		}
	}
	for _, k := range []string{`"id"`, `"scope"`, `"title"`, `"trigger"`, `"description"`, `"enabled"`, `"sortOrder"`, `"createdAt"`, `"updatedAt"`} {
		if !strings.Contains(w.Body.String(), k) {
			t.Fatalf("summary lacks %s: %s", k, w.Body)
		}
	}
	// Another user sees their own plus the built-in.
	_, list = s.page(s.do("GET", "/skills", s.as(2), nil))
	if !eqIDs(ids(list), []uint{bOn.ID, biOn.ID}) {
		t.Fatalf("user 2: %v", ids(list))
	}
	// Empty result is an empty array.
	total, list = s.page(s.do("GET", "/skills?q=zzzz-nothing", s.as(1), nil))
	if total != 0 || len(list) != 0 {
		t.Fatalf("empty: %d %d", total, len(list))
	}
}

func TestDiscoveryOrderingKeys(t *testing.T) {
	s := newSuite(t)
	// user skills first, then sortOrder, then newest update, then highest id.
	bi := s.builtin("bi", "bi")
	u1 := s.mine(1, "u1", "u1") // sort 1
	u2 := s.mine(1, "u2", "u2") // sort 2
	w := s.do("POST", "/skills/mine", s.as(1), map[string]any{"title": "u0", "trigger": "u0", "markdown": "m", "sortOrder": 1})
	u0 := s.one(w) // sort 1, newer than u1 after clock advance
	s.env.Clock.Advance(time.Minute)
	s.one(s.do("PATCH", fmt.Sprintf("/skills/mine/%d", u0.ID), s.as(1), map[string]any{"description": "bump"}))
	_, list := s.page(s.do("GET", "/skills", s.as(1), nil))
	want := []uint{u0.ID, u1.ID, u2.ID, bi.ID}
	if !eqIDs(ids(list), want) {
		t.Fatalf("order %v want %v", ids(list), want)
	}
	// Same sort order and timestamp: higher id first.
	x := s.mine(5, "x", "x")
	y := s.mine(5, "y", "y")
	s.one(s.do("PATCH", fmt.Sprintf("/skills/mine/%d", x.ID), s.as(5), map[string]any{"sortOrder": 9}))
	s.one(s.do("PATCH", fmt.Sprintf("/skills/mine/%d", y.ID), s.as(5), map[string]any{"sortOrder": 9}))
	_, list = s.page(s.do("GET", "/skills", s.as(5), nil))
	if len(list) < 2 || list[0].ID != y.ID || list[1].ID != x.ID {
		t.Fatalf("tie break: %v", ids(list))
	}
}

func TestDiscoveryIDFilter(t *testing.T) {
	s := newSuite(t)
	x := s.mine(1, "x", "x")
	y := s.mine(1, "y", "y")
	other := s.mine(2, "o", "o")
	_, list := s.page(s.do("GET", fmt.Sprintf("/skills?id=%d&id=%d", x.ID, x.ID), s.as(1), nil))
	if len(list) != 1 || list[0].ID != x.ID {
		t.Fatalf("duplicate ids: %v", ids(list))
	}
	total, list := s.page(s.do("GET", fmt.Sprintf("/skills?id=%d&id=%d&id=%d", x.ID, y.ID, other.ID), s.as(1), nil))
	if total != 2 || len(list) != 2 {
		t.Fatalf("invisible id leaked: %d %v", total, ids(list))
	}
	for _, q := range []string{"id=1&id=1&id=abc", "id=0", "id=-3", "id=", "id=1.5"} {
		w := s.do("GET", "/skills?"+q, s.as(1), nil)
		testkit.Expect(t, w, 400, "skill.invalid_id")
		if msg := testkit.Envelope(t, w).ErrorMsg; msg != "invalid skill id" {
			t.Fatalf("%s message %q", q, msg)
		}
	}
	many := make([]string, 129)
	for i := range many {
		many[i] = "id=1"
	}
	w := s.do("GET", "/skills?"+strings.Join(many, "&"), s.as(1), nil)
	testkit.Expect(t, w, 400, "skill.too_many_ids")
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "too many skill ids" {
		t.Fatalf("message %q", msg)
	}
	// Exactly 128 values (duplicates) is fine.
	testkit.Expect(t, s.do("GET", "/skills?"+strings.Join(many[:128], "&"), s.as(1), nil), 200, "")
}

func TestDiscoverySearchAndPaging(t *testing.T) {
	s := newSuite(t)
	s.one(s.do("POST", "/skills/mine", s.as(1), map[string]any{"title": "Alpha", "trigger": "zeta", "description": "gamma thing", "markdown": "only-in-markdown"}))
	s.mine(1, "Other", "other")
	for q, want := range map[string]int{
		"alpha": 1, "ALPHA": 1, "  alpha ": 1, "ZET": 1, "GAMMA": 1, "only-in-markdown": 0, "": 2, "nothing": 0,
	} {
		total, list := s.page(s.do("GET", "/skills?q="+strings.ReplaceAll(q, " ", "%20")+"", s.as(1), nil))
		if int(total) != want || len(list) != want {
			t.Errorf("q=%q: total %d len %d want %d", q, total, len(list), want)
		}
	}
	// "%25" is a literal percent that is not escaped for LIKE: it matches all.
	if total, _ := s.page(s.do("GET", "/skills?q=%25", s.as(1), nil)); total != 2 {
		t.Fatalf("unescaped wildcard total %d", total)
	}
	// Search never widens visibility.
	s.mine(2, "Alpha secret", "alphasecret")
	if total, _ := s.page(s.do("GET", "/skills?q=alpha", s.as(1), nil)); total != 1 {
		t.Fatalf("other user's match leaked: %d", total)
	}
	// Paging.
	for i := 0; i < 5; i++ {
		s.mine(3, fmt.Sprintf("p%d", i), fmt.Sprintf("p%d", i))
	}
	total, list := s.page(s.do("GET", "/skills?page=2&page_size=2", s.as(3), nil))
	if total != 5 || len(list) != 2 {
		t.Fatalf("page 2: %d %d", total, len(list))
	}
	total, list = s.page(s.do("GET", "/skills?page=99&page_size=2", s.as(3), nil))
	if total != 5 || len(list) != 0 {
		t.Fatalf("beyond: %d %d", total, len(list))
	}
	for _, q := range []string{"page=0&page_size=0", "page=x&page_size=y", "page=-1&page_size=-1"} {
		total, list = s.page(s.do("GET", "/skills?"+q, s.as(3), nil))
		if total != 5 || len(list) != 5 {
			t.Fatalf("%s: %d %d", q, total, len(list))
		}
	}
	testkit.Expect(t, s.do("GET", "/skills?page=9223372036854775807&page_size=1000", s.as(3), nil), 200, "")
}

func TestDetailVisibility(t *testing.T) {
	s := newSuite(t)
	mineOn := s.mine(1, "m", "m")
	mineOff := s.mine(1, "off", "off")
	s.setEnabled(1, mineOff.ID, false)
	theirs := s.mine(2, "t", "t")
	bi := s.builtin("bi", "bi")
	biOff := s.builtin("bioff", "bioff")
	s.setBuiltinEnabled(biOff.ID, false)

	get := func(id uint) (int, string) {
		w := s.do("GET", fmt.Sprintf("/skills/%d", id), s.as(1), nil)
		return w.Code, testkit.Envelope(t, w).ErrorCode
	}
	if c, _ := get(mineOn.ID); c != 200 {
		t.Fatalf("own: %d", c)
	}
	got := s.one(s.do("GET", fmt.Sprintf("/skills/%d", bi.ID), s.as(1), nil))
	if got.Markdown != "body of bi" || got.Scope != "builtin" {
		t.Fatalf("builtin detail: %+v", got)
	}
	for name, id := range map[string]uint{"other user": theirs.ID, "disabled own": mineOff.ID, "disabled builtin": biOff.ID, "missing": 99999} {
		if c, code := get(id); c != 404 || code != "skill.not_found" {
			t.Errorf("%s: %d %s", name, c, code)
		}
	}
	for _, p := range []string{"abc", "0", "-1", "1.5", "99999999999999999999"} {
		testkit.Expect(t, s.do("GET", "/skills/"+p, s.as(1), nil), 400, "skill.invalid_id")
	}
}

func TestMineRouteIsNotCapturedByID(t *testing.T) {
	s := newSuite(t)
	s.mine(1, "a", "a")
	total, list := s.page(s.do("GET", "/skills/mine", s.as(1), nil))
	if total != 1 || len(list) != 1 {
		t.Fatalf("mine: %d %d", total, len(list))
	}
	if list[0].Markdown == "" {
		t.Fatalf("mine must include markdown")
	}
}

func TestMineListingFilters(t *testing.T) {
	s := newSuite(t)
	a := s.mine(1, "alpha", "alpha")
	off := s.mine(1, "beta", "beta")
	s.setEnabled(1, off.ID, false)
	s.one(s.do("POST", "/skills/mine", s.as(1), map[string]any{"title": "gamma", "trigger": "gamma", "markdown": "needle in markdown"}))
	s.mine(2, "theirs", "theirs")
	s.builtin("bi", "bi")

	total, list := s.page(s.do("GET", "/skills/mine", s.as(1), nil))
	if total != 3 || len(list) != 3 {
		t.Fatalf("all own: %d", total)
	}
	// Enabled first, then sort order.
	if !list[0].Enabled || !list[1].Enabled || list[2].Enabled || list[2].ID != off.ID {
		t.Fatalf("order: %+v", list)
	}
	_, list = s.page(s.do("GET", "/skills/mine?enabled=false", s.as(1), nil))
	if len(list) != 1 || list[0].ID != off.ID {
		t.Fatalf("enabled=false: %v", ids(list))
	}
	_, list = s.page(s.do("GET", "/skills/mine?enabled=true", s.as(1), nil))
	if len(list) != 2 {
		t.Fatalf("enabled=true: %v", ids(list))
	}
	for _, v := range []string{"maybe", "", "%20true%20", "yes"} {
		total, _ = s.page(s.do("GET", "/skills/mine?enabled="+v, s.as(1), nil))
		if total != 3 {
			t.Errorf("enabled=%q should not filter: %d", v, total)
		}
	}
	for _, v := range []string{"1", "t", "T", "TRUE"} {
		if total, _ = s.page(s.do("GET", "/skills/mine?enabled="+v, s.as(1), nil)); total != 2 {
			t.Errorf("enabled=%q: %d", v, total)
		}
	}
	for _, v := range []string{"0", "f", "F", "FALSE"} {
		if total, _ = s.page(s.do("GET", "/skills/mine?enabled="+v, s.as(1), nil)); total != 1 {
			t.Errorf("enabled=%q: %d", v, total)
		}
	}
	// Markdown-only match: found in mine, not in discovery.
	total, list = s.page(s.do("GET", "/skills/mine?q=NEEDLE", s.as(1), nil))
	if total != 1 || list[0].Title != "gamma" {
		t.Fatalf("mine markdown search: %d", total)
	}
	if total, _ = s.page(s.do("GET", "/skills?q=needle", s.as(1), nil)); total != 0 {
		t.Fatalf("discovery must not search markdown: %d", total)
	}
	// Combined filter is conjunctive.
	if total, _ = s.page(s.do("GET", "/skills/mine?q=alpha&enabled=false", s.as(1), nil)); total != 0 {
		t.Fatalf("conjunction: %d", total)
	}
	_ = a
}

func TestPatchSemantics(t *testing.T) {
	s := newSuite(t)
	sk := s.mine(1, "orig", "orig")
	other := s.mine(1, "other", "other")
	bi := s.builtin("bi", "bi")
	path := fmt.Sprintf("/skills/mine/%d", sk.ID)

	// Built-in and foreign skills are not found.
	testkit.Expect(t, s.do("PATCH", fmt.Sprintf("/skills/mine/%d", bi.ID), s.as(1), map[string]any{"title": "x"}), 404, "skill.not_found")
	testkit.Expect(t, s.do("PATCH", path, s.as(2), map[string]any{"title": "x"}), 404, "skill.not_found")
	testkit.Expect(t, s.do("PATCH", "/skills/mine/99999", s.as(1), map[string]any{"title": "x"}), 404, "skill.not_found")
	testkit.Expect(t, s.do("PATCH", "/skills/mine/0", s.as(1), map[string]any{"title": "x"}), 400, "skill.invalid_id")
	testkit.Expect(t, s.do("PATCH", "/skills/mine/abc", s.as(1), map[string]any{"title": "x"}), 400, "skill.invalid_id")

	s.env.Clock.Advance(time.Hour)
	got := s.one(s.do("PATCH", path, s.as(1), map[string]any{"enabled": false}))
	if got.Enabled || got.UpdatedAt == sk.UpdatedAt || got.CreatedAt != sk.CreatedAt {
		t.Fatalf("disable: %+v (before %+v)", got, sk)
	}
	if got.Title != "orig" || got.Markdown != sk.Markdown {
		t.Fatalf("untouched fields changed: %+v", got)
	}

	// Validation.
	testkit.Expect(t, s.do("PATCH", path, s.as(1), map[string]any{"title": "  "}), 400, "request.invalid_skill")
	testkit.Expect(t, s.do("PATCH", path, s.as(1), map[string]any{"title": ""}), 400, "request.invalid_skill")
	testkit.Expect(t, s.do("PATCH", path, s.as(1), map[string]any{"trigger": "/"}), 400, "request.invalid_skill")
	testkit.Expect(t, s.do("PATCH", path, s.as(1), map[string]any{"markdown": " "}), 400, "request.invalid_skill")
	testkit.Expect(t, s.do("PATCH", path, s.as(1), map[string]any{"title": repeat("a", 65)}), 400, "request.invalid_body")
	testkit.Expect(t, s.do("PATCH", path, s.as(1), map[string]any{"enabled": "x"}), 400, "request.invalid_body")

	got = s.one(s.do("PATCH", path, s.as(1), map[string]any{"trigger": "/x"}))
	if got.Trigger != "x" {
		t.Fatalf("trigger: %q", got.Trigger)
	}
	got = s.one(s.do("PATCH", path, s.as(1), map[string]any{"description": "d"}))
	got = s.one(s.do("PATCH", path, s.as(1), map[string]any{"description": ""}))
	if got.Description != "" {
		t.Fatalf("description should clear: %q", got.Description)
	}
	got = s.one(s.do("PATCH", path, s.as(1), map[string]any{"sortOrder": -7, "title": " New ", "markdown": " md "}))
	if got.SortOrder != -7 || got.Title != "New" || got.Markdown != "md" {
		t.Fatalf("multi: %+v", got)
	}

	// Trigger collisions.
	testkit.Expect(t, s.do("PATCH", path, s.as(1), map[string]any{"trigger": "/other"}), 409, "skill_trigger.already_exists")
	if got = s.one(s.do("PATCH", path, s.as(1), map[string]any{"trigger": "x"})); got.Trigger != "x" {
		t.Fatalf("own value: %+v", got)
	}
	// The failed patch left the row untouched.
	if got = s.one(s.do("GET", path[:len("/skills")]+fmt.Sprintf("/%d", other.ID), s.as(1), nil)); got.Trigger != "other" {
		t.Fatalf("other row: %+v", got)
	}
}

func TestEmptyPatchStampsActor(t *testing.T) {
	s := newSuite(t)
	sk := s.mine(4, "t", "t")
	s.env.Clock.Advance(time.Minute)
	got := s.one(s.do("PATCH", fmt.Sprintf("/skills/mine/%d", sk.ID), s.as(4), map[string]any{}))
	if got.Title != "t" || got.UpdatedByUserID != 4 || got.UpdatedAt == sk.UpdatedAt {
		t.Fatalf("empty patch: %+v", got)
	}
}

func TestDeleteRemovesRowAndLinks(t *testing.T) {
	s := newSuite(t)
	sk := s.mine(1, "t", "t")
	keep := s.mine(1, "k", "k")
	s.env.DB.Exec("INSERT INTO chat_conversation_project_skills (project_id, skill_id, sort_order) VALUES (1, ?, 1), (1, ?, 2), (2, ?, 1)", sk.ID, keep.ID, sk.ID)

	testkit.Expect(t, s.do("DELETE", fmt.Sprintf("/skills/mine/%d", sk.ID), s.as(2), nil), 404, "skill.not_found")
	w := s.do("DELETE", fmt.Sprintf("/skills/mine/%d", sk.ID), s.as(1), nil)
	testkit.Expect(t, w, 200, "")
	var out struct{ Deleted bool }
	testkit.Decode(t, w, &out)
	if !out.Deleted {
		t.Fatalf("deleted flag: %s", w.Body)
	}
	testkit.Expect(t, s.do("DELETE", fmt.Sprintf("/skills/mine/%d", sk.ID), s.as(1), nil), 404, "skill.not_found")
	testkit.Expect(t, s.do("DELETE", "/skills/mine/abc", s.as(1), nil), 400, "skill.invalid_id")

	var links int64
	s.env.DB.Table("chat_conversation_project_skills").Where("skill_id = ?", sk.ID).Count(&links)
	if links != 0 {
		t.Fatalf("links left: %d", links)
	}
	s.env.DB.Table("chat_conversation_project_skills").Where("skill_id = ?", keep.ID).Count(&links)
	if links != 1 {
		t.Fatalf("unrelated link must remain: %d", links)
	}
	// The trigger is free again.
	testkit.Expect(t, s.do("POST", "/skills/mine", s.as(1), map[string]any{"title": "again", "trigger": "t", "markdown": "m"}), 200, "")
}

func TestDeleteBuiltinViaPersonalRouteIs404(t *testing.T) {
	s := newSuite(t)
	bi := s.builtin("bi", "bi")
	testkit.Expect(t, s.do("DELETE", fmt.Sprintf("/skills/mine/%d", bi.ID), s.as(1), nil), 404, "skill.not_found")
	testkit.Expect(t, s.do("GET", fmt.Sprintf("/skills/%d", bi.ID), s.as(1), nil), 200, "")
}

func TestAdminGateAndRoutes(t *testing.T) {
	s := newSuite(t)
	user := s.as(3)
	body := map[string]any{"title": "t", "trigger": "t", "markdown": "m"}
	testkit.Expect(t, s.do("GET", "/admin/skills", user, nil), 403, "auth.admin_required")
	testkit.Expect(t, s.do("POST", "/admin/skills", user, body), 403, "auth.admin_required")
	testkit.Expect(t, s.do("PATCH", "/admin/skills/1", user, body), 403, "auth.admin_required")
	testkit.Expect(t, s.do("DELETE", "/admin/skills/1", user, nil), 403, "auth.admin_required")
	testkit.Expect(t, s.do("GET", "/admin/skills", nil, nil), 401, "")

	bi := s.builtin("bi", "bi")
	mine := s.mine(3, "mine", "mine")
	// Admin routes only see built-ins.
	total, list := s.page(s.do("GET", "/admin/skills", s.admin(1), nil))
	if total != 1 || list[0].ID != bi.ID || list[0].Markdown == "" {
		t.Fatalf("admin list: %d %v", total, ids(list))
	}
	if total, _ = s.page(s.do("GET", "/admin/skills?q=body%20of", s.admin(1), nil)); total != 1 {
		t.Fatalf("admin markdown search: %d", total)
	}
	if total, _ = s.page(s.do("GET", "/admin/skills?enabled=false", s.admin(1), nil)); total != 0 {
		t.Fatalf("admin enabled filter: %d", total)
	}
	// Targets that are not built-in are 404.
	testkit.Expect(t, s.do("PATCH", fmt.Sprintf("/admin/skills/%d", mine.ID), s.admin(1), map[string]any{"title": "x"}), 404, "skill.not_found")
	testkit.Expect(t, s.do("DELETE", fmt.Sprintf("/admin/skills/%d", mine.ID), s.admin(1), nil), 404, "skill.not_found")
	testkit.Expect(t, s.do("PATCH", "/admin/skills/zero", s.admin(1), map[string]any{"title": "x"}), 400, "skill.invalid_id")
	// The built-in is stored with owner 0 and the acting admin.
	var owner uint
	s.env.DB.Table("skills").Select("owner_user_id").Where("id = ?", bi.ID).Scan(&owner)
	if owner != 0 || bi.CreatedByUserID != 1 {
		t.Fatalf("owner %d created-by %d", owner, bi.CreatedByUserID)
	}
	upd := s.one(s.do("PATCH", fmt.Sprintf("/admin/skills/%d", bi.ID), s.admin(9), map[string]any{"title": "renamed"}))
	if upd.UpdatedByUserID != 9 || upd.CreatedByUserID != 1 {
		t.Fatalf("actors: %+v", upd)
	}
}

type auditRow struct {
	Action      string
	Resource    string
	ResourceID  string
	ActorUserID uint
	DetailJSON  string
	RequestID   string
}

func (s *suite) audits() []auditRow {
	var rows []auditRow
	s.env.DB.Table("audit_logs").Order("id ASC").Find(&rows)
	return rows
}

func TestAdminAudit(t *testing.T) {
	s := newSuite(t)
	bi := s.builtin("bi", "/plan")
	s.one(s.do("PATCH", fmt.Sprintf("/admin/skills/%d", bi.ID), s.admin(2), map[string]any{"trigger": "/plan2"}))
	testkit.Expect(t, s.do("DELETE", fmt.Sprintf("/admin/skills/%d", bi.ID), s.admin(3), nil), 200, "")
	// Failed operations and personal operations are not audited.
	testkit.Expect(t, s.do("DELETE", fmt.Sprintf("/admin/skills/%d", bi.ID), s.admin(3), nil), 404, "")
	s.mine(1, "mine", "mine")

	rows := s.audits()
	if len(rows) != 3 {
		t.Fatalf("audit rows: %+v", rows)
	}
	id := fmt.Sprint(bi.ID)
	want := []auditRow{
		{Action: "skill.create_builtin", Resource: "skills", ResourceID: id, ActorUserID: 1, DetailJSON: `{"trigger":"plan"}`},
		{Action: "skill.update_builtin", Resource: "skills", ResourceID: id, ActorUserID: 2, DetailJSON: `{"trigger":"plan2"}`},
		{Action: "skill.delete_builtin", Resource: "skills", ResourceID: id, ActorUserID: 3, DetailJSON: `{}`},
	}
	for i, w := range want {
		g := rows[i]
		if g.Action != w.Action || g.Resource != w.Resource || g.ResourceID != w.ResourceID || g.ActorUserID != w.ActorUserID || g.DetailJSON != w.DetailJSON {
			t.Errorf("row %d: %+v want %+v", i, g, w)
		}
		if g.RequestID == "" {
			t.Errorf("row %d lacks request id", i)
		}
	}
}

func TestConcurrentPatchesAreSerialized(t *testing.T) {
	s := newSuite(t)
	sk := s.mine(1, "t", "t")
	var wg sync.WaitGroup
	errs := make(chan string, 16)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := s.do("PATCH", fmt.Sprintf("/skills/mine/%d", sk.ID), s.as(1), map[string]any{"description": fmt.Sprintf("d%d", i)})
			if w.Code != 200 {
				errs <- w.Body.String()
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("concurrent patch failed: %s", e)
	}
	got := s.one(s.do("GET", fmt.Sprintf("/skills/%d", sk.ID), s.as(1), nil))
	if !strings.HasPrefix(got.Description, "d") {
		t.Fatalf("final: %+v", got)
	}
}

func TestResponsesNeverLeakInternals(t *testing.T) {
	s := newSuite(t)
	w := s.do("GET", "/skills/99999", s.as(1), nil)
	if strings.Contains(strings.ToLower(w.Body.String()), "sql") {
		t.Fatalf("leak: %s", w.Body)
	}
}

func TestPatchWithBlankBodyIsAccepted(t *testing.T) {
	s := newSuite(t)
	sk := s.mine(6, "t", "t")
	s.env.Clock.Advance(time.Minute)
	got := s.one(s.do("PATCH", fmt.Sprintf("/skills/mine/%d", sk.ID), s.as(6), nil))
	if got.Title != "t" || got.UpdatedByUserID != 6 || got.UpdatedAt == sk.UpdatedAt {
		t.Fatalf("blank patch: %+v", got)
	}
}

func TestSortOrderOutsideColumnRangeIsInvalidBody(t *testing.T) {
	s := newSuite(t)
	big := int64(1) << 40
	testkit.Expect(t, s.do("POST", "/skills/mine", s.as(1), map[string]any{
		"title": "a", "trigger": "a", "markdown": "m", "sortOrder": big,
	}), 400, "request.invalid_body")
	sk := s.mine(1, "b", "b")
	testkit.Expect(t, s.do("PATCH", fmt.Sprintf("/skills/mine/%d", sk.ID), s.as(1), map[string]any{"sortOrder": big}), 400, "request.invalid_body")
	got := s.one(s.do("PATCH", fmt.Sprintf("/skills/mine/%d", sk.ID), s.as(1), map[string]any{"sortOrder": 2147483647}))
	if got.SortOrder != 2147483647 {
		t.Fatalf("sort order: %+v", got)
	}
}
