import { describe, expect, it } from "vitest";
import type { DaySummary, LedgerItem, Metrics } from "./types";
import { averageLatency, dayLatencyFor, groupLedger, isServiceOnly, modelSeries, OTHER_SERIES, seriesKeyOf, visibleDays } from "./usage-logic";

const m = (over: Partial<Metrics> = {}): Metrics => ({ recordCount: 0, totalTokens: 0, callCount: 0, avgLatencyMS: 0, billedUSD: 0, ...over });
const day = (date: string, models: [string, number, number?, number?][]): DaySummary => ({
  usageDate: date,
  ...m({ totalTokens: models.reduce((s, x) => s + x[1], 0) }),
  models: models.map(([name, tokens, lat = 0, rec = 0]) => ({ platformModelName: name, ...m({ totalTokens: tokens, avgLatencyMS: lat, recordCount: rec }) })),
});
const item = (id: number, over: Partial<LedgerItem> = {}): LedgerItem => ({
  id, conversationID: 1, platformModelName: "gpt", isFreeModel: false, billingAt: `2025-01-01T00:00:0${id}Z`, createdAt: "", inputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0,
  outputTokens: 0, reasoningTokens: 0, callCount: 1, durationSeconds: 0, latencyMS: 0, billedUSD: 0, balanceAfterUSD: null, pricingSnapshotJSON: "", ...over,
});

describe("averageLatency", () => {
  it("weights by record count and ignores entries missing either value", () => {
    expect(averageLatency([m({ avgLatencyMS: 100, recordCount: 1 }), m({ avgLatencyMS: 400, recordCount: 3 }), m({ avgLatencyMS: 0, recordCount: 5 }), m({ avgLatencyMS: 900, recordCount: 0 })])).toBe(325);
    expect(averageLatency([])).toBe(0);
  });
});

describe("visibleDays", () => {
  it("drops days after today and sorts ascending", () => {
    const now = new Date(2025, 0, 10, 12);
    const out = visibleDays([day("2025-01-12", []), day("2025-01-09", []), day("2025-01-10T00:00:00+08:00", []), day("2025-01-11", [])], now);
    expect(out.map((d) => d.usageDate.slice(0, 10))).toEqual(["2025-01-09", "2025-01-10"]);
  });
});

describe("modelSeries", () => {
  const many = (n: number) => day("2025-01-01", Array.from({ length: n }, (_, i) => [`m${String(i).padStart(2, "0")}`, 100 - i] as [string, number]));
  it("keeps ten models and merges the rest into one series", () => {
    const series = modelSeries([many(13)]);
    expect(series).toHaveLength(11);
    expect(series.at(-1)).toMatchObject({ key: OTHER_SERIES, total: 90 + 89 + 88 });
    expect(seriesKeyOf("m00", series)).toBe("m00");
    expect(seriesKeyOf("m12", series)).toBe(OTHER_SERIES);
  });
  it("has no 'other' series for ten or fewer models and breaks ties by name", () => {
    const series = modelSeries([day("2025-01-01", [["b", 5], ["a", 5]])]);
    expect(series.map((s) => s.key)).toEqual(["a", "b"]);
  });
  it("is empty without model breakdowns", () => {
    expect(modelSeries([day("2025-01-01", [])])).toEqual([]);
  });
  it("recomputes a day's latency over visible series only", () => {
    const d = day("2025-01-01", [["a", 10, 100, 1], ["b", 10, 500, 1]]);
    const series = modelSeries([d]);
    expect(dayLatencyFor(d, series, new Set())).toBe(300);
    expect(dayLatencyFor(d, series, new Set(["b"]))).toBe(100);
  });
});

describe("usage log grouping", () => {
  const svc = JSON.stringify({ service_items: [{ service_name: "Title", billed_nanousd: 10 }] });
  it("detects service-only entries by flag or by items with no main charge", () => {
    expect(isServiceOnly(item(1, { pricingSnapshotJSON: JSON.stringify({ service_only: true }) }))).toBe(true);
    expect(isServiceOnly(item(1, { pricingSnapshotJSON: JSON.stringify({ service_only: false, service_items: [{}] }) }))).toBe(false);
    expect(isServiceOnly(item(1, { pricingSnapshotJSON: svc }))).toBe(true);
    expect(isServiceOnly(item(1, { pricingSnapshotJSON: JSON.stringify({ service_items: [{}], input_billed_nanousd: 5 }) }))).toBe(false);
    expect(isServiceOnly(item(1))).toBe(false);
  });
  it("folds a service entry into the nearest row of the same conversation", () => {
    const a = item(1);
    const b = item(6, { pricingSnapshotJSON: svc });
    const c = item(9);
    const rows = groupLedger([a, b, c]);
    expect(rows.map((r) => r.item.id)).toEqual([1, 9]);
    expect(rows.find((r) => r.item.id === 9)?.attached.map((x) => x.id)).toEqual([6]);
    expect(rows.find((r) => r.item.id === 1)?.attached).toEqual([]);
  });
  it("drops orphans that match no row", () => {
    const orphan = item(2, { conversationID: 77, pricingSnapshotJSON: svc });
    expect(groupLedger([item(1), orphan]).map((r) => r.item.id)).toEqual([1]);
  });
});
