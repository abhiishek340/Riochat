package integration

import (
	"context"
	"time"

	"github.com/riochat/riochat/backend/internal/features/billing"
	"github.com/riochat/riochat/backend/internal/features/user"
)

// ---- user: subscriptions and balances shown and edited by administrators ----

type userBilling struct{ h *Hub }

func (b userBilling) Mode(ctx context.Context) string {
	if b.h.billing == nil {
		return "self"
	}
	return b.h.billing.Mode(ctx)
}

func (b userBilling) Subscriptions(ctx context.Context, ids []uint) (map[uint]user.SubscriptionInfo, error) {
	out := map[uint]user.SubscriptionInfo{}
	if b.h.billing == nil {
		return out, nil
	}
	snaps, err := b.h.billing.CurrentSubscriptions(ctx, ids)
	if err != nil {
		return nil, err
	}
	for id, s := range snaps {
		if s == nil {
			continue
		}
		planID, name := userPlanFields(s)
		out[id] = user.SubscriptionInfo{Tier: s.Tier, PlanID: planID, PlanName: name, Status: s.Status, ExpiresAt: s.ExpiresAt}
	}
	return out, nil
}

// userPlanFields maps a subscription snapshot to the plan id and name shown on
// the user object. A user on the free tier without a dated subscription has no
// plan id and the plan name "free" (the tier code); only a paid or dated
// subscription carries the plan's id and display name.
func userPlanFields(s *billing.SubscriptionSnapshot) (*uint, string) {
	if s.Tier == billing.PlanFree && s.Status == billing.PlanFree {
		return nil, billing.PlanFree
	}
	id := s.PlanID
	return &id, s.PlanName
}

func (b userBilling) Accounts(ctx context.Context, ids []uint) (map[uint]user.BillingAccountInfo, error) {
	out := map[uint]user.BillingAccountInfo{}
	if b.h.billing == nil {
		return out, nil
	}
	snaps, err := b.h.billing.AccountSnapshots(ctx, ids)
	if err != nil {
		return nil, err
	}
	for id, s := range snaps {
		out[id] = user.BillingAccountInfo{Currency: s.Currency, Balance: s.BalanceNanousd, Status: s.Status}
	}
	return out, nil
}

func (b userBilling) AssignPlan(ctx context.Context, userID uint, planCode string, expiresAt *time.Time) error {
	if b.h.billing == nil {
		return user.ErrPaymentRequired
	}
	return b.h.billing.AssignUserPlan(ctx, userID, planCode, expiresAt)
}

// ---- channel: plans that reference a permission group ----

type planReferences struct{ h *Hub }

func (p planReferences) PlansReferencingGroup(ctx context.Context, groupID uint) (int64, error) {
	if p.h.billing == nil {
		return 0, nil
	}
	return p.h.billing.PlansReferencingGroup(ctx, groupID)
}
