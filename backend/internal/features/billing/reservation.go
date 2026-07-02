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

// RenewReservation extends the lease of a live reservation.
func (s *Service) RenewReservation(ctx context.Context, userID uint, refNo string) error {
	now := s.now()
	res := s.db.WithContext(ctx).Model(&UsageReservation{}).
		Where("user_id = ? AND ref_no = ? AND status = ? AND expires_at > ?", userID, refNo, ResActive, now).
		Update("expires_at", now.Add(reservationLease))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrReservationConflict
	}
	return nil
}

// MarkReconciliation flags a reservation whose upstream cost could not be
// settled. Such a reservation keeps blocking budget and a slot until handled
// operationally.
func (s *Service) MarkReconciliation(ctx context.Context, userID uint, refNo, failureCode string) error {
	if len(failureCode) > 64 {
		failureCode = failureCode[:64]
	}
	now := s.now()
	res := s.db.WithContext(ctx).Model(&UsageReservation{}).
		Where("user_id = ? AND ref_no = ? AND status IN ?", userID, refNo, []string{ResActive, ResReconciliation}).
		Updates(map[string]any{"status": ResReconciliation, "failure_code": failureCode, "reconciliation_at": now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrReservationConflict
	}
	return nil
}

// Authorize decides, before the upstream call, whether the user may spend on
// the model and reserves budget for the run.
func (s *Service) Authorize(ctx context.Context, userID uint, platformModel, runID string) (*Authorization, error) {
	rs, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	refNo := strings.TrimSpace(runID)
	auth := &Authorization{UserID: userID, Mode: rs.Mode, RefNo: refNo}
	if rs.Mode != ModeUsage && rs.Mode != ModePeriod {
		return auth, nil
	}
	if personalModelRef(platformModel) {
		auth.Personal = true
	} else {
		spec, err := s.priceFor(ctx, platformModel)
		if err != nil {
			return nil, err
		}
		if spec == nil {
			return nil, ErrPricingRequired
		}
		if spec.IsFree {
			auth.Free = true
			return auth, nil
		}
		if spec.Mode == PricingDuration && !s.confirmsVideo(ctx, platformModel) {
			return nil, ErrPricingRequired
		}
	}
	if refNo == "" {
		return nil, errors.New("billing: a run reference is required to reserve budget")
	}
	if rs.prepaidErr != nil {
		return nil, rs.prepaidErr
	}
	var w *periodWindow
	if rs.Mode == ModePeriod {
		plan, err := s.effectivePlan(ctx, s.db, userID)
		if err != nil {
			return nil, err
		}
		start, end := s.monthWindow(s.now())
		w = &periodWindow{Limit: plan.PeriodCreditNanousd, Start: start.UTC(), End: end.UTC()}
	}
	r, err := s.reserve(ctx, userID, refNo, rs.Mode, rs.PrepaidAmountNanousd, w)
	if err != nil {
		return nil, err
	}
	auth.Reservation = r
	return auth, nil
}

func (s *Service) confirmsVideo(ctx context.Context, model string) bool {
	if s.ports.Models == nil {
		return false
	}
	info, ok, err := s.ports.Models.Model(ctx, strings.TrimSpace(model))
	return err == nil && ok && info.SupportsVideo
}

// EstimateInput describes the next upstream call for budget estimation.
type EstimateInput struct {
	PlatformModelName string
	ProviderProtocol  string
	CacheTimeout      string
	RequestSpeed      string
	UsageSpeed        string
	RequestTier       string
	UsageTier         string
	InputTokens       int64
	CacheReadTokens   int64
	CacheWriteTokens  int64
	// OutputTokens is the output already billed in earlier calls of the run.
	OutputTokens int64
	// MaxOutputTokens is the request's explicit output limit (0 = unbounded).
	MaxOutputTokens int64
	Calls           int64
	DurationSeconds int64
	// BilledSoFar is the amount already incurred by earlier calls of the run.
	BilledSoFar int64
}

// EstimateCost prices the worst case of the next call (0 for free, unpriced
// or unmetered runs).
func (s *Service) EstimateCost(ctx context.Context, auth *Authorization, in EstimateInput) (int64, error) {
	if auth == nil || (auth.Mode != ModeUsage && auth.Mode != ModePeriod) || auth.Free || auth.Personal {
		return 0, nil
	}
	spec, err := s.priceFor(ctx, in.PlatformModelName)
	if err != nil || spec == nil || spec.IsFree {
		return 0, err
	}
	now := s.now()
	speed := billingSpeed(in.ProviderProtocol, in.UsageSpeed, in.RequestSpeed)
	tier := billingTier(in.ProviderProtocol, in.UsageTier)
	mult := composeMultiplier(baseRatio(in.ProviderProtocol, speed, tier, in.PlatformModelName, ""),
		s.groupRate(ctx, auth.UserID, in.PlatformModelName, s.extraGroupOf(ctx, auth.UserID)), spec.scheduleAt(now, s.loc))
	mc, err := priceUsage(*spec, usageCounts{Input: in.InputTokens, CacheRead: in.CacheReadTokens, CacheWrite: in.CacheWriteTokens,
		Output: in.OutputTokens + in.MaxOutputTokens, Calls: maxI64(in.Calls, 1), Seconds: in.DurationSeconds},
		in.ProviderProtocol, in.CacheTimeout, mult.Total)
	if err != nil {
		return 0, err
	}
	return satAdd(in.BilledSoFar, mc.Total), nil
}

// EnsureBudget raises the run's reservation to cover the estimate. A failure is
// reported as insufficient funds so the run stops before any upstream spend.
func (s *Service) EnsureBudget(ctx context.Context, auth *Authorization, in EstimateInput) error {
	if auth == nil || auth.Reservation == nil {
		return nil
	}
	est, err := s.EstimateCost(ctx, auth, in)
	if err != nil {
		return err
	}
	if est <= satAdd(auth.Reservation.BalanceNanousd, auth.Reservation.PeriodCreditNanousd) {
		return nil
	}
	if err := s.RaiseReservation(ctx, auth.UserID, auth.RefNo, est); err != nil {
		if errors.Is(err, ErrInsufficientFunds) || errors.Is(err, ErrReservationConflict) {
			return ErrInsufficientFunds
		}
		return err
	}
	return nil
}

// MaxOutputTokens extracts the explicit output limit from request options.
func MaxOutputTokens(options map[string]any) int64 {
	pick := func(m map[string]any, keys ...string) int64 {
		for _, k := range keys {
			switch v := m[k].(type) {
			case float64:
				if v > 0 {
					return int64(v)
				}
			case int:
				if v > 0 {
					return int64(v)
				}
			case int64:
				if v > 0 {
					return v
				}
			}
		}
		return 0
	}
	if n := pick(options, "max_output_tokens", "max_completion_tokens", "max_tokens", "maxOutputTokens"); n > 0 {
		return n
	}
	if g, ok := options["generationConfig"].(map[string]any); ok {
		if n := pick(g, "maxOutputTokens"); n > 0 {
			return n
		}
	}
	if g, ok := options["generation_config"].(map[string]any); ok {
		if n := pick(g, "max_output_tokens"); n > 0 {
			return n
		}
	}
	return 0
}
