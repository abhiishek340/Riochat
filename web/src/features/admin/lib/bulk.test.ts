import { describe, expect, it } from "vitest";
import { batchDeleteInChunks, chunk, failedIds, mergeBatchResults, removedIds, splitBatch } from "./bulk";

const answer = (ids: number[], bad: number[] = [], missing: number[] = []) => ({
  total: ids.length,
  successCount: ids.length - bad.length - missing.length,
  notFoundCount: missing.length,
  failedCount: bad.length,
  results: ids.map((id) => ({ id, status: bad.includes(id) ? "failed" : missing.includes(id) ? "not_found" : "deleted" })),
});

describe("batch delete helpers", () => {
  it("splits ids into chunks of 100 and merges the answers", async () => {
    const ids = Array.from({ length: 230 }, (_, i) => i + 1);
    const calls: number[] = [];
    const merged = await batchDeleteInChunks(ids, async (group) => {
      calls.push(group.length);
      return answer(group, group.includes(150) ? [150] : [], group.includes(7) ? [7] : []);
    });
    expect(calls).toEqual([100, 100, 30]);
    expect(merged.total).toBe(230);
    expect(merged.successCount).toBe(228);
    expect(merged.notFoundCount).toBe(1);
    expect(merged.failedCount).toBe(1);
    expect([...failedIds(merged)]).toEqual([150]);
    expect(removedIds(merged).has(7)).toBe(true);
    expect(removedIds(merged).has(150)).toBe(false);
  });

  it("tolerates a partial answer", () => {
    expect(mergeBatchResults([{ successCount: 2 }, {}])).toMatchObject({ successCount: 2, failedCount: 0, results: [] });
    expect(chunk([], 100)).toEqual([]);
  });

  it("splits requested ids into those that left and those the server kept", () => {
    const result = mergeBatchResults([answer([1, 2, 3, 4], [3], [4])]);
    expect(splitBatch([1, 2, 3, 4], result)).toEqual({ gone: [1, 2, 4], kept: [3] });
  });
});
