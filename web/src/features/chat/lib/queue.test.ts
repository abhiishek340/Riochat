import { describe, expect, it } from "vitest";
import { EMPTY_SELECTIONS } from "./request";
import {
  cancelDescendants, editItem, enqueue, failedChains, guideToFront, parentRunForNew, pickDispatchable, pollDelay, removeItem, RunStatusBook,
  type DispatchContext, type ParentOutcome, type QueuedMessage,
} from "./queue";

function q(id: string, parentRunId: string | null, extra: Partial<QueuedMessage> = {}): QueuedMessage {
  return {
    id, runId: `run_${id}`, conversationKey: "c1", text: id, attachments: [], model: "m", options: {}, selections: EMPTY_SELECTIONS,
    parentRunId, parentMessageId: null, createdAt: 0, ...extra,
  };
}

function ctx(over: Partial<DispatchContext> & { outcomes?: Record<string, ParentOutcome> } = {}): DispatchContext {
  const outcomes = over.outcomes ?? {};
  return {
    activeStreams: 0,
    cap: 5,
    branchBusy: () => false,
    unresolvedExchange: () => false,
    parentOutcome: (id) => outcomes[id] ?? { state: "unknown" },
    ...over,
  };
}

describe("queue chain", () => {
  it("chains new items behind the previous one, else behind the active run", () => {
    let queue: QueuedMessage[] = [];
    expect(parentRunForNew(queue, "c1", "run_1")).toBe("run_1");
    queue = enqueue(queue, q("B", "run_1"));
    expect(parentRunForNew(queue, "c1", "run_1")).toBe("run_B");
    expect(parentRunForNew(queue, "c2", null)).toBeNull();
  });

  it("relinks followers when an item is removed", () => {
    const queue = [q("B", "run_1"), q("C", "run_B")];
    const res = removeItem(queue, "B");
    expect(res.queue.map((x) => [x.id, x.parentRunId])).toEqual([["C", "run_1"]]);
  });

  it("deletes an item whose text is blanked and has no attachments, otherwise trims", () => {
    const queue = [q("B", "run_1"), q("C", "run_B")];
    expect(editItem(queue, "B", "   ").queue.map((x) => x.id)).toEqual(["C"]);
    expect(editItem(queue, "B", "   ").queue[0]?.parentRunId).toBe("run_1");
    expect(editItem(queue, "B", "  new ").queue[0]?.text).toBe("new");
  });

  it("guides an item to the front", () => {
    const queue = [q("B", "run_1"), q("C", "run_B")];
    const out = guideToFront(queue, "C", "run_1");
    expect(out.map((x) => [x.id, x.parentRunId])).toEqual([["C", "run_1"], ["B", "run_C"]]);
  });

  it("cancels the whole chain behind a failed run", () => {
    const queue = [q("B", "run_1"), q("C", "run_B"), q("X", "run_other")];
    const res = cancelDescendants(queue, "run_1");
    expect(res.cancelled.map((x) => x.id)).toEqual(["B", "C"]);
    expect(res.queue.map((x) => x.id)).toEqual(["X"]);
  });

  it("failedChains reads outcomes", () => {
    const queue = [q("B", "run_1"), q("C", "run_B")];
    const res = failedChains(queue, (id) => (id === "run_1" ? { state: "failed" } : { state: "running" }));
    expect(res.cancelled.map((x) => x.id)).toEqual(["B", "C"]);
    const keep = failedChains(queue, () => ({ state: "running" }));
    expect(keep.queue).toHaveLength(2);
  });
});

describe("pickDispatchable", () => {
  it("waits for the parent run to produce a persisted success", () => {
    const queue = [q("B", "run_1")];
    expect(pickDispatchable(queue, ctx())).toBeNull();
    expect(pickDispatchable(queue, ctx({ outcomes: { run_1: { state: "running" } } }))).toBeNull();
    const ready = pickDispatchable(queue, ctx({ outcomes: { run_1: { state: "success", messageId: "m9" } } }));
    expect(ready?.item.id).toBe("B");
    expect(ready?.parentMessageId).toBe("m9");
  });

  it("dispatches an item without a parent at once, only heads, one at a time", () => {
    const queue = [q("B", null), q("C", "run_B")];
    expect(pickDispatchable(queue, ctx())?.item.id).toBe("B");
    const onlyC = [q("C", "run_B")];
    expect(pickDispatchable(onlyC, ctx())).toBeNull();
  });

  it("respects the cap, busy branches and unresolved exchanges", () => {
    const queue = [q("B", null)];
    expect(pickDispatchable(queue, ctx({ activeStreams: 5 }))).toBeNull();
    expect(pickDispatchable(queue, ctx({ branchBusy: () => true }))).toBeNull();
    expect(pickDispatchable(queue, ctx({ unresolvedExchange: () => true }))).toBeNull();
    expect(pickDispatchable([q("B", null, { sendAfterCurrent: true })], ctx({ unresolvedExchange: () => true }))?.item.id).toBe("B");
  });

  it("serves other conversations when one is busy", () => {
    const queue = [q("B", null), q("D", null, { conversationKey: "c2" })];
    expect(pickDispatchable(queue, ctx({ branchBusy: (k) => k === "c1" }))?.item.id).toBe("D");
  });
});

describe("RunStatusBook and delays", () => {
  it("treats a run absent for 60 s as unavailable", () => {
    const book = new RunStatusBook();
    book.ask("r", 0);
    expect(book.get("r", 59_000)).toBeNull();
    expect(book.get("r", 60_000)).toBe("unavailable");
    book.apply(["r"], [{ runID: "r", status: "running" }], 61_000);
    expect(book.get("r", 62_000)).toBe("running");
    book.apply(["r"], [{ runID: "r", status: "success" }], 63_000);
    expect(book.get("r", 64_000)).toBe("success");
  });

  it("backs off polls", () => {
    expect([0, 1, 2, 3, 6].map(pollDelay)).toEqual([1500, 3000, 6000, 6000, 6000]);
  });
});
