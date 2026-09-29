import { describe, expect, it } from "vitest";
import { billableOutput, breakdownOf, totalTokens, upstreamUsageOf, type UsageCounts } from "./breakdown";

const counts: UsageCounts = { inputTokens: 1000, cacheReadTokens: 200, cacheWriteTokens: 100, cacheWrite5mTokens: 0, cacheWrite1hTokens: 0, outputTokens: 500, reasoningTokens: 300, callCount: 1, durationSeconds: 0 };

describe("token totals", () => {
  it("adds reasoning to the billable output and sums every part", () => {
    expect(billableOutput(counts)).toBe(800);
    expect(totalTokens(counts)).toBe(1000 + 200 + 100 + 800);
  });
});

describe("breakdownOf", () => {
  const snapshot = JSON.stringify({
    pricing_mode: "token",
    rate_multiplier: 1.5,
    input_nanousd_per_m_tokens: 3_000_000_000,
    input_billed_nanousd: 4_500_000,
    output_nanousd_per_m_tokens: 15_000_000_000,
    output_billed_nanousd: 12_000_000,
    cache_read_nanousd_per_m_tokens: 300_000_000,
    cache_read_billed_nanousd: 90_000,
    schedule_period_name: "peak",
    schedule_rate_percent: 120,
    fast_mode: true,
    billing_service_tier: "priority",
    upstream_usage: { prompt_tokens: 1000 },
  });
  it("lists the priced lines with rates and amounts in USD", () => {
    const b = breakdownOf(snapshot, counts, 0.0166);
    expect(b?.rows.map((r) => r.kind)).toEqual(["input", "cacheRead", "output"]);
    expect(b?.rows[0]).toMatchObject({ quantity: 1000, rateUSD: 3, billedUSD: 0.0045 });
    expect(b?.rows[2]).toMatchObject({ quantity: 800, rateUSD: 15, billedUSD: 0.012 });
    expect(b?.rateMultiplier).toBe(1.5);
    expect(b?.schedule).toEqual({ name: "peak", percent: 120 });
    expect(b?.fastMode).toBe(true);
    expect(b?.serviceTier).toBe("priority");
  });
  it("shows the five-minute and one-hour cache split when the snapshot has one", () => {
    const b = breakdownOf(JSON.stringify({ cache_write_5m_tokens: 60, cache_write_1h_tokens: 40, cache_write_5m_billed_nanousd: 1_000_000, cache_write_1h_billed_nanousd: 2_000_000 }), counts, 0);
    expect(b?.rows.map((r) => r.kind)).toEqual(["cacheWrite5m", "cacheWrite1h"]);
  });
  it("marks a free model and reads tiered ranges", () => {
    const b = breakdownOf(JSON.stringify({ is_free_model: true, pricing_mode: "tiered", tiered_from_tokens: 0, tiered_up_to_tokens: 200000 }), counts, 0);
    expect(b?.free).toBe(true);
    expect(b?.tier).toEqual({ from: 0, upTo: 200000 });
  });
  it("returns null for text that is not a snapshot", () => {
    expect(breakdownOf("", counts, 0)).toBeNull();
    expect(breakdownOf("[1]", counts, 0)).toBeNull();
    expect(breakdownOf("nope", counts, 0)).toBeNull();
  });
  it("exposes the raw upstream usage", () => {
    expect(upstreamUsageOf(snapshot)).toEqual({ prompt_tokens: 1000 });
    expect(upstreamUsageOf("{}")).toBeUndefined();
  });
});
