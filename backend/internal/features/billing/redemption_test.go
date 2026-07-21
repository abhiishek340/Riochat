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

func TestRedeemLimits(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	// max 1: second user gets exhausted
	c := e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1})
	testkit.Expect(t, e.redeem(1, c.Code), 200, "")
	testkit.Expect(t, e.redeem(2, c.Code), 400, "billing.redemption_code_exhausted")
	// per-user limit 1 with a bigger total: the same user is stopped
	c = e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1, "maxRedemptions": 5})
	testkit.Expect(t, e.redeem(1, c.Code), 200, "")
	testkit.Expect(t, e.redeem(1, c.Code), 400, "billing.redemption_user_limit_exceeded")
	testkit.Expect(t, e.redeem(2, c.Code), 200, "")
	// per-user limit 2
	c = e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1, "maxRedemptions": 5, "perUserLimit": 2})
	testkit.Expect(t, e.redeem(3, c.Code), 200, "")
	testkit.Expect(t, e.redeem(3, c.Code), 200, "")
	testkit.Expect(t, e.redeem(3, c.Code), 400, "billing.redemption_user_limit_exceeded")
	// expired
	exp := e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1, "expiresAt": e.Clock.Now().Add(time.Hour).Format(time.RFC3339)})
	e.Clock.Advance(2 * time.Hour)
	testkit.Expect(t, e.redeem(4, exp.Code), 400, "billing.redemption_code_unavailable")
	// inactive
	in := e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1})
	testkit.Expect(t, e.do("PATCH", codesURL+"/"+uitoa(in.ID), e.admin(2), map[string]any{"status": "inactive"}), 200, "")
	testkit.Expect(t, e.redeem(4, in.Code), 400, "billing.redemption_code_unavailable")
	// unknown and malformed
	testkit.Expect(t, e.redeem(4, "NOSUCH-CODE"), 400, "billing.redemption_code_unavailable")
	testkit.Expect(t, e.redeem(4, "bad code!"), 400, "billing.invalid_redemption_code")
	testkit.Expect(t, e.do("POST", "/api/v1/billing/redemptions", e.user(4), map[string]any{"code": "ab"}), 400, "request.invalid_body")
	testkit.Expect(t, e.do("POST", "/api/v1/billing/redemptions", e.user(4), map[string]any{}), 400, "request.invalid_body")
}

func TestRedeemConcurrentRespectsMax(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	c := e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1, "maxRedemptions": 3, "perUserLimit": 1})
	done := make(chan error, 8)
	for i := 1; i <= 8; i++ {
		go func(uid uint) {
			_, err := e.svc.Redeem(t.Context(), uid, c.Code)
			done <- err
		}(uint(i))
	}
	okCount := 0
	for i := 0; i < 8; i++ {
		if err := <-done; err == nil {
			okCount++
		}
	}
	var code RedemptionCode
	e.DB.First(&code, c.ID)
	if okCount != 3 || code.RedeemedCount != 3 {
		t.Fatalf("ok=%d redeemed=%d", okCount, code.RedeemedCount)
	}
}

func TestRedeemWithRotatedKey(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	c := e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1, "code": "ROTATE-ME"})
	hashes := e.svc.secrets.LookupHashes("ROTATE-ME")
	if len(hashes) == 0 || hashes[0] == "" {
		t.Fatal("no lookup hashes")
	}
	testkit.Expect(t, e.redeem(1, c.Code), 200, "")
}

