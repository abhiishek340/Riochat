package announcement_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/announcement"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestUserListEmptyIsArray(t *testing.T) {
	s := newSuite(t)
	w := s.env.Do("GET", userListPath, s.user(1), nil)
	testkit.Expect(t, w, 200, "")
	if got := testkit.Envelope(t, w); got.Data == nil {
		t.Fatalf("data must be an empty array, not null: %s", w.Body)
	}
	if n := len(s.list(1, "")); n != 0 {
		t.Fatalf("len %d", n)
	}
}

func TestUserListOrdering(t *testing.T) {
	s := newSuite(t)
	plainOld := s.create(map[string]any{"title": "plain old", "contentMarkdown": "x"})
	s.env.Clock.Advance(time.Minute)
	plainNew := s.create(map[string]any{"title": "plain new", "contentMarkdown": "x"})
	hiPrio := s.create(map[string]any{"title": "priority", "contentMarkdown": "x", "priority": 5})
	pinnedLow := s.create(map[string]any{"title": "pinned low", "contentMarkdown": "x", "pinned": true, "priority": -1})
	pinnedHigh := s.create(map[string]any{"title": "pinned high", "contentMarkdown": "x", "pinned": true, "priority": 3})
	// Same pin, priority and update instant: higher id first.
	tieA := s.create(map[string]any{"title": "tie a", "contentMarkdown": "x", "priority": 2})
	tieB := s.create(map[string]any{"title": "tie b", "contentMarkdown": "x", "priority": 2})

	sameIDs(t, s.list(1, ""), pinnedHigh.ID, pinnedLow.ID, hiPrio.ID, tieB.ID, tieA.ID, plainNew.ID, plainOld.ID)
}

func TestUserListFiltersByEffectWindow(t *testing.T) {
	s := newSuite(t)
	visible := s.create(map[string]any{"title": "visible", "contentMarkdown": "x"})
	s.create(map[string]any{"title": "future", "contentMarkdown": "x", "startsAt": at(time.Second)})
	s.create(map[string]any{"title": "past", "contentMarkdown": "x", "expiresAt": at(-time.Second)})
	s.create(map[string]any{"title": "off", "contentMarkdown": "x", "status": "inactive"})
	gone := s.create(map[string]any{"title": "gone", "contentMarkdown": "x"})
	testkit.Expect(t, s.env.Do("DELETE", fmt.Sprintf("%s/%d", adminPath, gone.ID), s.adm(), nil), 200, "")
	inWindow := s.create(map[string]any{"title": "window", "contentMarkdown": "x", "startsAt": at(-time.Hour), "expiresAt": at(time.Hour)})
	startsNow := s.create(map[string]any{"title": "starts now", "contentMarkdown": "x", "startsAt": at(0)})
	endsNow := s.create(map[string]any{"title": "ends now", "contentMarkdown": "x", "expiresAt": at(0)})

	got := s.list(1, "")
	have := map[uint]bool{}
	for _, it := range got {
		have[it.ID] = true
	}
	if len(got) != 3 || !have[visible.ID] || !have[inWindow.ID] || !have[startsNow.ID] {
		t.Fatalf("visible set: %v (start bound inclusive, end bound exclusive)", ids(got))
	}
	if have[endsNow.ID] {
		t.Fatal("an announcement expiring exactly now is no longer in effect")
	}

	// Time moving makes the future one appear and the others expire.
	s.env.Clock.Advance(2 * time.Hour)
	got = s.list(1, "")
	if len(got) != 3 { // visible, startsNow, future (inWindow ended)
		t.Fatalf("after advancing: %v", ids(got))
	}
}

func TestDismissFlow(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	keep := s.create(map[string]any{"title": "y", "contentMarkdown": "y"})

	w := s.dismiss(1, it)
	testkit.Expect(t, w, 200, "")
	var out struct{ Dismissed bool }
	testkit.Decode(t, w, &out)
	if !out.Dismissed {
		t.Fatalf("body: %s", w.Body)
	}
	sameIDs(t, s.list(1, ""), keep.ID)
	got := s.list(1, "?include_dismissed=true")
	sameIDs(t, got, keep.ID, it.ID)
	for _, g := range got {
		if g.ClosedAt != nil {
			t.Fatal("dismissing must not set closedAt")
		}
	}
	// Other users still see it.
	sameIDs(t, s.list(2, ""), keep.ID, it.ID)
}

