package announcement_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/announcement"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestCreateDefaults(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "Maintenance", "contentMarkdown": "Tonight at 22:00"})
	if it.ID == 0 || it.Title != "Maintenance" || it.ContentMarkdown != "Tonight at 22:00" {
		t.Fatalf("created: %+v", it)
	}
	if it.Status != "active" || it.Type != "general" || it.Pinned || it.Priority != 0 || it.CreatedByUserID != 12 {
		t.Fatalf("defaults: %+v", it)
	}
	if it.StartsAt != nil || it.ExpiresAt != nil || it.ClosedAt != nil {
		t.Fatalf("bounds and closedAt must be null: %+v", it)
	}
	if it.CreatedAt == "" || it.UpdatedAt == "" {
		t.Fatalf("timestamps missing: %+v", it)
	}
	var row announcement.Announcement
	if err := s.env.DB.First(&row, it.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != "active" || row.Type != "general" || row.CreatedByUserID != 12 {
		t.Fatalf("stored row: %+v", row)
	}
}

func TestCreateResponseCarriesEveryField(t *testing.T) {
	s := newSuite(t)
	w := s.env.Do("POST", adminPath, s.adm(), map[string]any{"title": "t", "contentMarkdown": "c"})
	testkit.Expect(t, w, 200, "")
	var raw struct {
		Data struct {
			Announcement map[string]any `json:"announcement"`
		} `json:"data"`
	}
	testkit.Decode(t, w, &raw.Data)
	for _, k := range []string{"id", "title", "contentMarkdown", "status", "type", "pinned", "priority",
		"startsAt", "expiresAt", "createdByUserID", "createdAt", "updatedAt", "closedAt"} {
		if _, ok := raw.Data.Announcement[k]; !ok {
			t.Errorf("field %q missing from %v", k, raw.Data.Announcement)
		}
	}
	if len(raw.Data.Announcement) != 13 {
		t.Errorf("unexpected extra fields: %v", raw.Data.Announcement)
	}
}

func TestCreateRequestValidation(t *testing.T) {
	s := newSuite(t)
	cases := map[string]map[string]any{
		"missing content":    {"title": "x"},
		"missing title":      {"contentMarkdown": "x"},
		"empty title":        {"title": "", "contentMarkdown": "x"},
		"empty content":      {"title": "x", "contentMarkdown": ""},
		"status archived":    {"title": "x", "contentMarkdown": "x", "status": "archived"},
		"status padded":      {"title": "x", "contentMarkdown": "x", "status": " active"},
		"type urgent":        {"title": "x", "contentMarkdown": "x", "type": "urgent"},
		"title too long":     {"title": strings.Repeat("a", 121), "contentMarkdown": "x"},
		"content too long":   {"title": "x", "contentMarkdown": strings.Repeat("a", 20001)},
		"bad startsAt":       {"title": "x", "contentMarkdown": "x", "startsAt": "tomorrow"},
		"empty startsAt":     {"title": "x", "contentMarkdown": "x", "startsAt": ""},
		"priority not int":   {"title": "x", "contentMarkdown": "x", "priority": "high"},
		"pinned not boolean": {"title": "x", "contentMarkdown": "x", "pinned": "yes"},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			testkit.Expect(t, s.env.Do("POST", adminPath, s.adm(), body), 400, "request.invalid_body")
		})
	}
	testkit.Expect(t, s.env.Do("POST", adminPath, s.adm(), nil), 400, "request.invalid_body")
	testkit.Expect(t, s.env.Do("POST", adminPath, s.adm(), "{not json"), 400, "request.invalid_body")
	w := s.env.Do("POST", adminPath, s.adm(), map[string]any{"title": "x"})
	if testkit.Envelope(t, w).Details == nil {
		t.Fatalf("validation failure must carry field details: %s", w.Body)
	}
	var n int64
	s.env.DB.Model(&announcement.Announcement{}).Count(&n)
	if n != 0 {
		t.Fatalf("rejected requests must store nothing, found %d rows", n)
	}
}

