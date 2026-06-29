package billing

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// SubscriptionSnapshot is the view of a user's current subscription consumed by
// the permission and user areas.
type SubscriptionSnapshot struct {
	PlanID            uint       `json:"planID"`
	PlanName          string     `json:"planName"`
	Tier              string     `json:"tier"`
	Status            string     `json:"status"`
	ExpiresAt         *time.Time `json:"expiresAt"`
	PermissionGroupID *uint      `json:"permissionGroupID"`
}

// planRank orders plans: the free plan is 0, others use sortOrder when
// positive, else their id.
func planRank(p Plan) int {
	if p.Code == PlanFree {
		return 0
	}
	if p.SortOrder > 0 {
		return p.SortOrder
	}
	return int(p.ID)
}

func planDisplayName(p Plan) string {
	if p.Name != "" {
		return p.Name
	}
	return upper(p.Code)
}

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}

// isCurrentSub reports whether a stored subscription counts as current at now.
func isCurrentSub(sub Subscription, plan Plan, now time.Time) bool {
	if sub.Status != SubActive || !plan.IsActive || plan.ID == 0 {
		return false
	}
	if sub.CurrentPeriodStartAt.After(now) {
		return false
	}
	return sub.CurrentPeriodEndAt == nil || sub.CurrentPeriodEndAt.After(now)
}

// betterSub reports whether (a, pa) outranks (b, pb) for "current" selection.
func betterSub(a Subscription, pa Plan, b Subscription, pb Plan) bool {
	ra, rb := planRank(pa), planRank(pb)
	if ra != rb {
		return ra > rb
	}
	switch {
	case a.CurrentPeriodEndAt == nil && b.CurrentPeriodEndAt != nil:
		return true
	case a.CurrentPeriodEndAt != nil && b.CurrentPeriodEndAt == nil:
		return false
	case a.CurrentPeriodEndAt != nil && b.CurrentPeriodEndAt != nil && !a.CurrentPeriodEndAt.Equal(*b.CurrentPeriodEndAt):
		return a.CurrentPeriodEndAt.After(*b.CurrentPeriodEndAt)
	}
	return a.ID > b.ID
}

// currentOf picks the current subscription among a user's active rows.
func currentOf(subs []Subscription, plans map[uint]Plan, now time.Time) (Subscription, Plan, bool) {
	var best Subscription
	var bestPlan Plan
	found := false
	for _, sub := range subs {
		p, ok := plans[sub.PlanID]
		if !ok || !isCurrentSub(sub, p, now) {
			continue
		}
		if !found || betterSub(sub, p, best, bestPlan) {
			best, bestPlan, found = sub, p, true
		}
	}
	return best, bestPlan, found
}

// chainExpiry is the end of the contiguous run of same-plan segments that touch
// or overlap the current one (nil when the run is open-ended).
func chainExpiry(cur Subscription, subs []Subscription) *time.Time {
	if cur.CurrentPeriodEndAt == nil {
		return nil
	}
	end := *cur.CurrentPeriodEndAt
	for changed := true; changed; {
		changed = false
		for _, o := range subs {
			if o.ID == cur.ID || o.PlanID != cur.PlanID || o.Status != SubActive {
				continue
			}
			if o.CurrentPeriodStartAt.After(end) {
				continue
			}
			if o.CurrentPeriodEndAt == nil {
				return nil
			}
			if o.CurrentPeriodEndAt.After(end) {
				end = *o.CurrentPeriodEndAt
				changed = true
			}
		}
	}
	return &end
}

func (s *Service) loadPlansByID(ctx context.Context, db *gorm.DB) (map[uint]Plan, error) {
	var plans []Plan
	if err := db.WithContext(ctx).Find(&plans).Error; err != nil {
		return nil, err
	}
	m := make(map[uint]Plan, len(plans))
	for _, p := range plans {
		m[p.ID] = p
	}
	return m, nil
}

func snapshotOf(p Plan, expires *time.Time) *SubscriptionSnapshot {
	status := SubActive
	if p.Code == PlanFree && expires == nil {
		status = PlanFree
	}
	tier := p.Code
	if tier == "" {
		tier = PlanFree
	}
	return &SubscriptionSnapshot{PlanID: p.ID, PlanName: planDisplayName(p), Tier: tier, Status: status, ExpiresAt: expires, PermissionGroupID: p.PermissionGroupID}
}

// CurrentSubscriptions returns the snapshot of each listed user's current
// subscription. A user without one reports the active free plan (status
// "free"); when there is no free plan the entry is absent.
func (s *Service) CurrentSubscriptions(ctx context.Context, userIDs []uint) (map[uint]*SubscriptionSnapshot, error) {
	out := make(map[uint]*SubscriptionSnapshot, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	plans, err := s.loadPlansByID(ctx, s.db)
	if err != nil {
		return nil, err
	}
	var subs []Subscription
	if err := s.db.WithContext(ctx).Where("user_id IN ? AND status = ?", userIDs, SubActive).Find(&subs).Error; err != nil {
		return nil, err
	}
	byUser := map[uint][]Subscription{}
	for _, sub := range subs {
		byUser[sub.UserID] = append(byUser[sub.UserID], sub)
	}
	var free *Plan
	for _, p := range plans {
		if p.Code == PlanFree && p.IsActive {
			pp := p
			free = &pp
		}
	}
	now := s.now()
	for _, id := range userIDs {
		list := byUser[id]
		if cur, plan, ok := currentOf(list, plans, now); ok {
			out[id] = snapshotOf(plan, chainExpiry(cur, list))
		} else if free != nil {
			out[id] = snapshotOf(*free, nil)
		}
	}
	return out, nil
}

// CurrentSubscription is the single-user form of CurrentSubscriptions.
func (s *Service) CurrentSubscription(ctx context.Context, userID uint) (*SubscriptionSnapshot, error) {
	m, err := s.CurrentSubscriptions(ctx, []uint{userID})
	if err != nil {
		return nil, err
	}
	return m[userID], nil
}

func (s *Service) currentSubscriptionSnapshot(ctx context.Context, userID uint) (*SubscriptionSnapshot, error) {
	return s.CurrentSubscription(ctx, userID)
}

// effectivePlan is the plan used for period billing: the current
// subscription's plan, else the active free plan.
func (s *Service) effectivePlan(ctx context.Context, db *gorm.DB, userID uint) (Plan, error) {
	plans, err := s.loadPlansByID(ctx, db)
	if err != nil {
		return Plan{}, err
	}
	var subs []Subscription
	if err := db.WithContext(ctx).Where("user_id = ? AND status = ?", userID, SubActive).Find(&subs).Error; err != nil {
		return Plan{}, err
	}
	if _, plan, ok := currentOf(subs, plans, s.now()); ok {
		return plan, nil
	}
	for _, p := range plans {
		if p.Code == PlanFree && p.IsActive {
			return p, nil
		}
	}
	return Plan{}, ErrPlanNotFound
}

// addInterval returns t advanced by n intervals; lifetime has no end.
func addInterval(t time.Time, interval string, n int) (time.Time, bool) {
	if n < 1 {
		n = 1
	}
	switch interval {
	case IntervalLifetime:
		return time.Time{}, false
	case IntervalYear:
		return t.AddDate(n, 0, 0), true
	default:
		return t.AddDate(0, n, 0), true
	}
}

func normalizeInterval(v string) string {
	switch v {
	case IntervalYear:
		return IntervalYear
	case IntervalLifetime:
		return IntervalLifetime
	}
	return IntervalMonth
}
