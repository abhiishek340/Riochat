package billing

import (
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

const codesURL = "/api/v1/admin/billing/redemption-codes"

type codesOut struct {
	Results []CodeView `json:"results"`
}

func (e *env) createCodes(body map[string]any) (*codesOut, int, string) {
	e.t.Helper()
	w := e.do("POST", codesURL, e.admin(2), body)
	if w.Code >= 300 {
		return nil, w.Code, w.Body.String()
	}
	out := decodeData[codesOut](e.t, w)
	return &out, w.Code, ""
}

func (e *env) mustCode(body map[string]any) CodeView {
	e.t.Helper()
	out, code, msg := e.createCodes(body)
	if out == nil || len(out.Results) == 0 {
		e.t.Fatalf("create code: %d %s", code, msg)
	}
	return out.Results[0]
}

func (e *env) redeem(uid uint, code string) *httptest.ResponseRecorder {
	return e.do("POST", "/api/v1/billing/redemptions", e.user(uid), map[string]any{"code": code})
}

func TestCreateRandomCodes(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	out, status, msg := e.createCodes(map[string]any{"mode": "usage", "creditUSD": 5, "quantity": 3, "description": "  promo "})
	if out == nil || status != 200 {
		t.Fatalf("%d %s", status, msg)
	}
	if len(out.Results) != 3 {
		t.Fatalf("results %d", len(out.Results))
	}
	re := regexp.MustCompile(`^[0-9A-F]{8}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{12}$`)
	seen := map[string]bool{}
	for _, c := range out.Results {
		if !re.MatchString(c.Code) || seen[c.Code] {
			t.Fatalf("code %q", c.Code)
		}
		seen[c.Code] = true
		if c.Mode != "usage" || c.RewardType != "balance" || c.CreditNanousd != 5_000_000_000 || c.CreditUSD != 5 || c.Status != "active" ||
			c.MaxRedemptions == nil || *c.MaxRedemptions != 1 || c.PerUserLimit != 1 || c.Description != "promo" || c.CreatedByUserID != 2 ||
			c.RemainingRedemptions == nil || *c.RemainingRedemptions != 1 || c.CodeHint != c.Code[:4]+"***"+c.Code[len(c.Code)-4:] {
			t.Fatalf("code view %+v", c)
		}
	}
	// storage: hash index + ciphertext, never the plaintext
	var rows []RedemptionCode
	e.DB.Find(&rows)
	for _, r := range rows {
		if r.CodeHash == "" || r.CodeEncrypted == "" || seen[r.CodeEncrypted] || strings.Contains(r.CodeEncrypted, "-") && seen[r.CodeEncrypted] {
			t.Fatalf("row %+v", r)
		}
	}
	if _, ok := e.audit.last("create_redemption_code"); !ok {
		t.Fatal("no audit")
	}
}

func TestCreateCodeValidation(t *testing.T) {
	e := newEnv(t)
	base := func() map[string]any { return map[string]any{"mode": "usage", "creditUSD": 5} }
	check := func(name string, mut func(map[string]any), field, reason string) {
		t.Helper()
		b := base()
		mut(b)
		w := e.do("POST", codesURL, e.admin(2), b)
		testkit.Expect(t, w, 400, "billing.invalid_redemption_code")
		env := testkit.Envelope(t, w)
		raw, _ := json.Marshal(env.Details)
		var d map[string]string
		_ = json.Unmarshal(raw, &d)
		if d["field"] != field || d["reason"] != reason {
			t.Errorf("%s: details %s, want %s/%s", name, raw, field, reason)
		}
	}
	// shape limits are request.invalid_body with a named field error
	checkBody := func(name string, mut func(map[string]any), field, rule string) {
		t.Helper()
		b := base()
		mut(b)
		w := e.do("POST", codesURL, e.admin(2), b)
		testkit.Expect(t, w, 400, "request.invalid_body")
		var d struct {
			FieldErrors []struct{ Field, Rule string } `json:"fieldErrors"`
		}
		raw, _ := json.Marshal(testkit.Envelope(t, w).Details)
		_ = json.Unmarshal(raw, &d)
		if len(d.FieldErrors) != 1 || d.FieldErrors[0].Field != field || d.FieldErrors[0].Rule != rule {
			t.Errorf("%s: details %s, want %s/%s", name, raw, field, rule)
		}
	}
	checkBody("quantity 101", func(b map[string]any) { b["quantity"] = 101 }, "quantity", "max")
	checkBody("short manual", func(b map[string]any) { b["code"] = "ab" }, "code", "min")
	checkBody("long manual", func(b map[string]any) { b["code"] = strings.Repeat("A", 65) }, "code", "max")
	checkBody("max zero", func(b map[string]any) { b["maxRedemptions"] = 0 }, "maxRedemptions", "min")
	checkBody("negative credit", func(b map[string]any) { b["creditUSD"] = -1 }, "creditUSD", "gte")
	checkBody("bad mode", func(b map[string]any) { b["mode"] = "self" }, "mode", "oneof")
	checkBody("no mode", func(b map[string]any) { delete(b, "mode") }, "mode", "oneof")
	checkBody("per user 101", func(b map[string]any) { b["perUserLimit"] = 101 }, "perUserLimit", "max")
	checkBody("long description", func(b map[string]any) { b["description"] = strings.Repeat("d", 256) }, "description", "max")
	checkBody("long duration", func(b map[string]any) {
		b["mode"] = "period"
		b["planID"] = e.planByCode("pro").ID
		b["durationDays"] = 3661
	}, "durationDays", "max")
	check("manual qty", func(b map[string]any) { b["code"] = "ABC-1"; b["quantity"] = 2 }, "quantity", "manual_quantity")
	check("bad chars", func(b map[string]any) { b["code"] = "ab cd" }, "code", "code_format")
	check("limit relation", func(b map[string]any) { b["perUserLimit"] = 3; b["maxRedemptions"] = 2 }, "perUserLimit", "limit_relationship")
	check("past expiry", func(b map[string]any) { b["expiresAt"] = e.Clock.Now().Add(-time.Hour).Format(time.RFC3339) }, "expiresAt", "expires_at")
	check("no credit", func(b map[string]any) { b["creditUSD"] = 0 }, "creditUSD", "credit")
	check("period no plan", func(b map[string]any) { b["mode"] = "period" }, "planID", "plan")
	check("period free plan", func(b map[string]any) {
		b["mode"] = "period"
		b["planID"] = e.planByCode("free").ID
		b["durationDays"] = 30
	}, "planID", "plan")
	check("period unknown plan", func(b map[string]any) { b["mode"] = "period"; b["planID"] = 999; b["durationDays"] = 30 }, "planID", "plan")
	check("period no duration", func(b map[string]any) { b["mode"] = "period"; b["planID"] = e.planByCode("pro").ID }, "durationDays", "duration")
	// inactive plan
	e.DB.Model(&Plan{}).Where("code = ?", "max").Update("is_active", false)
	check("inactive plan", func(b map[string]any) {
		b["mode"] = "period"
		b["planID"] = e.planByCode("max").ID
		b["durationDays"] = 3
	}, "planID", "plan")
	var n int64
	e.DB.Model(&RedemptionCode{}).Count(&n)
	if n != 0 {
		t.Fatalf("invalid requests created %d codes", n)
	}
}

func TestManualCodeDefaultsAndConflict(t *testing.T) {
	e := newEnv(t)
	c := e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1, "code": "  welcome-2026 "})
	if c.Code != "WELCOME-2026" || c.MaxRedemptions != nil || c.RemainingRedemptions != nil {
		t.Fatalf("manual %+v", c)
	}
	if c.CodeHint != "WELC***2026" {
		t.Fatalf("hint %s", c.CodeHint)
	}
	if h := codeHint("ABCDEFGH"); h != "****" {
		t.Fatalf("short hint %s", h)
	}
	if h := codeHint("ABCDEFGHI"); h != "ABCD***FGHI" {
		t.Fatalf("hint %s", h)
	}
	_, status, _ := e.createCodes(map[string]any{"mode": "usage", "creditUSD": 1, "code": "WELCOME-2026"})
	if status != 409 {
		t.Fatalf("duplicate: %d", status)
	}
	w := e.do("POST", codesURL, e.admin(2), map[string]any{"mode": "usage", "creditUSD": 1, "code": "welcome-2026"})
	testkit.Expect(t, w, 409, "billing.redemption_code_conflict")
}

