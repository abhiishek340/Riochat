package billing

import (
	"context"
	"math"
	"strings"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// PriceView is the API form of a plan price.
type PriceView struct {
	ID              uint   `json:"id"`
	PlanID          uint   `json:"planID"`
	Code            string `json:"code"`
	BillingInterval string `json:"billingInterval"`
	Currency        string `json:"currency"`
	AmountCents     int64  `json:"amountCents"`
	IsDefault       bool   `json:"isDefault"`
}

// PlanView is the API form of a plan with its active prices.
type PlanView struct {
	ID                  uint        `json:"id"`
	Code                string      `json:"code"`
	Name                string      `json:"name"`
	Description         string      `json:"description"`
	FeatureJSON         string      `json:"featureJSON"`
	PeriodCreditUSD     float64     `json:"periodCreditUSD"`
	PeriodCreditNanousd int64       `json:"periodCreditNanousd"`
	SortOrder           int         `json:"sortOrder"`
	IsActive            bool        `json:"isActive"`
	PermissionGroupID   *uint       `json:"permissionGroupID"`
	Prices              []PriceView `json:"prices"`
}

func priceView(p Price) PriceView {
	return PriceView{ID: p.ID, PlanID: p.PlanID, Code: p.Code, BillingInterval: p.BillingInterval, Currency: p.Currency,
		AmountCents: p.AmountCents, IsDefault: p.IsDefault}
}

func buildPlanView(p Plan, prices []Price) PlanView {
	v := PlanView{ID: p.ID, Code: p.Code, Name: p.Name, Description: p.Description, FeatureJSON: p.FeatureJSON,
		PeriodCreditUSD: NanoToUSD(p.PeriodCreditNanousd), PeriodCreditNanousd: p.PeriodCreditNanousd, SortOrder: p.SortOrder,
		IsActive: p.IsActive, PermissionGroupID: p.PermissionGroupID, Prices: []PriceView{}}
	for _, pr := range prices {
		if pr.PlanID == p.ID && pr.IsActive {
			v.Prices = append(v.Prices, priceView(pr))
		}
	}
	return v
}

func (s *Service) activePrices(ctx context.Context, db *gorm.DB, planIDs []uint) ([]Price, error) {
	var prices []Price
	if len(planIDs) == 0 {
		return prices, nil
	}
	err := db.WithContext(ctx).Where("plan_id IN ? AND is_active = ?", planIDs, true).Order("amount_cents ASC, id ASC").Find(&prices).Error
	return prices, err
}

func (s *Service) planView(ctx context.Context, p Plan) (*PlanView, error) {
	prices, err := s.activePrices(ctx, s.db, []uint{p.ID})
	if err != nil {
		return nil, err
	}
	v := buildPlanView(p, prices)
	return &v, nil
}

// ListPlans returns the active plans in display order.
func (s *Service) ListPlans(ctx context.Context) ([]PlanView, error) {
	var plans []Plan
	if err := s.db.WithContext(ctx).Where("is_active = ?", true).Order("sort_order ASC, id ASC").Find(&plans).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(plans))
	for _, p := range plans {
		ids = append(ids, p.ID)
	}
	prices, err := s.activePrices(ctx, s.db, ids)
	if err != nil {
		return nil, err
	}
	out := make([]PlanView, 0, len(plans))
	for _, p := range plans {
		out = append(out, buildPlanView(p, prices))
	}
	return out, nil
}

// PlansReferencingGroup counts the plans bound to a permission group so the
// permission area can refuse to delete a group in use.
func (s *Service) PlansReferencingGroup(ctx context.Context, groupID uint) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&Plan{}).Where("permission_group_id = ?", groupID).Count(&n).Error
	return n, err
}

// UpdatePlanInput is the administrator's plan edit.
type UpdatePlanInput struct {
	Name              string
	Description       string
	PeriodCreditUSD   float64
	AmountUSD         float64
	BillingInterval   string
	PermissionGroupID *uint
}

// UpdatePlan edits a plan and upserts its default price.
func (s *Service) UpdatePlan(ctx context.Context, id uint, in UpdatePlanInput) (*PlanView, error) {
	var plan Plan
	if err := s.db.WithContext(ctx).First(&plan, id).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, ErrPlanNotFound
		}
		return nil, err
	}
	var group uint
	if in.PermissionGroupID == nil {
		if s.ports.Groups == nil {
			return nil, ErrInvalidPermissionGroup
		}
		g, ok, err := s.ports.Groups.DefaultGroupID(ctx)
		if err != nil || !ok || g == 0 {
			return nil, ErrInvalidPermissionGroup
		}
		group = g
	} else {
		group = *in.PermissionGroupID
		if group == 0 {
			return nil, ErrInvalidPermissionGroup
		}
		if s.ports.Groups == nil {
			return nil, ErrInvalidPermissionGroup
		}
		ok, err := s.ports.Groups.GroupExists(ctx, group)
		if err != nil || !ok {
			return nil, ErrInvalidPermissionGroup
		}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = plan.Name
	}
	credit := USDToNano(in.PeriodCreditUSD)
	cents := int64(0)
	if in.AmountUSD > 0 && !math.IsNaN(in.AmountUSD) && !math.IsInf(in.AmountUSD, 0) {
		cents = int64(math.Round(in.AmountUSD * 100))
	}
	interval := normalizeInterval(in.BillingInterval)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		plan.Name, plan.Description = name, strings.TrimSpace(in.Description)
		plan.PeriodCreditNanousd, plan.IsActive, plan.PermissionGroupID = credit, true, &group
		if err := tx.Save(&plan).Error; err != nil {
			return err
		}
		if err := tx.Model(&Price{}).Where("plan_id = ?", plan.ID).Update("is_default", false).Error; err != nil {
			return err
		}
		code := plan.Code + "-default"
		var price Price
		err := tx.Where("code = ?", code).First(&price).Error
		if err != nil && !database.IsNotFound(err) {
			return err
		}
		price.PlanID, price.Code, price.BillingInterval, price.Currency = plan.ID, code, interval, CurrencyUSD
		price.AmountCents, price.IsActive, price.IsDefault = cents, true, true
		return database.Translate(tx.Save(&price).Error)
	})
	if err != nil {
		return nil, err
	}
	return s.planView(ctx, plan)
}

// bindDefaultGroup gives plans without a permission group the default group.
func (s *Service) bindDefaultGroup(ctx context.Context) error {
	if s.ports.Groups == nil {
		return nil
	}
	g, ok, err := s.ports.Groups.DefaultGroupID(ctx)
	if err != nil || !ok || g == 0 {
		return err
	}
	return s.db.WithContext(ctx).Model(&Plan{}).Where("permission_group_id IS NULL").Update("permission_group_id", g).Error
}
