package billing

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

const (
	descUsageDebit   = "pay-as-you-go model usage charge"
	descPeriodExcess = "subscription overage charged to balance"
)

// findReplay returns the stored ledger of a run reference, if any.
func findReplay(tx *gorm.DB, userID uint, refNo string, ledgerID uint) (*UsageLedger, error) {
	var l UsageLedger
	var err error
	switch {
	case ledgerID != 0:
		err = tx.First(&l, ledgerID).Error
		if database.IsNotFound(err) && refNo != "" {
			err = tx.Where("user_id = ? AND ref_no = ?", userID, refNo).First(&l).Error
		}
	case refNo != "":
		err = tx.Where("user_id = ? AND ref_no = ?", userID, refNo).First(&l).Error
	default:
		return nil, nil
	}
	if err != nil {
		if database.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}

func chargeOf(d *LedgerDraft) int64 {
	if d.Row.IsFreeModel || d.Row.BilledNanousd <= 0 {
		return 0
	}
	return d.Row.BilledNanousd
}

// lockReservation loads and locks the authorization's reservation.
func lockReservation(tx *gorm.DB, a *Authorization) (*UsageReservation, error) {
	if a.Reservation == nil {
		return nil, nil
	}
	var r UsageReservation
	err := tx.Where("user_id = ? AND ref_no = ?", a.UserID, a.RefNo).First(&r).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, ErrReservationConflict
		}
		return nil, err
	}
	return &r, nil
}

// Settle writes the ledger of a finished run, moves the money and closes the
// reservation, atomically and idempotently by run reference.
func (s *Service) Settle(ctx context.Context, auth *Authorization, draft *LedgerDraft) (*UsageLedger, error) {
	if auth == nil || draft == nil {
		return nil, errLedgerInput
	}
	mode := auth.Mode
	if mode == "" {
		mode = s.currentMode(ctx)
	}
	draft.Row.UserID = auth.UserID
	if draft.Row.RefNo == "" {
		draft.Row.RefNo = auth.RefNo
	}
	var out *UsageLedger
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		switch {
		case mode == ModePeriod:
			out, err = s.settlePeriod(tx, auth, draft)
		case auth.Reservation != nil || mode == ModeUsage:
			out, err = s.settleUsage(tx, auth, draft, mode)
		default:
			out, err = s.settlePlain(tx, draft)
		}
		return err
	})
	if err != nil {
		if database.IsDuplicate(err) {
			return nil, ErrReservationConflict
		}
		return nil, err
	}
	return out, nil
}

func (s *Service) settlePlain(tx *gorm.DB, d *LedgerDraft) (*UsageLedger, error) {
	if old, err := findReplay(tx, d.Row.UserID, d.Row.RefNo, 0); err != nil || old != nil {
		return old, err
	}
	if err := insertLedger(tx, d); err != nil {
		return nil, err
	}
	return &d.Row, nil
}

func (s *Service) settleUsage(tx *gorm.DB, a *Authorization, d *LedgerDraft, mode string) (*UsageLedger, error) {
	acc, err := lockAccount(tx, a.UserID)
	if err != nil {
		return nil, err
	}
	res, err := lockReservation(tx, a)
	if err != nil {
		return nil, err
	}
	if res != nil {
		if res.Status == ResSettled {
			if old, err := findReplay(tx, a.UserID, a.RefNo, res.UsageLedgerID); err != nil || old != nil {
				return old, err
			}
			return nil, ErrReservationConflict
		}
		if res.Mode != mode || (res.Status != ResActive && res.Status != ResReconciliation) {
			return nil, ErrReservationConflict
		}
	} else if old, err := findReplay(tx, a.UserID, d.Row.RefNo, 0); err != nil || old != nil {
		return old, err
	}
	charge := chargeOf(d)
	after := acc.BalanceNanousd - charge
	d.Row.BalanceAfterNanousd = &after
	if err := insertLedger(tx, d); err != nil {
		return nil, err
	}
	if charge > 0 {
		if err := debit(tx, acc, charge, d.Row.ID, d.Row.RefNo, descUsageDebit); err != nil {
			return nil, err
		}
	}
	if res != nil {
		if err := closeReservation(tx, res, d.Row.ID, s.now()); err != nil {
			return nil, err
		}
	}
	return &d.Row, nil
}

// debit lowers the balance by amount and records a usage_debit transaction.
func debit(tx *gorm.DB, acc *Account, amount int64, ledgerID uint, refNo, description string) error {
	newBal := acc.BalanceNanousd - amount
	if err := tx.Model(&Account{}).Where("id = ?", acc.ID).
		Updates(map[string]any{"balance_nanousd": newBal, "currency": CurrencyUSD, "status": "active"}).Error; err != nil {
		return err
	}
	acc.BalanceNanousd = newBal
	return tx.Create(&BalanceTransaction{AccountID: acc.ID, UserID: acc.UserID, Type: TxUsageDebit, AmountNanousd: -amount,
		BalanceAfterNanousd: newBal, RefType: "usage_ledger", RefID: ledgerID, RefNo: refNo, Description: description}).Error
}

func closeReservation(tx *gorm.DB, r *UsageReservation, ledgerID uint, now time.Time) error {
	return tx.Model(&UsageReservation{}).Where("id = ?", r.ID).
		Updates(map[string]any{"status": ResSettled, "usage_ledger_id": ledgerID, "settled_at": now}).Error
}