func TestDismissUntilNextLocalMidnight(t *testing.T) {
	zone, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Skip("tz database unavailable")
	}
	old := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = old })

	s := newSuite(t) // clock: 2026-03-01 09:00Z = 14:30 IST
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	testkit.Expect(t, s.dismiss(1, it), 200, "")

	var st announcement.UserState
	if err := s.env.DB.Where("announcement_id = ? AND user_id = ?", it.ID, 1).First(&st).Error; err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 3, 2, 0, 0, 0, 0, zone)
	if st.DismissedUntil == nil || !st.DismissedUntil.Equal(want) {
		t.Fatalf("dismissed_until = %v, want %v", st.DismissedUntil, want)
	}
	// Still hidden five minutes before local midnight, visible again at midnight.
	s.env.Clock.Set(want.Add(-5 * time.Minute))
	if n := len(s.list(1, "")); n != 0 {
		t.Fatal("must stay hidden until midnight")
	}
	s.env.Clock.Set(want)
	if n := len(s.list(1, "")); n != 1 {
		t.Fatal("a dismissed_until equal to now no longer hides")
	}
	s.env.Clock.Set(want.Add(time.Second))
	if n := len(s.list(1, "")); n != 1 {
		t.Fatal("must be visible after midnight")
	}
}

func TestDismissJustBeforeMidnightIsStrictlyLater(t *testing.T) {
	zone, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("tz database unavailable")
	}
	old := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = old })

	s := newSuite(t)
	s.env.Clock.Set(time.Date(2026, 3, 1, 23, 59, 59, 999_000_000, zone))
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	testkit.Expect(t, s.dismiss(1, it), 200, "")
	var st announcement.UserState
	s.env.DB.Where("announcement_id = ?", it.ID).First(&st)
	if !st.DismissedUntil.After(s.env.Clock.Now()) || !st.DismissedUntil.Equal(time.Date(2026, 3, 2, 0, 0, 0, 0, zone)) {
		t.Fatalf("dismissed_until %v", st.DismissedUntil)
	}
}

func TestDismissAcrossDaylightSavingChange(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tz database unavailable")
	}
	old := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = old })

	s := newSuite(t)
	// 2026-03-07 is the day before the spring-forward change in New York.
	s.env.Clock.Set(time.Date(2026, 3, 7, 12, 0, 0, 0, zone))
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	testkit.Expect(t, s.dismiss(1, it), 200, "")
	var st announcement.UserState
	s.env.DB.Where("announcement_id = ?", it.ID).First(&st)
	if !st.DismissedUntil.Equal(time.Date(2026, 3, 8, 0, 0, 0, 0, zone)) {
		t.Fatalf("dismissed_until %v", st.DismissedUntil)
	}
}

func TestDismissTwiceKeepsOneRowAndRefreshesTime(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	testkit.Expect(t, s.dismiss(1, it), 200, "")
	var first announcement.UserState
	s.env.DB.Where("announcement_id = ?", it.ID).First(&first)

	s.env.Clock.Advance(48 * time.Hour) // a later day: the hide-until value must be replaced
	s.patchOK(it.ID, map[string]any{"priority": 0})
	var cur announcement.Announcement
	s.env.DB.First(&cur, it.ID)
	// Same version dismissed twice on the same version first.
	testkit.Expect(t, s.dismiss(2, it), 404, "announcement.not_found") // stale after the edit
	s.env.Clock.Advance(time.Minute)
	version := cur.UpdatedAt.UTC().Format(time.RFC3339Nano)
	testkit.Expect(t, s.act(1, "dismiss-today", it.ID, version), 200, "")
	s.env.Clock.Advance(24 * time.Hour)
	testkit.Expect(t, s.act(1, "dismiss-today", it.ID, version), 200, "")
	if n := countStates(t, s.env, it.ID, 1); n != 2 {
		t.Fatalf("one row per version expected (old + new version): %d", n)
	}
	var rows []announcement.UserState
	s.env.DB.Where("announcement_id = ? AND user_id = ? AND announcement_updated_at = ?", it.ID, 1, cur.UpdatedAt).Find(&rows)
	if len(rows) != 1 {
		t.Fatalf("rows for the current version: %d", len(rows))
	}
	if first.DismissedUntil == nil || !rows[0].DismissedUntil.After(*first.DismissedUntil) {
		t.Fatalf("the repeat call must replace the hide-until time: %v vs %v", rows[0].DismissedUntil, first.DismissedUntil)
	}
}