func TestRevealCode(t *testing.T) {
	e := newEnv(t)
	c := e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1})
	w := e.do("GET", codesURL+"/"+uitoa(c.ID)+"/code", e.admin(2), nil)
	testkit.Expect(t, w, 200, "")
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control %q", w.Header().Get("Cache-Control"))
	}
	got := decodeData[struct{ Code CodeView }](t, w)
	if got.Code.Code != c.Code || got.Code.ID != c.ID || got.Code.CodeHint == "" || got.Code.Mode != "usage" ||
		got.Code.Status != "active" || got.Code.CreditNanousd != 1_000_000_000 {
		t.Fatalf("reveal %+v want plaintext %s in data.code.code (%s)", got, c.Code, w.Body)
	}
	if _, ok := e.audit.last("reveal_redemption_code"); !ok {
		t.Fatal("no audit")
	}
	testkit.Expect(t, e.do("GET", codesURL+"/zero/code", e.admin(2), nil), 400, "redemption_code.invalid_id")
	testkit.Expect(t, e.do("GET", codesURL+"/999/code", e.admin(2), nil), 404, "billing.redemption_code_unavailable")
	// plaintext unavailable
	e.DB.Model(&RedemptionCode{}).Where("id = ?", c.ID).Update("code_encrypted", "")
	testkit.Expect(t, e.do("GET", codesURL+"/"+uitoa(c.ID)+"/code", e.admin(2), nil), 400, "billing.redemption_code_plaintext_unavailable")
	// deleted codes cannot be revealed
	c2 := e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1})
	testkit.Expect(t, e.do("DELETE", codesURL+"/"+uitoa(c2.ID), e.admin(2), nil), 200, "")
	testkit.Expect(t, e.do("GET", codesURL+"/"+uitoa(c2.ID)+"/code", e.admin(2), nil), 404, "billing.redemption_code_unavailable")
}

