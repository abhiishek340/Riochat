package billing

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

const (
	maxActiveReservations = 5
	reservationLease      = 3 * time.Hour
)

var errLedgerInput = errors.New("billing: invalid ledger input")

// Authorization is the pre-flight decision for one paid run.
type Authorization struct {
	UserID uint
	// Mode is fixed at authorization time and used for settlement.
	Mode        string
	RefNo       string
	Reservation *UsageReservation
	// Free marks a free platform model (no reservation was made).
	Free bool
	// Personal marks a user-owned model reference.
	Personal bool
}

// periodWindow is the plan credit window of a period reservation.
type periodWindow struct {
	Limit int64
	Start time.Time
	End   time.Time
}

// activeReservationScope selects reservations that currently consume budget:
// active and unexpired, or awaiting reconciliation (which never expires).
func activeReservationScope(tx *gorm.DB, userID uint, now time.Time) *gorm.DB {
	return tx.Model(&UsageReservation{}).Where(
		"user_id = ? AND ((status = ? AND expires_at > ?) OR status = ?)", userID, ResActive, now, ResReconciliation)
}

// usedInWindow sums the billed amount of the user's non-free ledger rows whose
// billing time falls in [start, end).
func usedInWindow(tx *gorm.DB, userID uint, start, end time.Time) (int64, error) {
	var sum int64
	err := tx.Model(&UsageLedger{}).
		Where("user_id = ? AND is_free_model = ? AND billing_at >= ? AND billing_at < ?", userID, false, start.UTC(), end.UTC()).
		Select("COALESCE(SUM(billed_nanousd), 0)").Scan(&sum).Error
	return sum, err
}

func sumReservations(tx *gorm.DB, userID uint, now time.Time, excludeID uint, window *periodWindow) (balance, credit int64, count int64, err error) {
	var rows []UsageReservation
	q := activeReservationScope(tx, userID, now)
	if excludeID != 0 {
		q = q.Where("id <> ?", excludeID)
	}
	if err = q.Find(&rows).Error; err != nil {
		return
	}
	for _, r := range rows {
		count++
		balance = satAdd(balance, r.BalanceNanousd)
		if window != nil && r.PeriodStartAt != nil && r.PeriodEndAt != nil &&
			r.PeriodStartAt.Equal(window.Start) && r.PeriodEndAt.Equal(window.End) {
			credit = satAdd(credit, r.PeriodCreditNanousd)
		}
	}
	return
}

// available computes the free balance and plan credit of a user excluding the
// reservation excludeID.
func available(tx *gorm.DB, acc *Account, now time.Time, excludeID uint, w *periodWindow) (bal, credit, count int64, err error) {
	if acc.BalanceNanousd < 0 {
		return 0, 0, 0, ErrInsufficientFunds
	}
	resBal, resCredit, count, err := sumReservations(tx, acc.UserID, now, excludeID, w)
	if err != nil {
		return
	}
	bal = clampNonNeg(acc.BalanceNanousd - resBal)
	if w != nil {
		var used int64
		used, err = usedInWindow(tx, acc.UserID, w.Start, w.End)
		if err != nil {
			return
		}
		credit = clampNonNeg(w.Limit - used - resCredit)
	}
	return
}

// reserve creates the reservation of a run.
func (s *Service) reserve(ctx context.Context, userID uint, refNo, mode string, requested int64, w *periodWindow) (*UsageReservation, error) {
	var out UsageReservation
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := s.now()
		acc, err := lockAccount(tx, userID)
		if err != nil {
			return err
		}
		var n int64
		if err := tx.Model(&UsageReservation{}).Where("user_id = ? AND ref_no = ?", userID, refNo).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			if err := tx.Model(&BalanceTransaction{}).
				Where("user_id = ? AND ref_no = ? AND type IN ?", userID, refNo, []string{TxUsageReserv, TxUsageRefund}).Count(&n).Error; err != nil {
				return err
			}
		}
		if n > 0 {
			return ErrReservationConflict
		}
		bal, credit, count, err := available(tx, acc, now, 0, w)
		if err != nil {
			return err
		}
		if count >= maxActiveReservations {
			return ErrConcurrencyLimit
		}
		total := satAdd(bal, credit)
		if requested <= 0 {
			slots := maxActiveReservations - count
			requested = (total + slots - 1) / slots
		}
		if requested <= 0 || requested > total {
			return ErrInsufficientFunds
		}
		creditPart := minI64(requested, credit)
		out = UsageReservation{
			UserID: userID, RefNo: refNo, Mode: mode, BalanceNanousd: requested - creditPart, PeriodCreditNanousd: creditPart,
			Status: ResActive, ExpiresAt: now.Add(reservationLease),
		}
		if w != nil {
			out.PeriodLimitNanousd = w.Limit
			out.PeriodStartAt, out.PeriodEndAt = utcPtr(w.Start), utcPtr(w.End)
		}
		return database.Translate(tx.Create(&out).Error)
	})
	if err != nil {
		if database.IsDuplicate(err) {
			return nil, ErrReservationConflict
		}
		return nil, err
	}
	return &out, nil
}

func windowOf(r *UsageReservation) *periodWindow {
	if r.Mode != ModePeriod || r.PeriodStartAt == nil || r.PeriodEndAt == nil {
		return nil
	}
	return &periodWindow{Limit: r.PeriodLimitNanousd, Start: *r.PeriodStartAt, End: *r.PeriodEndAt}
}

// RaiseReservation increases a run's reservation to cover requested
// nano-dollars. It never decreases it.
func (s *Service) RaiseReservation(ctx context.Context, userID uint, refNo string, requested int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := s.now()
		acc, err := lockAccount(tx, userID)
		if err != nil {
			return err
		}
		var r UsageReservation
		if err := tx.Where("user_id = ? AND ref_no = ?", userID, refNo).First(&r).Error; err != nil {
			if database.IsNotFound(err) {
				return ErrReservationConflict
			}
			return err
		}
		if r.Status != ResActive || !r.ExpiresAt.After(now) {
			return ErrReservationConflict
		}
		if requested <= satAdd(r.BalanceNanousd, r.PeriodCreditNanousd) {
			return nil
		}
		bal, credit, _, err := available(tx, acc, now, r.ID, windowOf(&r))
		if err != nil {
			return err
		}
		if requested > satAdd(bal, credit) {
			return ErrInsufficientFunds
		}
		creditPart := minI64(requested, credit)
		return tx.Model(&UsageReservation{}).Where("id = ?", r.ID).
			Updates(map[string]any{"balance_nanousd": requested - creditPart, "period_credit_nanousd": creditPart}).Error
	})
}

// ReleaseReservation frees an unused reservation (idempotent).
func (s *Service) ReleaseReservation(ctx context.Context, userID uint, refNo string) error {
	return s.db.WithContext(ctx).Model(&UsageReservation{}).
		Where("user_id = ? AND ref_no = ? AND status = ?", userID, refNo, ResActive).
		Updates(map[string]any{"status": ResReleased, "released_at": s.now()}).Error
}
