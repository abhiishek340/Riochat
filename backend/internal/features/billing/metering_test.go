package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func tokenModel(name string) ModelPrice {
	return ModelPrice{PlatformModelName: name, PricingMode: PricingToken, InputNanousdPerMTokens: 3_000_000_000,
		OutputNanousdPerMTokens: 15_000_000_000, CacheReadNanousdPerMTokens: 300_000_000, CacheWriteNanousdPerMTokens: 3_750_000_000}
}

func (e *env) settleRun(userID uint, ref string, in LedgerInput) (*UsageLedger, *Authorization, error) {
	e.t.Helper()
	ctx := context.Background()
	auth, err := e.svc.Authorize(ctx, userID, in.PlatformModelName, ref)
	if err != nil {
		return nil, nil, err
	}
	in.UserID, in.RefNo, in.Mode = userID, ref, auth.Mode
	d, err := e.svc.BuildLedger(ctx, in)
	if err != nil {
		return nil, auth, err
	}
	l, err := e.svc.Settle(ctx, auth, d)
	return l, auth, err
}

func snapNum(t *testing.T, l *UsageLedger, key string) float64 {
	t.Helper()
	v, ok := SnapshotOf(*l)[key]
	if !ok {
		t.Fatalf("snapshot key %s missing in %s", key, l.PricingSnapshotJSON)
	}
	f, _ := v.(float64)
	return f
}

func TestAuthorizeSelfModeNeverReserves(t *testing.T) {
	e := newEnv(t)
	a, err := e.svc.Authorize(context.Background(), 1, "unpriced", "run1")
	if err != nil || a.Reservation != nil || a.Mode != ModeSelf {
		t.Fatalf("%+v %v", a, err)
	}
}

func TestAuthorizeUsageDynamicBudgetAndSlots(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.price(tokenModel("m"))
	e.fund(1, 10)
	ctx := context.Background()
	var auths []*Authorization
	for i := 0; i < 5; i++ {
		a, err := e.svc.Authorize(ctx, 1, "m", fmt.Sprintf("run%d", i))
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		// ceil(available / remaining slots) : 10/5, 8/4, ... = 2 each
		if a.Reservation.BalanceNanousd != 2_000_000_000 {
			t.Fatalf("run %d reserved %d", i, a.Reservation.BalanceNanousd)
		}
		auths = append(auths, a)
	}
	_, err := e.svc.Authorize(ctx, 1, "m", "run-6")
	if !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("sixth: %v", err)
	}
	// releasing one frees a slot (idempotent)
	if err := e.svc.ReleaseReservation(ctx, 1, "run0"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ReleaseReservation(ctx, 1, "run0"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ReleaseReservation(ctx, 1, "does-not-exist"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Authorize(ctx, 1, "m", "run-6"); err != nil {
		t.Fatalf("after release: %v", err)
	}
	_ = auths
}

func TestAuthorizeErrors(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	ctx := context.Background()
	e.price(tokenModel("m"))
	// unpriced model
	if _, err := e.svc.Authorize(ctx, 1, "ghost", "r1"); !errors.Is(err, ErrPricingRequired) {
		t.Fatalf("unpriced: %v", err)
	}
	// no funds
	if _, err := e.svc.Authorize(ctx, 1, "m", "r2"); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("no funds: %v", err)
	}
	// negative balance
	e.DB.Model(&Account{}).Where("user_id = 1").Update("balance_nanousd", -5)
	if _, err := e.svc.Authorize(ctx, 1, "m", "r3"); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("negative: %v", err)
	}
	e.fund(1, 10)
	if _, err := e.svc.Authorize(ctx, 1, "m", "r4"); err != nil {
		t.Fatal(err)
	}
	// reuse of a run reference conflicts
	if _, err := e.svc.Authorize(ctx, 1, "m", "r4"); !errors.Is(err, ErrReservationConflict) {
		t.Fatalf("reuse: %v", err)
	}
	// legacy reserve/refund balance transactions also conflict
	acc, _ := ensureAccount(e.DB, 1)
	e.DB.Create(&BalanceTransaction{AccountID: acc.ID, UserID: 1, Type: TxUsageRefund, RefNo: "legacy"})
	if _, err := e.svc.Authorize(ctx, 1, "m", "legacy"); !errors.Is(err, ErrReservationConflict) {
		t.Fatalf("legacy ref: %v", err)
	}
	// duration pricing needs a video capable catalog
	e.price(ModelPrice{PlatformModelName: "vid", PricingMode: PricingDuration, DurationNanousdPerSecond: 1})
	if _, err := e.svc.Authorize(ctx, 1, "vid", "r5"); !errors.Is(err, ErrPricingRequired) {
		t.Fatalf("duration: %v", err)
	}
}

func TestAuthorizeFreeAndPersonalModels(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	ctx := context.Background()
	e.price(ModelPrice{PlatformModelName: "free", PricingMode: PricingToken, IsFree: true})
	a, err := e.svc.Authorize(ctx, 1, "free", "f1")
	if err != nil || !a.Free || a.Reservation != nil {
		t.Fatalf("free: %+v %v", a, err)
	}
	// personal models need no price row but still reserve
	if _, err := e.svc.Authorize(ctx, 1, "personal:my-model", "p1"); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("personal without funds: %v", err)
	}
	e.fund(1, 5)
	a, err = e.svc.Authorize(ctx, 1, "personal:my-model", "p2")
	if err != nil || !a.Personal || a.Reservation == nil {
		t.Fatalf("personal: %+v %v", a, err)
	}
}

