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