func TestUpdateCode(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	c := e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1, "maxRedemptions": 3, "perUserLimit": 2, "description": "old"})
	url := codesURL + "/" + uitoa(c.ID)
	patch := func(b map[string]any) (int, CodeView) {
		w := e.do("PATCH", url, e.admin(2), b)
		var wrap struct{ Code CodeView }
		if w.Code == 200 {
			testkit.Decode(t, w, &wrap)
		}
		return w.Code, wrap.Code
	}
	testkit.Expect(t, e.redeem(1, c.Code), 200, "")
	testkit.Expect(t, e.redeem(1, c.Code), 200, "")
	// below the redeemed count
	w := e.do("PATCH", url, e.admin(2), map[string]any{"maxRedemptions": 1})
	testkit.Expect(t, w, 400, "billing.invalid_redemption_code")
	// per-user limit above the total
	testkit.Expect(t, e.do("PATCH", url, e.admin(2), map[string]any{"perUserLimit": 5}), 400, "billing.invalid_redemption_code")
	testkit.Expect(t, e.do("PATCH", url, e.admin(2), map[string]any{"status": "deleted"}), 400, "request.invalid_body")
	testkit.Expect(t, e.do("PATCH", url, e.admin(2), map[string]any{"perUserLimit": 0}), 400, "request.invalid_body")
	testkit.Expect(t, e.do("PATCH", url, e.admin(2), map[string]any{"perUserLimit": 101}), 400, "request.invalid_body")
	testkit.Expect(t, e.do("PATCH", url, e.admin(2), map[string]any{"description": strings.Repeat("d", 256)}), 400, "request.invalid_body")
	testkit.Expect(t, e.do("PATCH", url, e.admin(2), map[string]any{"expiresAt": "tomorrow"}), 400, "request.invalid_body")
	testkit.Expect(t, e.do("PATCH", url, e.admin(2), map[string]any{"expiresAt": e.Clock.Now().Add(-time.Hour).Format(time.RFC3339)}), 400, "billing.invalid_redemption_code")
	testkit.Expect(t, e.do("PATCH", url, e.admin(2), "{not json"), 400, "request.invalid_body")
	// null removes the cap, absent leaves it unchanged
	code, v := patch(map[string]any{"description": " new "})
	if code != 200 || v.MaxRedemptions == nil || *v.MaxRedemptions != 3 || v.Description != "new" || v.PerUserLimit != 2 {
		t.Fatalf("absent: %d %+v", code, v)
	}
	code, v = patch(map[string]any{"maxRedemptions": nil})
	if code != 200 || v.MaxRedemptions != nil || v.RemainingRedemptions != nil {
		t.Fatalf("null: %d %+v", code, v)
	}
	code, v = patch(map[string]any{"expiresAt": e.Clock.Now().Add(time.Hour).Format(time.RFC3339), "perUserLimit": 4})
	if code != 200 || v.ExpiresAt == nil || v.PerUserLimit != 4 {
		t.Fatalf("expiry: %d %+v", code, v)
	}
	code, v = patch(map[string]any{"expiresAt": nil, "status": "inactive"})
	if code != 200 || v.ExpiresAt != nil || v.Status != "inactive" {
		t.Fatalf("clear expiry: %d %+v", code, v)
	}
	// the reward cannot be edited
	code, v = patch(map[string]any{"creditUSD": 99, "mode": "period"})
	if code != 200 || v.CreditNanousd != 1_000_000_000 || v.Mode != "usage" {
		t.Fatalf("reward edit: %d %+v", code, v)
	}
	if _, ok := e.audit.last("update_redemption_code"); !ok {
		t.Fatal("no audit")
	}
	testkit.Expect(t, e.do("PATCH", codesURL+"/999", e.admin(2), map[string]any{"status": "active"}), 404, "billing.redemption_code_unavailable")
	testkit.Expect(t, e.do("PATCH", codesURL+"/abc", e.admin(2), map[string]any{}), 400, "redemption_code.invalid_id")
}

func TestDeleteAndBatchDeleteCodes(t *testing.T) {
	e := newEnv(t)
	a := e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1})
	b := e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1})
	w := e.do("DELETE", codesURL+"/"+uitoa(a.ID), e.admin(2), nil)
	testkit.Expect(t, w, 200, "")
	if decodeData[map[string]any](t, w)["deleted"] != true {
		t.Fatalf("body %s", w.Body)
	}
	testkit.Expect(t, e.do("DELETE", codesURL+"/"+uitoa(a.ID), e.admin(2), nil), 404, "billing.redemption_code_unavailable")
	w = e.do("POST", codesURL+"/batch-delete", e.admin(2), map[string]any{"ids": []uint{b.ID, 12345}})
	testkit.Expect(t, w, 200, "")
	res := decodeData[struct {
		Total, SuccessCount, NotFoundCount, FailedCount int
		Results                                         []BatchDeleteResult
	}](t, w)
	if res.Total != 2 || res.SuccessCount != 1 || res.NotFoundCount != 1 || res.FailedCount != 0 ||
		res.Results[0].Status != "deleted" || res.Results[1].Status != "not_found" {
		t.Fatalf("batch %+v (%s)", res, w.Body)
	}
	if _, ok := e.audit.last("batch_delete_redemption_codes"); !ok {
		t.Fatal("no audit")
	}
	testkit.Expect(t, e.do("POST", codesURL+"/batch-delete", e.admin(2), map[string]any{"ids": []uint{}}), 400, "request.invalid_body")
	testkit.Expect(t, e.do("POST", codesURL+"/batch-delete", e.admin(2), map[string]any{"ids": []uint{0}}), 400, "request.invalid_body")
	// deleted codes cannot be redeemed and disappear from the list
	e.mode("usage")
	testkit.Expect(t, e.redeem(1, a.Code), 400, "billing.redemption_code_unavailable")
	var list struct {
		Total int64 `json:"total"`
	}
	testkit.Decode(t, e.do("GET", codesURL, e.admin(2), nil), &list)
	if list.Total != 0 {
		t.Fatalf("list total %d", list.Total)
	}
}