func TestAuthorizeConfiguredPrepaidAmount(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.setSettings(map[string]string{KeyPrepaidAmountUSD: "3"})
	e.price(tokenModel("m"))
	e.fund(1, 10)
	ctx := context.Background()
	a, err := e.svc.Authorize(ctx, 1, "m", "a")
	if err != nil || a.Reservation.BalanceNanousd != 3_000_000_000 {
		t.Fatalf("%+v %v", a, err)
	}
	// a request above the remaining availability is rejected
	for i := 0; i < 2; i++ {
		if _, err := e.svc.Authorize(ctx, 1, "m", fmt.Sprintf("b%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.Authorize(ctx, 1, "m", "c"); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("over budget: %v", err)
	}
}

func TestReservationRefillAfterSettlement(t *testing.T) {
	// balance 100 (nanousd units here are scaled): five reservations of 20,
	// settling 10 leaves enough for a new 10 budget, settling 50 leaves none.
	run := func(settle int64, wantErr bool) {
		e := newEnv(t)
		e.mode("usage")
		e.price(tokenModel("m"))
		ctx := context.Background()
		acc, _ := ensureAccount(e.DB, 1)
		e.DB.Model(&Account{}).Where("id = ?", acc.ID).Update("balance_nanousd", 100)
		for i := 0; i < 5; i++ {
			if _, err := e.svc.Authorize(ctx, 1, "m", fmt.Sprintf("r%d", i)); err != nil {
				t.Fatal(err)
			}
		}
		auth := &Authorization{UserID: 1, Mode: ModeUsage, RefNo: "r0"}
		var res UsageReservation
		e.DB.Where("ref_no = ?", "r0").First(&res)
		auth.Reservation = &res
		d := &LedgerDraft{Row: UsageLedger{UserID: 1, RefNo: "r0", PlatformModelName: "m", BilledNanousd: settle, BilledCurrency: "USD",
			BillingAt: e.Clock.Now(), UsageDate: DateOf(e.Clock.Now())}, Snapshot: map[string]any{}}
		if _, err := e.svc.Settle(ctx, auth, d); err != nil {
			t.Fatal(err)
		}
		// a new reservation with a fixed 10 budget
		e.setSettings(map[string]string{KeyPrepaidAmountUSD: "0.00000001"})
		_, err := e.svc.Authorize(ctx, 1, "m", "refill")
		if wantErr && !errors.Is(err, ErrInsufficientFunds) {
			t.Fatalf("settle %d: want insufficient, got %v", settle, err)
		}
		if !wantErr && err != nil {
			t.Fatalf("settle %d: %v", settle, err)
		}
	}
	run(10, false)
	run(50, true)
}

func TestRaiseReservation(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.price(tokenModel("m"))
	e.fund(1, 10)
	ctx := context.Background()
	e.setSettings(map[string]string{KeyPrepaidAmountUSD: "2"})
	a, err := e.svc.Authorize(ctx, 1, "m", "r")
	if err != nil {
		t.Fatal(err)
	}
	_ = a
	// lower value never decreases
	if err := e.svc.RaiseReservation(ctx, 1, "r", USDToNano(1)); err != nil {
		t.Fatal(err)
	}
	var res UsageReservation
	e.DB.Where("ref_no = 'r'").First(&res)
	if res.BalanceNanousd != USDToNano(2) {
		t.Fatalf("decreased: %d", res.BalanceNanousd)
	}
	if err := e.svc.RaiseReservation(ctx, 1, "r", USDToNano(6)); err != nil {
		t.Fatal(err)
	}
	e.DB.Where("ref_no = 'r'").First(&res)
	if res.BalanceNanousd != USDToNano(6) {
		t.Fatalf("raised: %d", res.BalanceNanousd)
	}
	if err := e.svc.RaiseReservation(ctx, 1, "r", USDToNano(11)); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("too much: %v", err)
	}
	if err := e.svc.RaiseReservation(ctx, 1, "missing", 5); !errors.Is(err, ErrReservationConflict) {
		t.Fatalf("missing: %v", err)
	}
	// expired lease: conflict
	e.Clock.Advance(4 * time.Hour)
	if err := e.svc.RaiseReservation(ctx, 1, "r", USDToNano(7)); !errors.Is(err, ErrReservationConflict) {
		t.Fatalf("expired: %v", err)
	}
}

func TestRenewAndReconciliation(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.price(tokenModel("m"))
	e.fund(1, 10)
	ctx := context.Background()
	if _, err := e.svc.Authorize(ctx, 1, "m", "r"); err != nil {
		t.Fatal(err)
	}
	e.Clock.Advance(2 * time.Hour)
	if err := e.svc.RenewReservation(ctx, 1, "r"); err != nil {
		t.Fatal(err)
	}
	e.Clock.Advance(2 * time.Hour) // would have expired without the renewal
	var res UsageReservation
	e.DB.Where("ref_no = 'r'").First(&res)
	if !res.ExpiresAt.After(e.Clock.Now()) {
		t.Fatal("renew did not extend the lease")
	}
	if err := e.svc.RenewReservation(ctx, 1, "none"); !errors.Is(err, ErrReservationConflict) {
		t.Fatalf("renew missing: %v", err)
	}
	if err := e.svc.MarkReconciliation(ctx, 1, "r", "ledger_build_failed"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.MarkReconciliation(ctx, 1, "none", "x"); !errors.Is(err, ErrReservationConflict) {
		t.Fatalf("recon missing: %v", err)
	}
	// reconciliation reservations never expire and keep consuming a slot
	e.Clock.Advance(100 * time.Hour)
	for i := 0; i < 4; i++ {
		if _, err := e.svc.Authorize(ctx, 1, "m", fmt.Sprintf("n%d", i)); err != nil {
			t.Fatalf("n%d: %v", i, err)
		}
	}
	if _, err := e.svc.Authorize(ctx, 1, "m", "n-over"); !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("slot not held: %v", err)
	}
	// release ignores reconciliation rows
	_ = e.svc.ReleaseReservation(ctx, 1, "r")
	e.DB.Where("ref_no = 'r'").First(&res)
	if res.Status != ResReconciliation || res.FailureCode != "ledger_build_failed" || res.ReconciliationAt == nil {
		t.Fatalf("%+v", res)
	}
}

func TestExpiredReservationsFreeSlots(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.price(tokenModel("m"))
	e.fund(1, 10)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := e.svc.Authorize(ctx, 1, "m", fmt.Sprintf("r%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	e.Clock.Advance(3*time.Hour + time.Second)
	if _, err := e.svc.Authorize(ctx, 1, "m", "late"); err != nil {
		t.Fatalf("expired leases must not hold slots: %v", err)
	}
}

func TestSettleUsageMode(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.price(tokenModel("m"))
	e.fund(1, 10)
	l, auth, err := e.settleRun(1, "run-a", LedgerInput{PlatformModelName: "m", InputTokens: 1_000_000, OutputTokens: 100_000, CallCount: 1, ProviderProtocol: "openai_chat_completions"})
	if err != nil {
		t.Fatal(err)
	}
	if l.BilledNanousd != 4_500_000_000 || l.IsFreeModel {
		t.Fatalf("billed %d free=%v", l.BilledNanousd, l.IsFreeModel)
	}
	if e.balance(1) != 10_000_000_000-4_500_000_000 {
		t.Fatalf("balance %d", e.balance(1))
	}
	if l.BalanceAfterNanousd == nil || *l.BalanceAfterNanousd != e.balance(1) {
		t.Fatalf("balance after %v", l.BalanceAfterNanousd)
	}
	var tx BalanceTransaction
	if err := e.DB.Where("user_id = 1 AND type = ?", TxUsageDebit).First(&tx).Error; err != nil {
		t.Fatal(err)
	}
	if tx.AmountNanousd != -4_500_000_000 || tx.RefType != "usage_ledger" || tx.RefID != l.ID || tx.RefNo != "run-a" || tx.Description == "" {
		t.Fatalf("tx %+v", tx)
	}
	var res UsageReservation
	e.DB.Where("ref_no = 'run-a'").First(&res)
	if res.Status != ResSettled || res.UsageLedgerID != l.ID || res.SettledAt == nil {
		t.Fatalf("reservation %+v", res)
	}
	// replay returns the original and does not debit again
	d, _ := e.svc.BuildLedger(context.Background(), LedgerInput{UserID: 1, RefNo: "run-a", Mode: auth.Mode, PlatformModelName: "m", InputTokens: 5})
	l2, err := e.svc.Settle(context.Background(), auth, d)
	if err != nil || l2.ID != l.ID || l2.BilledNanousd != l.BilledNanousd {
		t.Fatalf("replay %+v %v", l2, err)
	}
	if e.balance(1) != 10_000_000_000-4_500_000_000 {
		t.Fatal("replay debited twice")
	}
	var n int64
	e.DB.Model(&UsageLedger{}).Count(&n)
	if n != 1 {
		t.Fatalf("ledgers %d", n)
	}
}

func TestSettleChargeNotCappedByReservation(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.price(tokenModel("m"))
	e.fund(1, 1)
	e.setSettings(map[string]string{KeyPrepaidAmountUSD: "0.5"})
	l, _, err := e.settleRun(1, "big", LedgerInput{PlatformModelName: "m", OutputTokens: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if l.BilledNanousd != 15_000_000_000 || e.balance(1) != 1_000_000_000-15_000_000_000 {
		t.Fatalf("billed %d balance %d", l.BilledNanousd, e.balance(1))
	}
	// debt blocks further reservations
	if _, err := e.svc.Authorize(context.Background(), 1, "m", "next"); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("debt: %v", err)
	}
}

func TestSettleFreeModelNoReservationNoDebit(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.price(ModelPrice{PlatformModelName: "free", PricingMode: PricingToken, IsFree: true, InputNanousdPerMTokens: 1_000_000_000})
	e.fund(1, 2)
	l, auth, err := e.settleRun(1, "fr", LedgerInput{PlatformModelName: "free", InputTokens: 1_000_000})
	if err != nil || !auth.Free {
		t.Fatalf("%v", err)
	}
	if l.BilledNanousd != 0 || !l.IsFreeModel || e.balance(1) != 2_000_000_000 {
		t.Fatalf("%+v balance %d", l, e.balance(1))
	}
	// replay by ref
	d, _ := e.svc.BuildLedger(context.Background(), LedgerInput{UserID: 1, RefNo: "fr", Mode: ModeUsage, PlatformModelName: "free"})
	l2, err := e.svc.Settle(context.Background(), auth, d)
	if err != nil || l2.ID != l.ID {
		t.Fatalf("replay %v", err)
	}
}

func TestSettleSelfModeRecordsUsageWithoutCharge(t *testing.T) {
	e := newEnv(t)
	e.price(tokenModel("m"))
	l, _, err := e.settleRun(1, "s1", LedgerInput{PlatformModelName: "m", InputTokens: 1000, OutputTokens: 1000, CallCount: 2})
	if err != nil {
		t.Fatal(err)
	}
	if l.BilledNanousd != 0 || l.CallCount != 2 || l.InputTokens != 1000 {
		t.Fatalf("%+v", l)
	}
	// ledgers without a ref are independent
	for i := 0; i < 2; i++ {
		d, _ := e.svc.BuildLedger(context.Background(), LedgerInput{UserID: 1, PlatformModelName: "m", InputTokens: 1})
		if _, err := e.svc.Settle(context.Background(), &Authorization{UserID: 1, Mode: ModeSelf}, d); err != nil {
			t.Fatal(err)
		}
	}
	var n int64
	e.DB.Model(&UsageLedger{}).Count(&n)
	if n != 3 {
		t.Fatalf("ledgers %d", n)
	}
}

func TestSettlePeriodModeOverage(t *testing.T) {
	e := newEnv(t)
	e.mode("period")
	e.price(tokenModel("m"))
	free := e.planByCode("free")
	e.DB.Model(&Plan{}).Where("id = ?", free.ID).Update("period_credit_nanousd", 30_000_000_000)
	e.fund(1, 10)
	// 29 USD already used this month
	now := e.Clock.Now()
	e.DB.Create(&UsageLedger{UserID: 1, RefNo: "old", PlatformModelName: "m", BillingAt: now.Add(-time.Hour), UsageDate: DateOf(now),
		BilledNanousd: 29_000_000_000, BilledCurrency: "USD", PricingSnapshotJSON: "{}"})
	l, _, err := e.settleRun(1, "p1", LedgerInput{PlatformModelName: "m", InputTokens: 1_000_000, ReasoningTokens: 0, OutputTokens: 0, CallCount: 1, BillingAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if l.BilledNanousd != 3_000_000_000 {
		t.Fatalf("billed %d", l.BilledNanousd)
	}
	if snapNum(t, l, "period_credit_covered_nanousd") != 1e9 || snapNum(t, l, "period_overage_billed_nanousd") != 2e9 ||
		snapNum(t, l, "period_used_before_nanousd") != 29e9 || snapNum(t, l, "period_used_after_nanousd") != 32e9 ||
		snapNum(t, l, "period_credit_nanousd") != 30e9 {
		t.Fatalf("snapshot %s", l.PricingSnapshotJSON)
	}
	if e.balance(1) != 8_000_000_000 {
		t.Fatalf("balance %d", e.balance(1))
	}
	if l.BalanceAfterNanousd == nil || *l.BalanceAfterNanousd != 8_000_000_000 {
		t.Fatal("balance after")
	}
	var tx BalanceTransaction
	e.DB.Where("user_id = 1 AND type = ?", TxUsageDebit).First(&tx)
	if tx.AmountNanousd != -2_000_000_000 {
		t.Fatalf("tx %+v", tx)
	}
}

func TestSettlePeriodFullyCoveredByCredit(t *testing.T) {
	e := newEnv(t)
	e.mode("period")
	e.price(tokenModel("m"))
	l, _, err := e.settleRun(1, "p2", LedgerInput{PlatformModelName: "m", InputTokens: 100_000}) // 0.3 USD of a 1 USD credit
	if err != nil {
		t.Fatal(err)
	}
	if snapNum(t, l, "period_overage_billed_nanousd") != 0 || snapNum(t, l, "period_credit_covered_nanousd") != 300_000_000 {
		t.Fatalf("%s", l.PricingSnapshotJSON)
	}
	var n int64
	e.DB.Model(&BalanceTransaction{}).Where("type = ?", TxUsageDebit).Count(&n)
	if n != 0 {
		t.Fatal("credit usage must not create a balance transaction")
	}
	// dynamic budget: ceil(credit / 5 slots)
	var res UsageReservation
	e.DB.Where("ref_no = 'p2'").First(&res)
	if res.PeriodCreditNanousd != 200_000_000 || res.BalanceNanousd != 0 || res.PeriodLimitNanousd != 1_000_000_000 || res.Status != ResSettled {
		t.Fatalf("reservation %+v", res)
	}
}

func TestPeriodReservationSplitsCreditAndBalance(t *testing.T) {
	e := newEnv(t)
	e.mode("period")
	e.price(tokenModel("m"))
	e.setSettings(map[string]string{KeyPrepaidAmountUSD: "3"})
	e.fund(1, 5)
	a, err := e.svc.Authorize(context.Background(), 1, "m", "split")
	if err != nil {
		t.Fatal(err)
	}
	if a.Reservation.PeriodCreditNanousd != 1_000_000_000 || a.Reservation.BalanceNanousd != 2_000_000_000 {
		t.Fatalf("%+v", a.Reservation)
	}
	// the second run only sees what is left of the credit (none) and the balance
	b, err := e.svc.Authorize(context.Background(), 1, "m", "split2")
	if err != nil || b.Reservation.PeriodCreditNanousd != 0 || b.Reservation.BalanceNanousd != 3_000_000_000 {
		t.Fatalf("%+v %v", b, err)
	}
	if _, err := e.svc.Authorize(context.Background(), 1, "m", "split3"); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("third: %v", err)
	}
}

func TestSettleModeFixedAtAuthorization(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.price(tokenModel("m"))
	e.fund(1, 10)
	auth, err := e.svc.Authorize(context.Background(), 1, "m", "mm")
	if err != nil {
		t.Fatal(err)
	}
	e.mode("self") // admin flips the mode mid-run
	d, err := e.svc.BuildLedger(context.Background(), LedgerInput{UserID: 1, RefNo: "mm", Mode: auth.Mode, PlatformModelName: "m", InputTokens: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	l, err := e.svc.Settle(context.Background(), auth, d)
	if err != nil || l.BilledNanousd != 3_000_000_000 || e.balance(1) != 7_000_000_000 {
		t.Fatalf("%+v %v balance %d", l, err, e.balance(1))
	}
}

func TestSettleReservationMismatchConflicts(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.price(tokenModel("m"))
	e.fund(1, 10)
	ctx := context.Background()
	auth, _ := e.svc.Authorize(ctx, 1, "m", "x")
	if err := e.svc.ReleaseReservation(ctx, 1, "x"); err != nil {
		t.Fatal(err)
	}
	d, _ := e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, RefNo: "x", Mode: ModeUsage, PlatformModelName: "m", InputTokens: 1})
	if _, err := e.svc.Settle(ctx, auth, d); !errors.Is(err, ErrReservationConflict) {
		t.Fatalf("released reservation: %v", err)
	}
	// a reservation in reconciliation can still be settled
	auth2, _ := e.svc.Authorize(ctx, 1, "m", "y")
	_ = e.svc.MarkReconciliation(ctx, 1, "y", "boom")
	d2, _ := e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, RefNo: "y", Mode: ModeUsage, PlatformModelName: "m", InputTokens: 1})
	if _, err := e.svc.Settle(ctx, auth2, d2); err != nil {
		t.Fatalf("reconciliation settle: %v", err)
	}
}

func TestSettleWithRetryStopsOnBusinessErrors(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.price(tokenModel("m"))
	e.fund(1, 10)
	ctx := context.Background()
	auth, _ := e.svc.Authorize(ctx, 1, "m", "z")
	_ = e.svc.ReleaseReservation(ctx, 1, "z")
	d, _ := e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, RefNo: "z", Mode: ModeUsage, PlatformModelName: "m", InputTokens: 1})
	start := time.Now()
	if _, err := e.svc.SettleWithRetry(ctx, auth, d); !errors.Is(err, ErrReservationConflict) {
		t.Fatalf("%v", err)
	}
	if time.Since(start) > 90*time.Millisecond {
		t.Fatal("business errors must not back off")
	}
	// cancellation is never retried
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := e.svc.SettleWithRetry(cctx, auth, d); err == nil {
		t.Fatal("expected error")
	}
	if retryable(context.Canceled) || retryable(ErrInsufficientFunds) || retryable(ErrPricingRequired) || !retryable(errors.New("deadlock")) {
		t.Fatal("retry classification")
	}
}

func TestBuildLedgerPricingRequired(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	ctx := context.Background()
	if _, err := e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, PlatformModelName: "ghost", InputTokens: 1}); !errors.Is(err, ErrPricingRequired) {
		t.Fatalf("unpriced: %v", err)
	}
	// service-only rows (internal tasks) need no price
	if _, err := e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, PlatformModelName: "ghost", ServiceOnly: true}); err != nil {
		t.Fatalf("service only: %v", err)
	}
	// self mode needs no price either
	e.mode("self")
	if _, err := e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, PlatformModelName: "ghost", InputTokens: 1}); err != nil {
		t.Fatalf("self: %v", err)
	}
}

