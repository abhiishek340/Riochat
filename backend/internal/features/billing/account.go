package billing

import (
	"context"
	"net/http"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// AccountView is the API form of a billing account.
type AccountView struct {
	UserID         uint      `json:"userID"`
	Currency       string    `json:"currency"`
	BalanceNanousd int64     `json:"balanceNanousd"`
	BalanceUSD     float64   `json:"balanceUSD"`
	Status         string    `json:"status"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

func accountView(a Account) AccountView {
	return AccountView{UserID: a.UserID, Currency: a.Currency, BalanceNanousd: a.BalanceNanousd, BalanceUSD: NanoToUSD(a.BalanceNanousd),
		Status: a.Status, UpdatedAt: a.UpdatedAt}
}

// GetAccount returns the user's account, creating a zero one when missing.
func (s *Service) GetAccount(ctx context.Context, userID uint) (*AccountView, error) {
	acc, err := ensureAccount(s.db.WithContext(ctx), userID)
	if err != nil {
		return nil, err
	}
	v := accountView(*acc)
	return &v, nil
}

// AccountSnapshot is the minimal account state other areas need.
type AccountSnapshot struct {
	UserID         uint
	Currency       string
	BalanceNanousd int64
	Status         string
}

// AccountSnapshots returns the accounts of many users; users without one report
// USD, balance 0 and status active.
func (s *Service) AccountSnapshots(ctx context.Context, userIDs []uint) (map[uint]AccountSnapshot, error) {
	out := make(map[uint]AccountSnapshot, len(userIDs))
	for _, id := range userIDs {
		out[id] = AccountSnapshot{UserID: id, Currency: CurrencyUSD, Status: "active"}
	}
	if len(userIDs) == 0 {
		return out, nil
	}
	var accs []Account
	if err := s.db.WithContext(ctx).Where("user_id IN ?", userIDs).Find(&accs).Error; err != nil {
		return nil, err
	}
	for _, a := range accs {
		out[a.UserID] = AccountSnapshot{UserID: a.UserID, Currency: a.Currency, BalanceNanousd: a.BalanceNanousd, Status: a.Status}
	}
	return out, nil
}

// SetBalance sets a user's absolute balance (administrator action).
func (s *Service) SetBalance(ctx context.Context, userID uint, balanceUSD float64, description, requestID string) (*AccountView, error) {
	mode := s.currentMode(ctx)
	if mode != ModeUsage && mode != ModePeriod {
		return nil, ErrPaymentRequired.WithStatus(http.StatusBadRequest)
	}
	if balanceUSD < 0 || balanceUSD != balanceUSD || balanceUSD > 1e12 {
		return nil, ErrInvalidAccountBalance
	}
	target := USDToNano(balanceUSD)
	var out AccountView
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		acc, err := lockAccount(tx, userID)
		if err != nil {
			return err
		}
		delta := target - acc.BalanceNanousd
		if err := tx.Model(&Account{}).Where("id = ?", acc.ID).Updates(map[string]any{"balance_nanousd": target}).Error; err != nil {
			return err
		}
		acc.BalanceNanousd = target
		if delta != 0 {
			if description == "" {
				description = "balance set by administrator"
			}
			if err := tx.Create(&BalanceTransaction{AccountID: acc.ID, UserID: userID, Type: TxAdminSet, AmountNanousd: delta,
				BalanceAfterNanousd: target, RefType: "admin", RefNo: requestID, Description: description}).Error; err != nil {
				return err
			}
		}
		fresh := Account{}
		if err := tx.First(&fresh, acc.ID).Error; err != nil {
			return database.Translate(err)
		}
		out = accountView(fresh)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Overview is the account overview; its shape depends on the billing mode.
type Overview map[string]any

// Overview builds the billing overview of a user.
func (s *Service) Overview(ctx context.Context, userID uint) (*Overview, error) {
	mode := s.currentMode(ctx)
	// Every key is always present: a mode that does not use a figure reports
	// null or zero rather than omitting it, so clients can read them blindly.
	ov := Overview{
		"mode": mode, "plan": nil, "periodStartAt": nil, "periodEndAt": nil,
		"periodCreditUSD": 0.0, "periodCreditNanousd": int64(0),
		"periodUsedUSD": 0.0, "periodUsedNanousd": int64(0),
		"periodRemainingUSD": 0.0, "periodRemainingNanousd": int64(0),
		"account": nil, "totalSpentUSD": 0.0, "totalSpentNanousd": int64(0),
		"subscriptionEntitlements": []any{},
	}
	if mode != ModeUsage && mode != ModePeriod {
		return &ov, nil
	}
	acc, err := s.GetAccount(ctx, userID)
	if err != nil {
		return nil, err
	}
	ov["account"] = acc
	if mode == ModeUsage {
		var spent int64
		if err := s.db.WithContext(ctx).Model(&UsageLedger{}).Where("user_id = ? AND is_free_model = ?", userID, false).
			Select("COALESCE(SUM(billed_nanousd), 0)").Scan(&spent).Error; err != nil {
			return nil, err
		}
		ov["totalSpentNanousd"], ov["totalSpentUSD"] = spent, NanoToUSD(spent)
		return &ov, nil
	}
	plan, err := s.effectivePlan(ctx, s.db, userID)
	if err != nil {
		return nil, err
	}
	pv, err := s.planView(ctx, plan)
	if err != nil {
		return nil, err
	}
	start, end := s.monthWindow(s.now())
	used, err := usedInWindow(s.db.WithContext(ctx), userID, start.UTC(), end.UTC())
	if err != nil {
		return nil, err
	}
	remaining := clampNonNeg(plan.PeriodCreditNanousd - used)
	ent, err := s.entitlementQueue(ctx, userID)
	if err != nil {
		return nil, err
	}
	ov["plan"] = pv
	ov["periodStartAt"], ov["periodEndAt"] = start, end
	ov["periodCreditNanousd"], ov["periodCreditUSD"] = plan.PeriodCreditNanousd, NanoToUSD(plan.PeriodCreditNanousd)
	ov["periodUsedNanousd"], ov["periodUsedUSD"] = used, NanoToUSD(used)
	ov["periodRemainingNanousd"], ov["periodRemainingUSD"] = remaining, NanoToUSD(remaining)
	ov["subscriptionEntitlements"] = ent
	return &ov, nil
}