func TestCreateBusinessValidation(t *testing.T) {
	s := newSuite(t)
	cases := map[string]map[string]any{
		"blank content":     {"title": "x", "contentMarkdown": "   "},
		"blank title":       {"title": " \t\n", "contentMarkdown": "x"},
		"end equals start":  {"title": "x", "contentMarkdown": "x", "startsAt": at(time.Hour), "expiresAt": at(time.Hour)},
		"end before start":  {"title": "x", "contentMarkdown": "x", "startsAt": at(2 * time.Hour), "expiresAt": at(time.Hour)},
		"same instant, tz":  {"title": "x", "contentMarkdown": "x", "startsAt": "2026-03-01T10:00:00Z", "expiresAt": "2026-03-01T18:00:00+08:00"},
		"nanosecond before": {"title": "x", "contentMarkdown": "x", "startsAt": "2026-03-01T10:00:00.000000002Z", "expiresAt": "2026-03-01T10:00:00.000000001Z"},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			w := s.env.Do("POST", adminPath, s.adm(), body)
			testkit.Expect(t, w, 400, "request.invalid_announcement")
			if msg := testkit.Envelope(t, w).ErrorMsg; msg != "invalid announcement" {
				t.Fatalf("message %q", msg)
			}
		})
	}
}

func TestCreateWithWindowPriorityAndFlags(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{
		"title": "Window", "contentMarkdown": "body", "priority": 5, "pinned": true, "type": "warning",
		"status": "inactive", "startsAt": "2026-03-01T08:00:00Z", "expiresAt": "2026-03-01T09:00:00Z",
	})
	if it.Priority != 5 || !it.Pinned || it.Type != "warning" || it.Status != "inactive" {
		t.Fatalf("created: %+v", it)
	}
	if it.StartsAt == nil || *it.StartsAt != "2026-03-01T08:00:00Z" || it.ExpiresAt == nil || *it.ExpiresAt != "2026-03-01T09:00:00Z" {
		t.Fatalf("window echo: %v %v", deref(it.StartsAt), deref(it.ExpiresAt))
	}
	neg := s.create(map[string]any{"title": "n", "contentMarkdown": "n", "priority": -7})
	if neg.Priority != -7 {
		t.Fatalf("negative priority: %+v", neg)
	}
}

