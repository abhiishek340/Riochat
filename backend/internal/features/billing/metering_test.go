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