func TestBuildLedgerDurationRequiresBillableSeconds(t *testing.T) {
	cat := &fakeCatalog{models: map[string]ModelInfo{"vid": {Name: "vid", SupportsVideo: true}}}
	e := newEnv(t, withPorts(func(p *Ports) { p.Models = cat }))
	e.mode("usage")
	e.price(ModelPrice{PlatformModelName: "vid", PricingMode: PricingDuration, DurationNanousdPerSecond: 100_000_000})
	e.fund(1, 10)
	ctx := context.Background()
	auth, err := e.svc.Authorize(ctx, 1, "vid", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, RefNo: "v1", Mode: auth.Mode, PlatformModelName: "vid", DurationSeconds: 0, DurationBillable: true}); !errors.Is(err, ErrPricingRequired) {
		t.Fatalf("no seconds: %v", err)
	}
	if _, err := e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, RefNo: "v1", Mode: auth.Mode, PlatformModelName: "vid", DurationSeconds: 8, DurationBillable: false}); !errors.Is(err, ErrPricingRequired) {
		t.Fatalf("not billable: %v", err)
	}
	d, err := e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, RefNo: "v1", Mode: auth.Mode, PlatformModelName: "vid", DurationSeconds: 8, DurationBillable: true, MediaType: "video", InputImageCount: 1, UsageSource: "provider"})
	if err != nil || d.Row.BilledNanousd != 800_000_000 || d.Row.DurationSeconds != 8 {
		t.Fatalf("%+v %v", d, err)
	}
	if d.Snapshot["media_type"] != "video" || d.Snapshot["usage_source"] != "provider" {
		t.Fatalf("media keys %v", d.Snapshot)
	}
	// the failed build leaves the reservation to be moved to reconciliation by the caller
	if err := e.svc.MarkReconciliation(ctx, 1, "v1", "pricing_required"); err != nil {
		t.Fatal(err)
	}
}

