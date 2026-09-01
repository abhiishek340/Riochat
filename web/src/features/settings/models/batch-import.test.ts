import { describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api/errors";
import type { AvailableModel, PersonalProvider } from "./api";
import { runBatch, throttleSeconds, type BatchDeps, type EntryStatus } from "./batch-import";
import type { LinkEntry } from "./link-parser";

const entry = (n: number): LinkEntry => ({ baseURL: `https://h${n}.test`, host: `h${n}.test`, key: `k${n}` });
const listing = (n: number): AvailableModel[] => Array.from({ length: n }, (_, i) => ({ name: `m${i}`, suggestedProtocols: ["p"] }));

function setup(over: Partial<BatchDeps> = {}) {
  const statuses = new Map<number, EntryStatus[]>();
  const deps: BatchDeps = {
    probe: vi.fn(async () => listing(2)),
    create: vi.fn(async () => ({}) as PersonalProvider),
    sleep: vi.fn(async () => undefined),
    cancelled: () => false,
    describe: (e) => (e instanceof Error ? e.message : String(e)),
    report: (i, s) => statuses.set(i, [...(statuses.get(i) ?? []), s]),
    throttle: (e) => (e instanceof ApiError && e.status === 429 ? { retryAfter: e.retryAfterSeconds } : null),
    ...over,
  };
  return { deps, statuses, last: (i: number) => statuses.get(i)?.at(-1) };
}

describe("throttleSeconds", () => {
  it("rounds up, floors at 1, caps at 60 and defaults to 60", () => {
    expect(throttleSeconds(2.2)).toBe(3);
    expect(throttleSeconds(0)).toBe(1);
    expect(throttleSeconds(500)).toBe(60);
    expect(throttleSeconds(undefined)).toBe(60);
  });
});

describe("runBatch", () => {
  it("imports entries one by one and counts them", async () => {
    const { deps, last } = setup();
    const out = await runBatch([entry(1), entry(2)], [0, 1], 5, deps);
    expect(out).toEqual({ imported: 2, freeSlots: 3 });
    expect(last(0)).toEqual({ kind: "imported", count: 2 });
    expect(deps.create).toHaveBeenCalledTimes(2);
  });

  it("marks entries beyond the free slots as over the limit", async () => {
    const { deps, last } = setup();
    const out = await runBatch([entry(1), entry(2), entry(3)], [0, 1, 2], 2, deps);
    expect(out.imported).toBe(2);
    expect(last(2)).toEqual({ kind: "overLimit" });
    expect(deps.probe).toHaveBeenCalledTimes(2);
  });

  it("waits through a 429 with a per-second countdown, then retries", async () => {
    let calls = 0;
    const { deps, statuses, last } = setup({
      probe: vi.fn(async () => {
        if (++calls === 1) throw new ApiError({ message: "slow down", status: 429, retryAfterSeconds: 3 });
        return listing(1);
      }),
    });
    await runBatch([entry(1)], [0], 1, deps);
    const waits = (statuses.get(0) ?? []).filter((s) => s.kind === "throttled").map((s) => (s as { seconds: number }).seconds);
    expect(waits).toEqual([3, 2, 1]);
    expect(deps.sleep).toHaveBeenCalledTimes(3);
    expect(deps.probe).toHaveBeenCalledTimes(2);
    expect(last(0)).toEqual({ kind: "imported", count: 1 });
  });

  it("asks for a selection when the catalog is larger than 12", async () => {
    const { deps, last } = setup({ probe: vi.fn(async () => listing(20)) });
    const out = await runBatch([entry(1)], [0], 3, deps);
    expect(out.imported).toBe(0);
    expect(last(0)).toMatchObject({ kind: "needsSelection", count: 20 });
    expect(deps.create).not.toHaveBeenCalled();
  });

  it("reports an empty catalog and failures without stopping the run", async () => {
    let n = 0;
    const { deps, last } = setup({
      probe: vi.fn(async () => {
        n++;
        if (n === 1) return [];
        if (n === 2) throw new Error("bad key");
        return listing(1);
      }),
    });
    const out = await runBatch([entry(1), entry(2), entry(3)], [0, 1, 2], 3, deps);
    expect(last(0)).toEqual({ kind: "noModels" });
    expect(last(1)).toEqual({ kind: "failed", message: "bad key" });
    expect(last(2)).toEqual({ kind: "imported", count: 1 });
    expect(out.imported).toBe(1);
  });

  it("stops quietly when cancelled", async () => {
    let stop = false;
    const { deps } = setup({
      cancelled: () => stop,
      probe: vi.fn(async () => {
        stop = true;
        return listing(1);
      }),
    });
    const out = await runBatch([entry(1), entry(2)], [0, 1], 5, deps);
    expect(deps.probe).toHaveBeenCalledTimes(1);
    expect(out.imported).toBe(1);
  });
});