func TestRedeemBalanceCode(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.mustCode(map[string]any{"mode": "usage", "creditUSD": 7.5, "code": "Abc-123", "description": "gift"})
	w := e.redeem(9, "abc-123 ")
	testkit.Expect(t, w, 200, "")
	out := decodeData[RedeemResult](t, w)
	if out.Redemption.CreditNanousd != 7_500_000_000 || out.Redemption.RewardType != "balance" || out.Account == nil ||
		out.Account.BalanceNanousd != 7_500_000_000 || out.Subscription != nil || out.Overview == nil || out.Redemption.BalanceTransactionID == 0 {
		t.Fatalf("result %+v", out)
	}
	if len(*out.Overview) != 14 {
		t.Fatalf("redemption overview must be the full object: %v", *out.Overview)
	}
	var tx BalanceTransaction
	e.DB.First(&tx, out.Redemption.BalanceTransactionID)
	if tx.Type != "redemption" || tx.RefType != "redemption_code" || tx.Description != "gift" || tx.AmountNanousd != 7_500_000_000 ||
		!regexp.MustCompile(`^redemption_9_\d+_[0-9a-f]{8}$`).MatchString(tx.RefNo) {
		t.Fatalf("tx %+v", tx)
	}
	var code RedemptionCode
	e.DB.First(&code, out.Redemption.CodeID)
	if code.RedeemedCount != 1 {
		t.Fatalf("count %d", code.RedeemedCount)
	}
	var rec Redemption
	e.DB.First(&rec, out.Redemption.ID)
	var snap map[string]any
	_ = json.Unmarshal([]byte(rec.SnapshotJSON), &snap)
	for _, k := range []string{"code_id", "mode", "reward_type", "credit_nanousd", "plan_id", "duration_days", "description"} {
		if _, ok := snap[k]; !ok {
			t.Fatalf("snapshot %v missing %s", snap, k)
		}
	}
}