func TestBuildLedgerNativeToolsAndMCP(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.price(tokenModel("m"))
	e.price(ModelPrice{PlatformModelName: "free", PricingMode: PricingToken, IsFree: true})
	ctx := context.Background()
	in := LedgerInput{UserID: 1, PlatformModelName: "m", ProviderProtocol: "anthropic_messages", ServerSideToolUsage: map[string]int64{"web_search": 3, "web_fetch": 2, " ": 4, "x": 0}}
	d, err := e.svc.BuildLedger(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if d.Row.BilledNanousd != 30_000_000 || d.Snapshot["native_tool_billed_nanousd"].(int64) != 30_000_000 {
		t.Fatalf("native billed %d %v", d.Row.BilledNanousd, d.Snapshot["native_tool_billed_nanousd"])
	}
	items := d.Snapshot["service_items"].([]map[string]any)
	if len(items) != 1 || items[0]["service_code"] != "native_tool.anthropic.web_search_20260209" {
		t.Fatalf("items %v", items)
	}
	if d.Snapshot["native_tool_pricing_source"] != "provider_official_defaults" {
		t.Fatalf("source %v", d.Snapshot["native_tool_pricing_source"])
	}
	// free model: native tools skipped
	in.PlatformModelName = "free"
	d, _ = e.svc.BuildLedger(ctx, in)
	if d.Row.BilledNanousd != 0 || !d.Row.IsFreeModel {
		t.Fatalf("free: %+v", d.Row)
	}
	// override price 0 disables charging
	e.setSettings(map[string]string{KeyNativeToolPricingJSON: `{"anthropic.web_search_20260209":{"priceNanousd":0,"unit":"search","priceLabel":"","billable":false}}`})
	in.PlatformModelName = "m"
	d, _ = e.svc.BuildLedger(ctx, in)
	if d.Row.BilledNanousd != 0 || d.Snapshot["native_tool_pricing_source"] != "admin_configured" {
		t.Fatalf("override zero: %d %v", d.Row.BilledNanousd, d.Snapshot["native_tool_pricing_source"])
	}
	// an override price replaces the default
	e.setSettings(map[string]string{KeyNativeToolPricingJSON: `{"anthropic.web_search_20260209":{"priceNanousd":20000000,"unit":"search","priceLabel":"","billable":true}}`})
	d, _ = e.svc.BuildLedger(ctx, in)
	if d.Row.BilledNanousd != 60_000_000 {
		t.Fatalf("override: %d", d.Row.BilledNanousd)
	}
	// disabled billing
	e.setSettings(map[string]string{KeyNativeToolBillingEnabled: "false"})
	d, _ = e.svc.BuildLedger(ctx, in)
	if d.Row.BilledNanousd != 0 {
		t.Fatalf("disabled: %d", d.Row.BilledNanousd)
	}
	// self mode never bills native tools
	e.setSettings(map[string]string{KeyNativeToolBillingEnabled: "true", KeyNativeToolPricingJSON: defaultNativePricingJSON()})
	e.mode("self")
	d, _ = e.svc.BuildLedger(ctx, in)
	if d.Row.BilledNanousd != 0 {
		t.Fatalf("self: %d", d.Row.BilledNanousd)
	}
}

func TestBuildLedgerMCPTools(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.price(ModelPrice{PlatformModelName: "free", PricingMode: PricingToken, IsFree: true})
	ctx := context.Background()
	tools := []MCPToolUsage{{ServerID: 1, ServerName: "srv", ToolName: "t", CallCount: 4, PriceNanousd: 2_000_000}, {ServerID: 2, ToolName: "zero", CallCount: 3, PriceNanousd: 0}, {ServerID: 3, ToolName: "none", CallCount: 0, PriceNanousd: 9}}
	d, err := e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, PlatformModelName: "free", MCPTools: tools})
	if err != nil {
		t.Fatal(err)
	}
	if d.Row.BilledNanousd != 8_000_000 || d.Row.IsFreeModel {
		t.Fatalf("billed %d free=%v", d.Row.BilledNanousd, d.Row.IsFreeModel)
	}
	if len(d.Snapshot["mcp_tool_usage"].([]map[string]any)) != 2 {
		t.Fatalf("mcp usage %v", d.Snapshot["mcp_tool_usage"])
	}
	items := d.Snapshot["service_items"].([]map[string]any)
	if len(items) != 1 || items[0]["service_code"] != "mcp_tool.srv.t" || items[0]["service_name"] != "srv / t" {
		t.Fatalf("items %v", items)
	}
	// self mode: listed but not billed
	e.mode("self")
	d, _ = e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, PlatformModelName: "free", MCPTools: tools})
	if d.Row.BilledNanousd != 0 || len(d.Snapshot["mcp_tool_usage"].([]map[string]any)) != 2 {
		t.Fatalf("self: %d", d.Row.BilledNanousd)
	}
}

