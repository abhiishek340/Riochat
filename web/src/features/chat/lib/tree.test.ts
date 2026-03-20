import { describe, expect, it } from "vitest";
import { msg } from "./fixtures";
import { cycleSelection, inheritUsage, lastPersistedLeaf, navigatorsFor, reconcileSelection, rewriteSelection, visiblePath } from "./tree";

const u1 = msg({ id: "u1", role: "user" });
const a1 = msg({ id: "a1", role: "assistant", parentId: "u1" });
const u2 = msg({ id: "u2", role: "user", parentId: "a1" });
const a2 = msg({ id: "a2", role: "assistant", parentId: "u2" });
// A retry of a2 and an edit of u2.
const a2b = msg({ id: "a2b", role: "assistant", parentId: "u2", branchReason: "retry", sourceId: "a2" });

describe("visiblePath", () => {
  it("follows the newest sibling by default", () => {
    const list = [u1, a1, u2, a2, a2b];
    const sel = reconcileSelection(list, {});
    expect(visiblePath(list, sel).map((m) => m.id)).toEqual(["u1", "a1", "u2", "a2b"]);
  });

  it("keeps a previous choice that still exists", () => {
    const list = [u1, a1, u2, a2, a2b];
    const sel = reconcileSelection(list, { u2: "a2" });
    // The ancestry of the newest message wins for its own branch...
    expect(visiblePath(list, sel).at(-1)?.id).toBe("a2b");
    // ...but a branch that does not hold the newest message keeps the user's pick.
    const other = [u1, msg({ id: "a1b", role: "assistant", parentId: "u1" }), a1, u2];
    const sel2 = reconcileSelection(other, { u1: "a1b" });
    expect(sel2.u1).toBe("a1"); // u2 hangs under a1, so the newest message's ancestry wins
    const sel3 = reconcileSelection([u1, a1, msg({ id: "a1b", role: "assistant", parentId: "u1" }), msg({ id: "u9", role: "user" })], { u1: "a1" });
    expect(sel3.u1).toBe("a1"); // not on the newest message's ancestry: the user's pick is kept
  });

  it("falls back to the last sibling when the stored choice vanished", () => {
    const list = [u1, a1];
    expect(reconcileSelection(list, { u1: "gone" }).u1).toBe("a1");
  });

  it("walks up from the newest message when parents are missing", () => {
    const orphanA = msg({ id: "x2", role: "assistant", parentId: "x1" });
    const orphanB = msg({ id: "x3", role: "user", parentId: "x2" });
    const list = [orphanA, orphanB];
    expect(visiblePath(list, reconcileSelection(list, {})).map((m) => m.id)).toEqual(["x2", "x3"]);
  });

  it("stops at cycles", () => {
    const c1 = msg({ id: "c1", parentId: "c2" });
    const c2 = msg({ id: "c2", parentId: "c1" });
    const list = [c1, c2];
    expect(visiblePath(list, reconcileSelection(list, {})).length).toBeLessThanOrEqual(2);
  });
});

describe("navigators", () => {
  const list = [u1, a1, u2, a2, a2b];
  it("numbers siblings in list order and reports bounds", () => {
    const sel = reconcileSelection(list, {});
    const nav = navigatorsFor(visiblePath(list, sel), list);
    expect(nav.get("a2b")).toMatchObject({ index: 2, total: 2, canPrevious: true, canNext: false });
    expect(nav.has("u1")).toBe(false);
  });

  it("clamps at both ends and never wraps", () => {
    const sel = reconcileSelection(list, {});
    const first = cycleSelection(list, sel, "a2b", -1);
    expect(first.u2).toBe("a2");
    expect(cycleSelection(list, first, "a2", -1)).toBe(first);
    const back = cycleSelection(list, first, "a2", 1);
    expect(back.u2).toBe("a2b");
    expect(cycleSelection(list, back, "a2b", 1)).toBe(back);
  });
});

describe("helpers", () => {
  it("rewrites local ids in keys and values", () => {
    expect(rewriteSelection({ "": "local-u", "local-u": "local-a" }, { "local-u": "U", "local-a": "A" })).toEqual({ "": "U", U: "A" });
  });

  it("inherits input-side usage from the user message except on retries", () => {
    const user = msg({ id: "u", role: "user", usage: { input: 10, output: 0, cacheRead: 2, cacheWrite: 1, reasoning: 0 } });
    const reply = msg({ id: "a", role: "assistant", parentId: "u" });
    const retry = msg({ id: "r", role: "assistant", parentId: "u", branchReason: "retry", sourceId: "a" });
    expect(inheritUsage([user, reply])[1]?.usage).toMatchObject({ input: 10, cacheRead: 2, cacheWrite: 1 });
    expect(inheritUsage([user, retry])[1]?.usage.input).toBe(0);
  });

  it("picks the newest successful assistant as the send parent", () => {
    const path = [u1, a1, msg({ id: "u3", role: "user", parentId: "a1", status: "pending", persisted: false })];
    expect(lastPersistedLeaf(path)?.id).toBe("a1");
    expect(lastPersistedLeaf([msg({ id: "p", status: "pending" })])).toBeNull();
  });
});
