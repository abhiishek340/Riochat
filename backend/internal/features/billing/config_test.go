package billing

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/testkit"
)

type cfgOut struct {
	Config struct {
		Mode                     string  `json:"mode"`
		PrepaidAmountUSD         float64 `json:"prepaidAmountUSD"`
		PrepaidAmountNanousd     int64   `json:"prepaidAmountNanousd"`
		NativeToolBillingEnabled bool    `json:"nativeToolBillingEnabled"`
		NativeToolPricing        []struct {
			Provider     string `json:"provider"`
			ToolKey      string `json:"toolKey"`
			PriceNanousd int64  `json:"priceNanousd"`
			Billable     bool   `json:"billable"`
		} `json:"nativeToolPricing"`
		PaymentProviders []string `json:"paymentProviders"`
		USDToCNYRate     float64  `json:"usdToCNYRate"`
		DisplayCurrency  string   `json:"displayCurrency"`
		EpayTypes        []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"epayTypes"`
	} `json:"config"`
}

func getConfig(t *testing.T, e *env) cfgOut {
	t.Helper()
	w := e.do("GET", "/api/v1/billing/config", e.user(1), nil)
	testkit.Expect(t, w, 200, "")
	var out cfgOut
	testkit.Decode(t, w, &out)
	return out
}

func TestConfigDefaults(t *testing.T) {
	e := newEnv(t)
	c := getConfig(t, e).Config
	if c.Mode != "self" || c.PrepaidAmountUSD != 0 || !c.NativeToolBillingEnabled || c.USDToCNYRate != 7.2 || c.DisplayCurrency != "USD" {
		t.Fatalf("defaults: %+v", c)
	}
	if len(c.PaymentProviders) != 0 {
		t.Fatalf("providers %v", c.PaymentProviders)
	}
	if len(c.EpayTypes) != 2 || c.EpayTypes[0].Type != "alipay" || c.EpayTypes[1].Type != "wxpay" {
		t.Fatalf("epay types %+v", c.EpayTypes)
	}
	if len(c.NativeToolPricing) == 0 {
		t.Fatal("native tool catalog missing")
	}
	// the admin mount returns the same object
	w := e.do("GET", "/api/v1/admin/billing/config", e.admin(2), nil)
	testkit.Expect(t, w, 200, "")
	// regular users cannot read the admin mount
	w = e.do("GET", "/api/v1/admin/billing/config", e.user(1), nil)
	if w.Code != 403 && w.Code != 401 {
		t.Fatalf("user on admin route: %d", w.Code)
	}
}

func TestConfigNormalizesStoredValues(t *testing.T) {
	e := newEnv(t, withSeed(map[string]string{}))
	// bypass validation to simulate legacy/invalid stored values
	e.settings.mu.Lock()
	e.settings.m[KeyMode] = "bogus"
	e.settings.m[KeyPrepaidAmountUSD] = "-3"
	e.settings.m[KeyUSDToCNYRate] = "abc"
	e.settings.m[KeyDisplayCurrency] = "usd"
	e.settings.m[KeyPaymentProviders] = " Stripe, epay,stripe,paypal,disabled"
	e.settings.m[KeyEpayTypes] = `[{"name":"A","type":"a b"},{"name":"B","type":"b"},{"name":"B2","type":"b"},{"name":"","type":"c"}]`
	e.settings.mu.Unlock()
	c := getConfig(t, e).Config
	if c.Mode != "self" || c.PrepaidAmountUSD != 0 || c.USDToCNYRate != 7.2 || c.DisplayCurrency != "USD" {
		t.Fatalf("normalized: %+v", c)
	}
	if strings.Join(c.PaymentProviders, ",") != "stripe,epay" {
		t.Fatalf("providers %v", c.PaymentProviders)
	}
	if len(c.EpayTypes) != 1 || c.EpayTypes[0].Type != "b" || c.EpayTypes[0].Name != "B" {
		t.Fatalf("epay types %+v", c.EpayTypes)
	}
	e.settings.mu.Lock()
	e.settings.m[KeyEpayTypes] = "not json"
	e.settings.mu.Unlock()
	c = getConfig(t, e).Config
	if len(c.EpayTypes) != 2 {
		t.Fatalf("fallback epay types %+v", c.EpayTypes)
	}
}

func TestPatchConfig(t *testing.T) {
	e := newEnv(t)
	body := map[string]any{"mode": "usage", "prepaidAmountUSD": 1.5, "usdToCNYRate": 7.25, "displayCurrency": "CNY",
		"nativeToolBillingEnabled": false}
	w := e.do("PATCH", "/api/v1/admin/billing/config", e.admin(2), body)
	testkit.Expect(t, w, 200, "")
	var out cfgOut
	testkit.Decode(t, w, &out)
	c := out.Config
	if c.Mode != "usage" || c.PrepaidAmountNanousd != 1_500_000_000 || c.USDToCNYRate != 7.25 || c.DisplayCurrency != "CNY" || c.NativeToolBillingEnabled {
		t.Fatalf("patched: %+v", c)
	}
	if _, ok := e.audit.last("update_billing_config"); !ok {
		t.Fatalf("audit actions %v", e.audit.actions())
	}
	// mode is required
	testkit.Expect(t, e.do("PATCH", "/api/v1/admin/billing/config", e.admin(2), map[string]any{"prepaidAmountUSD": 1}), 400, "request.invalid_body")
	testkit.Expect(t, e.do("PATCH", "/api/v1/admin/billing/config", e.admin(2), map[string]any{"mode": "nope"}), 400, "request.invalid_body")
	testkit.Expect(t, e.do("PATCH", "/api/v1/admin/billing/config", e.admin(2), map[string]any{"mode": "usage", "usdToCNYRate": 0}), 400, "request.invalid_body")
	testkit.Expect(t, e.do("PATCH", "/api/v1/admin/billing/config", e.admin(2), map[string]any{"mode": "usage", "displayCurrency": "EUR"}), 400, "request.invalid_body")
	testkit.Expect(t, e.do("PATCH", "/api/v1/admin/billing/config", e.admin(2), map[string]any{"mode": "usage", "prepaidAmountUSD": -1}), 400, "request.invalid_body")
	// a value over the settings limit passes binding but fails settings validation
	w = e.do("PATCH", "/api/v1/admin/billing/config", e.admin(2), map[string]any{"mode": "usage", "prepaidAmountUSD": 2_000_000})
	if w.Code != 400 {
		t.Fatalf("over limit: %d %s", w.Code, w.Body)
	}
	// the failed patch changed nothing
	if getConfig(t, e).Config.PrepaidAmountUSD != 1.5 {
		t.Fatal("failed patch must not be applied")
	}
}

func TestPatchConfigNativeToolPricing(t *testing.T) {
	e := newEnv(t)
	bad := map[string]any{"mode": "usage", "nativeToolPricing": []map[string]any{{"toolKey": "unknown.tool", "priceNanousd": 1}}}
	testkit.Expect(t, e.do("PATCH", "/api/v1/admin/billing/config", e.admin(2), bad), 400, "billing.invalid_native_tool_pricing")
	neg := map[string]any{"mode": "usage", "nativeToolPricing": []map[string]any{{"toolKey": "openai.web_search", "priceNanousd": -5}}}
	testkit.Expect(t, e.do("PATCH", "/api/v1/admin/billing/config", e.admin(2), neg), 400, "billing.invalid_native_tool_pricing")
	key := "openai.web_search"
	ok := map[string]any{"mode": "usage", "nativeToolPricing": []map[string]any{{"toolKey": key, "priceNanousd": 0, "unit": "bogus", "priceLabel": "x", "billable": true}}}
	w := e.do("PATCH", "/api/v1/admin/billing/config", e.admin(2), ok)
	testkit.Expect(t, w, 200, "")
	var out cfgOut
	testkit.Decode(t, w, &out)
	found := false
	for _, it := range out.Config.NativeToolPricing {
		if it.ToolKey == key {
			found = true
			if it.PriceNanousd != 0 || it.Billable {
				t.Fatalf("override not applied: %+v", it)
			}
		}
	}
	if !found {
		t.Fatal("tool missing from view")
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(e.settings.m[KeyNativeToolPricingJSON]), &raw)
	if _, ok := raw[key]; !ok {
		t.Fatalf("stored json %s", e.settings.m[KeyNativeToolPricingJSON])
	}
}

func TestPaymentSettingsValidation(t *testing.T) {
	cases := []struct {
		name string
		kv   map[string]string
		ok   bool
	}{
		{"providers ok", map[string]string{KeyPaymentProviders: "stripe,epay"}, true},
		{"providers disabled", map[string]string{KeyPaymentProviders: "disabled"}, true},
		{"providers bad", map[string]string{KeyPaymentProviders: "paypal"}, false},
		{"gateway ok", map[string]string{KeyEpayGatewayURL: "https://pay.example.com/gw"}, true},
		{"gateway empty", map[string]string{KeyEpayGatewayURL: ""}, true},
		{"gateway creds", map[string]string{KeyEpayGatewayURL: "https://u:p@pay.example.com"}, false},
		{"gateway query", map[string]string{KeyEpayGatewayURL: "https://pay.example.com/?a=1"}, false},
		{"gateway scheme", map[string]string{KeyEpayGatewayURL: "ftp://pay.example.com"}, false},
		{"types ok", map[string]string{KeyEpayTypes: `[{"name":"Ali","type":"alipay"}]`}, true},
		{"types empty list", map[string]string{KeyEpayTypes: `[]`}, false},
		{"types dup", map[string]string{KeyEpayTypes: `[{"name":"a","type":"x"},{"name":"b","type":"x"}]`}, false},
		{"types charset", map[string]string{KeyEpayTypes: `[{"name":"a","type":"x y"}]`}, false},
		{"types not json", map[string]string{KeyEpayTypes: `nope`}, false},
		{"mode ok", map[string]string{KeyMode: "period"}, true},
		{"mode bad", map[string]string{KeyMode: "free"}, false},
		{"rate low", map[string]string{KeyUSDToCNYRate: "0"}, false},
		{"rate high", map[string]string{KeyUSDToCNYRate: "1001"}, false},
		{"currency bad", map[string]string{KeyDisplayCurrency: "EUR"}, false},
		{"prepaid high", map[string]string{KeyPrepaidAmountUSD: "1000001"}, false},
		{"prepaid ok", map[string]string{KeyPrepaidAmountUSD: "1000000"}, true},
		{"stripe key long", map[string]string{KeyStripeSecretKey: strings.Repeat("a", 513)}, false},
	}
	for _, c := range cases {
		err := ValidateSettingValues(c.kv)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v want ok=%v", c.name, err, c.ok)
		}
	}
	// payment keys use the dedicated settings code
	err := ValidateSettingValues(map[string]string{KeyPaymentProviders: "paypal"})
	if err == nil || !strings.Contains(err.Error(), "") {
		t.Fatal("expected error")
	}
	if te, ok := asTyped(err); !ok || te.Code != ErrBillingPaymentInvalid.Code {
		t.Fatalf("code: %v", err)
	}
}

func TestBillingGatingOff(t *testing.T) {
	e := localEnv(t)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/billing/plans"},
		{"POST", "/api/v1/billing/subscriptions"},
		{"POST", "/api/v1/billing/payments/checkout"},
		{"POST", "/api/v1/billing/redemptions"},
		{"POST", "/api/v1/billing/payments/stripe/webhook"},
		{"GET", "/api/v1/billing/payments/epay/notify"},
	} {
		w := e.do(tc.method, tc.path, e.user(1), map[string]any{})
		if w.Code != 404 {
			t.Errorf("%s %s: %d", tc.method, tc.path, w.Code)
			continue
		}
		testkit.Expect(t, w, 404, "feature.disabled")
	}
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/admin/billing/plans"},
		{"GET", "/api/v1/admin/billing/redemption-codes"},
		{"GET", "/api/v1/admin/payment-orders"},
		{"GET", "/api/v1/admin/redemptions"},
	} {
		testkit.Expect(t, e.do(tc.method, tc.path, e.admin(2), nil), 404, "feature.disabled")
	}
	// metering and config remain available
	testkit.Expect(t, e.do("GET", "/api/v1/billing/config", e.user(1), nil), 200, "")
	testkit.Expect(t, e.do("GET", "/api/v1/billing/overview", e.user(1), nil), 200, "")
	testkit.Expect(t, e.do("GET", "/api/v1/admin/billing/model-prices", e.admin(2), nil), 200, "")
	testkit.Expect(t, e.do("GET", "/api/v1/admin/usage-statistics", e.admin(2), nil), 200, "")
	testkit.Expect(t, e.do("GET", "/api/v1/admin/call-logs", e.admin(2), nil), 200, "")

	// changing the mode is rejected, writing the same value back is allowed
	w := e.do("PATCH", "/api/v1/admin/billing/config", e.admin(2), map[string]any{"mode": "usage"})
	testkit.Expect(t, w, 404, "feature.disabled")
	env := testkit.Envelope(t, w)
	raw, _ := json.Marshal(env.Details)
	if !strings.Contains(string(raw), "billing.mode") {
		t.Fatalf("details %s", raw)
	}
	testkit.Expect(t, e.do("PATCH", "/api/v1/admin/billing/config", e.admin(2), map[string]any{"mode": "self"}), 200, "")
}

// The configuration GET result can be sent back to PATCH unchanged, and every
// key is provider-prefixed ("openai.web_search").
func TestPatchConfigNativeToolPricingRoundTrip(t *testing.T) {
	e := newEnv(t)
	cur := getConfig(t, e).Config.NativeToolPricing
	if len(cur) < 20 {
		t.Fatalf("catalog has %d entries", len(cur))
	}
	items := make([]map[string]any, 0, len(cur))
	for _, it := range cur {
		if !strings.Contains(it.ToolKey, ".") || strings.ToLower(it.Provider) != strings.SplitN(it.ToolKey, ".", 2)[0] {
			t.Fatalf("key %q for provider %q is not provider-prefixed", it.ToolKey, it.Provider)
		}
		items = append(items, map[string]any{"toolKey": it.ToolKey, "priceNanousd": it.PriceNanousd, "billable": it.Billable})
	}
	w := e.do("PATCH", "/api/v1/admin/billing/config", e.admin(2), map[string]any{"mode": "usage", "nativeToolPricing": items})
	testkit.Expect(t, w, 200, "")
	// a single edited entry persists and shows in the next GET
	one := []map[string]any{{"toolKey": "xai.x_search", "priceNanousd": 7_000_000}}
	testkit.Expect(t, e.do("PATCH", "/api/v1/admin/billing/config", e.admin(2), map[string]any{"mode": "usage", "nativeToolPricing": one}), 200, "")
	for _, it := range getConfig(t, e).Config.NativeToolPricing {
		if it.ToolKey == "xai.x_search" && (it.PriceNanousd != 7_000_000 || !it.Billable) {
			t.Fatalf("edit lost: %+v", it)
		}
	}
	// unprefixed keys of the earlier catalog are rejected
	old := []map[string]any{{"toolKey": "web_search", "priceNanousd": 1}}
	testkit.Expect(t, e.do("PATCH", "/api/v1/admin/billing/config", e.admin(2), map[string]any{"mode": "usage", "nativeToolPricing": old}), 400, "billing.invalid_native_tool_pricing")
}