func TestBuildLedgerPersonalModel(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	d, err := e.svc.BuildLedger(context.Background(), LedgerInput{UserID: 1, PlatformModelName: "personal:abc", InputTokens: 1_000_000, OutputTokens: 1_000_000,
		MCPTools: []MCPToolUsage{{ServerName: "s", ToolName: "t", CallCount: 1, PriceNanousd: 5}}})
	if err != nil || d.Row.BilledNanousd != 5 || !d.ServiceOnly {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestBuildLedgerServiceItems(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.price(tokenModel("helper"))
	ctx := context.Background()
	d, err := e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, PlatformModelName: "helper", ServiceOnly: true,
		ServiceItems: []ServiceItemInput{{Code: "title", Name: "title", PlatformModelName: "helper", InputTokens: 1_000_000, OutputTokens: 100_000}}})
	if err != nil {
		t.Fatal(err)
	}
	if d.Row.BilledNanousd != 4_500_000_000 || !d.ServiceOnly {
		t.Fatalf("%+v", d.Row)
	}
	// missing price for an item in a paid mode
	if _, err := e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, PlatformModelName: "helper", ServiceOnly: true,
		ServiceItems: []ServiceItemInput{{Code: "labels", PlatformModelName: "ghost", InputTokens: 1}}}); !errors.Is(err, ErrPricingRequired) {
		t.Fatalf("item without price: %v", err)
	}
	// personal items cost nothing
	d, err = e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, PlatformModelName: "helper", ServiceOnly: true,
		ServiceItems: []ServiceItemInput{{Code: "title", PlatformModelName: "personal:x", Personal: true, InputTokens: 1_000_000}}})
	if err != nil || d.Row.BilledNanousd != 0 {
		t.Fatalf("personal item: %+v %v", d, err)
	}
	// legacy internal rows are recognised by the first item code
	d, err = e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, PlatformModelName: "ghost",
		ServiceItems: []ServiceItemInput{{Code: "compact", PlatformModelName: "helper", InputTokens: 1_000_000}}})
	if err != nil || !d.ServiceOnly || d.Row.BilledNanousd != 3_000_000_000 {
		t.Fatalf("legacy: %+v %v", d, err)
	}
}

