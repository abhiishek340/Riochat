package billing

import (
	"math"
	"testing"
	"time"
)

func TestUSDToNano(t *testing.T) {
	cases := []struct {
		in   float64
		want int64
	}{{1, 1_000_000_000}, {0.5, 500_000_000}, {0, 0}, {-3, 0}, {math.NaN(), 0}, {math.Inf(1), 0}, {1.2345678915, 1_234_567_892}, {12.5, 12_500_000_000}}
	for _, c := range cases {
		if got := USDToNano(c.in); got != c.want {
			t.Errorf("USDToNano(%v) = %d, want %d", c.in, got, c.want)
		}
	}
	if NanoToUSD(1_500_000_000) != 1.5 {
		t.Error("NanoToUSD")
	}
}

func TestTokenAmountRounding(t *testing.T) {
	if got := tokenAmount(1, 500_000); got != 1 {
		t.Errorf("1 token @500000 = %d, want 1", got)
	}
	if got := tokenAmount(1, 499_999); got != 0 {
		t.Errorf("1 token @499999 = %d, want 0", got)
	}
	if tokenAmount(0, 5) != 0 || tokenAmount(5, 0) != 0 || tokenAmount(-5, 10) != 0 {
		t.Error("non-positive inputs must be free")
	}
	// Large values must not overflow.
	if got := tokenAmount(math.MaxInt32, 1_000_000_000_000); got <= 0 {
		t.Errorf("large amount = %d", got)
	}
}

func TestTokenPricingScenario(t *testing.T) {
	spec := priceSpec{Mode: PricingToken, Currency: "USD", Input: 3_000_000_000, Output: 15_000_000_000}
	mc, err := priceUsage(spec, usageCounts{Input: 1_000_000, Output: 100_000}, "openai_chat_completions", "", one)
	if err != nil {
		t.Fatal(err)
	}
	if mc.Input != 3_000_000_000 || mc.Output != 1_500_000_000 || mc.Total != 4_500_000_000 {
		t.Fatalf("cost %+v", mc)
	}
}

func TestReasoningCountsAsOutput(t *testing.T) {
	spec := priceSpec{Mode: PricingToken, Output: 10_000_000_000}
	mc, _ := priceUsage(spec, usageCounts{Output: 500_000, Reasoning: 500_000}, "", "", one)
	if mc.Output != 10_000_000_000 {
		t.Fatalf("output %d", mc.Output)
	}
}