func TestListCodesFilters(t *testing.T) {
	e := newEnv(t)
	usage := e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1, "description": "Summer Sale"})
	period := e.mustCode(map[string]any{"mode": "period", "planID": e.planByCode("pro").ID, "durationDays": 3, "description": "pro trial"})
	exhausted := e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1, "maxRedemptions": 1})
	expiring := e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1, "expiresAt": e.Clock.Now().Add(time.Hour).Format(time.RFC3339)})
	inactive := e.mustCode(map[string]any{"mode": "usage", "creditUSD": 1})
	e.DB.Model(&RedemptionCode{}).Where("id = ?", inactive.ID).Update("status", "inactive")
	e.DB.Model(&RedemptionCode{}).Where("id = ?", exhausted.ID).Update("redeemed_count", 1)
	e.Clock.Advance(2 * time.Hour)
	type list struct {
		Total   int64      `json:"total"`
		Results []CodeView `json:"results"`
	}
	get := func(q string) list {
		w := e.do("GET", codesURL+q, e.admin(2), nil)
		testkit.Expect(t, w, 200, "")
		var l list
		testkit.Decode(t, w, &l)
		// the list never carries plaintext
		for _, c := range l.Results {
			if c.Code != "" {
				t.Fatalf("plaintext leaked in list: %+v", c)
			}
		}
		return l
	}
	ids := func(l list) map[uint]bool {
		m := map[uint]bool{}
		for _, c := range l.Results {
			m[c.ID] = true
		}
		return m
	}
	if l := get(""); l.Total != 5 || l.Results[0].ID != inactive.ID {
		t.Fatalf("all: %d first=%d", l.Total, l.Results[0].ID)
	}
	if l := get("?mode=period"); l.Total != 1 || l.Results[0].ID != period.ID {
		t.Fatalf("mode: %+v", l)
	}
	if l := get("?status=inactive"); l.Total != 1 || l.Results[0].ID != inactive.ID {
		t.Fatalf("status: %+v", l)
	}
	if l := get("?availability=expired"); l.Total != 1 || !ids(l)[expiring.ID] {
		t.Fatalf("expired: %+v", l)
	}
	if l := get("?availability=exhausted"); l.Total != 1 || !ids(l)[exhausted.ID] {
		t.Fatalf("exhausted: %+v", l)
	}
	if l := get("?q=SUMMER"); l.Total != 1 || !ids(l)[usage.ID] {
		t.Fatalf("q desc: %+v", l)
	}
	if l := get("?q=" + usage.CodeHint[:4]); !ids(l)[usage.ID] {
		t.Fatalf("q hint: %+v", l)
	}
	// available depends on the billing mode
	if l := get("?availability=available"); l.Total != 0 {
		t.Fatalf("self mode available: %+v", l)
	}
	e.mode("usage")
	if l := get("?availability=available"); l.Total != 1 || !ids(l)[usage.ID] {
		t.Fatalf("usage available: %+v", l.Results)
	}
	e.mode("period")
	if l := get("?availability=available"); l.Total != 2 || !ids(l)[period.ID] {
		t.Fatalf("period available: %+v", l.Results)
	}
	// unknown filter values are not errors: mode and status match nothing,
	// an unknown availability is ignored
	if l := get("?mode=self"); l.Total != 0 {
		t.Fatalf("unknown mode: %+v", l)
	}
	if l := get("?status=deleted"); l.Total != 0 {
		t.Fatalf("unknown status: %+v", l)
	}
	if l := get("?availability=maybe"); l.Total != 5 {
		t.Fatalf("unknown availability: %+v", l)
	}
	// pagination
	if l := get("?page_size=2&page=3"); l.Total != 5 || len(l.Results) != 1 {
		t.Fatalf("paging: %+v", l)
	}
}

