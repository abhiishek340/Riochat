import { describe, expect, it } from "vitest";
import { RunRegistry, STARTED_HERE_GRACE_MS } from "./run-registry";

describe("RunRegistry", () => {
  it("is running while a record is not finished and notifies only on flips", () => {
    const r = new RunRegistry();
    const events: [string, boolean][] = [];
    r.subscribe((c, running) => events.push([c, running]));
    r.register("r1", "c1");
    r.register("r2", "c1");
    expect(r.isRunning("c1")).toBe(true);
    r.finish("r1");
    expect(r.isRunning("c1")).toBe(true);
    r.finish("r2");
    expect(r.isRunning("c1")).toBe(false);
    expect(events).toEqual([["c1", true], ["c1", false]]);
  });

  it("ignores registration of finished runs and runs bound elsewhere", () => {
    const r = new RunRegistry();
    r.register("r1", "c1");
    r.register("r1", "c2");
    expect(r.get("r1")?.conversationId).toBe("c1");
    r.finish("r1");
    r.register("r1", "c1");
    expect(r.get("r1")?.state).toBe("finished");
  });

  it("rebinds a draft run to its conversation", () => {
    const r = new RunRegistry();
    r.register("r1", "");
    r.rebind("r1", "c9");
    expect(r.isRunning("c9")).toBe(true);
  });

  it("keeps a started_here run for 15 s after the snapshot loses it", () => {
    const r = new RunRegistry();
    r.register("r1", "c1");
    r.applySnapshot([], 1000);
    expect(r.isRunning("c1")).toBe(true);
    r.applySnapshot([], 1000 + STARTED_HERE_GRACE_MS - 1);
    expect(r.isRunning("c1")).toBe(true);
    r.applySnapshot([], 1000 + STARTED_HERE_GRACE_MS);
    expect(r.isRunning("c1")).toBe(false);
  });

  it("confirms started_here and orphaned runs a snapshot contains", () => {
    const r = new RunRegistry();
    r.register("r1", "c1");
    r.register("r2", "c1");
    r.orphan("r2");
    r.applySnapshot([{ runID: "r1", conversationPublicID: "c1" }, { runID: "r2", conversationPublicID: "c1" }], 5);
    expect(r.get("r1")?.state).toBe("confirmed");
    expect(r.get("r2")?.state).toBe("confirmed");
  });

  it("clears the missing timer when a run reappears", () => {
    const r = new RunRegistry();
    r.register("r1", "c1");
    r.applySnapshot([], 0);
    r.applySnapshot([{ runID: "r1", conversationPublicID: "c1" }], 10_000);
    expect(r.get("r1")?.missingSince).toBeNull();
    expect(r.get("r1")?.state).toBe("confirmed");
    r.applySnapshot([], 20_000); // a confirmed run the server no longer lists is gone at once
    expect(r.isRunning("c1")).toBe(false);
  });

  it("drops orphaned, confirmed and finished records a snapshot lacks and adds unknown ones", () => {
    const r = new RunRegistry();
    r.register("a", "c1");
    r.orphan("a");
    r.noticeStarted("b", "c2");
    r.register("f", "c3");
    r.finish("f");
    r.applySnapshot([{ runID: "z", conversationPublicID: "c4" }], 1);
    expect(r.get("a")).toBeUndefined();
    expect(r.get("b")).toBeUndefined();
    expect(r.get("f")).toBeUndefined();
    expect(r.get("z")?.state).toBe("confirmed");
    expect(r.isRunning("c4")).toBe(true);
  });

  it("lets the snapshot win on the owning conversation", () => {
    const r = new RunRegistry();
    r.register("r1", "c1");
    r.applySnapshot([{ runID: "r1", conversationPublicID: "c2" }], 1);
    expect(r.isRunning("c2")).toBe(true);
    expect(r.isRunning("c1")).toBe(false);
  });

  it("treats finished notices differently by origin", () => {
    const r = new RunRegistry();
    r.noticeStarted("r1", "c1");
    r.noticeFinished("r1", false);
    expect(r.get("r1")?.state).toBe("finished");
    expect(r.isRunning("c1")).toBe(false);
    r.noticeStarted("r1", "c1"); // finished runs cannot be revived
    expect(r.isRunning("c1")).toBe(false);
    r.noticeFinished("r1", true);
    expect(r.get("r1")).toBeUndefined();
  });

  it("clear empties everything and reports flips", () => {
    const r = new RunRegistry();
    const events: [string, boolean][] = [];
    r.register("r1", "c1");
    r.subscribe((c, running) => events.push([c, running]));
    r.clear();
    expect(events).toEqual([["c1", false]]);
  });
});