func TestBaseMultipliers(t *testing.T) {
	cases := []struct {
		name                         string
		protocol, speed, tier, p, up string
		want                         ratio
	}{
		{"priority gpt-5.5", protoOpenAIResponses, "", "priority", "gpt-5.5-x", "", ratio{5, 2}},
		{"priority upstream gpt-5.5", protoOpenAIChat, "", "priority", "alias", "GPT-5.5-mini", ratio{5, 2}},
		{"priority gpt-4.1", protoOpenAIResponses, "", "priority", "gpt-4.1", "", ratio{2, 1}},
		{"flex", protoOpenAIResponses, "", "flex", "gpt-4.1", "", ratio{1, 2}},
		{"default tier", protoOpenAIResponses, "", "default", "gpt-4.1", "", one},
		{"anthropic fast", protoAnthropic, "fast", "", "claude", "", ratio{6, 1}},
		{"anthropic standard", protoAnthropic, "", "", "claude", "", one},
		{"other protocol ignores fast", "gemini_generate_content", "fast", "priority", "g", "", one},
	}
	for _, c := range cases {
		if got := baseRatio(c.protocol, c.speed, c.tier, c.p, c.up); got != c.want {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}

func TestBillingSpeedAndTier(t *testing.T) {
	if got := billingSpeed(protoAnthropic, "", "FAST"); got != "fast" {
		t.Errorf("speed fallback to request: %q", got)
	}
	if got := billingSpeed(protoAnthropic, "standard", "fast"); got != "standard" {
		t.Errorf("usage speed wins: %q", got)
	}
	if got := billingSpeed(protoOpenAIChat, "fast", "fast"); got != "" {
		t.Errorf("non-anthropic speed: %q", got)
	}
	if got := billingTier(protoOpenAIResponses, "Priority"); got != "priority" {
		t.Errorf("tier: %q", got)
	}
	if got := billingTier(protoOpenAIResponses, "scale"); got != "default" {
		t.Errorf("unrecognized tier: %q", got)
	}
	if got := billingTier(protoAnthropic, "priority"); got != "" {
		t.Errorf("non-openai tier: %q", got)
	}
}

func TestMultipliersCompose(t *testing.T) {
	period := &SchedulePeriod{Name: "peak", RatePercent: 200}
	m := composeMultiplier(one, 150, period)
	if m.Total.float() != 3 {
		t.Fatalf("group 150 x schedule 200 = %v", m.Total.float())
	}
	m = composeMultiplier(ratio{5, 2}, 100, nil)
	if m.Total != (ratio{5, 2}) || m.GroupPercent != 100 {
		t.Fatalf("unchanged group: %+v", m)
	}
	m = composeMultiplier(one, 0, nil)
	if !m.Total.isOne() {
		t.Fatal("non-positive group percent must leave the multiplier unchanged")
	}
	m = composeMultiplier(ratio{6, 1}, 150, period)
	if m.Total.float() != 18 {
		t.Fatalf("fast x group x schedule = %v", m.Total.float())
	}
}

func TestRatioApplyRoundsHalfUp(t *testing.T) {
	if got := (ratio{1, 2}).apply(5); got != 3 { // 2.5 -> 3
		t.Errorf("5*1/2 = %d", got)
	}
	if got := (ratio{5, 2}).apply(1_000_000_000); got != 2_500_000_000 {
		t.Errorf("gpt-5.5 priority = %d", got)
	}
	if (ratio{3, 1}).apply(0) != 0 {
		t.Error("zero rate stays zero")
	}
}

func TestCacheWriteRates(t *testing.T) {
	const r = 10_000_000_000
	if got := effectiveCacheWriteRate("", protoAnthropic, "5m", r); got != 12_500_000_000 {
		t.Errorf("legacy 5m = %d", got)
	}
	if got := effectiveCacheWriteRate("", protoAnthropic, "1h", r); got != 20_000_000_000 {
		t.Errorf("legacy 1h = %d", got)
	}
	if got := effectiveCacheWriteRate("anthropic_5m", protoAnthropic, "5m", r); got != r {
		t.Errorf("anthropic_5m 5m = %d", got)
	}
	if got := effectiveCacheWriteRate("anthropic_5m", protoAnthropic, "1h", r); got != 16_000_000_000 {
		t.Errorf("anthropic_5m 1h = %d", got)
	}
	if got := effectiveCacheWriteRate("direct", protoAnthropic, "1h", r); got != r {
		t.Errorf("direct = %d", got)
	}
	if got := effectiveCacheWriteRate("", protoOpenAIChat, "1h", r); got != r {
		t.Errorf("non-anthropic = %d", got)
	}
	if got := effectiveCacheWriteRate("", protoAnthropic, "1h", 0); got != 0 {
		t.Errorf("zero rate = %d", got)
	}
	m5, m1 := CacheWriteMultipliers("")
	if m5 != 1.25 || m1 != 2.0 {
		t.Errorf("legacy multipliers %v %v", m5, m1)
	}
	m5, m1 = CacheWriteMultipliers("anthropic_5m")
	if m5 != 1.0 || m1 != 1.6 {
		t.Errorf("anthropic_5m multipliers %v %v", m5, m1)
	}
	m5, m1 = CacheWriteMultipliers("direct")
	if m5 != 1.0 || m1 != 1.0 {
		t.Errorf("direct multipliers %v %v", m5, m1)
	}
}

func TestCacheSplit(t *testing.T) {
	got := splitCacheWrite(protoAnthropic, 100, 30, 20, "1h")
	if got.Total != 100 || got.T5m != 30 || got.T1h != 70 {
		t.Errorf("remainder to 1h: %+v", got)
	}
	got = splitCacheWrite(protoAnthropic, 100, 30, 20, "5m")
	if got.T5m != 80 || got.T1h != 20 {
		t.Errorf("remainder to 5m: %+v", got)
	}
	got = splitCacheWrite(protoAnthropic, 40, 30, 20, "1h")
	if got.Total != 50 || got.T5m != 30 || got.T1h != 20 {
		t.Errorf("total raised to the split sum: %+v", got)
	}
	got = splitCacheWrite(protoAnthropic, 10, -5, -5, "")
	if got.Total != 10 || got.T5m != 10 || got.T1h != 0 {
		t.Errorf("negatives clamp: %+v", got)
	}
	got = splitCacheWrite(protoOpenAIChat, 100, 30, 20, "1h")
	if got.Split || got.Total != 100 || got.T5m != 0 || got.T1h != 0 {
		t.Errorf("non-anthropic keeps no split: %+v", got)
	}
}

func TestAnthropicCacheWriteCost(t *testing.T) {
	spec := priceSpec{Mode: PricingToken, CacheWrite: 10_000_000_000, Basis: "anthropic_5m"}
	mc, err := priceUsage(spec, usageCounts{CacheWrite: 2_000_000, CacheWrite5m: 1_000_000, CacheWrite1h: 1_000_000}, protoAnthropic, "5m", one)
	if err != nil {
		t.Fatal(err)
	}
	// 1M @10 USD + 1M @16 USD
	if mc.CacheW5m != 10_000_000_000 || mc.CacheW1h != 16_000_000_000 || mc.CacheW != 26_000_000_000 {
		t.Fatalf("%+v", mc)
	}
	// Recorded aggregate rate is back-derived: 26e9 per 2M tokens -> 13e9 per M.
	if mc.CacheRate != 13_000_000_000 {
		t.Fatalf("aggregate rate %d", mc.CacheRate)
	}
}

func TestTieredSelection(t *testing.T) {
	tc, err := parseTiersJSON(`{"tiers":[
		{"upToTokens":200000,"inputUSDPerMTokens":2,"outputUSDPerMTokens":10},
		{"upToTokens":0,"inputUSDPerMTokens":4,"outputUSDPerMTokens":20}]}`)
	if err != nil {
		t.Fatal(err)
	}
	spec := priceSpec{Mode: PricingTiered, Tiers: tc}
	mc, err := priceUsage(spec, usageCounts{Input: 150_000, CacheRead: 100_000, Output: 1_000_000}, protoAnthropic, "", one)
	if err != nil {
		t.Fatal(err)
	}
	if !mc.Tiered || mc.TierFrom != 200000 || mc.TierUpTo != nil {
		t.Fatalf("tier %+v", mc)
	}
	if mc.Input != 600_000_000 || mc.Output != 20_000_000_000 {
		t.Fatalf("cost %+v", mc)
	}
	mc, _ = priceUsage(spec, usageCounts{Input: 1000}, "", "", one)
	if mc.TierFrom != 0 || mc.TierUpTo == nil || *mc.TierUpTo != 200000 {
		t.Fatalf("first tier %+v", mc)
	}
	// Exactly at the limit stays in the first tier.
	mc, _ = priceUsage(spec, usageCounts{Input: 200000}, "", "", one)
	if mc.TierFrom != 0 {
		t.Fatalf("limit is inclusive: %+v", mc)
	}
}

func TestTieredAllBoundedFallsBackToLast(t *testing.T) {
	tc, err := parseTiersJSON(`{"tiers":[{"upToTokens":100,"inputUSDPerMTokens":1},{"upToTokens":500,"inputUSDPerMTokens":2}]}`)
	if err != nil {
		t.Fatal(err)
	}
	mc, _ := priceUsage(priceSpec{Mode: PricingTiered, Tiers: tc}, usageCounts{Input: 10_000}, "", "", one)
	if mc.TierFrom != 100 || mc.TierUpTo != nil {
		t.Fatalf("overflow tier: from=%d upTo=%v", mc.TierFrom, mc.TierUpTo)
	}
}

func TestTierValidation(t *testing.T) {
	bad := []string{
		``, `{}`, `{"tiers":[]}`, `not json`,
		`{"tiers":[{"upToTokens":100},{"upToTokens":100}]}`,
		`{"tiers":[{"upToTokens":100},{"upToTokens":50}]}`,
		`{"tiers":[{"upToTokens":0},{"upToTokens":100}]}`,
		`{"tiers":[{"upToTokens":100,"inputUSDPerMTokens":-1}]}`,
	}
	for _, b := range bad {
		if _, err := parseTiersJSON(b); err == nil {
			t.Errorf("accepted %q", b)
		}
	}
	var many []TierRate
	for i := 1; i <= 101; i++ {
		v := int64(i * 10)
		many = append(many, TierRate{UpToTokens: &v})
	}
	if _, err := normalizeTiers(&TierConfig{Tiers: many}); err == nil {
		t.Error("101 tiers accepted")
	}
	if _, err := normalizeTiers(&TierConfig{Tiers: many[:100]}); err != nil {
		t.Errorf("100 tiers rejected: %v", err)
	}
}

func TestCallAndDurationPricing(t *testing.T) {
	call, _ := priceUsage(priceSpec{Mode: PricingCall, Call: 7_000_000}, usageCounts{Calls: 0}, "", "", one)
	if call.Call != 7_000_000 {
		t.Errorf("call minimum is one: %d", call.Call)
	}
	call, _ = priceUsage(priceSpec{Mode: PricingCall, Call: 7_000_000}, usageCounts{Calls: 3}, "", "", ratio{2, 1})
	if call.Call != 42_000_000 {
		t.Errorf("3 calls x2 = %d", call.Call)
	}
	dur, _ := priceUsage(priceSpec{Mode: PricingDuration, Duration: 100_000_000}, usageCounts{Seconds: 8}, "", "", one)
	if dur.Duration != 800_000_000 {
		t.Errorf("duration = %d", dur.Duration)
	}
}

func TestScheduleValidation(t *testing.T) {
	overlap := `{"periods":[
		{"name":"a","weekdays":[1,2,3,4,5],"start":"09:00","end":"18:00","ratePercent":150},
		{"name":"b","weekdays":[1,2,3,4,5],"start":"08:00","end":"10:00","ratePercent":120}]}`
	if _, err := parseScheduleJSON(overlap); err == nil {
		t.Error("overlapping periods accepted")
	}
	cases := map[string]string{
		"rate 0":      `{"periods":[{"name":"a","weekdays":[1],"start":"09:00","end":"10:00","ratePercent":0}]}`,
		"rate 10001":  `{"periods":[{"name":"a","weekdays":[1],"start":"09:00","end":"10:00","ratePercent":10001}]}`,
		"empty name":  `{"periods":[{"name":"  ","weekdays":[1],"start":"09:00","end":"10:00","ratePercent":100}]}`,
		"long name":   `{"periods":[{"name":"123456789012345678901234567890123","weekdays":[1],"start":"09:00","end":"10:00","ratePercent":100}]}`,
		"no weekdays": `{"periods":[{"name":"a","weekdays":[],"start":"09:00","end":"10:00","ratePercent":100}]}`,
		"dup days":    `{"periods":[{"name":"a","weekdays":[1,1],"start":"09:00","end":"10:00","ratePercent":100}]}`,
		"bad day":     `{"periods":[{"name":"a","weekdays":[7],"start":"09:00","end":"10:00","ratePercent":100}]}`,
		"bad time":    `{"periods":[{"name":"a","weekdays":[1],"start":"25:00","end":"10:00","ratePercent":100}]}`,
		"bad format":  `{"periods":[{"name":"a","weekdays":[1],"start":"9am","end":"10:00","ratePercent":100}]}`,
		"start=end":   `{"periods":[{"name":"a","weekdays":[1],"start":"09:00","end":"09:00","ratePercent":100}]}`,
	}
	for name, js := range cases {
		if _, err := parseScheduleJSON(js); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	var b = `{"periods":[`
	for i := 0; i < 33; i++ {
		if i > 0 {
			b += ","
		}
		b += `{"name":"p","weekdays":[1],"start":"01:00","end":"02:00","ratePercent":100}`
	}
	b += `]}`
	if _, err := parseScheduleJSON(b); err == nil {
		t.Error("33 periods accepted")
	}
	for _, off := range []string{``, `{}`, `{"periods":[]}`, `null`} {
		sc, err := parseScheduleJSON(off)
		if err != nil || sc != nil {
			t.Errorf("%q should be disabled: %v %v", off, sc, err)
		}
		if marshalSchedule(sc) != "{}" {
			t.Error("disabled schedule stores {}")
		}
	}
}

func TestScheduleNormalizationAndMatching(t *testing.T) {
	sc, err := parseScheduleJSON(`{"periods":[{"name":" Night ","weekdays":[5],"start":"22:00","end":"6:00","ratePercent":80}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Periods[0].Name != "Night" || sc.Periods[0].End != "06:00" {
		t.Fatalf("normalized %+v", sc.Periods[0])
	}
	// 2026-03-06 is a Friday.
	fri := func(h, m int) time.Time { return time.Date(2026, 3, 6, h, m, 0, 0, time.UTC) }
	sat := func(h, m int) time.Time { return time.Date(2026, 3, 7, h, m, 0, 0, time.UTC) }
	if sc.match(fri(21, 59)) != nil {
		t.Error("before start")
	}
	if p := sc.match(fri(22, 0)); p == nil || p.RatePercent != 80 {
		t.Error("Friday 22:00 should match")
	}
	if sc.match(sat(5, 59)) == nil {
		t.Error("Saturday 05:59 belongs to Friday's span")
	}
	if sc.match(sat(6, 0)) != nil {
		t.Error("end is exclusive")
	}
	if sc.match(time.Date(2026, 3, 5, 23, 0, 0, 0, time.UTC)) != nil {
		t.Error("Thursday must not match")
	}
}

func TestScheduleWrapsSundayNightIntoMonday(t *testing.T) {
	sc, err := parseScheduleJSON(`{"periods":[{"name":"w","weekdays":[0],"start":"23:00","end":"01:00","ratePercent":50}]}`)
	if err != nil {
		t.Fatal(err)
	}
	sun := time.Date(2026, 3, 1, 23, 30, 0, 0, time.UTC)
	mon := time.Date(2026, 3, 2, 0, 30, 0, 0, time.UTC)
	if sc.match(sun) == nil || sc.match(mon) == nil {
		t.Error("Sunday night must reach Monday")
	}
	// A Saturday-night period wraps to Sunday; combined with the above they do not overlap.
	if _, err := parseScheduleJSON(`{"periods":[
		{"name":"a","weekdays":[6],"start":"23:00","end":"02:00","ratePercent":50},
		{"name":"b","weekdays":[0],"start":"01:00","end":"03:00","ratePercent":50}]}`); err == nil {
		t.Error("Saturday-night span overlaps Sunday 01:00")
	}
	// Midnight crossing from Sunday collides with an early Monday period.
	if _, err := parseScheduleJSON(`{"periods":[
		{"name":"a","weekdays":[0],"start":"23:00","end":"02:00","ratePercent":50},
		{"name":"b","weekdays":[1],"start":"01:00","end":"03:00","ratePercent":50}]}`); err == nil {
		t.Error("Sunday-night span overlaps Monday 01:00")
	}
}

func TestScheduleAppliesToPriceOnlyWhenNotFree(t *testing.T) {
	sc, _ := parseScheduleJSON(`{"periods":[{"name":"all","weekdays":[0,1,2,3,4,5,6],"start":"00:00","end":"23:59","ratePercent":200}]}`)
	at := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	if (priceSpec{Schedule: sc}).scheduleAt(at, time.UTC) == nil {
		t.Error("paid price should see the period")
	}
	if (priceSpec{Schedule: sc, IsFree: true}).scheduleAt(at, time.UTC) != nil {
		t.Error("free price must ignore schedules")
	}
}

func TestNativeToolCatalog(t *testing.T) {
	if e, ok := nativeEntryFor("anthropic_messages", "web_search"); !ok || e.Key != "anthropic.web_search_20260209" || e.DefaultPrice != 10_000_000 {
		t.Errorf("anthropic: %+v %v", e, ok)
	}
	if e, ok := nativeEntryFor("openai_responses", "web_search_preview"); !ok || e.DefaultPrice != 25_000_000 {
		t.Errorf("openai preview: %+v", e)
	}
	if e, ok := nativeEntryFor("openai_responses", "image_generation"); !ok || e.DefaultPrice != 0 {
		t.Errorf("image generation is unmetered: %+v", e)
	}
	if e, ok := nativeEntryFor("xai_responses", "x_search"); !ok || e.DefaultPrice != 5_000_000 {
		t.Errorf("xai x search: %+v", e)
	}
	if e, ok := nativeEntryFor("xai_responses", "attachment_search"); !ok || e.DefaultPrice != 10_000_000 {
		t.Errorf("xai attachment: %+v", e)
	}
	if e, ok := nativeEntryFor("xai_responses", "collections_search"); !ok || e.DefaultPrice != 2_500_000 {
		t.Errorf("xai collections: %+v", e)
	}
	if _, ok := nativeEntryFor("mystery", "web_search"); ok {
		t.Error("unknown protocol must not resolve")
	}
	keys := map[string]bool{}
	for _, e := range nativeToolCatalog {
		if keys[e.Key] {
			t.Errorf("duplicate catalog key %q", e.Key)
		}
		keys[e.Key] = true
	}
}

func TestNativeOverridesNormalization(t *testing.T) {
	in := map[string]NativeToolOverride{"openai.web_search": {PriceNanousd: 1, Unit: "weird", PriceLabel: "x", Billable: false}}
	out, err := normalizeNativeOverrides(in)
	if err != nil {
		t.Fatal(err)
	}
	ov := out["openai.web_search"]
	if ov.Unit != "call" || ov.PriceLabel != "" || !ov.Billable || ov.PriceNanousd != 1 {
		t.Errorf("normalized %+v", ov)
	}
	if _, err := normalizeNativeOverrides(map[string]NativeToolOverride{"nope": {}}); err == nil {
		t.Error("unknown key accepted")
	}
	if _, err := normalizeNativeOverrides(map[string]NativeToolOverride{"openai.web_search": {PriceNanousd: -1}}); err == nil {
		t.Error("negative price accepted")
	}
	if nativePricingSource(map[string]NativeToolOverride{}) != "provider_official_defaults" {
		t.Error("empty overrides are defaults")
	}
	if nativePricingSource(defaultNativeOverrides()) != "provider_official_defaults" {
		t.Error("explicit defaults are defaults")
	}
	if nativePricingSource(map[string]NativeToolOverride{"openai.web_search": {PriceNanousd: 1}}) != "admin_configured" {
		t.Error("override must be admin_configured")
	}
	parsed, err := parseNativeOverrides(defaultNativePricingJSON())
	if err != nil || len(parsed) != len(nativeToolCatalog)-len(nativeUsageOnly) {
		t.Errorf("default table round trip: %v %d", err, len(parsed))
	}
}
