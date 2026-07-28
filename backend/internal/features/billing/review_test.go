package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

// ---- spec review regressions ----

func TestStripeWebhookNotConfiguredWinsOverBodyLimit(t *testing.T) {
	e := newEnv(t, withSeed(map[string]string{KeyMode: "period"}))
	big := bytes.Repeat([]byte("x"), stripeWebhookMaxBody+10)
	w := e.do("POST", "/api/v1/billing/payments/stripe/webhook", nil, big)
	testkit.Expect(t, w, 400, "payment.webhook_not_configured")
	// with a secret configured the size limit applies
	e.setSettings(map[string]string{KeyStripeWebhookSecret: testStripeWebhook})
	w = e.do("POST", "/api/v1/billing/payments/stripe/webhook", nil, big)
	testkit.Expect(t, w, 413, "payment.webhook_body_too_large")
}

func TestCheckoutInactivePlanOrPriceIsInvalidOrder(t *testing.T) {
	st := newStripeFake(t)
	e := payEnv(t, st)
	price := e.priceID("pro-monthly")
	e.DB.Model(&Price{}).Where("id = ?", price).Update("is_active", false)
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price}), 400, "payment.invalid_order")
	e.DB.Model(&Price{}).Where("id = ?", price).Update("is_active", true)
	e.DB.Model(&Plan{}).Where("code = ?", "pro").Update("is_active", false)
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": price}), 400, "payment.invalid_order")
	// a price that does not exist is still plan_not_found
	testkit.Expect(t, e.checkout(5, map[string]any{"priceID": 9999}), 400, "billing.plan_not_found")
}

func TestPaymentRequiredDefaultsTo402ButBillingRoutesAnswer400(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	exp := e.Clock.Now().Add(48 * time.Hour)
	err := e.svc.AssignUserPlan(ctx, 3, "pro", &exp)
	te, ok := asTyped(err)
	if !ok || te.Code != "billing.payment_required" || te.Status != 402 {
		t.Fatalf("service level error: %#v", err)
	}
	e.mode("period")
	testkit.Expect(t, e.do("POST", "/api/v1/billing/subscriptions", e.user(5), map[string]any{"priceID": e.priceID("pro-monthly")}), 400, "billing.payment_required")
	e.mode("self")
	testkit.Expect(t, e.do("PATCH", "/api/v1/admin/billing/accounts/5/balance", e.admin(2), map[string]any{"balanceUSD": 1}), 400, "billing.payment_required")
}

func TestUpstreamUsageSnapshotOnlyKeepsContainers(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.price(tokenModel("m"))
	cases := map[string]string{`{"a":1}`: "map", `[1,2]`: "array", `5`: "empty", `"txt"`: "empty", `not json`: "empty", ``: "empty"}
	for raw, want := range cases {
		d, err := e.svc.BuildLedger(context.Background(), LedgerInput{UserID: 1, Mode: ModeUsage, PlatformModelName: "m", UpstreamUsage: json.RawMessage(raw)})
		if err != nil {
			t.Fatal(err)
		}
		switch v := d.Snapshot["upstream_usage"].(type) {
		case map[string]any:
			if (want == "map" && len(v) != 1) || (want == "empty" && len(v) != 0) || want == "array" {
				t.Fatalf("%q -> %v", raw, v)
			}
		case []any:
			if want != "array" {
				t.Fatalf("%q -> %v", raw, v)
			}
		default:
			t.Fatalf("%q -> %#v", raw, v)
		}
	}
}

type fakeRuns struct{ refs map[string][]string }

func (f fakeRuns) RefNosForRequestID(_ context.Context, id string) ([]string, error) {
	return f.refs[id], nil
}

func TestCallLogsSearchByRunRequestID(t *testing.T) {
	e := newEnv(t, withPorts(func(p *Ports) { p.Runs = fakeRuns{refs: map[string][]string{"req-123": {"run-a", "run-b"}}} }))
	e.ledger(ledgerSpec{user: 1, model: "alpha", ref: "run-a"})
	e.ledger(ledgerSpec{user: 1, model: "beta", ref: "run-b"})
	e.ledger(ledgerSpec{user: 1, model: "gamma", ref: "run-c"})
	list := func(q string) int64 {
		t.Helper()
		w := e.do("GET", "/api/v1/admin/call-logs?query="+q, e.admin(9), nil)
		testkit.Expect(t, w, 200, "")
		var out struct {
			Total int64 `json:"total"`
		}
		testkit.Decode(t, w, &out)
		return out.Total
	}
	if n := list("req-123"); n != 2 {
		t.Fatalf("by request id: %d", n)
	}
	if n := list("run-c"); n != 1 { // exact ref no still matches
		t.Fatalf("by ref no: %d", n)
	}
	if n := list("nothing-here"); n != 0 {
		t.Fatalf("no match: %d", n)
	}
}

func TestPaymentSettingsLimitCountsCharacters(t *testing.T) {
	// 512 multi-byte characters are within the limit even though they exceed
	// 512 bytes
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	ok := strings.Repeat("\u5bc6", 512)
	if err := ValidateSettingValues(map[string]string{KeyEpayPID: ok}); err != nil {
		t.Fatalf("512 chars rejected: %v", err)
	}
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	if err := ValidateSettingValues(map[string]string{KeyEpayPID: ok + "\u5bc6"}); err == nil {
		t.Fatal("513 chars accepted")
	}
}

func TestServiceItemScheduleUsesCurrentTime(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	e.price(tokenModel("main"))
	helper := tokenModel("helper")
	helper.SchedulePricingJSON = `{"periods":[{"name":"all-week","weekdays":[0,1,2,3,4,5,6],"start":"00:00","end":"23:59","ratePercent":200}]}`
	e.price(helper)
	d, err := e.svc.BuildLedger(context.Background(), LedgerInput{UserID: 1, Mode: ModeUsage, PlatformModelName: "main",
		BillingAt:    e.Clock.Now().Add(-24 * 365 * time.Hour),
		ServiceItems: []ServiceItemInput{{Code: "title", Name: "title", PlatformModelName: "helper", InputTokens: 1_000_000}}})
	if err != nil {
		t.Fatal(err)
	}
	items := d.Snapshot["service_items"].([]map[string]any)
	if len(items) != 1 || items[0]["schedule_rate_percent"] != 200 {
		t.Fatalf("item %v", items)
	}
	if got := items[0]["billed_nanousd"].(int64); got != 6_000_000_000 {
		t.Fatalf("billed %d", got)
	}
}
