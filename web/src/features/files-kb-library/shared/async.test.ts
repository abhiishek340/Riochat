import { describe, expect, it } from "vitest";
import { NetworkError } from "@/lib/api/errors";
import { chunk, cleanIDs, retryNetwork, runChunks, runPool, uniqueBy } from "./async";

describe("runPool", () => {
  it("never runs more than the limit at once and keeps result order", async () => {
    let active = 0;
    let peak = 0;
    const out = await runPool([1, 2, 3, 4, 5, 6, 7], 4, async (n) => {
      active += 1;
      peak = Math.max(peak, active);
      await new Promise((r) => setTimeout(r, 5));
      active -= 1;
      if (n === 3) throw new Error("bad");
      return n * 2;
    });
    expect(peak).toBe(4);
    expect(out.map((r) => (r.ok ? r.value : "x"))).toEqual([2, 4, "x", 8, 10, 12, 14]);
  });
  it("reports unstarted items as aborted once the signal fires", async () => {
    const ctrl = new AbortController();
    const out = await runPool([1, 2, 3], 1, async (n) => {
      if (n === 1) ctrl.abort();
      return n;
    }, ctrl.signal);
    expect(out[0]).toEqual({ ok: true, value: 1 });
    expect(out[1]?.ok).toBe(false);
    expect(out[2]?.ok).toBe(false);
  });
});

describe("runChunks (scenario 10)", () => {
  it("deletes 25 items in chunks of 10, reporting progress per chunk and tolerating failures", async () => {
    const seen: number[] = [];
    const progress: Array<[number, number]> = [];
    const result = await runChunks(
      Array.from({ length: 25 }, (_, i) => i),
      10,
      async (n) => {
        seen.push(n);
        if (n === 4 || n === 20) throw new Error(`fail ${n}`);
      },
      (processed, total) => progress.push([processed, total]),
    );
    expect(seen).toHaveLength(25);
    expect(progress).toEqual([[10, 25], [20, 25], [25, 25]]);
    expect(result).toMatchObject({ success: 23, failed: 2 });
    expect((result.firstError as Error).message).toBe("fail 4");
  });
});

describe("retryNetwork", () => {
  const waits: number[] = [];
  const opts = { wait: async (ms: number) => void waits.push(ms), random: () => 0.5 };
  it("retries network failures twice with growing delays", async () => {
    waits.length = 0;
    let calls = 0;
    const result = await retryNetwork(async () => {
      calls += 1;
      if (calls < 3) throw new NetworkError();
      return "ok";
    }, opts);
    expect(result).toBe("ok");
    expect(waits).toEqual([250 + 75, 500 + 75]);
  });
  it("gives up after the retries", async () => {
    let calls = 0;
    await expect(
      retryNetwork(async () => {
        calls += 1;
        throw new NetworkError();
      }, opts),
    ).rejects.toBeInstanceOf(NetworkError);
    expect(calls).toBe(3);
  });
  it("does not retry other errors", async () => {
    let calls = 0;
    await expect(
      retryNetwork(async () => {
        calls += 1;
        throw new Error("http");
      }, opts),
    ).rejects.toThrow("http");
    expect(calls).toBe(1);
  });
});

describe("list helpers", () => {
  it("chunks, de-duplicates and cleans IDs", () => {
    expect(chunk([1, 2, 3, 4, 5], 2)).toEqual([[1, 2], [3, 4], [5]]);
    expect(uniqueBy([{ k: "a" }, { k: "b" }, { k: "a" }], (x) => x.k)).toHaveLength(2);
    expect(cleanIDs([" a ", "", "a", "b"])).toEqual(["a", "b"]);
  });
});