func deref(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestCreateWindowShapes(t *testing.T) {
	s := newSuite(t)
	s.create(map[string]any{"title": "start only", "contentMarkdown": "x", "startsAt": at(time.Hour)})
	s.create(map[string]any{"title": "end only", "contentMarkdown": "x", "expiresAt": at(time.Hour)})
	past := s.create(map[string]any{"title": "ended", "contentMarkdown": "x", "startsAt": at(-3 * time.Hour), "expiresAt": at(-2 * time.Hour)})
	if past.ID == 0 {
		t.Fatal("an already ended window must still be creatable")
	}
	sameInstant := s.create(map[string]any{"title": "tz", "contentMarkdown": "x", "startsAt": "2026-03-01T10:00:00Z", "expiresAt": "2026-03-01T18:00:01+08:00"})
	if sameInstant.ID == 0 {
		t.Fatal("offsets must be compared as instants")
	}
}

func TestCreateTrimsAndStoresVerbatim(t *testing.T) {
	s := newSuite(t)
	md := "# Heading\n\n<script>alert(1)</script> **bold**\n- a\n- b"
	it := s.create(map[string]any{"title": "  Padded title \n", "contentMarkdown": "\n  " + md + "  \t"})
	if it.Title != "Padded title" || it.ContentMarkdown != md {
		t.Fatalf("trim/verbatim failed: %q %q", it.Title, it.ContentMarkdown)
	}
}

func TestCreateLengthLimitsCountCharacters(t *testing.T) {
	s := newSuite(t)
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	title := strings.Repeat("\u516c", 120)
	content := strings.Repeat("é", 20000)
	it := s.create(map[string]any{"title": title, "contentMarkdown": content})
	if it.Title != title || it.ContentMarkdown != content {
		t.Fatal("multi-byte text at the character limit must be accepted")
	}
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	testkit.Expect(t, s.env.Do("POST", adminPath, s.adm(), map[string]any{"title": strings.Repeat("\u516c", 121), "contentMarkdown": "x"}), 400, "request.invalid_body")
}

func TestCreateCreatorIsAuthenticatedAdmin(t *testing.T) {
	s := newSuite(t)
	w := s.env.Do("POST", adminPath, s.env.Bearer(testkit.Admin(77)), map[string]any{
		"title": "x", "contentMarkdown": "x", "createdByUserID": 1, "id": 999, "unknown": true,
	})
	testkit.Expect(t, w, 200, "")
	var out wrapped
	testkit.Decode(t, w, &out)
	if out.Announcement.CreatedByUserID != 77 || out.Announcement.ID == 999 {
		t.Fatalf("client-supplied fields must be ignored: %+v", out.Announcement)
	}
}

func TestPatchValidation(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	bad := map[string]map[string]any{
		"status archived": {"status": "archived"},
		"status padded":   {"status": " active"},
		"status empty":    {"status": ""},
		"type urgent":     {"type": "urgent"},
		"type empty":      {"type": ""},
		"title empty":     {"title": ""},
		"content empty":   {"contentMarkdown": ""},
		"title too long":  {"title": strings.Repeat("a", 121)},
		"content long":    {"contentMarkdown": strings.Repeat("a", 20001)},
		"startsAt junk":   {"startsAt": "soon"},
		"startsAt number": {"startsAt": 5},
		"expiresAt junk":  {"expiresAt": "2026-13-45T00:00:00Z"},
		"expiresAt bool":  {"expiresAt": true},
		"pinned string":   {"pinned": "true"},
		"priority float":  {"priority": 1.5},
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			testkit.Expect(t, s.patch(it.ID, body), 400, "request.invalid_body")
		})
	}
	for name, body := range map[string]map[string]any{
		"title blank":   {"title": "   "},
		"content blank": {"contentMarkdown": "\n\t"},
	} {
		t.Run(name, func(t *testing.T) {
			testkit.Expect(t, s.patch(it.ID, body), 400, "request.invalid_announcement")
		})
	}
	testkit.Expect(t, s.patch(it.ID, nil), 400, "request.invalid_body")
	got := s.patchOK(it.ID, map[string]any{})
	if got.UpdatedAt != it.UpdatedAt {
		t.Fatal("rejected patches must not change the version")
	}
}

func TestPatchStatusAndVisibility(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	testkit.Expect(t, s.patch(it.ID, map[string]any{"status": "archived"}), 400, "request.invalid_body")
	got := s.patchOK(it.ID, map[string]any{"status": "inactive"})
	if got.Status != "inactive" {
		t.Fatalf("status: %+v", got)
	}
	if n := len(s.list(1, "")); n != 0 {
		t.Fatalf("inactive announcement must leave the user list, got %d", n)
	}
	s.patchOK(it.ID, map[string]any{"status": "active"})
	if n := len(s.list(1, "")); n != 1 {
		t.Fatalf("reactivated announcement must return, got %d", n)
	}
}

func TestPatchUpdatesEveryField(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "old", "contentMarkdown": "old"})
	got := s.patchOK(it.ID, map[string]any{
		"title": " new ", "contentMarkdown": " body ", "status": "inactive", "type": "critical",
		"pinned": true, "priority": -3, "startsAt": "2026-04-01T00:00:00Z", "expiresAt": "2026-04-02T00:00:00Z",
	})
	if got.Title != "new" || got.ContentMarkdown != "body" || got.Status != "inactive" || got.Type != "critical" ||
		!got.Pinned || got.Priority != -3 || deref(got.StartsAt) != "2026-04-01T00:00:00Z" || deref(got.ExpiresAt) != "2026-04-02T00:00:00Z" {
		t.Fatalf("patched: %+v", got)
	}
	if got.CreatedAt != it.CreatedAt || got.CreatedByUserID != it.CreatedByUserID || got.ID != it.ID {
		t.Fatalf("immutable fields changed: %+v vs %+v", got, it)
	}
}

