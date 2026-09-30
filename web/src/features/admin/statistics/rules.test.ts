import { describe, expect, it } from "vitest";
import { activeFilterCount, buildQuery, bucketLabel, cacheKey, inclusiveDays, niceAxis, NO_FILTERS, presetRange, RankCache, validateRange, withSubject } from "./rules";

describe("ranges", () => {
  it("builds presets ending today", () => {
    expect(presetRange(7, new Date(2026, 5, 15))).toEqual({ start: "2026-06-09", end: "2026-06-15" });
    expect(presetRange(30, new Date(2026, 0, 10))).toEqual({ start: "2025-12-12", end: "2026-01-10" });
  });
  it("counts days inclusively", () => {
    expect(inclusiveDays(new Date(2026, 0, 1), new Date(2026, 0, 1))).toBe(1);
    expect(inclusiveDays(new Date(2026, 0, 1), new Date(2026, 11, 31))).toBe(365);
  });
  it("validates in three ways", () => {
    expect(validateRange("", "2026-01-01")).toBe("incomplete");
    expect(validateRange("2026-01-01", "")).toBe("incomplete");
    expect(validateRange("2026-02-30", "2026-03-01")).toBe("invalid");
    expect(validateRange("2026-03-02", "2026-03-01")).toBe("invalid");
    expect(validateRange("abc", "2026-03-01")).toBe("invalid");
    expect(validateRange("2025-01-01", "2026-03-01")).toBe("tooLong");
    expect(validateRange("2026-01-01", "2026-03-01")).toBeNull();
  });
  it("allows exactly 366 days", () => {
    expect(validateRange("2024-01-01", "2024-12-31")).toBeNull();
    expect(validateRange("2024-01-01", "2025-01-01")).toBe("tooLong");
  });
});

describe("filters and query", () => {
  const base = { start: "2026-01-01", end: "2026-01-31", section: "all" as const, modelRankBy: "cost" as const, userRankBy: "cost" as const };
  it("counts active filters", () => {
    expect(activeFilterCount(NO_FILTERS)).toBe(0);
    expect(activeFilterCount({ subject: { kind: "user", id: 0, label: "" }, model: "", scope: "all" })).toBe(0);
    expect(activeFilterCount({ subject: { kind: "group", id: 2 }, model: " m ", scope: "free" })).toBe(3);
  });
  it("sends exactly one subject", () => {
    const user = withSubject(NO_FILTERS, { kind: "user", id: 5, label: "x" });
    expect(buildQuery({ ...base, filters: user })).toMatchObject({ user_id: 5 });
    expect(buildQuery({ ...base, filters: user }).permission_group_id).toBeUndefined();
    const group = withSubject(user, { kind: "group", id: 9 });
    const q = buildQuery({ ...base, filters: group });
    expect(q.permission_group_id).toBe(9);
    expect(q.user_id).toBeUndefined();
  });
  it("trims the model and carries the rank metrics", () => {
    const q = buildQuery({ ...base, filters: { ...NO_FILTERS, model: " gpt " }, section: "models", modelRankBy: "tokens" });
    expect(q).toMatchObject({ platform_model_name: "gpt", section: "models", model_rank_by: "tokens", billing_scope: "all", start_date: "2026-01-01" });
    expect(buildQuery({ ...base, filters: NO_FILTERS }).platform_model_name).toBeUndefined();
  });
});

describe("rank cache", () => {
  it("keeps answers per metric until the filters change", () => {
    const cache = new RankCache<string>();
    const key = cacheKey("a", "b", NO_FILTERS);
    cache.reset(key);
    cache.set("cost", "x");
    cache.reset(key);
    expect(cache.get("cost")).toBe("x");
    cache.reset(cacheKey("a", "c", NO_FILTERS));
    expect(cache.get("cost")).toBeUndefined();
  });
});

describe("chart helpers", () => {
  it("labels week and month buckets by their start, clipped to the range", () => {
    const range = { startDate: "2026-03-04", endDate: "2026-03-31" };
    const clipped = bucketLabel("2026-03-02T00:00:00", "week", range, "en-US");
    const day = bucketLabel("2026-03-04T00:00:00", "day", range, "en-US");
    expect(clipped).toBe(day);
    expect(bucketLabel("2026-03-09T00:00:00", "week", range, "en-US")).not.toBe(clipped);
  });
  it("reads the server's calendar date instead of shifting it into the browser zone", () => {
    const range = { startDate: "2026-03-01", endDate: "2026-03-31" };
    const east = bucketLabel("2026-03-09T00:00:00+13:00", "day", range, "en-US");
    const west = bucketLabel("2026-03-09T00:00:00-11:00", "day", range, "en-US");
    expect(east).toBe(west);
    expect(east).toContain("9");
  });
  it("rounds an axis up to a tidy step", () => {
    expect(niceAxis(0)).toEqual({ max: 1, ticks: [0, 1] });
    const axis = niceAxis(87);
    expect(axis.max).toBeGreaterThanOrEqual(87);
    expect(axis.ticks[0]).toBe(0);
    expect(axis.ticks[axis.ticks.length - 1]).toBe(axis.max);
  });
});
