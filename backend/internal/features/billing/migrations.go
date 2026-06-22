package billing

import (
	"context"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/migrate"
)

// Migrations returns the area's idempotent steps.
func Migrations() []migrate.Step {
	return []migrate.Step{
		migrate.AutoMigrate("tables", allModels()...),
		migrate.Exec("ledger_user_ref_unique", migrate.PhaseSchema, map[string]string{
			"sqlite":   "CREATE UNIQUE INDEX IF NOT EXISTS uidx_billing_usage_ledgers_user_ref ON billing_usage_ledgers(user_id, ref_no) WHERE ref_no <> ''",
			"postgres": "CREATE UNIQUE INDEX IF NOT EXISTS uidx_billing_usage_ledgers_user_ref ON billing_usage_ledgers(user_id, ref_no) WHERE ref_no <> ''",
		}),
		migrate.Seed("default_plans", seedPlans),
	}
}

type seedPlan struct {
	code, name, desc, features string
	credit                     int64
	sort                       int
	priceCode, interval        string
	cents                      int64
}

var seedPlanTable = []seedPlan{
	{"free", "Free", "Free basic plan", `{"priority":"shared"}`, 1_000_000_000, 10, "free-default", IntervalLifetime, 0},
	{"pro", "Pro", "Plan for light usage", `{"priority":"standard"}`, 30_000_000_000, 20, "pro-monthly", IntervalMonth, 2000},
	{"max", "Max", "Plan for everyday usage", `{"priority":"advanced"}`, 75_000_000_000, 30, "max-monthly", IntervalMonth, 5000},
	{"ultra", "Ultra", "Plan for heavy usage", `{"priority":"premium"}`, 300_000_000_000, 40, "ultra-monthly", IntervalMonth, 20000},
}

// seedPlans inserts the built-in plans and prices, but only into an entirely
// empty catalog so administrator edits and deletions are never undone. The
// permission group binding is applied at start-up (see bindDefaultGroup).
func seedPlans(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var plans, prices int64
		if err := tx.Unscoped().Model(&Plan{}).Count(&plans).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Model(&Price{}).Count(&prices).Error; err != nil {
			return err
		}
		if plans > 0 || prices > 0 {
			return nil
		}
		for _, sp := range seedPlanTable {
			plan := Plan{Code: sp.code, Name: sp.name, Description: sp.desc, FeatureJSON: sp.features,
				PeriodCreditNanousd: sp.credit, SortOrder: sp.sort, IsActive: true}
			if err := tx.Create(&plan).Error; err != nil {
				return err
			}
			price := Price{PlanID: plan.ID, Code: sp.priceCode, BillingInterval: sp.interval,
				Currency: CurrencyUSD, AmountCents: sp.cents, IsActive: true, IsDefault: true}
			if err := tx.Create(&price).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
