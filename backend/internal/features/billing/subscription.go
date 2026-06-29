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

// SubscribeFree activates a zero-priced price for the user.
func (s *Service) SubscribeFree(ctx context.Context, userID, priceID uint, cycles int) (*Subscription, error) {
	if cycles < 1 {
		cycles = 1
	}
	var price Price
	if err := s.db.WithContext(ctx).First(&price, priceID).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, ErrPlanNotFound
		}
		return nil, err
	}
	var plan Plan
	if err := s.db.WithContext(ctx).First(&plan, price.PlanID).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, ErrPlanNotFound
		}
		return nil, err
	}
	if !price.IsActive || !plan.IsActive {
		return nil, ErrPlanNotFound
	}
	if price.AmountCents > 0 {
		return nil, ErrPaymentRequired.WithStatus(http.StatusBadRequest)
	}
	now := s.now()
	var created Subscription
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if plan.Code == PlanFree {
			plans, err := s.loadPlansByID(ctx, tx)
			if err != nil {
				return err
			}
			var active []Subscription
			if err := tx.Where("user_id = ? AND status = ?", userID, SubActive).Find(&active).Error; err != nil {
				return err
			}
			for _, sub := range active {
				p := plans[sub.PlanID]
				if p.Code != PlanFree && (sub.CurrentPeriodEndAt == nil || sub.CurrentPeriodEndAt.After(now)) {
					return ErrEntitlementActive
				}
			}
		}
		interval := normalizeInterval(price.BillingInterval)
		end, finite := addInterval(now, interval, cycles)
		created = Subscription{UserID: userID, PlanID: plan.ID, PriceID: price.ID, Status: SubActive, StartAt: now,
			CurrentPeriodStartAt: now, AutoRenew: finite}
		if finite {
			created.CurrentPeriodEndAt = utcPtr(end)
		}
		return replaceActiveSubscriptions(tx, userID, now, &created)
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

// replaceActiveSubscriptions expires every active subscription of the user and
// inserts next.
func replaceActiveSubscriptions(tx *gorm.DB, userID uint, now time.Time, next *Subscription) error {
	if err := tx.Model(&Subscription{}).Where("user_id = ? AND status = ?", userID, SubActive).
		Updates(map[string]any{"status": SubExpired, "auto_renew": false, "current_period_end_at": now}).Error; err != nil {
		return err
	}
	return database.Translate(tx.Create(next).Error)
}

// AssignUserPlan sets a user's plan by code (administrator action). An empty
// code means the free plan. Non-free plans need an expiry in the future.
func (s *Service) AssignUserPlan(ctx context.Context, userID uint, planCode string, expiresAt *time.Time) error {
	if s.currentMode(ctx) != ModePeriod {
		return ErrPaymentRequired
	}
	if planCode == "" {
		planCode = PlanFree
	}
	var plan Plan
	if err := s.db.WithContext(ctx).Where("code = ? AND is_active = ?", planCode, true).First(&plan).Error; err != nil {
		if database.IsNotFound(err) {
			return ErrInvalidSubscriptionTier
		}
		return err
	}
	price, err := s.defaultPrice(ctx, s.db, plan.ID, true)
	if err != nil {
		return err
	}
	if price == nil {
		return ErrInvalidSubscriptionTier
	}
	now := s.now()
	sub := Subscription{UserID: userID, PlanID: plan.ID, PriceID: price.ID, Status: SubActive, StartAt: now, CurrentPeriodStartAt: now}
	if plan.Code == PlanFree {
		sub.AutoRenew = normalizeInterval(price.BillingInterval) != IntervalLifetime
	} else {
		if expiresAt == nil {
			return ErrSubscriptionExpiryReq
		}
		if !expiresAt.After(now) {
			return ErrInvalidSubscriptionExp
		}
		sub.CurrentPeriodEndAt = utcPtr(*expiresAt)
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return replaceActiveSubscriptions(tx, userID, now, &sub)
	})
}

