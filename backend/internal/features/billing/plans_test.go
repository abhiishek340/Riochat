package billing

import (
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestSeedPlansAndPrices(t *testing.T) {
	e := newEnv(t)
	var plans []Plan
	e.DB.Order("sort_order").Find(&plans)
	if len(plans) != 4 {
		t.Fatalf("plans %d", len(plans))
	}
	want := []struct {
		code   string
		credit int64
		sort   int
	}{{"free", 1_000_000_000, 10}, {"pro", 30_000_000_000, 20}, {"max", 75_000_000_000, 30}, {"ultra", 300_000_000_000, 40}}
	for i, w := range want {
		if plans[i].Code != w.code || plans[i].PeriodCreditNanousd != w.credit || plans[i].SortOrder != w.sort || !plans[i].IsActive {
			t.Fatalf("plan %d: %+v", i, plans[i])
		}
	}
	var prices []Price
	e.DB.Order("id").Find(&prices)
	if len(prices) != 4 || prices[0].AmountCents != 0 || prices[0].BillingInterval != "lifetime" ||
		prices[1].AmountCents != 2000 || prices[2].AmountCents != 5000 || prices[3].AmountCents != 20000 {
		t.Fatalf("prices %+v", prices)
	}
	// the seed binds the default group at start-up
	for _, p := range plans {
		if p.PermissionGroupID == nil || *p.PermissionGroupID != 1 {
			t.Fatalf("plan %s group %v", p.Code, p.PermissionGroupID)
		}
	}
	// the seed is idempotent and never resurrects deleted rows
	e.DB.Where("code = ?", "ultra").Delete(&Plan{})
	for _, st := range Migrations() {
		_ = st
	}
	if err := seedPlans(t.Context(), e.DB); err != nil {
		t.Fatal(err)
	}
	var n int64
	e.DB.Model(&Plan{}).Count(&n)
	if n != 3 {
		t.Fatalf("seed re-ran: %d", n)
	}
}

func TestListPlans(t *testing.T) {
	e := newEnv(t)
	w := e.do("GET", "/api/v1/billing/plans", e.user(1), nil)
	testkit.Expect(t, w, 200, "")
	plans := decodeData[[]PlanView](t, w)
	if len(plans) != 4 || plans[0].Code != "free" || plans[3].Code != "ultra" {
		t.Fatalf("%+v", plans)
	}
	if plans[1].PeriodCreditUSD != 30 || len(plans[1].Prices) != 1 || plans[1].Prices[0].AmountCents != 2000 || !plans[1].Prices[0].IsDefault {
		t.Fatalf("pro %+v", plans[1])
	}
	// inactive plans and prices are hidden; extra prices are ordered by amount
	e.DB.Model(&Plan{}).Where("code = ?", "max").Update("is_active", false)
	p := e.planByCode("pro")
	e.DB.Create(&Price{PlanID: p.ID, Code: "pro-yearly", BillingInterval: "year", Currency: "USD", AmountCents: 1000, IsActive: true})
	e.DB.Create(&Price{PlanID: p.ID, Code: "pro-hidden", BillingInterval: "year", Currency: "USD", AmountCents: 1, IsActive: false})
	w = e.do("GET", "/api/v1/admin/billing/plans", e.admin(2), nil)
	plans = decodeData[[]PlanView](t, w)
	if len(plans) != 3 {
		t.Fatalf("active plans %d", len(plans))
	}
	if len(plans[1].Prices) != 2 || plans[1].Prices[0].Code != "pro-yearly" {
		t.Fatalf("prices %+v", plans[1].Prices)
	}
}

func TestSubscribeFreePlan(t *testing.T) {
	e := newEnv(t)
	free := e.priceID("free-default")
	w := e.do("POST", "/api/v1/billing/subscriptions", e.user(5), map[string]any{"priceID": free})
	testkit.Expect(t, w, 200, "")
	out := decodeData[struct {
		Subscription SubscriptionView `json:"subscription"`
	}](t, w)
	s := out.Subscription
	if s.UserID != 5 || s.Status != "active" || s.CurrentPeriodEndAt != nil || s.AutoRenew {
		t.Fatalf("sub %+v", s)
	}
	if _, ok := e.audit.last("subscribe"); !ok {
		t.Fatal("no audit")
	}
	// subscribing again replaces the previous row
	testkit.Expect(t, e.do("POST", "/api/v1/billing/subscriptions", e.user(5), map[string]any{"priceID": free}), 200, "")
	var active, expired int64
	e.DB.Model(&Subscription{}).Where("user_id = 5 AND status = 'active'").Count(&active)
	e.DB.Model(&Subscription{}).Where("user_id = 5 AND status = 'expired'").Count(&expired)
	if active != 1 || expired != 1 {
		t.Fatalf("active %d expired %d", active, expired)
	}
}

func TestSubscribeErrors(t *testing.T) {
	e := newEnv(t)
	testkit.Expect(t, e.do("POST", "/api/v1/billing/subscriptions", e.user(5), map[string]any{"priceID": e.priceID("pro-monthly")}), 400, "billing.payment_required")
	testkit.Expect(t, e.do("POST", "/api/v1/billing/subscriptions", e.user(5), map[string]any{"priceID": 9999}), 404, "billing.plan_not_found")
	testkit.Expect(t, e.do("POST", "/api/v1/billing/subscriptions", e.user(5), map[string]any{}), 400, "request.invalid_body")
	testkit.Expect(t, e.do("POST", "/api/v1/billing/subscriptions", e.user(5), map[string]any{"priceID": 1, "cycles": 121}), 400, "request.invalid_body")
	// inactive price
	free := e.priceID("free-default")
	e.DB.Model(&Price{}).Where("id = ?", free).Update("is_active", false)
	testkit.Expect(t, e.do("POST", "/api/v1/billing/subscriptions", e.user(5), map[string]any{"priceID": free}), 404, "billing.plan_not_found")
	e.DB.Model(&Price{}).Where("id = ?", free).Update("is_active", true)
	// free while holding a paid entitlement
	now := e.Clock.Now()
	end := now.Add(10 * 24 * time.Hour)
	e.grantSub(5, "pro", now.Add(-time.Hour), &end)
	testkit.Expect(t, e.do("POST", "/api/v1/billing/subscriptions", e.user(5), map[string]any{"priceID": free}), 400, "billing.subscription_entitlement_active")
	// an expired paid entitlement does not block
	e.Clock.Advance(11 * 24 * time.Hour)
	testkit.Expect(t, e.do("POST", "/api/v1/billing/subscriptions", e.user(5), map[string]any{"priceID": free}), 200, "")
}

func TestSubscribeZeroPricedMonthlyHasEnd(t *testing.T) {
	e := newEnv(t)
	p := e.planByCode("pro")
	price := Price{PlanID: p.ID, Code: "pro-trial", BillingInterval: "month", Currency: "USD", AmountCents: 0, IsActive: true}
	e.DB.Create(&price)
	w := e.do("POST", "/api/v1/billing/subscriptions", e.user(7), map[string]any{"priceID": price.ID, "cycles": 3})
	testkit.Expect(t, w, 200, "")
	s := decodeData[struct {
		Subscription SubscriptionView `json:"subscription"`
	}](t, w).Subscription
	want := e.Clock.Now().AddDate(0, 3, 0)
	if s.CurrentPeriodEndAt == nil || !s.CurrentPeriodEndAt.Equal(want) || !s.AutoRenew {
		t.Fatalf("sub %+v want end %v", s, want)
	}
}

func TestUpdatePlan(t *testing.T) {
	e := newEnv(t)
	pro := e.planByCode("pro")
	url := "/api/v1/admin/billing/plans/"
	body := map[string]any{"name": "Pro+", "description": "  desc  ", "periodCreditUSD": 40, "amountUSD": 25.5, "billingInterval": "year", "currency": "CNY"}
	w := e.do("PATCH", url+"abc", e.admin(2), body)
	testkit.Expect(t, w, 400, "plan.invalid_id")
	testkit.Expect(t, e.do("PATCH", url+"0", e.admin(2), body), 400, "plan.invalid_id")
	testkit.Expect(t, e.do("PATCH", url+"999", e.admin(2), body), 404, "billing.plan_not_found")
	long := map[string]any{"name": "Pro+", "description": "d", "periodCreditUSD": 40, "amountUSD": 25.5, "billingInterval": "year", "currency": "ABCDEFGHIJKLMNOPQ"}
	testkit.Expect(t, e.do("PATCH", url+uitoa(pro.ID), e.admin(2), long), 400, "request.invalid_body")
	w = e.do("PATCH", url+uitoa(pro.ID), e.admin(2), body)
	testkit.Expect(t, w, 200, "")
	pv := decodeData[struct {
		Plan PlanView `json:"plan"`
	}](t, w).Plan
	if pv.Name != "Pro+" || pv.Description != "desc" || pv.PeriodCreditNanousd != 40_000_000_000 || pv.Code != "pro" || pv.SortOrder != 20 || !pv.IsActive {
		t.Fatalf("plan %+v", pv)
	}
	// active prices are ordered by amount; the old monthly price stays but loses the default flag
	if len(pv.Prices) != 2 || pv.Prices[0].Code != "pro-monthly" || pv.Prices[0].IsDefault {
		t.Fatalf("prices %+v", pv.Prices)
	}
	if d := pv.Prices[1]; d.Code != "pro-default" || d.AmountCents != 2550 || d.BillingInterval != "year" || d.Currency != "USD" || !d.IsDefault {
		t.Fatalf("default price %+v", d)
	}
	// the seeded pro-monthly price is retained but no longer default
	var old Price
	e.DB.Where("code = ?", "pro-monthly").First(&old)
	if old.IsDefault {
		t.Fatal("old price still default")
	}
	if pv.PermissionGroupID == nil || *pv.PermissionGroupID != 1 {
		t.Fatalf("group %v", pv.PermissionGroupID)
	}
	if _, ok := e.audit.last("update_billing_plan"); !ok {
		t.Fatal("no audit")
	}
	// explicit group
	body["permissionGroupID"] = 2
	w = e.do("PATCH", url+uitoa(pro.ID), e.admin(2), body)
	pv = decodeData[struct {
		Plan PlanView `json:"plan"`
	}](t, w).Plan
	if pv.PermissionGroupID == nil || *pv.PermissionGroupID != 2 {
		t.Fatalf("group %v", pv.PermissionGroupID)
	}
	// unknown group and zero group
	body["permissionGroupID"] = 77
	testkit.Expect(t, e.do("PATCH", url+uitoa(pro.ID), e.admin(2), body), 400, "billing.invalid_permission_group")
	body["permissionGroupID"] = 0
	testkit.Expect(t, e.do("PATCH", url+uitoa(pro.ID), e.admin(2), body), 400, "billing.invalid_permission_group")
	// body validation
	for _, mut := range []func(map[string]any){
		func(b map[string]any) { delete(b, "name") },
		func(b map[string]any) { delete(b, "description") },
		func(b map[string]any) { b["amountUSD"] = -1 },
		func(b map[string]any) { b["billingInterval"] = "week" },
		func(b map[string]any) { delete(b, "periodCreditUSD") },
	} {
		b := map[string]any{"name": "N", "description": "", "periodCreditUSD": 1, "amountUSD": 1, "billingInterval": "month"}
		mut(b)
		testkit.Expect(t, e.do("PATCH", url+uitoa(pro.ID), e.admin(2), b), 400, "request.invalid_body")
	}
	// empty description is allowed
	b := map[string]any{"name": "N", "description": "", "periodCreditUSD": 1, "amountUSD": 0, "billingInterval": "lifetime"}
	testkit.Expect(t, e.do("PATCH", url+uitoa(pro.ID), e.admin(2), b), 200, "")
}

func TestPlansReferencingGroup(t *testing.T) {
	e := newEnv(t)
	n, err := e.svc.PlansReferencingGroup(t.Context(), 1)
	if err != nil || n != 4 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	n, _ = e.svc.PlansReferencingGroup(t.Context(), 2)
	if n != 0 {
		t.Fatalf("n=%d", n)
	}
}

func TestAssignUserPlan(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	exp := e.Clock.Now().Add(48 * time.Hour)
	if err := e.svc.AssignUserPlan(ctx, 3, "pro", &exp); err == nil || !isCode(err, ErrPaymentRequired) {
		t.Fatalf("non period mode: %v", err)
	}
	e.mode("period")
	if err := e.svc.AssignUserPlan(ctx, 3, "nope", &exp); !isCode(err, ErrInvalidSubscriptionTier) {
		t.Fatalf("unknown: %v", err)
	}
	if err := e.svc.AssignUserPlan(ctx, 3, "pro", nil); !isCode(err, ErrSubscriptionExpiryReq) {
		t.Fatalf("missing expiry: %v", err)
	}
	past := e.Clock.Now()
	if err := e.svc.AssignUserPlan(ctx, 3, "pro", &past); !isCode(err, ErrInvalidSubscriptionExp) {
		t.Fatalf("past expiry: %v", err)
	}
	if err := e.svc.AssignUserPlan(ctx, 3, "pro", &exp); err != nil {
		t.Fatal(err)
	}
	snap, err := e.svc.CurrentSubscription(ctx, 3)
	if err != nil || snap.Tier != "pro" || snap.ExpiresAt == nil || !snap.ExpiresAt.Equal(exp) {
		t.Fatalf("%+v %v", snap, err)
	}
	// empty code means the free plan, replacing the paid one atomically
	if err := e.svc.AssignUserPlan(ctx, 3, "", nil); err != nil {
		t.Fatal(err)
	}
	snap, _ = e.svc.CurrentSubscription(ctx, 3)
	if snap.Tier != "free" || snap.Status != "free" || snap.ExpiresAt != nil {
		t.Fatalf("%+v", snap)
	}
	var active int64
	e.DB.Model(&Subscription{}).Where("user_id = 3 AND status = 'active'").Count(&active)
	if active != 1 {
		t.Fatalf("active %d", active)
	}
}

func TestCurrentSubscriptionRanking(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	now := e.Clock.Now()
	// no subscription: free snapshot
	snap, err := e.svc.CurrentSubscription(ctx, 9)
	if err != nil || snap.Tier != "free" || snap.Status != "free" {
		t.Fatalf("%+v %v", snap, err)
	}
	e1, e2 := now.Add(10*24*time.Hour), now.Add(20*24*time.Hour)
	e.grantSub(9, "pro", now.Add(-time.Hour), &e1)
	e.grantSub(9, "max", now.Add(-time.Hour), &e2)
	snap, _ = e.svc.CurrentSubscription(ctx, 9)
	if snap.Tier != "max" || snap.PlanName != "Max" {
		t.Fatalf("higher rank wins: %+v", snap)
	}
	// an inactive plan is ignored
	e.DB.Model(&Plan{}).Where("code = ?", "max").Update("is_active", false)
	snap, _ = e.svc.CurrentSubscription(ctx, 9)
	if snap.Tier != "pro" {
		t.Fatalf("inactive plan: %+v", snap)
	}
	// not yet started / already ended are ignored
	many, err := e.svc.CurrentSubscriptions(ctx, []uint{9, 10})
	if err != nil || many[9].Tier != "pro" || many[10].Tier != "free" {
		t.Fatalf("%+v %v", many, err)
	}
	e.Clock.Advance(15 * 24 * time.Hour)
	snap, _ = e.svc.CurrentSubscription(ctx, 9)
	if snap.Tier != "free" {
		t.Fatalf("ended: %+v", snap)
	}
}

func TestChainExpiryMergesContiguousSegments(t *testing.T) {
	e := newEnv(t)
	now := e.Clock.Now()
	a := now.Add(10 * 24 * time.Hour)
	b := now.Add(20 * 24 * time.Hour)
	e.grantSub(4, "pro", now.Add(-time.Hour), &a)
	e.grantSub(4, "pro", a, &b)
	snap, _ := e.svc.CurrentSubscription(t.Context(), 4)
	if snap.ExpiresAt == nil || !snap.ExpiresAt.Equal(b) {
		t.Fatalf("chain expiry %v want %v", snap.ExpiresAt, b)
	}
}

func TestAccountEndpointAndSnapshots(t *testing.T) {
	e := newEnv(t)
	w := e.do("GET", "/api/v1/billing/account", e.user(11), nil)
	testkit.Expect(t, w, 200, "")
	acc := decodeData[struct {
		Account AccountView `json:"account"`
	}](t, w).Account
	if acc.UserID != 11 || acc.Currency != "USD" || acc.Status != "active" || acc.BalanceNanousd != 0 {
		t.Fatalf("%+v", acc)
	}
	var n int64
	e.DB.Model(&Account{}).Where("user_id = 11").Count(&n)
	if n != 1 {
		t.Fatal("account not created")
	}
	e.fund(12, 3)
	snaps, err := e.svc.AccountSnapshots(t.Context(), []uint{11, 12, 13})
	if err != nil || snaps[12].BalanceNanousd != 3_000_000_000 || snaps[13].Currency != "USD" || snaps[13].Status != "active" {
		t.Fatalf("%+v %v", snaps, err)
	}
}

func TestAdminSetBalance(t *testing.T) {
	e := newEnv(t)
	url := "/api/v1/admin/billing/accounts/21/balance"
	testkit.Expect(t, e.do("PATCH", url, e.admin(2), map[string]any{"balanceUSD": 5}), 400, "billing.payment_required")
	e.mode("usage")
	testkit.Expect(t, e.do("PATCH", "/api/v1/admin/billing/accounts/x/balance", e.admin(2), map[string]any{"balanceUSD": 5}), 400, "user.invalid_id")
	testkit.Expect(t, e.do("PATCH", url, e.admin(2), map[string]any{"balanceUSD": -1}), 400, "request.invalid_body")
	testkit.Expect(t, e.do("PATCH", url, e.admin(2), map[string]any{}), 400, "request.invalid_body")
	w := e.do("PATCH", url, e.admin(2), map[string]any{"balanceUSD": 5})
	testkit.Expect(t, w, 200, "")
	w = e.do("PATCH", url, e.admin(2), map[string]any{"balanceUSD": 12.5, "description": "manual"})
	testkit.Expect(t, w, 200, "")
	acc := decodeData[struct {
		Account AccountView `json:"account"`
	}](t, w).Account
	if acc.BalanceNanousd != 12_500_000_000 {
		t.Fatalf("%+v", acc)
	}
	var txs []BalanceTransaction
	e.DB.Where("user_id = 21").Order("id").Find(&txs)
	if len(txs) != 2 || txs[1].Type != "admin_set" || txs[1].AmountNanousd != 7_500_000_000 || txs[1].RefType != "admin" ||
		txs[1].Description != "manual" || txs[1].BalanceAfterNanousd != 12_500_000_000 {
		t.Fatalf("txs %+v", txs)
	}
	if txs[0].Description == "" {
		t.Fatal("default description missing")
	}
	// same value: no new transaction
	testkit.Expect(t, e.do("PATCH", url, e.admin(2), map[string]any{"balanceUSD": 12.5}), 200, "")
	var n int64
	e.DB.Model(&BalanceTransaction{}).Where("user_id = 21").Count(&n)
	if n != 2 {
		t.Fatalf("tx count %d", n)
	}
	if _, ok := e.audit.last("update_billing_balance"); !ok {
		t.Fatal("no audit")
	}
	// period mode is allowed as well
	e.mode("period")
	testkit.Expect(t, e.do("PATCH", url, e.admin(2), map[string]any{"balanceUSD": 1}), 200, "")
	e.mode("self")
	testkit.Expect(t, e.do("PATCH", url, e.admin(2), map[string]any{"balanceUSD": 1}), 400, "billing.payment_required")
}

func TestOverviewByMode(t *testing.T) {
	e := newEnv(t)
	get := func() map[string]any {
		w := e.do("GET", "/api/v1/billing/overview", e.user(31), nil)
		testkit.Expect(t, w, 200, "")
		return decodeData[struct {
			Overview map[string]any `json:"overview"`
		}](t, w).Overview
	}
	ov := get()
	if ov["mode"] != "self" || len(ov) != 14 {
		t.Fatalf("self: %v", ov)
	}
	for _, k := range []string{"plan", "periodStartAt", "periodEndAt", "account"} {
		if v, ok := ov[k]; !ok || v != nil {
			t.Fatalf("self %s should be null: %v", k, ov)
		}
	}
	for _, k := range []string{"periodCreditUSD", "periodCreditNanousd", "periodUsedUSD", "periodUsedNanousd",
		"periodRemainingUSD", "periodRemainingNanousd", "totalSpentUSD", "totalSpentNanousd"} {
		if v, ok := ov[k].(float64); !ok || v != 0 {
			t.Fatalf("self %s should be 0: %v", k, ov)
		}
	}
	if ent, ok := ov["subscriptionEntitlements"].([]any); !ok || len(ent) != 0 {
		t.Fatalf("self entitlements should be []: %v", ov)
	}
	e.mode("usage")
	e.fund(31, 4)
	ov = get()
	if ov["account"] == nil || ov["totalSpentNanousd"] == nil || ov["plan"] != nil {
		t.Fatalf("usage: %v", ov)
	}
	e.mode("period")
	ov = get()
	if ov["plan"] == nil || ov["account"] == nil || ov["periodCreditNanousd"].(float64) != 1e9 || ov["periodRemainingNanousd"].(float64) != 1e9 {
		t.Fatalf("period: %v", ov)
	}
	if ov["periodStartAt"] == nil || ov["periodEndAt"] == nil {
		t.Fatalf("window missing: %v", ov)
	}
}

func TestEntitlementQueueInOverview(t *testing.T) {
	e := newEnv(t)
	e.mode("period")
	now := e.Clock.Now()
	a, b := now.Add(10*24*time.Hour), now.Add(40*24*time.Hour)
	e.grantSub(41, "pro", now.Add(-time.Hour), &a)
	e.grantSub(41, "pro", a, &b)
	e.grantSub(41, "max", b, ptrTime(b.Add(24*time.Hour)))
	e.grantSub(41, "free", now.Add(-time.Hour), nil)
	w := e.do("GET", "/api/v1/billing/overview", e.user(41), nil)
	ov := decodeData[struct {
		Overview struct {
			Entitlements []struct {
				PlanID    uint `json:"planID"`
				IsCurrent bool `json:"isCurrent"`
				Plan      struct {
					Code string `json:"code"`
				} `json:"plan"`
				CurrentPeriodEndAt *time.Time `json:"currentPeriodEndAt"`
			} `json:"subscriptionEntitlements"`
		} `json:"overview"`
	}](t, w).Overview
	ents := ov.Entitlements
	if len(ents) != 2 {
		t.Fatalf("entitlements %+v", ents)
	}
	if ents[0].Plan.Code != "pro" || !ents[0].IsCurrent || ents[0].CurrentPeriodEndAt == nil || !ents[0].CurrentPeriodEndAt.Equal(b) {
		t.Fatalf("first %+v", ents[0])
	}
	if ents[1].Plan.Code != "max" || ents[1].IsCurrent {
		t.Fatalf("second %+v", ents[1])
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
