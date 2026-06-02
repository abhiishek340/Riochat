import { describe, expect, it } from "vitest";
import { processInChunks } from "./bulk";

describe("processInChunks", () => {
  it("runs chunks one after another and reports progress", async () => {
    const order: string[] = [];
    const progress: [number, number][] = [];
    const results = await processInChunks(
      [1, 2, 3, 4, 5],
      2,
      async (n) => {
        order.push(`start${n}`);
        await Promise.resolve();
        order.push(`end${n}`);
      },
      { onProgress: (d, t) => progress.push([d, t]) },
    );
    expect(results.every((r) => r.ok)).toBe(true);
    expect(order).toEqual(["start1", "end1", "start2", "end2", "start3", "end3", "start4", "end4", "start5", "end5"]);
    expect(progress).toEqual([[2, 5], [4, 5], [5, 5]]);
  });
  it("can run a chunk in parallel but still waits for it before the next", async () => {
    const order: string[] = [];
    await processInChunks(
      [1, 2, 3],
      2,
      async (n) => {
        order.push(`start${n}`);
        await Promise.resolve();
        order.push(`end${n}`);
      },
      { parallelWithinChunk: true },
    );
    expect(order.slice(0, 4)).toEqual(["start1", "start2", "end1", "end2"]);
    expect(order.slice(4)).toEqual(["start3", "end3"]);
  });
  it("keeps going after a failure and reports it per item", async () => {
    const results = await processInChunks([1, 2, 3], 10, async (n) => {
      if (n === 2) throw new Error("nope");
    });
    expect(results.map((r) => r.ok)).toEqual([true, false, true]);
    expect((results[1]!.error as Error).message).toBe("nope");
  });
  it("handles an empty list", async () => {
    expect(await processInChunks([], 10, async () => undefined)).toEqual([]);
  });
});
