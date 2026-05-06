import { describe, expect, it, vi } from "vitest";
import { createVersionWatcher } from "./version-watch";

function setup(ids: (string | Error)[]) {
  let time = 0;
  const queue = [...ids];
  const onChanged = vi.fn();
  const fetchId = vi.fn(async () => {
    const next = queue.shift() ?? "";
    if (next instanceof Error) throw next;
    return next;
  });
  const check = createVersionWatcher({ fetchId, onChanged, now: () => time });
  return { check, onChanged, fetchId, advance: (ms: number) => (time += ms) };
}

const TEN_MIN = 10 * 60 * 1000;

describe("createVersionWatcher", () => {
  it("only records the build on the first check", async () => {
    const t = setup(["A"]);
    await t.check(true);
    expect(t.onChanged).not.toHaveBeenCalled();
  });
  it("prompts when a later check reports another build", async () => {
    const t = setup(["A", "B"]);
    await t.check(true);
    t.advance(TEN_MIN);
    await t.check();
    expect(t.onChanged).toHaveBeenCalledWith("B");
  });
  it("never prompts for identical builds", async () => {
    const t = setup(["A", "A", "A"]);
    await t.check(true);
    t.advance(TEN_MIN);
    await t.check();
    t.advance(TEN_MIN);
    await t.check();
    expect(t.onChanged).not.toHaveBeenCalled();
  });
  it("throttles later checks to one per ten minutes", async () => {
    const t = setup(["A", "B", "B"]);
    await t.check(true);
    t.advance(TEN_MIN - 1);
    await t.check();
    expect(t.fetchId).toHaveBeenCalledTimes(1);
    t.advance(1);
    await t.check();
    expect(t.fetchId).toHaveBeenCalledTimes(2);
  });
  it("does not overlap with a check in flight", async () => {
    let release: (v: string) => void = () => undefined;
    const fetchId = vi.fn(() => new Promise<string>((resolve) => (release = resolve)));
    const check = createVersionWatcher({ fetchId, onChanged: vi.fn(), now: () => 0 });
    const first = check(true);
    await check(true);
    expect(fetchId).toHaveBeenCalledTimes(1);
    release("A");
    await first;
  });
  it("ignores blank identities and failures", async () => {
    const t = setup(["", new Error("offline"), "A", "B"]);
    await t.check(true);
    t.advance(TEN_MIN);
    await t.check();
    t.advance(TEN_MIN);
    await t.check();
    t.advance(TEN_MIN);
    await t.check();
    expect(t.onChanged).toHaveBeenCalledTimes(1);
    expect(t.onChanged).toHaveBeenCalledWith("B");
  });
});
