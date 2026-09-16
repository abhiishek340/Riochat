package contentmoderation

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// Retention of recorded data.
const (
	contentRetention  = 30 * 24 * time.Hour
	metadataRetention = 90 * 24 * time.Hour
	statsRetention    = 90 * 24 * time.Hour
)

// statDelta is an additive change to one statistics row.
type statDelta struct {
	Direction, Modality, Result, Category string
	Checks, Items, Hits, Failures         int64
	LatencySum, LatencyCount              int64
}

func utcDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// statDeltasFor builds the increments of one finished check task.
func statDeltasFor(direction, modality, result string, items int, latencyMS int64, hitCategories []string) []statDelta {
	if items < 1 {
		items = 1
	}
	base := statDelta{
		Direction: direction, Modality: modality, Result: result,
		Checks: 1, Items: int64(items), LatencySum: latencyMS,
	}
	if latencyMS > 0 {
		base.LatencyCount = 1
	}
	switch result {
	case ResultHit:
		base.Hits = 1
	case ResultFailedOpen:
		base.Failures = 1
	}
	out := []statDelta{base}
	if result == ResultHit {
		for _, c := range hitCategories {
			out = append(out, statDelta{Direction: direction, Modality: modality, Result: result, Category: c, Hits: 1})
		}
	}
	return out
}

// addStats applies the increments as additive upserts on the unique key. A
// failure is logged by the caller and never reaches users.
func (s *Service) addStats(ctx context.Context, deltas ...statDelta) error {
	day := utcDay(s.clock.Now())
	now := s.clock.Now()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		const t = "content_moderation_daily_stats."
		for _, d := range deltas {
			row := DailyStat{
				StatDate: day, Direction: d.Direction, Modality: d.Modality, Result: d.Result, Category: d.Category,
				CheckCount: d.Checks, ContentItems: d.Items, HitCount: d.Hits, FailureCount: d.Failures,
				LatencySumMS: d.LatencySum, LatencyCount: d.LatencyCount,
				CreatedAt: now, UpdatedAt: now,
			}
			res := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "stat_date"}, {Name: "direction"}, {Name: "modality"}, {Name: "result"}, {Name: "category"}},
				DoUpdates: clause.Assignments(map[string]any{
					"check_count":    gorm.Expr(t+"check_count + ?", d.Checks),
					"content_items":  gorm.Expr(t+"content_items + ?", d.Items),
					"hit_count":      gorm.Expr(t+"hit_count + ?", d.Hits),
					"failure_count":  gorm.Expr(t+"failure_count + ?", d.Failures),
					"latency_sum_ms": gorm.Expr(t+"latency_sum_ms + ?", d.LatencySum),
					"latency_count":  gorm.Expr(t+"latency_count + ?", d.LatencyCount),
					"updated_at":     now,
				}),
			}).Create(&row)
			if res.Error != nil {
				return database.Translate(res.Error)
			}
		}
		return nil
	})
}

// StatItem is one statistics row as returned by the API.
type StatItem struct {
	StatDate     time.Time `json:"statDate"`
	Direction    string    `json:"direction"`
	Modality     string    `json:"modality"`
	Result       string    `json:"result"`
	Category     string    `json:"category"`
	CheckCount   int64     `json:"checkCount"`
	ContentItems int64     `json:"contentItems"`
	HitCount     int64     `json:"hitCount"`
	FailureCount int64     `json:"failureCount"`
	LatencySumMS int64     `json:"latencySumMS"`
	LatencyCount int64     `json:"latencyCount"`
}

// StatsWindow resolves the requested range: the default window is the last 90
// days, a start before that is clamped, and both ends are compared by UTC day.
// ok is false when the range is reversed.
func statsWindow(now time.Time, from, to *time.Time) (start, end time.Time, ok bool) {
	floor := utcDay(now.Add(-statsRetention))
	start = floor
	if from != nil {
		if d := utcDay(*from); d.After(floor) {
			start = d
		}
	}
	end = utcDay(now)
	if to != nil {
		end = utcDay(*to)
	}
	if end.Before(start) {
		return start, end, false
	}
	return start, end, true
}

func (s *Service) listStats(ctx context.Context, start, end time.Time) ([]StatItem, error) {
	var rows []DailyStat
	err := s.db.WithContext(ctx).
		Where("stat_date >= ? AND stat_date <= ?", start, end).
		Order("stat_date ASC, direction ASC, modality ASC, result ASC, category ASC").
		Find(&rows).Error
	if err != nil {
		return nil, database.Translate(err)
	}
	out := make([]StatItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, StatItem{
			StatDate: utcDay(r.StatDate), Direction: r.Direction, Modality: r.Modality, Result: r.Result, Category: r.Category,
			CheckCount: r.CheckCount, ContentItems: r.ContentItems, HitCount: r.HitCount, FailureCount: r.FailureCount,
			LatencySumMS: r.LatencySumMS, LatencyCount: r.LatencyCount,
		})
	}
	return out, nil
}

// pruneStats removes statistics older than the retention window.
func (s *Service) pruneStats(ctx context.Context) error {
	cutoff := utcDay(s.clock.Now().Add(-statsRetention))
	return s.db.WithContext(ctx).Where("stat_date < ?", cutoff).Delete(&DailyStat{}).Error
}
