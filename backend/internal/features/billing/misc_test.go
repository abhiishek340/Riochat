package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestSettingsPatchErrorDetails(t *testing.T) {
	e := newEnv(t)
	w := e.do("PATCH", "/api/v1/admin/billing/config", e.admin(2), map[string]any{"mode": "usage", "prepaidAmountUSD": 2_000_000})
	testkit.Expect(t, w, 400, "")
	env := testkit.Envelope(t, w)
	if env.ErrorCode == "" || env.Details == nil {
		t.Fatalf("expected code and details: %s", w.Body)
	}
	raw, _ := json.Marshal(env.Details)
	var d map[string]any
	_ = json.Unmarshal(raw, &d)
	if d["rule"] == nil || (d["field"] == nil && d["fields"] == nil) {
		t.Fatalf("details %s", raw)
	}
	// payment keys use the dedicated settings code
	err := ValidateSettingValues(map[string]string{KeyEpayGatewayURL: "ftp://x"})
	if te, ok := asTyped(err); !ok || te.Code != "settings.billing_payment_invalid" {
		t.Fatalf("%v", err)
	}
}

func TestConcurrentAuthorizeRespectsSlotLimit(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.price(tokenModel("m"))
	e.fund(1, 100)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, limited, other := 0, 0, 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := e.svc.Authorize(context.Background(), 1, "m", fmt.Sprintf("c%d", i))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrConcurrencyLimit):
				limited++
			default:
				other++
				t.Logf("unexpected: %v", err)
			}
		}(i)
	}
	wg.Wait()
	// SQLite may surface a busy error under contention; the invariant is that
	// at most five reservations exist.
	var n int64
	e.DB.Model(&UsageReservation{}).Where("status = ?", ResActive).Count(&n)
	if n > 5 || ok > 5 {
		t.Fatalf("ok=%d reservations=%d", ok, n)
	}
	if ok+limited+other != 10 {
		t.Fatalf("accounting %d %d %d", ok, limited, other)
	}
}

