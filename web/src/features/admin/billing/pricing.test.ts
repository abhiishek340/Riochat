import { describe, expect, it } from "vitest";
import type { ModelPrice } from "../lib/wire";
import { bodyFromForm, defaultTiers, exportPricing, filterRows, formFromPrice, freeToggleBody, isVideoModel, orderRows, parseImport, parsePrice, tiersJson } from "./pricing";

function price(over: Partial<ModelPrice> = {}): ModelPrice {
  return {
    id: 1, platformModelName: "m1", modelVendor: "openai", modelIcon: "", currency: "USD", isFree: false, pricingMode: "token", inputUSDPerMTokens: 3, cacheReadUSDPerMTokens: 0.3, cacheWriteUSDPerMTokens: 3.75,
    outputUSDPerMTokens: 15, callUSDPerCall: 0, durationUSDPerSecond: 0, tieredPricingJSON: "", schedulePricingJSON: "", createdAt: "", updatedAt: "", ...over,
  };
}

describe("prices", () => {
  it("count negative and non-numeric input as zero", () => {
    expect(parsePrice("1.5")).toBe(1.5);
    expect(parsePrice("-3")).toBe(0);
    expect(parsePrice("abc")).toBe(0);
    expect(parsePrice("")).toBe(0);
  });
  it("store only the prices of the chosen mode", () => {
    const token = bodyFromForm("m", { ...formFromPrice(price()), call: "9", duration: "9" });
    expect([token.inputUSDPerMTokens, token.callUSDPerCall, token.durationUSDPerSecond]).toEqual([3, 0, 0]);
    const call = bodyFromForm("m", { ...formFromPrice(price()), mode: "call", call: "0.02" });
    expect([call.inputUSDPerMTokens, call.outputUSDPerMTokens, call.callUSDPerCall]).toEqual([0, 0, 0.02]);
  });
  it("start the tiered editor with two tiers", () => {
    expect(defaultTiers().map((t) => t.upToTokens)).toEqual(["200000", "0"]);
    expect(JSON.parse(tiersJson(defaultTiers())).tiers[0]).toMatchObject({ upToTokens: 200000, inputUSDPerMTokens: 0 });
    const body = bodyFromForm("m", { ...formFromPrice(undefined), mode: "tiered" });
    expect(JSON.parse(body.tieredPricingJSON).tiers).toHaveLength(2);
    expect(bodyFromForm("m", formFromPrice(undefined)).tieredPricingJSON).toBe("");
  });
  it("re-send the existing price with only the free flag changed", () => {
    const body = freeToggleBody("m1", price(), true);
    expect(body).toMatchObject({ isFree: true, pricingMode: "token", inputUSDPerMTokens: 3, outputUSDPerMTokens: 15 });
    expect(freeToggleBody("new", undefined, true)).toMatchObject({ isFree: true, pricingMode: "token", inputUSDPerMTokens: 0 });
  });
  it("recognise video models", () => {
    expect(isVideoModel('["video_gen"]')).toBe(true);
    expect(isVideoModel('["chat"]')).toBe(false);
    expect(isVideoModel("nope")).toBe(false);
  });
});

describe("rows", () => {
  it("float unpriced models above priced ones and keep admin order otherwise", () => {
    const rows = [
      { name: "a", sortOrder: 1 },
      { name: "b", sortOrder: 2 },
      { name: "c", sortOrder: 3 },
    ];
    expect(orderRows(rows, new Set(["a"])).map((r) => r.name)).toEqual(["b", "c", "a"]);
  });
  it("filter by search, vendor, mode, free and configured", () => {
    const rows = [{ name: "gpt-x", vendor: "openai" }, { name: "claude-y", vendor: "anthropic" }, { name: "free-z", vendor: "openai" }];
    const prices = new Map([["gpt-x", price({ platformModelName: "gpt-x" })], ["free-z", price({ platformModelName: "free-z", isFree: true, pricingMode: "call" })]]);
    const base = { q: "", vendor: "", mode: "", free: "" as const, configured: "" as const };
    expect(filterRows(rows, prices, { ...base, q: "CLAUDE" }).map((r) => r.name)).toEqual(["claude-y"]);
    expect(filterRows(rows, prices, { ...base, vendor: "openai" }).map((r) => r.name)).toEqual(["gpt-x", "free-z"]);
    expect(filterRows(rows, prices, { ...base, mode: "call" }).map((r) => r.name)).toEqual(["free-z"]);
    expect(filterRows(rows, prices, { ...base, free: "free" }).map((r) => r.name)).toEqual(["free-z"]);
    expect(filterRows(rows, prices, { ...base, configured: "unconfigured" }).map((r) => r.name)).toEqual(["claude-y"]);
  });
});