// defaultPrice returns the plan's active default price; with fallback it
// returns the cheapest active price when no default exists.
func (s *Service) defaultPrice(ctx context.Context, db *gorm.DB, planID uint, fallback bool) (*Price, error) {
	var price Price
	err := db.WithContext(ctx).Where("plan_id = ? AND is_active = ? AND is_default = ?", planID, true, true).Order("id").First(&price).Error
	if err == nil {
		return &price, nil
	}
	if !database.IsNotFound(err) {
		return nil, err
	}
	if !fallback {
		return nil, nil
	}
	err = db.WithContext(ctx).Where("plan_id = ? AND is_active = ?", planID, true).Order("amount_cents ASC, id ASC").First(&price).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &price, nil
}

// grantTime places dur of plan on the user's entitlement timeline starting at
// at, inside tx. It returns the stored subscription carrying the new time.
func (s *Service) grantTime(tx *gorm.DB, userID uint, plan Plan, price Price, dur time.Duration, at time.Time) (*Subscription, error) {
	if dur <= 0 {
		return nil, ErrInvalidOrder
	}
	var existing []Subscription
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND status = ? AND current_period_end_at IS NOT NULL AND current_period_end_at > ?", userID, SubActive, at).
		Find(&existing).Error; err != nil {
		return nil, err
	}
	var plans []Plan
	if err := tx.Find(&plans).Error; err != nil {
		return nil, err
	}
	pm := make(map[uint]Plan, len(plans))
	for _, p := range plans {
		pm[p.ID] = p
	}
	segs := make([]tlSeg, 0, len(existing))
	rows := map[uint]Subscription{}
	for _, e := range existing {
		rows[e.ID] = e
		segs = append(segs, tlSeg{Src: e.ID, PlanID: e.PlanID, PriceID: e.PriceID, Rank: planRank(pm[e.PlanID]),
			Start: e.CurrentPeriodStartAt, End: *e.CurrentPeriodEndAt, AutoRenew: e.AutoRenew})
	}
	result := placeOnTimeline(segs, at, tlGrant{PlanID: plan.ID, PriceID: price.ID, Rank: planRank(plan), Dur: dur})

	used := map[uint]bool{}
	var firstNew *Subscription
	persist := func(seg tlSeg) (*Subscription, error) {
		if seg.Src != 0 && !used[seg.Src] {
			used[seg.Src] = true
			row := rows[seg.Src]
			row.StartAt, row.CurrentPeriodStartAt = seg.Start, seg.Start
			row.CurrentPeriodEndAt = utcPtr(seg.End)
			row.PlanID, row.PriceID, row.AutoRenew = seg.PlanID, seg.PriceID, seg.AutoRenew
			if err := tx.Save(&row).Error; err != nil {
				return nil, err
			}
			return &row, nil
		}
		row := Subscription{UserID: userID, PlanID: seg.PlanID, PriceID: seg.PriceID, Status: SubActive,
			StartAt: seg.Start, CurrentPeriodStartAt: seg.Start, CurrentPeriodEndAt: utcPtr(seg.End), AutoRenew: seg.AutoRenew}
		if err := tx.Create(&row).Error; err != nil {
			return nil, err
		}
		return &row, nil
	}
	for _, seg := range result {
		row, err := persist(seg)
		if err != nil {
			return nil, err
		}
		if seg.PlanID == plan.ID && seg.End.After(at) && (firstNew == nil || row.CurrentPeriodStartAt.Before(firstNew.CurrentPeriodStartAt)) {
			firstNew = row
		}
	}
	for _, e := range existing {
		if used[e.ID] {
			continue
		}
		end := minTime(maxTime(at, e.CurrentPeriodStartAt), *e.CurrentPeriodEndAt)
		if err := tx.Model(&Subscription{}).Where("id = ?", e.ID).Updates(map[string]any{
			"status": SubExpired, "auto_renew": false, "cancel_at_period_end": false, "current_period_end_at": end.UTC(),
		}).Error; err != nil {
			return nil, err
		}
	}
	if firstNew == nil {
		return nil, errors.New("billing: grant produced no segment")
	}
	return firstNew, nil
}