func TestPatchClearsAndKeepsBounds(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x", "startsAt": "2026-03-01T08:00:00Z", "expiresAt": "2026-03-01T12:00:00Z"})

	got := s.patchOK(it.ID, map[string]any{"title": "renamed"})
	if got.StartsAt == nil || got.ExpiresAt == nil {
		t.Fatalf("absent keys must leave bounds: %+v", got)
	}
	got = s.patchOK(it.ID, map[string]any{"expiresAt": nil})
	if got.ExpiresAt != nil || got.StartsAt == nil {
		t.Fatalf("null clears only the end: %+v", got)
	}
	got = s.patchOK(it.ID, map[string]any{"startsAt": ""})
	if got.StartsAt != nil {
		t.Fatalf("empty string clears the start: %+v", got)
	}
	s.patchOK(it.ID, map[string]any{"startsAt": "2026-03-01T08:00:00Z", "expiresAt": "2026-03-01T12:00:00Z"})
	got = s.patchOK(it.ID, map[string]any{"startsAt": "   ", "expiresAt": ""})
	if got.StartsAt != nil || got.ExpiresAt != nil {
		t.Fatalf("whitespace and empty strings clear both: %+v", got)
	}
	var row announcement.Announcement
	s.env.DB.First(&row, it.ID)
	if row.StartsAt != nil || row.ExpiresAt != nil {
		t.Fatalf("columns must be NULL: %+v", row)
	}
}

func TestPatchWindowRuleAgainstStoredBounds(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x", "startsAt": "2026-03-01T08:00:00Z", "expiresAt": "2026-03-01T12:00:00Z"})
	for name, body := range map[string]map[string]any{
		"start after stored end":    {"startsAt": "2026-03-01T13:00:00Z"},
		"start equals stored end":   {"startsAt": "2026-03-01T12:00:00Z"},
		"end before stored start":   {"expiresAt": "2026-03-01T07:00:00Z"},
		"end equals stored start":   {"expiresAt": "2026-03-01T08:00:00Z"},
		"both in patch conflicting": {"startsAt": "2026-03-02T00:00:00Z", "expiresAt": "2026-03-01T00:00:00Z"},
		"both equal":                {"startsAt": "2026-03-02T00:00:00Z", "expiresAt": "2026-03-02T00:00:00Z"},
	} {
		t.Run(name, func(t *testing.T) {
			testkit.Expect(t, s.patch(it.ID, body), 400, "request.invalid_announcement")
		})
	}
	// A conflicting patch changed nothing.
	var row announcement.Announcement
	s.env.DB.First(&row, it.ID)
	if row.StartsAt == nil || row.ExpiresAt == nil || row.UpdatedAt.UTC().Format(time.RFC3339Nano) != it.UpdatedAt {
		t.Fatalf("failed patch must not write: %+v", row)
	}
	// Moving both bounds together is fine even if each alone would conflict.
	got := s.patchOK(it.ID, map[string]any{"startsAt": "2026-03-05T00:00:00Z", "expiresAt": "2026-03-06T00:00:00Z"})
	if deref(got.StartsAt) != "2026-03-05T00:00:00Z" {
		t.Fatalf("moved window: %+v", got)
	}
	// Clearing one side removes the conflict.
	s.patchOK(it.ID, map[string]any{"expiresAt": nil, "startsAt": "2026-04-01T00:00:00Z"})
}

func TestPatchNotFoundAndBadIDs(t *testing.T) {
	s := newSuite(t)
	w := s.patch(999, map[string]any{"title": "x"})
	testkit.Expect(t, w, 404, "announcement.not_found")
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "announcement not found" {
		t.Fatalf("message %q", msg)
	}
	for _, id := range []string{"abc", "0", "-1", "1.5", "99999999999999999999999"} {
		w := s.env.Do("PATCH", adminPath+"/"+id, s.adm(), map[string]any{"title": "x"})
		testkit.Expect(t, w, 400, "announcement.invalid_id")
		if msg := testkit.Envelope(t, w).ErrorMsg; msg != "invalid announcement id" {
			t.Fatalf("message %q", msg)
		}
	}
	// Even an empty patch on a missing id is a 404.
	testkit.Expect(t, s.patch(999, map[string]any{}), 404, "announcement.not_found")
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	testkit.Expect(t, s.env.Do("DELETE", fmt.Sprintf("%s/%d", adminPath, it.ID), s.adm(), nil), 200, "")
	testkit.Expect(t, s.patch(it.ID, map[string]any{"title": "y"}), 404, "announcement.not_found")
}

