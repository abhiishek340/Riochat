package integration

import (
	"context"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/features/auth"
	"github.com/riochat/riochat/backend/internal/features/billing"
	"github.com/riochat/riochat/backend/internal/features/channel"
	"github.com/riochat/riochat/backend/internal/features/conversation"
)

// ---- auth: subscription shown on the user object ----

type authSubscriptions struct{ h *Hub }

func (a authSubscriptions) SubscriptionFor(ctx context.Context, userID uint) (auth.Subscription, bool) {
	if a.h.billing == nil {
		return auth.Subscription{}, false
	}
	snap, err := a.h.billing.CurrentSubscription(ctx, userID)
	if err != nil || snap == nil {
		return auth.Subscription{}, false
	}
	planID, name := userPlanFields(snap)
	return auth.Subscription{Tier: snap.Tier, PlanID: planID, PlanName: name, Status: snap.Status, ExpiresAt: snap.ExpiresAt}, true
}

// ---- channel: prices and plan groups ----

type pricingSource struct{ h *Hub }

func (p pricingSource) BillingMode(ctx context.Context) string {
	if p.h.billing == nil {
		return channel.BillingModeSelf
	}
	return p.h.billing.Mode(ctx)
}

func (p pricingSource) PublicPrices(ctx context.Context) (map[string]*channel.Pricing, bool, error) {
	if p.h.billing == nil {
		return nil, false, nil
	}
	_, prices, err := p.h.billing.PublicPricing(ctx)
	if err != nil {
		return nil, false, err
	}
	out := make(map[string]*channel.Pricing, len(prices))
	for name, pr := range prices {
		out[name] = toChannelPricing(pr)
	}
	return out, true, nil
}

func toChannelPricing(p billing.PublicPricing) *channel.Pricing {
	out := &channel.Pricing{
		Currency: p.Currency, IsFree: p.IsFree, Mode: p.Mode,
		InputUSDPerMTokens: p.InputUSDPerMTokens, CacheReadUSDPerMTokens: p.CacheReadUSDPerMTokens,
		CacheWriteUSDPerMTokens: p.CacheWriteUSDPerMTokens, CacheWrite5mMultiplier: p.CacheWrite5mMultiplier,
		CacheWrite1hMultiplier: p.CacheWrite1hMultiplier, OutputUSDPerMTokens: p.OutputUSDPerMTokens,
		CallUSDPerCall: p.CallUSDPerCall, DurationUSDPerSecond: p.DurationUSDPerSecond,
		Tiers: []channel.PricingTier{}, SchedulePeriods: []channel.PricingPeriod{},
		ScheduleUTCOffsetMinutes: p.ScheduleUTCOffsetMinutes,
	}
	for _, t := range p.Tiers {
		out.Tiers = append(out.Tiers, channel.PricingTier(t))
	}
	for _, sp := range p.SchedulePeriods {
		out.SchedulePeriods = append(out.SchedulePeriods, channel.PricingPeriod(sp))
	}
	return out
}

type subscriptionGroups struct {
	h  *Hub
	db *gorm.DB
}

func (s subscriptionGroups) SubscriptionGroupID(ctx context.Context, userID uint, _ time.Time) (uint, bool, error) {
	if s.h.billing == nil {
		return 0, false, nil
	}
	snap, err := s.h.billing.CurrentSubscription(ctx, userID)
	if err != nil || snap == nil || snap.PermissionGroupID == nil || *snap.PermissionGroupID == 0 {
		return 0, false, err
	}
	return *snap.PermissionGroupID, true, nil
}

// SubscriptionGroupUserIDs lists users holding a current subscription whose
// plan is bound to the group.
func (s subscriptionGroups) SubscriptionGroupUserIDs(ctx context.Context, groupID uint, now time.Time) ([]uint, error) {
	var ids []uint
	err := s.db.WithContext(ctx).Table("billing_subscriptions AS s").
		Joins("JOIN billing_plans p ON p.id = s.plan_id AND p.deleted_at IS NULL").
		Where("s.deleted_at IS NULL AND s.status = ? AND p.is_active = ? AND p.permission_group_id = ?", billing.SubActive, true, groupID).
		Where("s.current_period_start_at <= ? AND (s.current_period_end_at IS NULL OR s.current_period_end_at > ?)", now.UTC(), now.UTC()).
		Distinct().Pluck("s.user_id", &ids).Error
	return ids, err
}

// ---- billing: catalog, rates, groups, users ----

type modelCatalog struct{ h *Hub }

func (m modelCatalog) Model(ctx context.Context, name string) (billing.ModelInfo, bool, error) {
	if m.h.channel == nil {
		return billing.ModelInfo{}, false, nil
	}
	id, err := m.h.channel.ResolveModelIdentity(ctx, name)
	if err != nil {
		return billing.ModelInfo{}, false, nil
	}
	video, _ := m.h.channel.SupportsVideoGeneration(ctx, id.Name)
	return billing.ModelInfo{Name: id.Name, Vendor: id.Vendor, Icon: id.Icon, SupportsVideo: video}, true, nil
}

func (m modelCatalog) ActiveModels(ctx context.Context) ([]billing.ModelInfo, error) {
	if m.h.channel == nil {
		return nil, nil
	}
	list, err := m.h.channel.ListActiveModels(ctx, 0)
	if err != nil {
		return nil, err
	}
	out := make([]billing.ModelInfo, 0, len(list))
	for _, pm := range list {
		video, _ := m.h.channel.SupportsVideoGeneration(ctx, pm.PlatformModelName)
		out = append(out, billing.ModelInfo{Name: pm.PlatformModelName, Vendor: pm.Vendor, Icon: pm.Icon, SupportsVideo: video})
	}
	return out, nil
}

type rateResolver struct{ h *Hub }

func (r rateResolver) RatePercent(ctx context.Context, userID uint, model string, _ uint) (int, error) {
	if r.h.channel == nil {
		return 100, nil
	}
	id, err := r.h.channel.ResolveModelIdentity(ctx, model)
	if err != nil {
		return 100, nil
	}
	return r.h.channel.RateMultiplier(ctx, userID, id.ID)
}

type permissionGroups struct{ db *gorm.DB }

func (p permissionGroups) DefaultGroupID(ctx context.Context) (uint, bool, error) {
	var g channel.PermissionGroup
	err := p.db.WithContext(ctx).Where("is_default = ?", true).Order("id ASC").Limit(1).Find(&g).Error
	return g.ID, g.ID != 0, err
}
