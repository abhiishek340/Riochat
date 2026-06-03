import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createPoller } from "./polling";

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe("createPoller", () => {
  it("runs every interval while the answer says to continue", async () => {
    const run = vi.fn(async () => true);
    const p = createPoller({ intervalMs: 3000, maxFailures: 5, run });
    p.start();
    await vi.advanceTimersByTimeAsync(3000);
    await vi.advanceTimersByTimeAsync(3000);
    expect(run).toHaveBeenCalledTimes(2);
    p.stop();
    await vi.advanceTimersByTimeAsync(9000);
    expect(run).toHaveBeenCalledTimes(2);
  });

  it("stops by itself when nothing is active any more", async () => {
    const run = vi.fn(async () => false);
    const p = createPoller({ intervalMs: 1000, maxFailures: 5, run });
    p.start();
    await vi.advanceTimersByTimeAsync(5000);
    expect(run).toHaveBeenCalledTimes(1);
    expect(p.running()).toBe(false);
  });

  it("stops after five failures in a row, and a success resets the count", async () => {
    let calls = 0;
    const run = vi.fn(async () => {
      calls += 1;
      if (calls === 3) return true;
      throw new Error("down");
    });
    const p = createPoller({ intervalMs: 1000, maxFailures: 5, run });
    p.start();
    await vi.advanceTimersByTimeAsync(20_000);
    // 2 failures, 1 success (resets), then 5 failures.
    expect(run).toHaveBeenCalledTimes(8);
    expect(p.running()).toBe(false);
  });

  it("skips the call while the page is hidden but keeps the timer", async () => {
    let hidden = true;
    const run = vi.fn(async () => true);
    const p = createPoller({ intervalMs: 1000, maxFailures: 5, run, isHidden: () => hidden });
    p.start();
    await vi.advanceTimersByTimeAsync(3500);
    expect(run).not.toHaveBeenCalled();
    hidden = false;
    await vi.advanceTimersByTimeAsync(1000);
    expect(run).toHaveBeenCalledTimes(1);
    p.stop();
  });

  it("discards an answer that arrives after a restart", async () => {
    const resolvers: Array<(v: boolean) => void> = [];
    const run = vi.fn(() => new Promise<boolean>((resolve) => resolvers.push(resolve)));
    const p = createPoller({ intervalMs: 1000, maxFailures: 5, run });
    p.start();
    await vi.advanceTimersByTimeAsync(1000);
    p.start();
    resolvers[0]?.(false); // the old tick says "stop", but it is stale
    await vi.advanceTimersByTimeAsync(0);
    expect(p.running()).toBe(true);
    p.stop();
  });
});
