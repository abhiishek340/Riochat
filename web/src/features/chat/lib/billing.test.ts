import { describe, expect, it } from "vitest";
import type { BillingCostDTO } from "../types";
import { buildBreakdown, formatMoney, shouldShowCost, showsFreeNote, tokenLineParts, USD } from "./billing";

const cost = (snap: Record<string, unknown>, nano: number): BillingCostDTO => ({
  billingMode: "usage", billedCurrency: "USD", billedNanousd: nano, billedUSD: nano / 1e9, pricingSnapshotJSON: JSON.stringify(snap),
});
const usage = { input: 1000, output: 0, reasoning: 0, cacheRead: 0, cacheWrite: 0 };

describe("formatMoney", () => {
  it("trims zeros down to two decimals", () => {
    expect(formatMoney(3_000_000_000)).toBe("$3.00");
    expect(formatMoney(3_000_000)).toBe("$0.003");
    expect(formatMoney(500_000_000)).toBe("$0.50");
    expect(formatMoney(0)).toBe("$0");
    expect(formatMoney(1_000_000_000, { code: "CNY", rate: 7.2 })).toBe("¥7.20");
  });
});

describe("buildBreakdown", () => {
  it("prices token usage line by line", () => {
    const b = buildBreakdown(cost({ pricing_mode: "token", input_nanousd_per_m_tokens: 3_000_000_000, input_billed_nanousd: 3_000_000, output_billed_nanousd: 0 }, 3_000_000), usage);
    expect(b?.lines).toHaveLength(1);
    const line = b?.lines[0];
    expect(line?.kind).toBe("input");
    expect(tokenLineParts(line!, USD)).toEqual({ tokens: "1,000", rate: "$3.00", amount: "$0.003" });
    expect(b?.totalNano).toBe(3_000_000);
  });

  it("counts reasoning tokens with output", () => {
    const b = buildBreakdown(cost({ pricing_mode: "token", output_nanousd_per_m_tokens: 1, output_billed_nanousd: 10 }, 10), { ...usage, input: 0, output: 5, reasoning: 7 });
    expect(b?.lines[0]).toMatchObject({ kind: "output", tokens: 12 });
  });

  it("uses a single line for per-call and per-second pricing", () => {
    expect(buildBreakdown(cost({ pricing_mode: "call", call_nanousd_per_call: 5_000_000, call_billed_nanousd: 5_000_000 }, 5_000_000), usage)?.lines[0]).toMatchObject({ kind: "call", amountNano: 5_000_000 });
    expect(buildBreakdown(cost({ pricing_mode: "duration", duration_seconds: 4, duration_nanousd_per_second: 1, duration_billed_nanousd: 4 }, 4), usage)?.lines[0]).toMatchObject({ kind: "duration", seconds: 4 });
  });

  it("shows the matched tier range", () => {
    const b = buildBreakdown(cost({ pricing_mode: "token", tiered_from_tokens: 200_000, tiered_up_to_tokens: null, input_billed_nanousd: 1 }, 1), usage);
    expect(b?.mode).toBe("tiered");
    expect(b?.tier).toEqual({ from: 200_000, upTo: null });
  });

  it("splits mixed cache writes and notes the schedule rate", () => {
    const b = buildBreakdown(
      cost({ cache_write_5m_tokens: 10, cache_write_1h_tokens: 20, cache_write_5m_billed_nanousd: 1, cache_write_1h_billed_nanousd: 2, schedule_period_name: "night", schedule_rate_percent: 50 }, 3),
      { ...usage, input: 0, cacheWrite: 30 },
    );
    expect(b?.hasMixedCache).toBe(true);
    expect(b?.lines.map((l) => l.kind)).toEqual(["cacheWrite5m", "cacheWrite1h"]);
    expect(b?.schedule).toEqual({ name: "night", percent: 50 });
  });

  it("free models show the note only when the whole bill is zero", () => {
    const zero = buildBreakdown(cost({ is_free_model: true }, 0), usage)!;
    expect(showsFreeNote(zero)).toBe(true);
    const withTool = buildBreakdown(cost({ is_free_model: true, service_items: [{ service_name: "Search", call_count: 2, billed_nanousd: 50_000_000 }] }, 50_000_000), usage)!;
    expect(showsFreeNote(withTool)).toBe(false);
    expect(withTool.items[0]).toMatchObject({ name: "Search", count: 2 });
    expect(withTool.totalNano).toBe(50_000_000);
  });

  it("returns null for unreadable snapshots", () => {
    expect(buildBreakdown({ ...cost({}, 0), pricingSnapshotJSON: "nope" }, usage)).toBeNull();
    expect(buildBreakdown(null, usage)).toBeNull();
  });

  it("hides the cost badge in self-hosted mode", () => {
    expect(shouldShowCost(cost({}, 1), "self")).toBe(false);
    expect(shouldShowCost(cost({}, 1), "usage")).toBe(true);
    expect(shouldShowCost(null, "usage")).toBe(false);
  });
});
