import { describe, expect, it } from "vitest";
import type { ActivityDay } from "./api";
import { buildWeeks, cellLevel, computeStreaks, hasAnyActivity, monthMarks, peakTokens } from "./heatmap-logic";

const days = (counts: number[], start = "2025-01-05"): ActivityDay[] =>
  counts.map((requestCount, i) => {
    const d = new Date(`${start}T00:00:00`);
    d.setDate(d.getDate() + i);
    const p = (n: number) => String(n).padStart(2, "0");
    return { date: `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`, requestCount, tokenUsage: requestCount * 10 };
  });

describe("computeStreaks", () => {
  it("skips an empty last day when counting the current streak", () => {
    expect(computeStreaks(days([0, 3, 2, 0]))).toEqual({ longest: 2, current: 2, activeDays: 2 });
  });
  it("counts a busy last day", () => {
    expect(computeStreaks(days([1, 0, 1, 1, 1]))).toEqual({ longest: 3, current: 3, activeDays: 4 });
  });
  it("is zero when the last two days are empty", () => {
    expect(computeStreaks(days([1, 1, 0, 0])).current).toBe(0);
    expect(computeStreaks([])).toEqual({ longest: 0, current: 0, activeDays: 0 });
  });
});

describe("cellLevel", () => {
  it("scales tokens against the peak day", () => {
    expect(cellLevel(25, "tokens", 100)).toBe(1);
    expect(cellLevel(26, "tokens", 100)).toBe(2);
    expect(cellLevel(100, "tokens", 100)).toBe(4);
    expect(cellLevel(0, "tokens", 100)).toBe(0);
  });
  it("steps requests by five", () => {
    expect(cellLevel(11, "requests", 0)).toBe(3);
    expect(cellLevel(1, "requests", 0)).toBe(1);
    expect(cellLevel(500, "requests", 0)).toBe(4);
    expect(cellLevel(-3, "requests", 0)).toBe(0);
  });
});

describe("buildWeeks", () => {
  it("aligns to Sunday with leading blanks and pads the last week", () => {
    // 2025-01-08 is a Wednesday.
    const weeks = buildWeeks(days([1, 1, 1, 1, 1], "2025-01-08"));
    expect(weeks).toHaveLength(2);
    expect(weeks[0]?.slice(0, 3)).toEqual([null, null, null]);
    expect(weeks[0]?.[3]?.date).toBe("2025-01-08");
    expect(weeks[1]?.at(-1)).toBeNull();
    expect(buildWeeks([])).toEqual([]);
  });
});

describe("month marks and summaries", () => {
  it("marks where each month first appears", () => {
    const marks = monthMarks(buildWeeks(days(Array(70).fill(1), "2025-01-05")));
    expect(marks.map((m) => m.month)).toEqual([0, 1, 2]);
    expect(marks[1]?.column).toBeGreaterThan(marks[0]?.column ?? 0);
  });
  it("finds the peak and detects emptiness", () => {
    expect(peakTokens(days([1, 5, 2]))).toBe(50);
    expect(hasAnyActivity(days([0, 0]))).toBe(false);
    expect(hasAnyActivity(days([0, 1]))).toBe(true);
  });
});