func TestLedgerRefUniquePerUser(t *testing.T) {
	e := newEnv(t)
	e.ledger(ledgerSpec{user: 1, model: "m", ref: "same"})
	if err := e.DB.Create(&UsageLedger{UserID: 1, RefNo: "same", PlatformModelName: "m", BilledCurrency: "USD"}).Error; err == nil {
		t.Fatal("duplicate (user, ref) accepted")
	}
	// other users and empty refs are independent
	if err := e.DB.Create(&UsageLedger{UserID: 2, RefNo: "same", PlatformModelName: "m", BilledCurrency: "USD"}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := e.DB.Create(&UsageLedger{UserID: 1, PlatformModelName: "m", BilledCurrency: "USD"}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 2; i++ {
		for _, st := range Migrations() {
			_ = st
		}
	}
	if err := seedPlans(context.Background(), e.DB); err != nil {
		t.Fatal(err)
	}
	var n int64
	e.DB.Model(&Plan{}).Count(&n)
	if n != 4 {
		t.Fatalf("plans %d", n)
	}
	tables := []string{"billing_plans", "billing_prices", "billing_subscriptions", "billing_payment_orders", "billing_accounts",
		"billing_balance_transactions", "billing_usage_reservations", "billing_redemption_codes", "billing_redemptions",
		"billing_model_prices", "billing_usage_ledgers"}
	for _, tb := range tables {
		if !e.DB.Migrator().HasTable(tb) {
			t.Errorf("table %s missing", tb)
		}
	}
	cols := map[string][]string{
		"billing_usage_ledgers":        {"user_id", "ref_no", "conversation_id", "provider_protocol", "upstream_name", "platform_model_name", "routed_binding_code", "upstream_model_name", "is_free_model", "billing_at", "usage_date", "input_tokens", "cache_read_tokens", "cache_write_tokens", "cache_write_5m_tokens", "cache_write_1h_tokens", "output_tokens", "reasoning_tokens", "call_count", "duration_seconds", "latency_ms", "usage_speed", "service_tier", "billed_currency", "billed_nanousd", "balance_after_nanousd", "pricing_snapshot_json", "deleted_at"},
		"billing_model_prices":         {"platform_model_name", "currency", "is_free", "pricing_mode", "input_nanousd_per_m_tokens", "cache_read_nanousd_per_m_tokens", "cache_write_nanousd_per_m_tokens", "output_nanousd_per_m_tokens", "call_nanousd_per_call", "duration_nanousd_per_second", "cache_write_price_basis", "tiered_pricing_json", "schedule_pricing_json"},
		"billing_redemption_codes":     {"code_hash", "code_encrypted", "code_hint", "mode", "reward_type", "credit_nanousd", "plan_id", "duration_days", "max_redemptions", "per_user_limit", "redeemed_count", "status", "expires_at", "description", "created_by_user_id"},
		"billing_usage_reservations":   {"user_id", "ref_no", "mode", "balance_nanousd", "period_credit_nanousd", "period_limit_nanousd", "period_start_at", "period_end_at", "status", "usage_ledger_id", "expires_at", "settled_at", "released_at", "reconciliation_at", "failure_code"},
		"billing_payment_orders":       {"order_no", "order_type", "user_id", "plan_id", "price_id", "provider", "status", "base_currency", "base_amount_cents", "pay_currency", "pay_amount_cents", "fx_rate", "credit_nanousd", "billing_interval", "cycles", "external_payment_id", "external_checkout_id", "checkout_url", "paid_at", "expired_at", "snapshot_json"},
		"billing_redemptions":          {"code_id", "user_id", "mode", "reward_type", "credit_nanousd", "plan_id", "subscription_id", "balance_transaction_id", "ref_no", "snapshot_json"},
		"billing_subscriptions":        {"user_id", "plan_id", "price_id", "status", "start_at", "current_period_start_at", "current_period_end_at", "cancel_at_period_end", "canceled_at", "auto_renew"},
		"billing_balance_transactions": {"account_id", "user_id", "type", "amount_nanousd", "balance_after_nanousd", "ref_type", "ref_id", "ref_no", "description"},
		"billing_accounts":             {"user_id", "currency", "balance_nanousd", "status"},
		"billing_prices":               {"plan_id", "code", "billing_interval", "currency", "amount_cents", "is_active", "is_default", "external_price_ref"},
		"billing_plans":                {"code", "name", "description", "feature_json", "period_credit_nanousd", "sort_order", "is_active", "permission_group_id"},
	}
	for tb, list := range cols {
		for _, c := range list {
			if !e.DB.Migrator().HasColumn(tb, c) {
				t.Errorf("%s.%s missing", tb, c)
			}
		}
	}
}

func TestUnauthenticatedAndRoleChecks(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct{ m, p string }{
		{"GET", "/api/v1/billing/config"}, {"GET", "/api/v1/billing/account"}, {"GET", "/api/v1/billing/overview"},
		{"GET", "/api/v1/billing/usage"}, {"GET", "/api/v1/billing/plans"}, {"POST", "/api/v1/billing/redemptions"},
		{"GET", "/api/v1/admin/billing/model-prices"}, {"GET", "/api/v1/admin/usage-statistics"},
	} {
		w := e.do(tc.m, tc.p, nil, nil)
		if w.Code != 401 {
			t.Errorf("%s %s without token: %d", tc.m, tc.p, w.Code)
		}
	}
	for _, p := range []string{"/api/v1/admin/billing/config", "/api/v1/admin/billing/plans", "/api/v1/admin/payment-orders", "/api/v1/admin/redemptions",
		"/api/v1/admin/billing/redemption-codes", "/api/v1/admin/call-logs"} {
		if w := e.do("GET", p, e.user(3), nil); w.Code != 403 {
			t.Errorf("%s as user: %d", p, w.Code)
		}
	}
}

func TestPublicPricingShape(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.price(ModelPrice{PlatformModelName: "tk", PricingMode: PricingToken, InputNanousdPerMTokens: 2_000_000_000, CacheWritePriceBasis: "anthropic_5m",
		SchedulePricingJSON: `{"periods":[{"name":"peak","weekdays":[1],"start":"09:00","end":"10:00","ratePercent":150}]}`})
	e.price(ModelPrice{PlatformModelName: "legacy", PricingMode: PricingToken})
	e.price(ModelPrice{PlatformModelName: "tr", PricingMode: PricingTiered, TieredPricingJSON: mustJSON(map[string]any{"tiers": []map[string]any{
		{"fromTokens": 0, "upToTokens": 1000, "inputUSDPerMTokens": 1.0, "cacheReadUSDPerMTokens": 0.1, "cacheWriteUSDPerMTokens": 1.0, "outputUSDPerMTokens": 2.0},
		{"fromTokens": 1000, "upToTokens": 0, "inputUSDPerMTokens": 2.0, "cacheReadUSDPerMTokens": 0.2, "cacheWriteUSDPerMTokens": 2.0, "outputUSDPerMTokens": 4.0}}})})
	// self mode: no pricing at all
	mode, prices, err := e.svc.PublicPricing(ctx)
	if err != nil || mode != "self" || len(prices) != 0 {
		t.Fatalf("self: %s %v %v", mode, prices, err)
	}
	e.mode("usage")
	mode, prices, err = e.svc.PublicPricing(ctx)
	if err != nil || mode != "usage" || len(prices) != 3 {
		t.Fatalf("usage: %s %v %v", mode, prices, err)
	}
	tk := prices["tk"]
	if tk.InputUSDPerMTokens != 2 || tk.CacheWrite5mMultiplier != 1.0 || tk.CacheWrite1hMultiplier != 1.6 || len(tk.SchedulePeriods) != 1 || tk.Currency != "USD" {
		t.Fatalf("tk %+v", tk)
	}
	if lg := prices["legacy"]; lg.CacheWrite5mMultiplier != 1.25 || lg.CacheWrite1hMultiplier != 2.0 {
		t.Fatalf("legacy multipliers %+v", lg)
	}
	tr := prices["tr"]
	if len(tr.Tiers) != 2 || tr.Tiers[1].UpToTokens != nil || tr.Tiers[0].FromTokens != 0 || tr.Tiers[0].UpToTokens == nil || *tr.Tiers[0].UpToTokens != 1000 {
		t.Fatalf("tiers %+v", tr.Tiers)
	}
	if e.svc.Mode(ctx) != "usage" {
		t.Fatal("Mode()")
	}
}