func TestBuildLedgerSnapshotKeysAndRates(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.price(tokenModel("m"))
	e.rates.percent["m"] = 150
	ctx := context.Background()
	at := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	d, err := e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, PlatformModelName: "m", ProviderProtocol: "openai_responses", UsageServiceTier: "priority",
		UpstreamModelName: "gpt-5.5-x", InputTokens: 1_000_000, BillingAt: at, UpstreamUsage: json.RawMessage(`{"a":1}`), ModelVendor: "v", ModelIcon: "i"})
	if err != nil {
		t.Fatal(err)
	}
	// 3 USD x 2.5 (gpt-5.5 priority) x 1.5 (group rate)
	if d.Row.BilledNanousd != 11_250_000_000 {
		t.Fatalf("billed %d", d.Row.BilledNanousd)
	}
	if d.Row.ServiceTier != "priority" || !d.Row.BillingAt.Equal(at) || d.Row.UsageDate != DateOf(at) {
		t.Fatalf("row %+v", d.Row)
	}
	for _, k := range []string{"platform_model_name", "billing_mode", "pricing_mode", "rate_multiplier", "input_nanousd_per_m_tokens", "base_input_nanousd_per_m_tokens",
		"tiered_pricing_json", "schedule_pricing_json", "input_billed_nanousd", "upstream_usage", "service_items", "mcp_tool_usage", "model_vendor", "model_icon",
		"billing_service_tier", "cache_write_5m_multiplier", "native_tool_pricing_source", "duration_billed_nanousd", "call_billed_nanousd", "base_service_billed_nanousd"} {
		if _, ok := d.Snapshot[k]; !ok {
			t.Errorf("snapshot key %q missing", k)
		}
	}
	// the self-mode rate multiplier ignores the group rate
	e.mode("self")
	d, _ = e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, PlatformModelName: "m", InputTokens: 1_000_000})
	if d.Row.BilledNanousd != 0 {
		t.Fatalf("self billed %d", d.Row.BilledNanousd)
	}
}

