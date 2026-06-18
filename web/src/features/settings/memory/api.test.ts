import { describe, expect, it } from "vitest";
import { localEntry, normalizeEntries, upsertEntry } from "./api";

describe("normalizeEntries", () => {
  it("keeps entries that have a key", () => {
    const list = normalizeEntries([{ id: 1, memoryKey: "tone", value: "dry", scope: "preference" }, { value: "no key" }, null]);
    expect(list).toHaveLength(1);
    expect(list[0]).toMatchObject({ memoryKey: "tone", scope: "preference" });
    expect(normalizeEntries({})).toEqual([]);
  });
});

describe("upsertEntry", () => {
  const a = localEntry("tone", "dry", "preference");
  const b = localEntry("lang", "en", "preference");
  it("replaces an existing key in place", () => {
    const next = upsertEntry([a, b], localEntry("tone", "warm", "preference"));
    expect(next.map((e) => e.memoryKey)).toEqual(["tone", "lang"]);
    expect(next[0]?.value).toBe("warm");
    expect(next).toHaveLength(2);
  });
  it("puts a new key first", () => {
    const next = upsertEntry([a], b);
    expect(next.map((e) => e.memoryKey)).toEqual(["lang", "tone"]);
  });
});
