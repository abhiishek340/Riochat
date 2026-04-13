import { describe, expect, it } from "vitest";
import { msg } from "../lib/fixtures";
import { messageKey } from "../lib/message-map";
import { localAssistantId, localUserId } from "../lib/ids";
import type { ChatMessage } from "../types";
import type { Turn } from "./engine-types";
import { derive, mergeMessages } from "./engine-merge";

function turn(runId: string, over: Partial<Turn> = {}, parent: string | null = null): Turn {
  const user = msg({ id: localUserId(runId), key: messageKey("user", runId, "x"), runId, role: "user", persisted: false, parentId: parent, content: "q", optimistic: true });
  const assistant = msg({
    id: localAssistantId(runId), key: messageKey("assistant", runId, "y"), runId, role: "assistant", persisted: false, status: "pending",
    parentId: user.id, content: "partial", optimistic: true, live: { streaming: true, label: null, startedAt: 0 },
  });
  return {
    runId, key: "c1", conversationId: "c1", task: "chat", assistantOnly: false, user, assistant, result: null, stage: "running", attachments: [],
    optimisticTitle: "", controller: new AbortController(), cancelRequested: false, cancelTimer: null, startedAt: 0, order: 1, terminal: false,
    fromComposer: true, restored: false, model: "m", branchReason: "default", ...over,
  };
}

const serverUser = (runId: string, parent: string | null = null): ChatMessage => msg({ id: "U1", key: messageKey("user", runId, "U1"), runId, role: "user", parentId: parent, content: "q" });
const serverAsst = (runId: string, over: Partial<ChatMessage> = {}): ChatMessage =>
  msg({ id: "A1", key: messageKey("assistant", runId, "A1"), runId, role: "assistant", parentId: "U1", content: "", status: "pending", ...over });

describe("mergeMessages", () => {
  it("appends an optimistic turn after the server messages", () => {
    const merged = mergeMessages([msg({ id: "old" })], [turn("run_1", {}, "old")], null);
    expect(merged.map((m) => m.id)).toEqual(["old", localUserId("run_1"), localAssistantId("run_1")]);
  });

  it("never duplicates: the persisted copy of a running turn takes the local text and live flags", () => {
    const merged = mergeMessages([serverUser("run_1"), serverAsst("run_1")], [turn("run_1")], null);
    expect(merged).toHaveLength(2);
    expect(merged[1]).toMatchObject({ id: "A1", content: "partial", status: "pending", persisted: true });
    expect(merged[1]?.live?.streaming).toBe(true);
  });

  it("uses the server row once the turn has settled and the reload brought a terminal message", () => {
    const t = turn("run_1", { stage: "settled" });
    const merged = mergeMessages([serverUser("run_1"), serverAsst("run_1", { status: "success", content: "final" })], [t], null);
    expect(merged[1]).toMatchObject({ id: "A1", content: "final", status: "success" });
    expect(merged[1]?.live).toBeUndefined();
  });

  it("shows the final result before the reload catches up", () => {
    const user = serverUser("run_1");
    const asst = serverAsst("run_1", { status: "success", content: "final" });
    const t = turn("run_1", { stage: "settled", result: { user, assistant: asst } });
    const merged = mergeMessages([], [t], null);
    expect(merged.map((m) => [m.id, m.content])).toEqual([["U1", "q"], ["A1", "final"]]);
    // and the stale pending server row is replaced by the known final result
    const stale = mergeMessages([serverUser("run_1"), serverAsst("run_1")], [t], null);
    expect(stale[1]).toMatchObject({ status: "success", content: "final" });
  });

  it("rewrites parents of appended messages when the user message already came from the server", () => {
    const t = turn("run_1");
    const merged = mergeMessages([serverUser("run_1")], [t], null);
    expect(merged.map((m) => m.id)).toEqual(["U1", localAssistantId("run_1")]);
    expect(merged[1]?.parentId).toBe("U1");
  });

  it("overlays the replayed text on a re-attached pending reply", () => {
    const attach = { runId: "run_9", controller: new AbortController(), cursor: 3, text: "replayed", usage: { input: 0, output: 4, cacheRead: 0, cacheWrite: 0, reasoning: 0 }, trace: null, label: null, contentType: null, blocked: false };
    const merged = mergeMessages([serverAsst("run_9", { id: "A9", key: messageKey("assistant", "run_9", "A9") })], [], attach);
    expect(merged[0]).toMatchObject({ content: "replayed", status: "pending" });
    expect(merged[0]?.live?.streaming).toBe(true);
    expect(merged[0]?.usage.output).toBe(4);
  });
});

describe("derive", () => {
  it("selects the optimistic turn as the visible branch", () => {
    const st = {
      key: "c1", server: [serverUser("run_0"), serverAsst("run_0", { status: "success", content: "ok" })], total: 2, pagesLoaded: 1, loading: false, loadedOnce: true,
      loadError: null, loadingOlder: false, turns: [turn("run_1", {}, "A1")], selection: {}, attach: null, version: 1, loadSeq: 0, signature: "",
    };
    const d = derive(st);
    expect(d.path.map((m) => m.id)).toEqual(["U1", "A1", localUserId("run_1"), localAssistantId("run_1")]);
    expect(st.selection).toMatchObject({ A1: localUserId("run_1") });
  });
});
