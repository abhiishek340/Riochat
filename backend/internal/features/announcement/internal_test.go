package announcement

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNextLocalMidnight(t *testing.T) {
	old := time.Local
	defer func() { time.Local = old }()

	for _, zone := range []string{"UTC", "Asia/Shanghai", "America/Los_Angeles", "Pacific/Kiritimati", "Pacific/Pago_Pago"} {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Skip("tz database unavailable")
		}
		time.Local = loc
		for _, now := range []time.Time{
			time.Date(2026, 3, 1, 0, 0, 0, 0, loc),
			time.Date(2026, 3, 1, 12, 34, 56, 789, loc),
			time.Date(2026, 3, 1, 23, 59, 59, 999999999, loc),
			time.Date(2026, 12, 31, 23, 0, 0, 0, loc),
			time.Date(2028, 2, 28, 5, 0, 0, 0, loc),
		} {
			got := nextLocalMidnight(now.UTC())
			l := got.In(loc)
			if l.Hour() != 0 || l.Minute() != 0 || l.Second() != 0 || l.Nanosecond() != 0 {
				t.Errorf("%s %v: not a local midnight: %v", zone, now, l)
			}
			if !got.After(now) {
				t.Errorf("%s %v: must be strictly later than now, got %v", zone, now, got)
			}
			if got.Sub(now) > 25*time.Hour {
				t.Errorf("%s %v: more than a day away: %v", zone, now, got)
			}
			if l.YearDay() == now.In(loc).YearDay() && l.Year() == now.In(loc).Year() {
				t.Errorf("%s %v: must fall on the next calendar day: %v", zone, now, l)
			}
		}
	}
}

func TestStatusAndTypeNormalisation(t *testing.T) {
	for in, want := range map[string]string{"": "active", "  ": "active", " active ": "active", "inactive": "inactive", "\tinactive\n": "inactive"} {
		got, err := cleanStatus(in)
		if err != nil || got != want {
			t.Errorf("cleanStatus(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"archived", "ACTIVE", "act ive"} {
		if _, err := cleanStatus(in); err == nil {
			t.Errorf("cleanStatus(%q) must fail", in)
		}
	}
	for in, want := range map[string]string{"": "general", " \t": "general", "critical": "critical", " warning ": "warning", "info": "info", "normal": "normal", "general": "general"} {
		got, err := cleanType(in)
		if err != nil || got != want {
			t.Errorf("cleanType(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"urgent", "Critical", "info rmation"} {
		if _, err := cleanType(in); err == nil {
			t.Errorf("cleanType(%q) must fail", in)
		}
	}
}

func TestCleanTextLimitsAreInCharacters(t *testing.T) {
	long := ""
	for i := 0; i < 120; i++ {
		// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
		long += "\u5b57"
	}
	if got, err := cleanText("  "+long+" ", "title", maxTitleChars); err != nil || got != long {
		t.Fatalf("120 multibyte characters must pass: %v", err)
	}
	if _, err := cleanText(long+"x", "title", maxTitleChars); err == nil {
		t.Fatal("121 characters must fail")
	}
	if _, err := cleanText("  \t ", "title", maxTitleChars); err == nil {
		t.Fatal("whitespace-only must fail")
	}
}

func TestWindowAndEffectRules(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	later := t0.Add(time.Hour)
	if err := checkWindow(&t0, &t0); err == nil {
		t.Error("equal bounds are invalid")
	}
	if err := checkWindow(&later, &t0); err == nil {
		t.Error("end before start is invalid")
	}
	if checkWindow(&t0, &later) != nil || checkWindow(nil, &t0) != nil || checkWindow(&t0, nil) != nil || checkWindow(nil, nil) != nil {
		t.Error("valid windows rejected")
	}

	a := Announcement{Status: StatusActive, StartsAt: &t0, ExpiresAt: &later}
	for at, want := range map[time.Time]bool{
		t0.Add(-time.Nanosecond): false, t0: true, t0.Add(30 * time.Minute): true,
		later.Add(-time.Nanosecond): true, later: false,
	} {
		if got := a.inEffect(at); got != want {
			t.Errorf("inEffect(%v) = %v, want %v", at, got, want)
		}
	}
	a.Status = StatusInactive
	if a.inEffect(t0) {
		t.Error("inactive is never in effect")
	}
}

func TestOptionalTimeDecoding(t *testing.T) {
	type holder struct {
		A optionalTime `json:"a"`
		B optionalTime `json:"b"`
	}
	var h holder
	if err := json.Unmarshal([]byte(`{"b": null}`), &h); err != nil {
		t.Fatal(err)
	}
	if h.A.patch.Set {
		t.Error("absent key must stay unset")
	}
	if !h.B.patch.Set || h.B.patch.Value != nil {
		t.Errorf("null must clear: %+v", h.B.patch)
	}
	h = holder{}
	if err := json.Unmarshal([]byte(`{"a": "  ", "b": "2026-03-01T10:00:00+02:00"}`), &h); err != nil {
		t.Fatal(err)
	}
	if !h.A.patch.Set || h.A.patch.Value != nil {
		t.Errorf("blank must clear: %+v", h.A.patch)
	}
	if h.B.patch.Value == nil || !h.B.patch.Value.Equal(time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("set value: %+v", h.B.patch)
	}
	for _, bad := range []string{`{"a": "nope"}`, `{"a": 5}`, `{"a": true}`, `{"a": {}}`, `{"a": "2026-03-01"}`} {
		var x holder
		if err := json.Unmarshal([]byte(bad), &x); err == nil {
			t.Errorf("%s must fail", bad)
		}
	}
}

func TestListOrderingComparator(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	closedAt := now
	mk := func(id uint, pinned bool, prio int, upd time.Time, closed bool) listItem {
		it := listItem{Announcement: Announcement{Pinned: pinned, Priority: prio}}
		it.ID, it.UpdatedAt = id, upd
		if closed {
			it.ClosedAt = &closedAt
		}
		return it
	}
	steps := []struct{ hi, lo listItem }{
		{mk(1, false, 0, now, false), mk(2, true, 9, now, true)},
		{mk(1, true, 0, now, false), mk(2, false, 9, now, false)},
		{mk(1, false, 2, now, false), mk(2, false, 1, now.Add(time.Hour), false)},
		{mk(1, false, 1, now.Add(time.Hour), false), mk(2, false, 1, now, false)},
		{mk(5, false, 1, now, false), mk(2, false, 1, now, false)},
	}
	for i, s := range steps {
		if !listBefore(&s.hi, &s.lo) || listBefore(&s.lo, &s.hi) {
			t.Errorf("case %d: wrong order", i)
		}
	}
}