func TestBuildLedgerScheduleRate(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	mp := tokenModel("m")
	mp.SchedulePricingJSON = `{"periods":[{"name":"peak","weekdays":[1],"start":"09:00","end":"18:00","ratePercent":200}]}`
	e.price(mp)
	ctx := context.Background()
	monday := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC) // a Monday
	d, _ := e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, PlatformModelName: "m", InputTokens: 1_000_000, BillingAt: monday})
	if d.Row.BilledNanousd != 6_000_000_000 || d.Snapshot["schedule_period_name"] != "peak" {
		t.Fatalf("in period: %d %v", d.Row.BilledNanousd, d.Snapshot["schedule_period_name"])
	}
	outside := monday.Add(10 * time.Hour)
	d, _ = e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, PlatformModelName: "m", InputTokens: 1_000_000, BillingAt: outside})
	if d.Row.BilledNanousd != 3_000_000_000 {
		t.Fatalf("outside: %d", d.Row.BilledNanousd)
	}
	// an unparsable stored schedule never blocks billing
	e.DB.Model(&ModelPrice{}).Where("platform_model_name = 'm'").Update("schedule_pricing_json", "###")
	d, err := e.svc.BuildLedger(ctx, LedgerInput{UserID: 1, PlatformModelName: "m", InputTokens: 1_000_000, BillingAt: monday})
	if err != nil || d.Row.BilledNanousd != 3_000_000_000 {
		t.Fatalf("broken schedule: %v %v", d, err)
	}
}

