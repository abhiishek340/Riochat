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

func TestModelPriceTieredValidation(t *testing.T) {
	e := newEnv(t)
	tier := func(limit int64) map[string]any {
		return map[string]any{"fromTokens": 0, "upToTokens": limit, "inputUSDPerMTokens": 1.0, "cacheReadUSDPerMTokens": 0.1,
			"cacheWriteUSDPerMTokens": 1.0, "outputUSDPerMTokens": 5.0}
	}
	asJSON := func(tiers ...map[string]any) string {
		return mustJSON(map[string]any{"tiers": tiers})
	}
	var many []map[string]any
	for i := 1; i <= 101; i++ {
		many = append(many, tier(int64(i*1000)))
	}
	bad := map[string]string{
		"non increasing": asJSON(tier(200000), tier(100000)),
		"empty":          `{"tiers":[]}`,
		"too many":       asJSON(many...),
		"garbage":        `[[`,
		"equal":          asJSON(tier(1000), tier(1000)),
	}
	for name, js := range bad {
		b := priceBody("tm", "tiered")
		b["tieredPricingJSON"] = js
		w := e.do("PUT", "/api/v1/admin/billing/model-prices", e.admin(2), b)
		testkit.Expect(t, w, 400, "billing.invalid_model_pricing")
		_ = name
	}
	good := asJSON(tier(200000), map[string]any{"fromTokens": 200000, "upToTokens": 0, "inputUSDPerMTokens": 4.0, "cacheReadUSDPerMTokens": 0.4,
		"cacheWriteUSDPerMTokens": 4.0, "outputUSDPerMTokens": 20.0})
	b := priceBody("tm", "tiered")
	b["tieredPricingJSON"] = good
	w := e.do("PUT", "/api/v1/admin/billing/model-prices", e.admin(2), b)
	testkit.Expect(t, w, 200, "")
	var out struct {
		ModelPricing ModelPriceView `json:"modelPricing"`
	}
	testkit.Decode(t, w, &out)
	v := out.ModelPricing
	if v.InputNanousdPerMTokens != 0 || v.OutputNanousdPerMTokens != 0 || !strings.Contains(v.TieredPricingJSON, "tiers") {
		t.Fatalf("tiered view %+v", v)
	}
}

func TestModelPriceScheduleValidationViaAPI(t *testing.T) {
	e := newEnv(t)
	body := func(js string) int {
		b := priceBody("sm", "token")
		b["schedulePricingJSON"] = js
		return e.do("PUT", "/api/v1/admin/billing/model-prices", e.admin(2), b).Code
	}
	overlap := `{"periods":[{"name":"a","weekdays":[1,2,3,4,5],"start":"09:00","end":"18:00","ratePercent":150},{"name":"b","weekdays":[1,2,3,4,5],"start":"08:00","end":"10:00","ratePercent":120}]}`
	if body(overlap) != 400 {
		t.Fatal("overlap accepted")
	}
	ok := `{"periods":[{"name":"night","weekdays":[5],"start":"22:00","end":"06:00","ratePercent":150}]}`
	if body(ok) != 200 {
		t.Fatal("night period rejected")
	}
	if body(`{"periods":[{"name":"x","weekdays":[1],"start":"09:00","end":"10:00","ratePercent":0}]}`) != 400 {
		t.Fatal("rate 0 accepted")
	}
	if body(`{"periods":[{"name":"x","weekdays":[1],"start":"09:00","end":"10:00","ratePercent":10001}]}`) != 400 {
		t.Fatal("rate 10001 accepted")
	}
	if body(`{}`) != 200 || body(``) != 200 {
		t.Fatal("empty schedule rejected")
	}
}

func TestModelPriceCatalogRules(t *testing.T) {
	cat := &fakeCatalog{models: map[string]ModelInfo{
		"chat":  {Name: "chat", Vendor: "acme", Icon: "acme.svg"},
		"video": {Name: "video", SupportsVideo: true},
	}}
	e := newEnv(t, withPorts(func(p *Ports) { p.Models = cat }))
	put := func(name, mode string) int {
		return e.do("PUT", "/api/v1/admin/billing/model-prices", e.admin(2), priceBody(name, mode)).Code
	}
	if put("ghost", "token") != 400 {
		t.Fatal("unknown model accepted")
	}
	if put("chat", "token") != 200 {
		t.Fatal("active model rejected")
	}
	if put("chat", "duration") != 400 {
		t.Fatal("duration on non-video model accepted")
	}
	if put("video", "duration") != 200 {
		t.Fatal("duration on video model rejected")
	}
	// orphan rows are hidden from the list
	e.price(ModelPrice{PlatformModelName: "orphan", PricingMode: "token"})
	w := e.do("GET", "/api/v1/admin/billing/model-prices", e.admin(2), nil)
	testkit.Expect(t, w, 200, "")
	var out struct {
		Total   int64            `json:"total"`
		Results []ModelPriceView `json:"results"`
	}
	testkit.Decode(t, w, &out)
	if out.Total != 2 || len(out.Results) != 2 {
		t.Fatalf("list %+v", out)
	}
	for _, r := range out.Results {
		if r.PlatformModelName == "orphan" {
			t.Fatal("orphan listed")
		}
		if r.PlatformModelName == "chat" && (r.ModelVendor != "acme" || r.ModelIcon != "acme.svg") {
			t.Fatalf("vendor/icon %+v", r)
		}
	}
	// query filter is a case-insensitive substring
	w = e.do("GET", "/api/v1/admin/billing/model-prices?q=CHA", e.admin(2), nil)
	testkit.Decode(t, w, &out)
	if out.Total != 1 || out.Results[0].PlatformModelName != "chat" {
		t.Fatalf("filtered %+v", out)
	}
}

func TestModelPriceListWithoutCatalogOrdersByName(t *testing.T) {
	e := newEnv(t)
	for _, n := range []string{"b", "c", "a"} {
		e.price(ModelPrice{PlatformModelName: n, PricingMode: "token"})
	}
	w := e.do("GET", "/api/v1/admin/billing/model-prices?page_size=2", e.admin(2), nil)
	var out struct {
		Total   int64            `json:"total"`
		Results []ModelPriceView `json:"results"`
	}
	testkit.Decode(t, w, &out)
	if out.Total != 3 || len(out.Results) != 2 || out.Results[0].PlatformModelName != "a" || out.Results[1].PlatformModelName != "b" {
		t.Fatalf("%+v", out)
	}
}

func TestPublicPricingInvalidatedOnUpsert(t *testing.T) {
	e := newEnv(t)
	e.mode("usage")
	ctx := t.Context()
	e.price(ModelPrice{PlatformModelName: "p1", PricingMode: "token", InputNanousdPerMTokens: 1_000_000_000})
	mode, prices, err := e.svc.PublicPricing(ctx)
	if err != nil || mode != "usage" || prices["p1"].InputUSDPerMTokens != 1 {
		t.Fatalf("%s %+v %v", mode, prices, err)
	}
	b := priceBody("p1", "token")
	b["inputUSDPerMTokens"] = 2.0
	testkit.Expect(t, e.do("PUT", "/api/v1/admin/billing/model-prices", e.admin(2), b), 200, "")
	_, prices, _ = e.svc.PublicPricing(ctx)
	if prices["p1"].InputUSDPerMTokens != 2 {
		t.Fatalf("stale cache: %+v", prices["p1"])
	}
}