func TestAdminRedemptionsList(t *testing.T) {
	e := newEnv(t)
	e.mode("period")
	e.users.users[7] = UserInfo{Username: "alice", DisplayName: "Alice A"}
	a := e.mustCode(map[string]any{"mode": "usage", "creditUSD": 2, "description": "freebie"})
	b := e.mustCode(map[string]any{"mode": "period", "planID": e.planByCode("pro").ID, "durationDays": 9})
	testkit.Expect(t, e.redeem(7, a.Code), 200, "")
	e.Clock.Advance(time.Minute)
	testkit.Expect(t, e.redeem(8, b.Code), 200, "")
	type item struct {
		CodeID               uint   `json:"codeID"`
		CodeHint             string `json:"codeHint"`
		CodeDescription      string `json:"codeDescription"`
		CodeStatus           string `json:"codeStatus"`
		UserID               uint   `json:"userID"`
		Username             string `json:"username"`
		UserDisplayName      string `json:"userDisplayName"`
		UserLabel            string `json:"userLabel"`
		RewardType           string `json:"rewardType"`
		PlanName             string `json:"planName"`
		DurationDays         int    `json:"durationDays"`
		BalanceBeforeNanousd *int64 `json:"balanceBeforeNanousd"`
		BalanceAfterNanousd  *int64 `json:"balanceAfterNanousd"`
		RefNo                string `json:"refNo"`
		SnapshotJSON         string `json:"snapshotJSON"`
	}
	var list struct {
		Total   int64  `json:"total"`
		Results []item `json:"results"`
	}
	w := e.do("GET", "/api/v1/admin/redemptions", e.admin(2), nil)
	testkit.Expect(t, w, 200, "")
	testkit.Decode(t, w, &list)
	if list.Total != 2 || list.Results[0].UserID != 8 || list.Results[1].UserID != 7 {
		t.Fatalf("list %+v", list)
	}
	bal := list.Results[1]
	if bal.Username != "alice" || bal.UserDisplayName != "Alice A" || bal.UserLabel == "" || bal.CodeDescription != "freebie" || bal.CodeHint == "" ||
		bal.BalanceBeforeNanousd == nil || *bal.BalanceBeforeNanousd != 0 || bal.BalanceAfterNanousd == nil || *bal.BalanceAfterNanousd != 2_000_000_000 ||
		bal.RefNo == "" || bal.CodeStatus != "active" {
		t.Fatalf("balance item %+v", bal)
	}
	sub := list.Results[0]
	if sub.RewardType != "subscription" || sub.PlanName != "Pro" || sub.DurationDays != 9 || sub.BalanceAfterNanousd != nil {
		t.Fatalf("subscription item %+v", sub)
	}
	get := func(q string) int64 {
		var l struct {
			Total int64 `json:"total"`
		}
		w := e.do("GET", "/api/v1/admin/redemptions"+q, e.admin(2), nil)
		testkit.Expect(t, w, 200, "")
		testkit.Decode(t, w, &l)
		return l.Total
	}
	if get("?user_id=7") != 1 || get("?reward_type=subscription") != 1 || get("?code_id="+uitoa(a.ID)) != 1 || get("?query=freebie") != 1 || get("?query=nomatch") != 0 {
		t.Fatal("filters")
	}
	if get("?created_from="+e.Clock.Now().Add(-30*time.Second).UTC().Format(time.RFC3339)) != 1 || get("?created_to="+e.Clock.Now().Add(-30*time.Second).UTC().Format(time.RFC3339)) != 1 {
		t.Fatal("time filters")
	}
	testkit.Expect(t, e.do("GET", "/api/v1/admin/redemptions?created_from=bad", e.admin(2), nil), 400, "request.invalid_query")
	l := struct {
		Results []item `json:"results"`
	}{}
	testkit.Decode(t, e.do("GET", "/api/v1/admin/redemptions?sort=created_asc", e.admin(2), nil), &l)
	if l.Results[0].UserID != 7 {
		t.Fatal("created_asc")
	}
	// deleted codes still join
	e.DB.Model(&RedemptionCode{}).Where("id = ?", a.ID).Update("status", "deleted")
	testkit.Decode(t, e.do("GET", "/api/v1/admin/redemptions?user_id=7", e.admin(2), nil), &l)
	if len(l.Results) != 1 || l.Results[0].CodeStatus != "deleted" {
		t.Fatalf("deleted join %+v", l.Results)
	}
	_ = strconv.Itoa
}