func TestPatchVersionRules(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})

	for name, body := range map[string]any{
		"empty object":       map[string]any{},
		"unknown keys only":  map[string]any{"foo": 1, "createdByUserID": 5},
		"explicit nulls":     map[string]any{"title": nil, "contentMarkdown": nil, "status": nil, "type": nil, "pinned": nil, "priority": nil},
		"id and timestamps":  map[string]any{"id": 4, "updatedAt": "2020-01-01T00:00:00Z"},
		"creator is ignored": map[string]any{"createdByUserID": 99},
	} {
		got := s.patchOK(it.ID, body)
		if got.UpdatedAt != it.UpdatedAt || got.CreatedByUserID != 12 || got.Title != "x" {
			t.Fatalf("%s: version or data changed: %+v", name, got)
		}
	}

	// The fake clock does not move, yet a write still produces a new version.
	same := s.patchOK(it.ID, map[string]any{"title": "x"})
	if same.UpdatedAt == it.UpdatedAt {
		t.Fatal("writing an identical value must still produce a new version")
	}
	next := s.patchOK(it.ID, map[string]any{"priority": 0})
	if next.UpdatedAt == same.UpdatedAt {
		t.Fatal("every write needs its own version")
	}
	a, _ := time.Parse(time.RFC3339Nano, same.UpdatedAt)
	b, _ := time.Parse(time.RFC3339Nano, next.UpdatedAt)
	if !b.After(a) {
		t.Fatalf("versions must increase: %v then %v", a, b)
	}
	// createdByUserID mixed with a real field is still ignored.
	mixed := s.patchOK(it.ID, map[string]any{"createdByUserID": 99, "pinned": true})
	if mixed.CreatedByUserID != 12 || !mixed.Pinned {
		t.Fatalf("mixed: %+v", mixed)
	}
}

func TestPatchVersionFollowsClock(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	s.env.Clock.Advance(90 * time.Minute)
	got := s.patchOK(it.ID, map[string]any{"title": "y"})
	if got.UpdatedAt != baseTime.Add(90*time.Minute).Format(time.RFC3339Nano) {
		t.Fatalf("version should be the clock reading, got %s", got.UpdatedAt)
	}
	if got.CreatedAt != it.CreatedAt {
		t.Fatal("created_at must not move")
	}
}

func TestDeleteFlow(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	other := s.create(map[string]any{"title": "y", "contentMarkdown": "y"})
	testkit.Expect(t, s.dismiss(5, it), 200, "")
	url := fmt.Sprintf("%s/%d", adminPath, it.ID)

	w := s.env.Do("DELETE", url, s.adm(), nil)
	testkit.Expect(t, w, 200, "")
	var out struct{ Deleted bool }
	testkit.Decode(t, w, &out)
	if !out.Deleted {
		t.Fatalf("body: %s", w.Body)
	}
	testkit.Expect(t, s.env.Do("DELETE", url, s.adm(), nil), 404, "announcement.not_found")
	testkit.Expect(t, s.env.Do("DELETE", adminPath+"/abc", s.adm(), nil), 400, "announcement.invalid_id")
	testkit.Expect(t, s.env.Do("DELETE", adminPath+"/0", s.adm(), nil), 400, "announcement.invalid_id")
	testkit.Expect(t, s.env.Do("DELETE", adminPath+"/12345", s.adm(), nil), 404, "announcement.not_found")

	sameIDs(t, s.list(1, ""), other.ID)
	sameIDs(t, s.list(5, "?include_dismissed=true"), other.ID)
	var page struct {
		Total   int64
		Results []item
	}
	testkit.Decode(t, s.env.Do("GET", adminPath, s.adm(), nil), &page)
	if page.Total != 1 || page.Results[0].ID != other.ID {
		t.Fatalf("admin list after delete: %+v", page)
	}
	testkit.Expect(t, s.dismiss(1, it), 404, "announcement.not_found")
	testkit.Expect(t, s.closeIt(1, it), 404, "announcement.not_found")
	// The row is kept (soft delete) and the user's state stays in place.
	var n int64
	s.env.DB.Unscoped().Model(&announcement.Announcement{}).Where("id = ?", it.ID).Count(&n)
	if n != 1 {
		t.Fatalf("soft-deleted row must be kept, found %d", n)
	}
	if countStates(t, s.env, it.ID, 5) != 1 {
		t.Fatal("existing user-state rows must be left in place")
	}
}

