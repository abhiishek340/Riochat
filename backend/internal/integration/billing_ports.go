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

func (p permissionGroups) GroupExists(ctx context.Context, id uint) (bool, error) {
	var n int64
	err := p.db.WithContext(ctx).Model(&channel.PermissionGroup{}).Where("id = ?", id).Count(&n).Error
	return n > 0, err
}

func (p permissionGroups) Members(ctx context.Context, id uint) (billing.GroupMembers, error) {
	var g channel.PermissionGroup
	if err := p.db.WithContext(ctx).Where("id = ?", id).Limit(1).Find(&g).Error; err != nil {
		return billing.GroupMembers{}, err
	}
	if g.ID == 0 {
		return billing.GroupMembers{}, nil
	}
	out := billing.GroupMembers{Exists: true, Everyone: g.IsDefault}
	err := p.db.WithContext(ctx).Model(&channel.PermissionGroupUserAccess{}).Where("group_id = ?", id).Pluck("user_id", &out.UserIDs).Error
	return out, err
}

// ---- conversation: billing of generation runs ----

// runExtras carries billing facts the generator learns that the conversation
// area's result type has no field for (tool usage, duration), keyed by run id.
type runExtras struct {
	MCP             []billing.MCPToolUsage
	ServerTools     map[string]int64
	DurationSeconds int64
	MediaType       string
	InputImages     int
	Speed           string
	ServiceTier     string
	UpstreamUsage   []byte
}

type heldRun struct {
	auth   *billing.Authorization
	model  string
	convID uint
}

type convBilling struct {
	h *Hub

	mu   sync.Mutex
	held map[string]heldRun
}

func newConvBilling(h *Hub) *convBilling { return &convBilling{h: h, held: map[string]heldRun{}} }

func (b *convBilling) take(runID string) (heldRun, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.held[runID]
	return r, ok
}

func (b *convBilling) Reserve(ctx context.Context, req conversation.ReserveRequest) (conversation.Reservation, error) {
	if b.h.billing == nil {
		return conversation.Reservation{Mode: "self"}, nil
	}
	a, err := b.h.billing.Authorize(ctx, req.UserID, req.Model, req.RunID)
	if err != nil {
		return conversation.Reservation{}, err
	}
	b.mu.Lock()
	b.held[req.RunID] = heldRun{auth: a, model: req.Model, convID: req.ConversationID}
	b.mu.Unlock()
	return conversation.Reservation{ID: req.RunID, Mode: a.Mode}, nil
}

