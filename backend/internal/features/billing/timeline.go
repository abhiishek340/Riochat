package billing

import (
	"sort"
	"time"
)

// tlSeg is one contiguous stretch of entitlement on a user's timeline.
type tlSeg struct {
	Src       uint // id of the stored subscription this fragment derives from (0 = new)
	PlanID    uint
	PriceID   uint
	Rank      int
	Start     time.Time
	End       time.Time
	AutoRenew bool
}

func (s tlSeg) length() time.Duration { return s.End.Sub(s.Start) }

// tlGrant is a request to add time of a plan.
type tlGrant struct {
	PlanID    uint
	PriceID   uint
	Rank      int
	Dur       time.Duration
	AutoRenew bool
}

type tlItem struct {
	tlGrant
	from time.Time
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// placeOnTimeline adds grant g, starting no earlier than s, to the existing
// closed segments. No previously granted time is shortened or lost: a segment
// of equal or higher rank blocks the grant (it resumes right after the
// blocker), free gaps before a later blocker are used, and segments of strictly
// lower rank are split around the allocation, their displaced portion being
// re-queued as a new grant of the same plan after the allocation.
//
// The result is sorted by start with adjacent identical fragments merged.
func placeOnTimeline(existing []tlSeg, s time.Time, g tlGrant) []tlSeg {
	var fixed, work []tlSeg
	for _, e := range existing {
		if !e.End.After(s) {
			fixed = append(fixed, e)
			continue
		}
		if e.Start.Before(s) {
			pre := e
			pre.End = s
			fixed = append(fixed, pre)
			e.Start = s
		}
		work = append(work, e)
	}

	queue := []tlItem{{tlGrant: g, from: s}}
	for guard := 0; len(queue) > 0 && guard < 10000; guard++ {
		it := queue[0]
		queue = queue[1:]
		if it.Dur <= 0 {
			continue
		}
		// Blockers: segments of equal or higher rank, by start.
		var blockers []tlSeg
		for _, w := range work {
			if w.Rank >= it.Rank {
				blockers = append(blockers, w)
			}
		}
		sort.Slice(blockers, func(i, j int) bool { return blockers[i].Start.Before(blockers[j].Start) })

		type span struct{ a, b time.Time }
		var allocs []span
		cursor, remaining := it.from, it.Dur
		for _, b := range blockers {
			if !b.End.After(cursor) {
				continue
			}
			if b.Start.After(cursor) {
				gap := b.Start.Sub(cursor)
				use := minDuration(gap, remaining)
				allocs = append(allocs, span{cursor, cursor.Add(use)})
				remaining -= use
				if remaining == 0 {
					break
				}
			}
			cursor = maxTime(cursor, b.End)
		}
		if remaining > 0 {
			allocs = append(allocs, span{cursor, cursor.Add(remaining)})
		}

		for _, a := range allocs {
			var next []tlSeg
			for _, w := range work {
				if w.Rank >= it.Rank || !w.Start.Before(a.b) || !w.End.After(a.a) {
					next = append(next, w)
					continue
				}
				// w is lower rank and overlaps the allocation: split around it.
				ovStart, ovEnd := maxTime(w.Start, a.a), minTime(w.End, a.b)
				if w.Start.Before(a.a) {
					left := w
					left.End = a.a
					next = append(next, left)
				}
				if w.End.After(a.b) {
					right := w
					right.Start = a.b
					next = append(next, right)
				}
				queue = append(queue, tlItem{
					tlGrant: tlGrant{PlanID: w.PlanID, PriceID: w.PriceID, Rank: w.Rank, Dur: ovEnd.Sub(ovStart), AutoRenew: w.AutoRenew},
					from:    maxTime(a.b, w.End),
				})
			}
			next = append(next, tlSeg{PlanID: it.PlanID, PriceID: it.PriceID, Rank: it.Rank, Start: a.a, End: a.b, AutoRenew: it.AutoRenew})
			work = next
		}
	}

	all := append(fixed, work...)
	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].Start.Equal(all[j].Start) {
			return all[i].Start.Before(all[j].Start)
		}
		return all[i].End.Before(all[j].End)
	})
	var merged []tlSeg
	for _, sg := range all {
		if sg.length() <= 0 {
			continue
		}
		if n := len(merged); n > 0 {
			last := &merged[n-1]
			if last.PlanID == sg.PlanID && last.PriceID == sg.PriceID && last.AutoRenew == sg.AutoRenew && last.End.Equal(sg.Start) {
				last.End = sg.End
				continue
			}
		}
		merged = append(merged, sg)
	}
	return merged
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
