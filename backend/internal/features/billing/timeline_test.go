package billing

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

var tlEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func day(n int) time.Time { return tlEpoch.AddDate(0, 0, n) }

func seg(plan uint, rank, from, to int) tlSeg {
	return tlSeg{PlanID: plan, PriceID: plan, Rank: rank, Start: day(from), End: day(to)}
}

func grant(plan uint, rank, days int) tlGrant {
	return tlGrant{PlanID: plan, PriceID: plan, Rank: rank, Dur: time.Duration(days) * 24 * time.Hour}
}

func render(segs []tlSeg) string {
	var parts []string
	for _, s := range segs {
		parts = append(parts, fmt.Sprintf("P%d[%d,%d)", s.PlanID, int(s.Start.Sub(tlEpoch).Hours()/24), int(s.End.Sub(tlEpoch).Hours()/24)))
	}
	return strings.Join(parts, " ")
}

func TestTimelinePlacement(t *testing.T) {
	cases := []struct {
		name     string
		existing []tlSeg
		start    int
		grant    tlGrant
		want     string
	}{
		{"empty timeline", nil, 0, grant(3, 3, 30), "P3[0,30)"},
		{"higher rank displaces lower", []tlSeg{seg(2, 2, 0, 30)}, 0, grant(3, 3, 30), "P3[0,30) P2[30,60)"},
		{"higher rank inside a long lower segment", []tlSeg{seg(2, 2, 0, 90)}, 30, grant(3, 3, 30), "P2[0,30) P3[30,60) P2[60,120)"},
		{"lower rank queues behind higher", []tlSeg{seg(3, 3, 0, 30)}, 0, grant(2, 2, 30), "P3[0,30) P2[30,60)"},
		{"lower rank fills the gap before a later higher segment", []tlSeg{seg(3, 3, 10, 40)}, 0, grant(2, 2, 30), "P2[0,10) P3[10,40) P2[40,60)"},
		{"same plan queues contiguously", []tlSeg{seg(2, 2, 0, 30)}, 0, grant(2, 2, 30), "P2[0,60)"},
		{"same rank other plan queues behind", []tlSeg{seg(2, 2, 0, 30)}, 0, grant(4, 2, 10), "P2[0,30) P4[30,40)"},
		{"start after existing end", []tlSeg{seg(2, 2, 0, 10)}, 20, grant(3, 3, 5), "P2[0,10) P3[20,25)"},
		{"grant starting mid segment clips history", []tlSeg{seg(2, 2, 0, 20)}, 10, grant(3, 3, 5), "P2[0,10) P3[10,15) P2[15,25)"},
		{"zero duration is ignored", []tlSeg{seg(2, 2, 0, 10)}, 0, grant(3, 3, 0), "P2[0,10)"},
	}
	for _, c := range cases {
		got := render(placeOnTimeline(c.existing, day(c.start), c.grant))
		if got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestTimelineNeverLosesTime(t *testing.T) {
	existing := []tlSeg{seg(2, 2, 0, 20), seg(3, 3, 25, 40), seg(2, 2, 40, 50)}
	var total time.Duration
	for _, s := range existing {
		total += s.length()
	}
	for _, g := range []tlGrant{grant(4, 4, 15), grant(1, 1, 15), grant(3, 3, 7), grant(2, 2, 3)} {
		got := placeOnTimeline(existing, day(5), g)
		var sum time.Duration
		for i, s := range got {
			sum += s.length()
			if i > 0 && s.Start.Before(got[i-1].End) {
				t.Fatalf("overlap in %s", render(got))
			}
		}
		// history before day 5 is untouched; everything after is old time plus the new grant
		var wantTotal = total + g.Dur
		if sum != wantTotal {
			t.Errorf("grant %+v: total %v want %v (%s)", g, sum, wantTotal, render(got))
		}
	}
}

func TestGrantTimeOnDatabaseTimeline(t *testing.T) {
	e := newEnv(t)
	e.mode("period")
	ctx := t.Context()
	now := e.Clock.Now()
	pro, max := e.planByCode("pro"), e.planByCode("max")
	var proPrice, maxPrice Price
	e.DB.Where("plan_id = ?", pro.ID).First(&proPrice)
	e.DB.Where("plan_id = ?", max.ID).First(&maxPrice)
	d30 := 30 * 24 * time.Hour
	tx := e.DB.Begin()
	if _, err := e.svc.grantTime(tx, 1, pro, proPrice, d30, now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.grantTime(tx, 1, max, maxPrice, d30, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	var subs []Subscription
	e.DB.Where("user_id = 1 AND status = 'active'").Order("current_period_start_at").Find(&subs)
	if len(subs) != 2 || subs[0].PlanID != max.ID || subs[1].PlanID != pro.ID {
		t.Fatalf("subs %+v", subs)
	}
	if !subs[0].CurrentPeriodStartAt.Equal(now) || !subs[0].CurrentPeriodEndAt.Equal(now.Add(d30)) ||
		!subs[1].CurrentPeriodStartAt.Equal(now.Add(d30)) || !subs[1].CurrentPeriodEndAt.Equal(now.Add(2*d30)) {
		t.Fatalf("segments %+v", subs)
	}
	snap, _ := e.svc.CurrentSubscription(ctx, 1)
	if snap.Tier != "max" {
		t.Fatalf("current %s", snap.Tier)
	}
	// halfway the max segment ends and pro takes over
	e.Clock.Advance(31 * 24 * time.Hour)
	snap, _ = e.svc.CurrentSubscription(ctx, 1)
	if snap.Tier != "pro" {
		t.Fatalf("after max: %s", snap.Tier)
	}
}