func TestRepeatedDismissSameVersionSingleRow(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	for i := 0; i < 3; i++ {
		testkit.Expect(t, s.dismiss(1, it), 200, "")
	}
	if n := countStates(t, s.env, it.ID, 1); n != 1 {
		t.Fatalf("rows: %d", n)
	}
}

func TestEditResurfacesAfterDismissAndClose(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	closed := s.create(map[string]any{"title": "c", "contentMarkdown": "c"})
	testkit.Expect(t, s.dismiss(1, it), 200, "")
	testkit.Expect(t, s.closeIt(1, closed), 200, "")
	if n := len(s.list(1, "")); n != 1 {
		t.Fatalf("setup: %d", n)
	}
	edited := s.patchOK(it.ID, map[string]any{"title": "x2"})
	s.patchOK(closed.ID, map[string]any{"title": "c2"})
	got := s.list(1, "")
	if len(got) != 2 {
		t.Fatalf("both edited announcements must show again: %v", ids(got))
	}
	for _, g := range got {
		if g.ClosedAt != nil {
			t.Fatalf("closedAt must reset with a new version: %+v", g)
		}
	}
	// The old version's timestamp no longer works; the new one does.
	testkit.Expect(t, s.dismiss(1, it), 404, "announcement.not_found")
	testkit.Expect(t, s.dismiss(1, edited), 200, "")
}

func TestCloseFlow(t *testing.T) {
	s := newSuite(t)
	a := s.create(map[string]any{"title": "a", "contentMarkdown": "x", "pinned": true, "priority": 10})
	b := s.create(map[string]any{"title": "b", "contentMarkdown": "x"})
	c := s.create(map[string]any{"title": "c", "contentMarkdown": "x", "priority": -5})

	s.env.Clock.Advance(7 * time.Minute)
	w := s.closeIt(1, a)
	testkit.Expect(t, w, 200, "")
	var out struct{ Closed bool }
	testkit.Decode(t, w, &out)
	if !out.Closed {
		t.Fatalf("body: %s", w.Body)
	}

	got := s.list(1, "")
	sameIDs(t, got, b.ID, c.ID, a.ID) // closed one drops after every non-closed one
	if got[2].ClosedAt == nil || *got[2].ClosedAt != at(7*time.Minute) {
		t.Fatalf("closedAt = %v, want %s", got[2].ClosedAt, at(7*time.Minute))
	}
	if got[0].ClosedAt != nil || got[1].ClosedAt != nil {
		t.Fatal("others must have null closedAt")
	}
	// Other users are unaffected.
	sameIDs(t, s.list(2, ""), a.ID, b.ID, c.ID)

	// Closing again succeeds and refreshes the stored close time.
	s.env.Clock.Advance(time.Hour)
	testkit.Expect(t, s.closeIt(1, a), 200, "")
	got = s.list(1, "")
	if *got[2].ClosedAt != at(7*time.Minute+time.Hour) {
		t.Fatalf("closedAt after repeat = %s", *got[2].ClosedAt)
	}
	if n := countStates(t, s.env, a.ID, 1); n != 1 {
		t.Fatalf("rows: %d", n)
	}
}