func (b *convBilling) Settle(ctx context.Context, res conversation.Reservation, req conversation.SettleRequest) (conversation.Settlement, error) {
	held, ok := b.take(req.RunID)
	b.mu.Lock()
	delete(b.held, req.RunID)
	b.mu.Unlock()
	extras := b.h.takeExtras(req.RunID)
	mode := res.Mode
	if !ok || b.h.billing == nil {
		return conversation.Settlement{Mode: orDefault(mode, "self"), Currency: "USD"}, nil
	}
	spent := req.Usage.Input+req.Usage.Output+req.Usage.CacheRead+req.Usage.CacheWrite+req.Usage.Reasoning > 0 ||
		len(extras.MCP) > 0 || len(extras.ServerTools) > 0 || extras.DurationSeconds > 0
	if req.Failed && !spent {
		_ = b.h.billing.ReleaseReservation(ctx, req.UserID, held.auth.RefNo)
		return conversation.Settlement{Mode: orDefault(held.auth.Mode, "self"), Currency: "USD"}, nil
	}
	model := req.Route.PlatformModelName
	if model == "" {
		model = held.model
	}
	in := billing.LedgerInput{
		UserID: req.UserID, RefNo: held.auth.RefNo, Mode: held.auth.Mode,
		PlatformModelName: model, RoutedBindingCode: req.Route.RoutedBindingCode,
		UpstreamModelName: req.Route.UpstreamModelName, UpstreamName: req.Route.UpstreamName,
		ModelVendor: req.Route.ModelVendor, ModelIcon: req.Route.ModelIcon, ProviderProtocol: req.Route.ProviderProtocol,
		Personal: held.auth.Personal, ConversationID: held.convID,
		InputTokens: req.Usage.Input, CacheReadTokens: req.Usage.CacheRead, CacheWriteTokens: req.Usage.CacheWrite,
		OutputTokens: req.Usage.Output, ReasoningTokens: req.Usage.Reasoning, CallCount: 1, LatencyMS: req.LatencyMS,
		DurationSeconds: extras.DurationSeconds, DurationBillable: extras.DurationSeconds > 0,
		UsageSpeed: extras.Speed, UsageServiceTier: extras.ServiceTier,
		ServerSideToolUsage: extras.ServerTools, MCPTools: extras.MCP,
		MediaType: extras.MediaType, InputImageCount: extras.InputImages,
	}
	draft, err := b.h.billing.BuildLedger(ctx, in)
	if err != nil {
		_ = b.h.billing.MarkReconciliation(ctx, req.UserID, held.auth.RefNo, "ledger_build_failed")
		return conversation.Settlement{}, err
	}
	row, err := b.h.billing.SettleWithRetry(ctx, held.auth, draft)
	if err != nil {
		_ = b.h.billing.MarkReconciliation(ctx, req.UserID, held.auth.RefNo, "settle_failed")
		return conversation.Settlement{}, err
	}
	return conversation.Settlement{
		Mode: orDefault(held.auth.Mode, "self"), Currency: orDefault(row.BilledCurrency, "USD"),
		Nanousd: row.BilledNanousd, PricingSnapshotJSON: row.PricingSnapshotJSON,
	}, nil
}

func (b *convBilling) Release(ctx context.Context, res conversation.Reservation) {
	b.mu.Lock()
	held, ok := b.held[res.ID]
	delete(b.held, res.ID)
	b.mu.Unlock()
	if ok && b.h.billing != nil {
		_ = b.h.billing.ReleaseReservation(ctx, held.auth.UserID, held.auth.RefNo)
	}
	b.h.takeExtras(res.ID)
}

func (b *convBilling) ChargeBasicService(ctx context.Context, c conversation.BasicServiceCharge) error {
	if b.h.billing == nil {
		return nil
	}
	mode := b.h.billing.Mode(ctx)
	in := billing.LedgerInput{
		UserID: c.UserID, RefNo: c.RunID, Mode: mode, ServiceOnly: true, ProviderProtocol: c.Route.ProviderProtocol,
		ConversationID: c.ConversationID, PlatformModelName: c.Route.PlatformModelName,
		RoutedBindingCode: c.Route.RoutedBindingCode, UpstreamModelName: c.Route.UpstreamModelName,
		UpstreamName: c.Route.UpstreamName, ModelVendor: c.Route.ModelVendor, ModelIcon: c.Route.ModelIcon,
		InputTokens: c.Usage.Input, CacheReadTokens: c.Usage.CacheRead, CacheWriteTokens: c.Usage.CacheWrite,
		OutputTokens: c.Usage.Output, ReasoningTokens: c.Usage.Reasoning, CallCount: 1, LatencyMS: c.LatencyMS,
		ServiceItems: []billing.ServiceItemInput{{
			Code: c.Service, Name: c.Service, PlatformModelName: c.Route.PlatformModelName,
			ProviderProtocol: c.Route.ProviderProtocol, InputTokens: c.Usage.Input, CacheReadTokens: c.Usage.CacheRead,
			CacheWriteTokens: c.Usage.CacheWrite, OutputTokens: c.Usage.Output, ReasoningTokens: c.Usage.Reasoning, CallCount: 1,
		}},
	}
	draft, err := b.h.billing.BuildLedger(ctx, in)
	if err != nil {
		return err
	}
	_, err = b.h.billing.SettleWithRetry(ctx, &billing.Authorization{UserID: c.UserID, Mode: mode, RefNo: c.RunID}, draft)
	return err
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
