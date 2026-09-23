import { describe, expect, it } from "vitest";
import {
  dailyStats,
  policyDirty,
  serviceBody,
  serviceDirty,
  serviceDraftOf,
  togglePolicyCategory,
  totalsOf,
  validatePolicy,
  validateRange,
  validateService,
  type ModerationConfig,
  type Policy,
  type StatItem,
} from "./rules";

const emptyPolicy: Policy = { version: 1, inputTextCategories: [], inputImageCategories: [], outputTextCategories: [], outputImageCategories: [] };
const saved: ModerationConfig = { enabled: false, baseUrl: "https://mod.test/v1", model: "omni", hasAPIKey: false, timeoutSeconds: 10, maxConcurrency: 4, queueCapacity: 100, policy: emptyPolicy };

describe("range checks", () => {
  it("accepts digits inside the bounds only", () => {
    expect(validateRange("timeoutSeconds", "60")).toBeNull();
    expect(validateRange("timeoutSeconds", "61")?.args).toMatchObject({ min: 1, max: 60 });
    expect(validateRange("maxConcurrency", "0")).not.toBeNull();
    expect(validateRange("queueCapacity", "4096")).toBeNull();
    expect(validateRange("queueCapacity", "1e3")).not.toBeNull();
    expect(validateRange("queueCapacity", "")).not.toBeNull();
  });
});

describe("validateService", () => {
  const draft = { ...serviceDraftOf(saved), enabled: true };
  const withCategory: Policy = { ...emptyPolicy, inputTextCategories: ["violence"] };
  it("never blocks disabling", () => {
    expect(validateService({ ...draft, enabled: false }, saved, emptyPolicy)).toBeNull();
  });
  it("needs a category when switching on", () => {
    expect(validateService(draft, saved, emptyPolicy)?.key).toBe("adminModeration.problems.noCategory");
  });
  it("needs a key, typed or stored", () => {
    expect(validateService(draft, saved, withCategory)?.key).toBe("adminModeration.problems.keyRequired");
    expect(validateService({ ...draft, apiKey: "k" }, saved, withCategory)).toBeNull();
    expect(validateService(draft, { ...saved, hasAPIKey: true }, withCategory)).toBeNull();
  });
  it("checks the numbers last", () => {
    expect(validateService({ ...draft, apiKey: "k", timeoutSeconds: "61" }, saved, withCategory)?.key).toBe("adminModeration.problems.range");
  });
});

describe("serviceBody", () => {
  it("disabling sends only the flag", () => {
    expect(serviceBody({ ...serviceDraftOf(saved), enabled: false }, { ...saved, enabled: true }, emptyPolicy)).toEqual({ enabled: false });
  });
  it("enabling sends the key only when typed and the policy only on the switch-on", () => {
    const policy: Policy = { ...emptyPolicy, inputTextCategories: ["hate"] };
    const on = serviceBody({ ...serviceDraftOf(saved), enabled: true, apiKey: " sk " }, saved, policy);
    expect(on).toMatchObject({ enabled: true, apiKey: "sk", timeoutSeconds: 10, policy: { inputTextCategories: ["hate"] } });
    const already = serviceBody({ ...serviceDraftOf({ ...saved, enabled: true }), enabled: true }, { ...saved, enabled: true, hasAPIKey: true }, policy);
    expect(already.apiKey).toBeUndefined();
    expect(already.policy).toBeUndefined();
  });
});

describe("dirty tracking", () => {
  it("counts a typed key as a change", () => {
    expect(serviceDirty(serviceDraftOf(saved), saved)).toBe(false);
    expect(serviceDirty({ ...serviceDraftOf(saved), apiKey: "x" }, saved)).toBe(true);
    expect(serviceDirty({ ...serviceDraftOf(saved), model: "other" }, saved)).toBe(true);
  });
  it("compares policy lists as sets", () => {
    const a = { ...emptyPolicy, inputTextCategories: ["a", "b"] };
    const b = { ...emptyPolicy, inputTextCategories: ["b", "a"] };
    expect(policyDirty(a, b)).toBe(false);
    expect(policyDirty(togglePolicyCategory(a, "inputTextCategories", "c", true), b)).toBe(true);
  });
  it("requires one category to save a policy", () => {
    expect(validatePolicy(emptyPolicy)).not.toBeNull();
    expect(validatePolicy({ ...emptyPolicy, outputImageCategories: ["x"] })).toBeNull();
  });
});

describe("statistics", () => {
  const item = (day: string, over: Partial<StatItem> = {}): StatItem => ({
    statDate: `${day}T00:00:00Z`, direction: "input", modality: "text", result: "passed", category: "", checkCount: 10, contentItems: 10, hitCount: 1, failureCount: 0, latencySumMS: 200, latencyCount: 10, ...over,
  });
  it("sums totals, hit rate and mean latency", () => {
    const t = totalsOf([item("2026-01-01"), item("2026-01-01", { checkCount: 30, hitCount: 3, failureCount: 2, latencySumMS: 600, latencyCount: 30 })]);
    expect(t).toEqual({ checks: 40, hits: 4, failures: 2, hitRate: 0.1, avgLatencyMs: 20 });
  });
  it("has no mean latency without samples", () => {
    expect(totalsOf([item("2026-01-01", { latencyCount: 0, latencySumMS: 0 })]).avgLatencyMs).toBeNull();
  });
  it("groups by day, oldest first", () => {
    const days = dailyStats([item("2026-01-02"), item("2026-01-01"), item("2026-01-02")]);
    expect(days.map((d) => [d.date, d.checks])).toEqual([["2026-01-01", 10], ["2026-01-02", 20]]);
  });
});