func TestAdminListFiltersAndPaging(t *testing.T) {
	s := newSuite(t)
	a := s.create(map[string]any{"title": "Site notice", "contentMarkdown": "one", "type": "warning", "pinned": true, "priority": 1})
	b := s.create(map[string]any{"title": "Other", "contentMarkdown": "Contains NOTICE inside", "status": "inactive", "type": "warning"})
	c := s.create(map[string]any{"title": "Third", "contentMarkdown": "three", "type": "info", "priority": 9})
	d := s.create(map[string]any{"title": "Fourth", "contentMarkdown": "four", "startsAt": at(48 * time.Hour)})
	e := s.create(map[string]any{"title": "Fifth", "contentMarkdown": "five", "startsAt": at(-48 * time.Hour), "expiresAt": at(-24 * time.Hour)})

	type page struct {
		Total   int64
		Results []item
	}
	query := func(q string) page {
		t.Helper()
		w := s.env.Do("GET", adminPath+q, s.adm(), nil)
		testkit.Expect(t, w, 200, "")
		var p page
		testkit.Decode(t, w, &p)
		return p
	}

	all := query("")
	// pinned first, then priority desc, then newest update, then id desc.
	sameIDs(t, all.Results, a.ID, c.ID, e.ID, d.ID, b.ID)
	if all.Total != 5 {
		t.Fatalf("total %d", all.Total)
	}
	for _, r := range all.Results {
		if r.ClosedAt != nil {
			t.Fatal("closedAt must be null in admin results")
		}
	}

	if p := query("?q=notice"); p.Total != 2 {
		t.Fatalf("q=notice: %+v", p)
	} else {
		sameIDs(t, p.Results, a.ID, b.ID)
	}
	if p := query("?q=%20%20NoTiCe%20"); p.Total != 2 {
		t.Fatalf("keyword is trimmed and case-insensitive: %+v", p)
	}
	if p := query("?q=%20%20"); p.Total != 5 {
		t.Fatalf("blank keyword is no filter: %+v", p)
	}
	if p := query("?q=100%25"); p.Total != 0 {
		t.Fatalf("percent must not act as a wildcard: %+v", p)
	}
	if p := query("?q=on_"); p.Total != 0 {
		t.Fatalf("underscore must not act as a wildcard: %+v", p)
	}
	if p := query("?status=inactive"); p.Total != 1 || p.Results[0].ID != b.ID {
		t.Fatalf("status filter: %+v", p)
	}
	if p := query("?status=%20inactive%20"); p.Total != 1 {
		t.Fatalf("status filter is trimmed: %+v", p)
	}
	if p := query("?status=archived"); p.Total != 0 || p.Results == nil || len(p.Results) != 0 {
		t.Fatalf("unknown status matches nothing: %+v", p)
	}
	if p := query("?type=warning"); p.Total != 2 {
		t.Fatalf("type filter: %+v", p)
	}
	if p := query("?type=urgent"); p.Total != 0 {
		t.Fatalf("unknown type matches nothing: %+v", p)
	}
	if p := query("?pinned=true"); p.Total != 1 || p.Results[0].ID != a.ID {
		t.Fatalf("pinned=true: %+v", p)
	}
	if p := query("?pinned=false"); p.Total != 4 {
		t.Fatalf("pinned=false: %+v", p)
	}
	for _, q := range []string{"?pinned=", "?pinned=maybe", "?pinned=yes", "?pinned=%20true"} {
		if p := query(q); p.Total != 5 {
			t.Fatalf("%s must not filter: %+v", q, p)
		}
	}
	if p := query("?q=notice&status=inactive&type=warning&pinned=false"); p.Total != 1 || p.Results[0].ID != b.ID {
		t.Fatalf("combined filters use AND: %+v", p)
	}
	if p := query("?q=notice&status=inactive&pinned=true"); p.Total != 0 {
		t.Fatalf("combined filters use AND: %+v", p)
	}

	p2 := query("?page_size=2&page=2")
	if p2.Total != 5 {
		t.Fatalf("full total expected: %+v", p2)
	}
	sameIDs(t, p2.Results, e.ID, d.ID)
	if p := query("?page_size=2&page=3"); p.Total != 5 || len(p.Results) != 1 {
		t.Fatalf("last page: %+v", p)
	}
	if p := query("?page_size=2&page=9"); p.Total != 5 || p.Results == nil || len(p.Results) != 0 {
		t.Fatalf("beyond last page keeps the true total: %+v", p)
	}
	if p := query("?page=0&page_size=-3"); p.Total != 5 || len(p.Results) != 5 {
		t.Fatalf("invalid paging falls back to defaults: %+v", p)
	}
	if p := query("?page_size=abc&page=xyz"); len(p.Results) != 5 {
		t.Fatalf("junk paging falls back to defaults: %+v", p)
	}
	if p := query("?page_size=1000000"); len(p.Results) != 5 {
		t.Fatalf("huge page size is capped: %+v", p)
	}
}