describe("export and import", () => {
  it("exports sorted by name with off-mode prices at zero", () => {
    const out = exportPricing([price({ platformModelName: "b", callUSDPerCall: 5 }), price({ platformModelName: "a" })]);
    expect(Object.keys(out)).toEqual(["a", "b"]);
    expect(out.b?.callUSDPerCall).toBe(0);
    expect(out.b?.tieredPricing).toBeUndefined();
  });
  const known = new Map([["m1", { kindsJSON: '["chat"]' }], ["vid", { kindsJSON: '["video_gen"]' }]]);
  const kinds = (text: string) => {
    const r = parseImport(text, known);
    return r.ok ? "ok" : r.errors.map((e) => e.kind);
  };
  it("round-trips an export", () => {
    const text = JSON.stringify(exportPricing([price({ platformModelName: "m1" })]));
    const result = parseImport(text, known);
    expect(result.ok && result.entries[0]).toMatchObject({ platformModelName: "m1", pricingMode: "token", inputUSDPerMTokens: 3 });
  });
  it("rejects bad files with specific reasons", () => {
    expect(kinds("nope")).toEqual(["syntax"]);
    expect(kinds("[]")).toEqual(["notObject"]);
    expect(kinds("{}")).toEqual(["empty"]);
    expect(kinds('{"m1": 3}')).toEqual(["entryNotObject"]);
    expect(kinds('{"m1": {"pricingMode":"weird"}}')).toEqual(["mode"]);
    expect(kinds('{"m1": {"pricingMode":"token","cacheWritePriceBasis":"x"}}')).toEqual(["basis"]);
    expect(kinds('{"m1": {"pricingMode":"token","inputUSDPerMTokens":-1}}')).toEqual(["number"]);
    expect(kinds('{"m1": {"pricingMode":"tiered"}}')).toEqual(["tiers"]);
    expect(kinds('{"m1": {"pricingMode":"tiered","tieredPricing":{"tiers":[{"upToTokens":-5}]}}}')).toEqual(["tierLimit"]);
  });
  it("allows duration pricing only for video models", () => {
    expect(kinds('{"m1": {"pricingMode":"duration"}}')).toEqual(["durationNotVideo"]);
    expect(kinds('{"vid": {"pricingMode":"duration","durationUSDPerSecond":0.1}}')).toBe("ok");
  });
  it("catches duplicates after trimming and empty names", () => {
    expect(kinds('{"m1": {"pricingMode":"token"}, " m1 ": {"pricingMode":"token"}}')).toEqual(["duplicate"]);
    expect(kinds('{" ": {"pricingMode":"token"}}')).toEqual(["emptyName"]);
  });
  it("aborts on unknown models and lists at most five", () => {
    const text = JSON.stringify(Object.fromEntries(["a", "b", "c", "d", "e", "f", "g"].map((n) => [n, { pricingMode: "token" }])));
    const result = parseImport(text, known);
    expect(!result.ok && result.errors).toEqual([{ kind: "unknownModels", names: ["a", "b", "c", "d", "e"], more: 2 }]);
  });
  it("validates schedule pricing", () => {
    const bad = JSON.stringify({ m1: { pricingMode: "token", schedulePricing: { periods: [{ name: "a", weekdays: [1], start: "09:00", end: "12:00", ratePercent: 100 }, { name: "b", weekdays: [1], start: "11:00", end: "13:00", ratePercent: 100 }] } } });
    expect(kinds(bad)).toEqual(["schedule"]);
  });
  it("defaults the currency and free flag", () => {
    const r = parseImport('{"m1": {"pricingMode":"call","callUSDPerCall":0.5}}', known);
    expect(r.ok && r.entries[0]).toMatchObject({ currency: "USD", isFree: false, callUSDPerCall: 0.5 });
  });
});
