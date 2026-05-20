import { describe, expect, it } from "vitest";
import { LruCache, clock, formatUpdatedLabel, groupByUpdated, mergeResults, normalizeQuery, sameDay, stepIndex, type SearchItem } from "./search-model";

const item = (id: string, updatedAt: string): SearchItem => ({ publicID: id, title: id, projectName: "", status: "active", updatedAt });
const now = new Date(2025, 5, 15, 10, 0, 0);

describe("groupByUpdated", () => {
  it("puts today first, then months newest first, dropping unreadable dates", () => {
    const groups = groupByUpdated(
      [
        item("old", new Date(2024, 11, 3, 9).toISOString()),
        item("today", new Date(2025, 5, 15, 8).toISOString()),
        item("may", new Date(2025, 4, 20, 8).toISOString()),
        item("may2", new Date(2025, 4, 2, 8).toISOString()),
        item("bad", "garbage"),
      ],
      now,
    );
    expect(groups.map((g) => g.id)).toEqual(["today", "2025-4", "2024-11"]);
    expect(groups[1]!.items.map((i) => i.publicID)).toEqual(["may", "may2"]);
  });
});

describe("labels and helpers", () => {
  it("formats the updated label by recency", () => {
    expect(formatUpdatedLabel(new Date(2025, 5, 15, 8, 5, 9).toISOString(), now, "en-US")).toBe("08:05:09");
    expect(formatUpdatedLabel(new Date(2025, 2, 1, 8, 5, 9).toISOString(), now, "en-US")).toMatch(/Mar 1.*08:05:09/);
    expect(formatUpdatedLabel(new Date(2023, 2, 1, 8, 5, 9).toISOString(), now, "en-US")).toMatch(/2023.*08:05:09/);
    expect(formatUpdatedLabel("nope", now, "en-US")).toBe("");
    expect(clock(new Date(2025, 0, 1, 1, 2, 3))).toBe("01:02:03");
    expect(sameDay(new Date(2025, 0, 1, 1), new Date(2025, 0, 1, 23))).toBe(true);
  });
  it("normalises queries", () => {
    expect(normalizeQuery("  RePort ")).toBe("report");
  });
  it("merges without duplicates, keeping the first occurrence", () => {
    const merged = mergeResults([item("a", "1"), item("b", "2")], [item("b", "x"), item("c", "3")]);
    expect(merged.map((m) => m.publicID)).toEqual(["a", "b", "c"]);
    expect(merged[1]!.updatedAt).toBe("2");
  });
  it("loops keyboard selection", () => {
    expect(stepIndex(-1, 1, 3)).toBe(0);
    expect(stepIndex(-1, -1, 3)).toBe(2);
    expect(stepIndex(2, 1, 3)).toBe(0);
    expect(stepIndex(0, -1, 3)).toBe(2);
    expect(stepIndex(0, 1, 0)).toBe(-1);
  });
});

describe("LruCache", () => {
  it("evicts the least recently used entry", () => {
    const cache = new LruCache<number>(2);
    cache.set("a", 1);
    cache.set("b", 2);
    expect(cache.get("a")).toBe(1); // a is now newest
    cache.set("c", 3);
    expect(cache.get("b")).toBeUndefined();
    expect(cache.get("a")).toBe(1);
    expect(cache.size).toBe(2);
    cache.delete("a");
    expect(cache.get("a")).toBeUndefined();
  });
});