func TestRedeemDefaultDescription(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	c := e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1})
	testkit.Expect(t, e.redeem(9, c.Code), 200, "")
	var tx BalanceTransaction
	e.DB.Where("type = ?", TxRedemption).First(&tx)
	if tx.Description != "redemption credit" {
		t.Fatalf("desc %q", tx.Description)
	}
}

func TestRedeemModeCompatibility(t *testing.T) {
	e := newEnv(t)
	usage := e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1})
	period := e.mustCode(map[string]any{"mode": "period", "planID": e.planByCode("pro").ID, "durationDays": 30})
	if period.RewardType != "subscription" || period.DurationDays != 30 {
		t.Fatalf("period code %+v", period)
	}
	// self mode: nothing is redeemable
	testkit.Expect(t, e.redeem(9, usage.Code), 400, "billing.redemption_code_unavailable")
	e.mode("usage")
	testkit.Expect(t, e.redeem(9, period.Code), 400, "billing.redemption_code_unavailable")
	testkit.Expect(t, e.redeem(9, usage.Code), 200, "")
	e.mode("period")
	usage2 := e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1})
	testkit.Expect(t, e.redeem(9, usage2.Code), 200, "")
	testkit.Expect(t, e.redeem(9, period.Code), 200, "")
}

func TestRedeemSubscriptionCode(t *testing.T) {
	e := newEnv(t)
	e.mode("period")
	c := e.mustCode(map[string]any{"mode": "period", "planID": e.planByCode("max").ID, "durationDays": 10})
	w := e.redeem(9, c.Code)
	testkit.Expect(t, w, 200, "")
	out := decodeData[RedeemResult](t, w)
	if out.Subscription == nil || out.Account != nil || out.Redemption.SubscriptionID != out.Subscription.ID || out.Redemption.PlanID != e.planByCode("max").ID {
		t.Fatalf("result %+v", out)
	}
	want := e.Clock.Now().Add(10 * 24 * time.Hour)
	if out.Subscription.CurrentPeriodEndAt == nil || !out.Subscription.CurrentPeriodEndAt.Equal(want) {
		t.Fatalf("end %v want %v", out.Subscription.CurrentPeriodEndAt, want)
	}
	snap, _ := e.svc.CurrentSubscription(t.Context(), 9)
	if snap.Tier != "max" {
		t.Fatalf("tier %s", snap.Tier)
	}
	// a lower plan code afterwards is queued behind the higher one
	c2 := e.mustCode(map[string]any{"mode": "period", "planID": e.planByCode("pro").ID, "durationDays": 5})
	testkit.Expect(t, e.redeem(9, c2.Code), 200, "")
	var proSub Subscription
	e.DB.Where("user_id = 9 AND plan_id = ?", e.planByCode("pro").ID).First(&proSub)
	if !proSub.CurrentPeriodStartAt.Equal(want) || !proSub.CurrentPeriodEndAt.Equal(want.Add(5*24*time.Hour)) {
		t.Fatalf("queued %+v", proSub)
	}
	// the overview of a period-mode redemption lists both entitlements
	ov := decodeData[RedeemResult](t, e.redeem(9, e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1}).Code)).Overview
	if ents, _ := (*ov)["subscriptionEntitlements"].([]any); len(ents) != 2 {
		t.Fatalf("entitlements %v", (*ov)["subscriptionEntitlements"])
	}
}
