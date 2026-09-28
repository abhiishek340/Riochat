import { describe, expect, it } from "vitest";
import { buildBreakdown, NANO, tokenAmount, type LedgerFacts } from "./billing-breakdown";

const facts = (over: Partial<LedgerFacts> = {}): LedgerFacts => ({
  isFreeModel: false,
  inputTokens: 0,
  outputTokens: 0,
  reasoningTokens: 0,
  cacheReadTokens: 0,
  cacheWriteTokens: 0,
  callCount: 1,
  durationSeconds: 0,
  billedNano: 0,
  ...over,
});

describe("tokenAmount", () => {
  it("uses the stored amount when positive, else tokens * rate / 1M", () => {
    expect(tokenAmount(1000, 3_000_000_000, 5)).toBe(5);
    expect(tokenAmount(1000, 3_000_000_000, 0)).toBe(3_000_000);
  });
});

describe("token mode", () => {
  it("lists input, output (with reasoning), cache read and write lines", () => {
    const snap = JSON.stringify({
      pricing_mode: "token",
      input_nanousd_per_m_tokens: 2 * NANO,
      output_nanousd_per_m_tokens: 8 * NANO,
      cache_read_nanousd_per_m_tokens: NANO,
      input_billed_nanousd: 2000,
    });
    const b = buildBreakdown(snap, facts({ inputTokens: 1000, outputTokens: 400, reasoningTokens: 100, cacheReadTokens: 500 }));
    expect(b.mode).toBe("token");
    expect(b.lines.map((l) => l.kind)).toEqual(["input", "output", "cacheRead"]);
    expect(b.lines[0]).toMatchObject({ quantity: 1000, billedNano: 2000 }); // stored value wins
    expect(b.lines[1]).toMatchObject({ quantity: 500, billedNano: 4_000_000 }); // 500 * 8e9 / 1e6
    expect(b.totalNano).toBe(2000 + 4_000_000 + 500_000);
  });
});

describe("call and duration modes", () => {
  it("falls back to count * rate", () => {
    const call = buildBreakdown(JSON.stringify({ pricing_mode: "call", call_nanousd_per_call: 7000 }), facts({ callCount: 3 }));
    expect(call.lines).toEqual([{ kind: "call", quantity: 3, rateNano: 7000, billedNano: 21000 }]);
    const dur = buildBreakdown(JSON.stringify({ pricing_mode: "duration", duration_nanousd_per_second: 100, duration_billed_nanousd: 900 }), facts({ durationSeconds: 5 }));
    expect(dur.lines[0]).toMatchObject({ kind: "duration", quantity: 5, billedNano: 900 });
  });
});

describe("tiered mode", () => {
  it("reports the price tier", () => {
    const b = buildBreakdown(JSON.stringify({ pricing_mode: "tiered", tiered_from_tokens: 1000, tiered_up_to_tokens: null, input_nanousd_per_m_tokens: NANO }), facts({ inputTokens: 10 }));
    expect(b.tier).toEqual({ from: 1000, upTo: null });
  });
});

describe("free models and service items", () => {
  it("reads zero with a free note when nothing is billed", () => {
    const b = buildBreakdown(JSON.stringify({ is_free_model: true, input_nanousd_per_m_tokens: NANO }), facts({ isFreeModel: true, inputTokens: 1000 }));
    expect(b.free).toBe(true);
    expect(b.totalNano).toBe(0);
  });
  it("bills a free model's service charges normally", () => {
    const snap = JSON.stringify({ service_items: [{ service_name: "Web search", call_count: 2, billed_nanousd: 5000 }] });
    const b = buildBreakdown(snap, facts({ isFreeModel: true }));
    expect(b.serviceNano).toBe(5000);
    expect(b.totalNano).toBe(5000);
    expect(b.services[0]).toEqual({ name: "Web search", calls: 2, unitNano: 2500, billedNano: 5000 });
  });
  it("adds attached service lines to the total", () => {
    const b = buildBreakdown("{}", facts(), [{ name: "Title", calls: 1, unitNano: 100, billedNano: 100 }]);
    expect(b.totalNano).toBe(100);
  });
  it("degrades a damaged snapshot to an empty one", () => {
    const b = buildBreakdown("{broken", facts({ inputTokens: 5 }));
    expect(b.mode).toBe("token");
    expect(b.totalNano).toBe(0);
  });
});