func TestDismissAndCloseAreIndependent(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	other := s.create(map[string]any{"title": "y", "contentMarkdown": "y", "priority": -10})

	testkit.Expect(t, s.dismiss(1, it), 200, "")
	testkit.Expect(t, s.closeIt(1, it), 200, "")
	var st announcement.UserState
	s.env.DB.Where("announcement_id = ?", it.ID).First(&st)
	if st.DismissedUntil == nil || st.ClosedAt == nil {
		t.Fatalf("closing must not drop the hide-until: %+v", st)
	}
	// Still hidden by default (dismissed), and closed items stay listed
	// otherwise, so the default list only has the other one.
	sameIDs(t, s.list(1, ""), other.ID)
	// With include_dismissed the item appears, closedAt set, ordered last.
	got := s.list(1, "?include_dismissed=true")
	sameIDs(t, got, other.ID, it.ID)
	if got[1].ClosedAt == nil {
		t.Fatal("closedAt expected")
	}
	// Dismissing again must not drop the close time either.
	testkit.Expect(t, s.dismiss(1, it), 200, "")
	s.env.DB.Where("announcement_id = ?", it.ID).First(&st)
	if st.ClosedAt == nil {
		t.Fatal("dismissing must not remove an existing close time")
	}
}

func TestCloseDoesNotHideWithoutDismissal(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	testkit.Expect(t, s.closeIt(1, it), 200, "")
	for _, q := range []string{"", "?include_dismissed=true", "?include_dismissed=false"} {
		got := s.list(1, q)
		if len(got) != 1 || got[0].ClosedAt == nil {
			t.Fatalf("%q: closed items are listed with closedAt: %+v", q, got)
		}
	}
}

func TestStateIsPerUser(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	testkit.Expect(t, s.dismiss(1, it), 200, "")
	testkit.Expect(t, s.closeIt(2, it), 200, "")
	sameIDs(t, s.list(1, ""))
	got := s.list(2, "")
	if len(got) != 1 || got[0].ClosedAt == nil {
		t.Fatalf("user 2: %+v", got)
	}
	got = s.list(3, "")
	if len(got) != 1 || got[0].ClosedAt != nil {
		t.Fatalf("user 3: %+v", got)
	}
}

func TestIncludeDismissedSpellings(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	testkit.Expect(t, s.dismiss(1, it), 200, "")
	for _, v := range []string{"1", "t", "T", "true", "True", "TRUE"} {
		if n := len(s.list(1, "?include_dismissed="+v)); n != 1 {
			t.Errorf("include_dismissed=%s should include, got %d", v, n)
		}
	}
	for _, v := range []string{"0", "f", "F", "false", "False", "FALSE", "", "yes", "on", "2", "tRuE", "%20true", "true%20", "+true"} {
		if n := len(s.list(1, "?include_dismissed="+v)); n != 0 {
			t.Errorf("include_dismissed=%q should not include, got %d", v, n)
		}
	}
}

func TestDismissAndCloseNotFoundCases(t *testing.T) {
	s := newSuite(t)
	live := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	future := s.create(map[string]any{"title": "future", "contentMarkdown": "x", "startsAt": at(time.Hour)})
	expired := s.create(map[string]any{"title": "expired", "contentMarkdown": "x", "expiresAt": at(-time.Hour)})
	inactive := s.create(map[string]any{"title": "off", "contentMarkdown": "x", "status": "inactive"})
	deleted := s.create(map[string]any{"title": "gone", "contentMarkdown": "x"})
	testkit.Expect(t, s.env.Do("DELETE", fmt.Sprintf("%s/%d", adminPath, deleted.ID), s.adm(), nil), 200, "")
	unknown := item{ID: 4242, UpdatedAt: live.UpdatedAt}

	stale := live
	stale.UpdatedAt = at(-time.Hour) // a plausible but wrong version
	nearly := live
	nearly.UpdatedAt = "2026-03-01T09:00:00.000001Z" // differs by a microsecond

	for name, it := range map[string]item{
		"stale version": stale, "off by a microsecond": nearly, "future": future, "expired": expired,
		"inactive": inactive, "deleted": deleted, "unknown": unknown,
	} {
		for _, action := range []string{"dismiss-today", "close"} {
			t.Run(name+"/"+action, func(t *testing.T) {
				w := s.act(1, action, it.ID, it.UpdatedAt)
				testkit.Expect(t, w, 404, "announcement.not_found")
				if msg := testkit.Envelope(t, w).ErrorMsg; msg != "announcement not found" {
					t.Fatalf("message %q", msg)
				}
			})
		}
	}
	var n int64
	s.env.DB.Model(&announcement.UserState{}).Count(&n)
	if n != 0 {
		t.Fatalf("failed attempts must not write state, found %d rows", n)
	}
}