func (s *Service) settlePeriod(tx *gorm.DB, a *Authorization, d *LedgerDraft) (*UsageLedger, error) {
	if d.Row.BillingAt.IsZero() || d.Row.UsageDate.IsZero() {
		return nil, errLedgerInput
	}
	acc, err := lockAccount(tx, a.UserID)
	if err != nil {
		return nil, err
	}
	res, err := lockReservation(tx, a)
	if err != nil {
		return nil, err
	}
	var w periodWindow
	if res != nil {
		if res.Status == ResSettled {
			if old, err := findReplay(tx, a.UserID, a.RefNo, res.UsageLedgerID); err != nil || old != nil {
				return old, err
			}
			return nil, ErrReservationConflict
		}
		pw := windowOf(res)
		if res.Mode != ModePeriod || pw == nil || !pw.End.After(pw.Start) || pw.Limit < 0 ||
			(res.Status != ResActive && res.Status != ResReconciliation) {
			return nil, ErrReservationConflict
		}
		w = *pw
	} else {
		if old, err := findReplay(tx, a.UserID, d.Row.RefNo, 0); err != nil || old != nil {
			return old, err
		}
		plan, err := s.effectivePlan(context.Background(), tx, a.UserID)
		if err != nil {
			return nil, err
		}
		start, end := s.monthWindow(d.Row.BillingAt)
		w = periodWindow{Limit: plan.PeriodCreditNanousd, Start: start.UTC(), End: end.UTC()}
	}
	charge := chargeOf(d)
	usedBefore, err := usedInWindow(tx, a.UserID, w.Start, w.End)
	if err != nil {
		return nil, err
	}
	var reservedOther int64
	{
		var rows []UsageReservation
		q := activeReservationScope(tx, a.UserID, s.now())
		if res != nil {
			q = q.Where("id <> ?", res.ID)
		}
		if err := q.Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			if r.PeriodStartAt != nil && r.PeriodEndAt != nil && r.PeriodStartAt.Equal(w.Start) && r.PeriodEndAt.Equal(w.End) {
				reservedOther = satAdd(reservedOther, r.PeriodCreditNanousd)
			}
		}
	}
	remaining := clampNonNeg(w.Limit - usedBefore - reservedOther)
	covered := minI64(charge, remaining)
	overage := charge - covered
	after := acc.BalanceNanousd - overage
	d.Row.BalanceAfterNanousd = &after

	var resCredit, resBalance int64
	if res != nil {
		resCredit, resBalance = res.PeriodCreditNanousd, res.BalanceNanousd
	}
	usedAfter := usedBefore
	if !d.Row.IsFreeModel {
		usedAfter = satAdd(usedBefore, maxI64(d.Row.BilledNanousd, 0))
	}
	d.Snapshot["period_credit_nanousd"] = w.Limit
	d.Snapshot["period_used_before_nanousd"] = usedBefore
	d.Snapshot["period_used_after_nanousd"] = usedAfter
	d.Snapshot["period_credit_covered_nanousd"] = covered
	d.Snapshot["period_credit_reserved_nanousd"] = resCredit
	d.Snapshot["period_overage_billed_nanousd"] = overage
	d.Snapshot["period_balance_charged_nanousd"] = overage
	d.Snapshot["period_balance_reserved_nanousd"] = resBalance
	d.Snapshot["period_balance_settlement_delta_nanousd"] = overage - resBalance

	if err := insertLedger(tx, d); err != nil {
		return nil, err
	}
	if overage > 0 {
		if err := debit(tx, acc, overage, d.Row.ID, d.Row.RefNo, descPeriodExcess); err != nil {
			return nil, err
		}
	}
	if res != nil {
		if err := closeReservation(tx, res, d.Row.ID, s.now()); err != nil {
			return nil, err
		}
	}
	return &d.Row, nil
}

// SettleWithRetry settles with up to three attempts and exponential backoff
// (100 ms start) for transient failures. Business outcomes and cancellation are
// never retried, and nothing is retried unless the settlement is idempotent
// (a reservation exists or the ledger carries a run reference).
func (s *Service) SettleWithRetry(ctx context.Context, auth *Authorization, draft *LedgerDraft) (*UsageLedger, error) {
	idempotent := auth != nil && (auth.Reservation != nil || auth.RefNo != "" || (draft != nil && draft.Row.RefNo != ""))
	delay := 100 * time.Millisecond
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		l, err := s.Settle(ctx, auth, draft)
		if err == nil {
			return l, nil
		}
		lastErr = err
		if !idempotent || !retryable(err) || attempt == 2 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
	}
	return nil, lastErr
}

func retryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ErrInsufficientFunds) || errors.Is(err, ErrReservationConflict) ||
		errors.Is(err, ErrPricingRequired) || errors.Is(err, errLedgerInput) ||
		errors.Is(err, ErrPlanNotFound) || errors.Is(err, ErrConcurrencyLimit) ||
		database.IsNotFound(err) || database.IsDuplicate(err) {
		return false
	}
	return true
}

// SnapshotOf decodes a stored ledger snapshot (empty map when unreadable).
func SnapshotOf(l UsageLedger) map[string]any {
	m := map[string]any{}
	if l.PricingSnapshotJSON != "" {
		_ = json.Unmarshal([]byte(l.PricingSnapshotJSON), &m)
	}
	return m
}
