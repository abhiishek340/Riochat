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