func TestBuildLedgerTieredSnapshot(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.price(ModelPrice{PlatformModelName: "t", PricingMode: PricingTiered, TieredPricingJSON: mustJSON(map[string]any{"tiers": []map[string]any{
		{"fromTokens": 0, "upToTokens": 200000, "inputUSDPerMTokens": 2.0, "cacheReadUSDPerMTokens": 0.2, "cacheWriteUSDPerMTokens": 2.0, "outputUSDPerMTokens": 10.0},
		{"fromTokens": 200000, "upToTokens": 0, "inputUSDPerMTokens": 4.0, "cacheReadUSDPerMTokens": 0.4, "cacheWriteUSDPerMTokens": 4.0, "outputUSDPerMTokens": 20.0},
	}})})
	d, err := e.svc.BuildLedger(context.Background(), LedgerInput{UserID: 1, PlatformModelName: "t", InputTokens: 150_000, CacheReadTokens: 100_000, OutputTokens: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	// second tier: 150k x 4 + 100k x 0.4 + 10k x 20 = 600000+40000+200000 micro-usd
	if d.Row.BilledNanousd != 840_000_000 {
		t.Fatalf("billed %d", d.Row.BilledNanousd)
	}
	if d.Snapshot["tiered_from_tokens"] != int64(200000) || d.Snapshot["tiered_up_to_tokens"] != nil {
		t.Fatalf("tier bounds %v %v", d.Snapshot["tiered_from_tokens"], d.Snapshot["tiered_up_to_tokens"])
	}
	// a stored tier definition that no longer parses fails billing
	e.DB.Model(&ModelPrice{}).Where("platform_model_name = 't'").Update("tiered_pricing_json", "{bad")
	if _, err := e.svc.BuildLedger(context.Background(), LedgerInput{UserID: 1, PlatformModelName: "t", InputTokens: 1}); err == nil {
		t.Fatal("expected error for unparsable tiers")
	}
}

func TestEstimateAndEnsureBudget(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.price(tokenModel("m"))
	e.fund(1, 10)
	e.setSettings(map[string]string{KeyPrepaidAmountUSD: "1"})
	ctx := context.Background()
	auth, err := e.svc.Authorize(ctx, 1, "m", "est")
	if err != nil {
		t.Fatal(err)
	}
	est, err := e.svc.EstimateCost(ctx, auth, EstimateInput{PlatformModelName: "m", InputTokens: 100_000, MaxOutputTokens: 10_000})
	if err != nil || est != 300_000_000+150_000_000 {
		t.Fatalf("estimate %d %v", est, err)
	}
	est, _ = e.svc.EstimateCost(ctx, auth, EstimateInput{PlatformModelName: "m", InputTokens: 100_000, MaxOutputTokens: 10_000, BilledSoFar: 100})
	if est != 450_000_100 {
		t.Fatalf("cumulative %d", est)
	}
	// within the reservation: nothing changes
	if err := e.svc.EnsureBudget(ctx, auth, EstimateInput{PlatformModelName: "m", InputTokens: 100_000, MaxOutputTokens: 10_000}); err != nil {
		t.Fatal(err)
	}
	var res UsageReservation
	e.DB.Where("ref_no = 'est'").First(&res)
	if res.BalanceNanousd != 1_000_000_000 {
		t.Fatalf("unchanged expected, got %d", res.BalanceNanousd)
	}
	// above the reservation: raised
	if err := e.svc.EnsureBudget(ctx, auth, EstimateInput{PlatformModelName: "m", InputTokens: 1_000_000, MaxOutputTokens: 100_000}); err != nil {
		t.Fatal(err)
	}
	e.DB.Where("ref_no = 'est'").First(&res)
	if res.BalanceNanousd != 4_500_000_000 {
		t.Fatalf("raised to %d", res.BalanceNanousd)
	}
	// above the balance: insufficient funds before any upstream spend
	if err := e.svc.EnsureBudget(ctx, auth, EstimateInput{PlatformModelName: "m", InputTokens: 10_000_000}); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("too big: %v", err)
	}
	// free/unpriced runs estimate to zero
	e.price(ModelPrice{PlatformModelName: "free", PricingMode: PricingToken, IsFree: true})
	if est, _ := e.svc.EstimateCost(ctx, auth, EstimateInput{PlatformModelName: "free", InputTokens: 5}); est != 0 {
		t.Fatalf("free %d", est)
	}
	if est, _ := e.svc.EstimateCost(ctx, auth, EstimateInput{PlatformModelName: "ghost", InputTokens: 5}); est != 0 {
		t.Fatalf("unpriced %d", est)
	}
	// call mode
	e.price(ModelPrice{PlatformModelName: "c", PricingMode: PricingCall, CallNanousdPerCall: 7})
	if est, _ := e.svc.EstimateCost(ctx, auth, EstimateInput{PlatformModelName: "c"}); est != 7 {
		t.Fatalf("call %d", est)
	}
}

func TestMaxOutputTokensExtraction(t *testing.T) {
	cases := []struct {
		opts map[string]any
		want int64
	}{
		{map[string]any{"max_output_tokens": float64(100)}, 100},
		{map[string]any{"max_completion_tokens": 50}, 50},
		{map[string]any{"max_tokens": int64(7)}, 7},
		{map[string]any{"maxOutputTokens": float64(9)}, 9},
		{map[string]any{"generationConfig": map[string]any{"maxOutputTokens": float64(11)}}, 11},
		{map[string]any{"generation_config": map[string]any{"max_output_tokens": float64(13)}}, 13},
		{map[string]any{"max_tokens": float64(-1)}, 0},
		{map[string]any{}, 0},
		{nil, 0},
	}
	for i, c := range cases {
		if got := MaxOutputTokens(c.opts); got != c.want {
			t.Errorf("case %d: got %d want %d", i, got, c.want)
		}
	}
}