func TestAdminListPageSizeDefaultsToTwenty(t *testing.T) {
	s := newSuite(t)
	for i := 0; i < 23; i++ {
		s.create(map[string]any{"title": fmt.Sprintf("n%d", i), "contentMarkdown": "x"})
	}
	var p struct {
		Total   int64
		Results []item
	}
	testkit.Decode(t, s.env.Do("GET", adminPath, s.adm(), nil), &p)
	if p.Total != 23 || len(p.Results) != 20 {
		t.Fatalf("default page: total=%d len=%d", p.Total, len(p.Results))
	}
	testkit.Decode(t, s.env.Do("GET", adminPath+"?page_size=1000&page=1", s.adm(), nil), &p)
	if len(p.Results) != 23 {
		t.Fatalf("cap allows 1000: %d", len(p.Results))
	}
}

func TestAdminListOrderingByUpdatedAtThenID(t *testing.T) {
	s := newSuite(t)
	first := s.create(map[string]any{"title": "first", "contentMarkdown": "x"})
	second := s.create(map[string]any{"title": "second", "contentMarkdown": "x"})
	third := s.create(map[string]any{"title": "third", "contentMarkdown": "x"})
	s.env.Clock.Advance(time.Minute)
	s.patchOK(first.ID, map[string]any{"title": "first!"})
	var p struct{ Results []item }
	testkit.Decode(t, s.env.Do("GET", adminPath, s.adm(), nil), &p)
	sameIDs(t, p.Results, first.ID, third.ID, second.ID)
}

func TestAccessControl(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	user := s.user(3)
	id := fmt.Sprintf("%s/%d", adminPath, it.ID)
	for _, call := range []struct{ method, path string }{
		{"GET", adminPath}, {"POST", adminPath}, {"PATCH", id}, {"DELETE", id},
	} {
		testkit.Expect(t, s.env.Do(call.method, call.path, user, map[string]any{"title": "x", "contentMarkdown": "x"}), 403, "auth.admin_required")
		testkit.Expect(t, s.env.Do(call.method, call.path, nil, map[string]any{"title": "x"}), 401, "auth.invalid_token")
	}
	testkit.Expect(t, s.env.Do("GET", userListPath, nil, nil), 401, "auth.invalid_token")
	testkit.Expect(t, s.env.Do("POST", userListPath+"/1/close", nil, map[string]any{"updatedAt": it.UpdatedAt}), 401, "auth.invalid_token")
	// Administrators may use the user endpoints too.
	testkit.Expect(t, s.env.Do("GET", userListPath, s.adm(), nil), 200, "")
}