func TestDismissStopsWorkingOnceExpired(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x", "expiresAt": at(time.Hour)})
	s.env.Clock.Advance(time.Hour) // the end bound is exclusive
	testkit.Expect(t, s.dismiss(1, it), 404, "announcement.not_found")
	testkit.Expect(t, s.closeIt(1, it), 404, "announcement.not_found")
}

func TestVersionAcceptedInAnyOffset(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	parsed, err := time.Parse(time.RFC3339Nano, it.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	shifted := parsed.In(time.FixedZone("x", 5*3600+1800)).Format(time.RFC3339Nano)
	testkit.Expect(t, s.act(1, "close", it.ID, shifted), 200, "")
}

func TestChoiceBodyValidation(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	url := fmt.Sprintf("%s/%d/", userListPath, it.ID)
	for _, action := range []string{"dismiss-today", "close"} {
		bodies := map[string]any{
			"no body":          nil,
			"empty object":     map[string]any{},
			"null timestamp":   map[string]any{"updatedAt": nil},
			"not a timestamp":  map[string]any{"updatedAt": "yesterday"},
			"number":           map[string]any{"updatedAt": 12345},
			"empty string":     map[string]any{"updatedAt": ""},
			"zero value":       map[string]any{"updatedAt": "0001-01-01T00:00:00Z"},
			"zero value offs.": map[string]any{"updatedAt": "0001-01-01T00:00:00+05:00"},
			"not json":         "{{{",
			"wrong key":        map[string]any{"updated_at": it.UpdatedAt},
		}
		for name, body := range bodies {
			t.Run(action+"/"+name, func(t *testing.T) {
				w := s.env.Do("POST", url+action, s.user(1), body)
				testkit.Expect(t, w, 400, "request.invalid_body")
			})
		}
		// Detail for the required-field case names the field.
		w := s.env.Do("POST", url+action, s.user(1), map[string]any{})
		if msg := testkit.Envelope(t, w).ErrorMsg; msg != "invalid request body: updatedAt is required" {
			t.Fatalf("message %q", msg)
		}
		// Bad ids.
		for _, id := range []string{"abc", "0", "-3", "1e3"} {
			w := s.env.Do("POST", fmt.Sprintf("%s/%s/%s", userListPath, id, action), s.user(1), map[string]any{"updatedAt": it.UpdatedAt})
			testkit.Expect(t, w, 400, "announcement.invalid_id")
		}
	}
	if n := countStates(t, s.env, it.ID, 1); n != 0 {
		t.Fatalf("invalid requests must not write: %d", n)
	}
}

func TestRevivesSoftDeletedStateRow(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	testkit.Expect(t, s.closeIt(1, it), 200, "")
	if err := s.env.DB.Where("announcement_id = ?", it.ID).Delete(&announcement.UserState{}).Error; err != nil {
		t.Fatal(err)
	}
	if n := len(s.list(1, "")); n != 1 || s.list(1, "")[0].ClosedAt != nil {
		t.Fatal("a soft-deleted state row must not apply")
	}
	s.env.Clock.Advance(time.Minute)
	testkit.Expect(t, s.dismiss(1, it), 200, "")
	if n := countStates(t, s.env, it.ID, 1); n != 1 {
		t.Fatalf("the old row must be revived, not duplicated: %d rows", n)
	}
	var st announcement.UserState
	if err := s.env.DB.Where("announcement_id = ?", it.ID).First(&st).Error; err != nil {
		t.Fatalf("revived row must be live: %v", err)
	}
	if st.DismissedUntil == nil {
		t.Fatal("dismiss must be recorded")
	}
	sameIDs(t, s.list(1, ""))
}

func TestStateUniqueIndexExists(t *testing.T) {
	s := newSuite(t)
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	testkit.Expect(t, s.closeIt(1, it), 200, "")
	var st announcement.UserState
	s.env.DB.Where("announcement_id = ?", it.ID).First(&st)
	dup := announcement.UserState{AnnouncementID: it.ID, UserID: 1, AnnouncementUpdatedAt: st.AnnouncementUpdatedAt}
	if err := s.env.DB.Create(&dup).Error; err == nil {
		t.Fatal("a second row for the same user and version must violate the unique index")
	}
}

func TestVersionRoundTripKeepsSubsecondPrecision(t *testing.T) {
	s := newSuite(t)
	s.env.Clock.Set(time.Date(2026, 3, 1, 9, 0, 0, 123456789, time.UTC))
	it := s.create(map[string]any{"title": "x", "contentMarkdown": "x"})
	if it.UpdatedAt != "2026-03-01T09:00:00.123456Z" {
		t.Fatalf("version is kept at microsecond precision, got %s", it.UpdatedAt)
	}
	listed := s.list(1, "")
	if listed[0].UpdatedAt != it.UpdatedAt {
		t.Fatalf("list %s vs create %s", listed[0].UpdatedAt, it.UpdatedAt)
	}
	testkit.Expect(t, s.dismiss(1, listed[0]), 200, "")
	s.env.Clock.Set(time.Date(2026, 3, 1, 9, 0, 1, 500, time.UTC))
	patched := s.patchOK(it.ID, map[string]any{"title": "y"})
	testkit.Expect(t, s.closeIt(1, patched), 200, "")
	testkit.Expect(t, s.closeIt(1, it), 404, "announcement.not_found")
}

func TestCapabilityOff(t *testing.T) {
	s := newSuite(t, capabilityOff())
	// The poll endpoint stays up and answers an empty list.
	w := s.env.Do("GET", userListPath, s.user(1), nil)
	testkit.Expect(t, w, 200, "")
	var list []item
	testkit.Decode(t, w, &list)
	if list == nil || len(list) != 0 {
		t.Fatalf("expected empty array: %s", w.Body)
	}
	if testkit.Envelope(t, w).Data == nil {
		t.Fatal("data must not be null")
	}

	checks := []struct {
		method, path string
		auth         map[string]string
		body         any
	}{
		{"POST", userListPath + "/1/close", s.user(1), map[string]any{"updatedAt": baseClockText}},
		{"POST", userListPath + "/1/dismiss-today", s.user(1), map[string]any{"updatedAt": baseClockText}},
		{"GET", adminPath, s.adm(), nil},
		{"POST", adminPath, s.adm(), map[string]any{"title": "x", "contentMarkdown": "x"}},
		{"PATCH", adminPath + "/1", s.adm(), map[string]any{"title": "x"}},
		{"DELETE", adminPath + "/1", s.adm(), nil},
	}
	for _, c := range checks {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			w := c.method
			resp := s.env.Do(w, c.path, c.auth, c.body)
			testkit.Expect(t, resp, 404, "feature.disabled")
			details, _ := testkit.Envelope(t, resp).Details.(map[string]any)
			if details["feature"] != feature {
				t.Fatalf("details: %v", details)
			}
		})
	}
	// The gate still sits behind authentication and the admin check.
	testkit.Expect(t, s.env.Do("GET", adminPath, s.user(1), nil), 403, "auth.admin_required")
	testkit.Expect(t, s.env.Do("POST", userListPath+"/1/close", nil, nil), 401, "auth.invalid_token")
}

func TestCapabilityOffHidesExistingRows(t *testing.T) {
	s := newSuite(t, capabilityOff())
	row := announcement.Announcement{Title: "t", ContentMarkdown: "c", Status: "active", Type: "general"}
	row.CreatedAt, row.UpdatedAt = baseTime, baseTime
	if err := s.env.DB.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if n := len(s.list(1, "")); n != 0 {
		t.Fatalf("flag off must hide rows from the poll, got %d", n)
	}
}
