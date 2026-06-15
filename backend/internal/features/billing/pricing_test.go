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
