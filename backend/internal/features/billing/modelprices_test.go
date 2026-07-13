package billing

import (
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func priceBody(name, mode string) map[string]any {
	return map[string]any{
		"platformModelName": name, "isFree": false, "pricingMode": mode,
		"inputUSDPerMTokens": 3.0, "cacheReadUSDPerMTokens": 0.3, "cacheWriteUSDPerMTokens": 3.75, "outputUSDPerMTokens": 15.0,
		"callUSDPerCall": 0.5, "durationUSDPerSecond": 0.1,
	}
}

func TestModelPriceUpsertModes(t *testing.T) {
	e := newEnv(t)
	type mp struct {
		ModelPricing ModelPriceView `json:"modelPricing"`
	}
	put := func(body map[string]any) (int, ModelPriceView) {
		w := e.do("PUT", "/api/v1/admin/billing/model-prices", e.admin(2), body)
		var out mp
		if w.Code == 200 {
			testkit.Decode(t, w, &out)
		}
		return w.Code, out.ModelPricing
	}
	// token mode keeps the four token rates only
	code, v := put(priceBody("  m-token  ", "token"))
	if code != 200 || v.PlatformModelName != "m-token" {
		t.Fatalf("token: %d %+v", code, v)
	}
	if v.InputNanousdPerMTokens != 3_000_000_000 || v.OutputNanousdPerMTokens != 15_000_000_000 ||
		v.CacheReadNanousdPerMTokens != 300_000_000 || v.CacheWriteNanousdPerMTokens != 3_750_000_000 ||
		v.CallNanousdPerCall != 0 || v.DurationNanousdPerSecond != 0 || v.Currency != "USD" {
		t.Fatalf("token view %+v", v)
	}
	if v.TieredPricingJSON != "{}" || v.SchedulePricingJSON != "{}" {
		t.Fatalf("json defaults %q %q", v.TieredPricingJSON, v.SchedulePricingJSON)
	}
	// call mode keeps only the per call rate
	_, v = put(priceBody("m-call", "call"))
	if v.CallNanousdPerCall != 500_000_000 || v.InputNanousdPerMTokens != 0 || v.OutputNanousdPerMTokens != 0 {
		t.Fatalf("call view %+v", v)
	}
	// currency is ignored
	b := priceBody("m-cur", "token")
	b["currency"] = "CNY"
	_, v = put(b)
	if v.Currency != "USD" {
		t.Fatalf("currency %s", v.Currency)
	}
	// duration needs a catalog that confirms video support
	code, _ = put(priceBody("m-dur", "duration"))
	if code != 400 {
		t.Fatalf("duration without catalog: %d", code)
	}
	// replacing updates in place (same id)
	_, v1 := put(priceBody("m-up", "token"))
	b = priceBody("m-up", "token")
	b["inputUSDPerMTokens"] = 4.0
	_, v2 := put(b)
	if v1.ID != v2.ID || v2.InputNanousdPerMTokens != 4_000_000_000 {
		t.Fatalf("upsert %+v %+v", v1, v2)
	}
	if _, ok := e.audit.last("upsert_model_pricing"); !ok {
		t.Fatal("audit missing")
	}
}

func TestModelPriceValidation(t *testing.T) {
	e := newEnv(t)
	send := func(mut func(map[string]any)) int {
		b := priceBody("m", "token")
		mut(b)
		return e.do("PUT", "/api/v1/admin/billing/model-prices", e.admin(2), b).Code
	}
	w := func(mut func(map[string]any)) string {
		b := priceBody("m", "token")
		mut(b)
		r := e.do("PUT", "/api/v1/admin/billing/model-prices", e.admin(2), b)
		return testkit.Envelope(t, r).ErrorCode
	}
	if c := w(func(b map[string]any) { b["platformModelName"] = "   " }); c != "llm.platform_model_name_required" {
		t.Fatalf("blank name: %s", c)
	}
	if c := w(func(b map[string]any) { b["cacheWritePriceBasis"] = "weird" }); c != "request.invalid_body" {
		t.Fatalf("basis: %s", c)
	}
	if c := w(func(b map[string]any) { b["platformModelName"] = "" }); c != "request.invalid_body" {
		t.Fatalf("empty name: %s", c)
	}
	if c := w(func(b map[string]any) { delete(b, "platformModelName") }); c != "request.invalid_body" {
		t.Fatalf("missing name: %s", c)
	}
	for _, basis := range []string{"direct", "anthropic_5m", ""} {
		if code := send(func(b map[string]any) { b["cacheWritePriceBasis"] = basis }); code != 200 {
			t.Fatalf("basis %q: %d", basis, code)
		}
	}
	if code := send(func(b map[string]any) { b["pricingMode"] = "bogus" }); code != 400 {
		t.Fatalf("mode: %d", code)
	}
	if code := send(func(b map[string]any) { b["inputUSDPerMTokens"] = -1 }); code != 400 {
		t.Fatalf("negative: %d", code)
	}
	if code := send(func(b map[string]any) { delete(b, "outputUSDPerMTokens") }); code != 400 {
		t.Fatalf("missing: %d", code)
	}
	if code := send(func(b map[string]any) { delete(b, "isFree") }); code != 400 {
		t.Fatalf("missing isFree: %d", code)
	}
	if code := send(func(b map[string]any) { b["platformModelName"] = strings.Repeat("x", 129) }); code != 400 {
		t.Fatalf("long name: %d", code)
	}
}