// SubscriptionView is the API form of a subscription.
type SubscriptionView struct {
	ID                   uint       `json:"id"`
	UserID               uint       `json:"userID"`
	PlanID               uint       `json:"planID"`
	PriceID              uint       `json:"priceID"`
	Status               string     `json:"status"`
	StartAt              time.Time  `json:"startAt"`
	CurrentPeriodStartAt time.Time  `json:"currentPeriodStartAt"`
	CurrentPeriodEndAt   *time.Time `json:"currentPeriodEndAt"`
	CancelAtPeriodEnd    bool       `json:"cancelAtPeriodEnd"`
	AutoRenew            bool       `json:"autoRenew"`
}

func subscriptionView(sub Subscription) SubscriptionView {
	return SubscriptionView{ID: sub.ID, UserID: sub.UserID, PlanID: sub.PlanID, PriceID: sub.PriceID, Status: sub.Status,
		StartAt: sub.StartAt, CurrentPeriodStartAt: sub.CurrentPeriodStartAt, CurrentPeriodEndAt: sub.CurrentPeriodEndAt,
		CancelAtPeriodEnd: sub.CancelAtPeriodEnd, AutoRenew: sub.AutoRenew}
}

// EntitlementView is one queued entitlement in the overview.
type EntitlementView struct {
	SubscriptionView
	Plan      *PlanView `json:"plan"`
	IsCurrent bool      `json:"isCurrent"`
}

// entitlementQueue builds the merged queue of non-free active entitlements.
func (s *Service) entitlementQueue(ctx context.Context, userID uint) ([]EntitlementView, error) {
	plans, err := s.loadPlansByID(ctx, s.db)
	if err != nil {
		return nil, err
	}
	var subs []Subscription
	if err := s.db.WithContext(ctx).Where("user_id = ? AND status = ?", userID, SubActive).Find(&subs).Error; err != nil {
		return nil, err
	}
	now := s.now()
	cur, _, hasCur := currentOf(subs, plans, now)
	var list []Subscription
	for _, sub := range subs {
		p := plans[sub.PlanID]
		if p.Code == PlanFree || (sub.CurrentPeriodEndAt != nil && !sub.CurrentPeriodEndAt.After(now)) {
			continue
		}
		list = append(list, sub)
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if !a.CurrentPeriodStartAt.Equal(b.CurrentPeriodStartAt) {
			return a.CurrentPeriodStartAt.Before(b.CurrentPeriodStartAt)
		}
		ra, rb := planRank(plans[a.PlanID]), planRank(plans[b.PlanID])
		if ra != rb {
			return ra > rb
		}
		return a.ID < b.ID
	})
	var merged []EntitlementView
	for _, sub := range list {
		isCur := hasCur && sub.ID == cur.ID
		if n := len(merged); n > 0 {
			last := &merged[n-1]
			if last.PlanID == sub.PlanID && last.PriceID == sub.PriceID && last.CurrentPeriodEndAt != nil &&
				!sub.CurrentPeriodStartAt.After(*last.CurrentPeriodEndAt) {
				if sub.CurrentPeriodEndAt == nil || sub.CurrentPeriodEndAt.After(*last.CurrentPeriodEndAt) {
					last.CurrentPeriodEndAt = sub.CurrentPeriodEndAt
				}
				last.IsCurrent = last.IsCurrent || isCur
				continue
			}
		}
		v := EntitlementView{SubscriptionView: subscriptionView(sub), IsCurrent: isCur}
		merged = append(merged, v)
	}
	for i := range merged {
		pv, err := s.planView(ctx, plans[merged[i].PlanID])
		if err != nil {
			return nil, err
		}
		merged[i].Plan = pv
	}
	if merged == nil {
		merged = []EntitlementView{}
	}
	return merged, nil
}
