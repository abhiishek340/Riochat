import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api/errors";
import { RunRegistry } from "../lib/run-registry";
import type { RunStreamHandlers, StreamOutcome } from "../lib/stream-consume";
import type { ConversationDTO, MessageDTO, SendResultDTO } from "../types";
import { ChatEngine } from "./chat-engine";
import { DRAFT_KEY, type EngineDeps, type Notice, type SendParams } from "./engine-types";
import { EMPTY_SELECTIONS } from "../lib/request";

function conv(id: string, over: Partial<ConversationDTO> = {}): ConversationDTO {
  return {
    publicID: id, projectID: "", projectName: "", title: "New chat", labelsJSON: "[]", model: "m", isStarred: false, starredAt: null, messageCount: 0,
    status: "active", shareStatus: "none", shareID: "", createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z", ...over,
  };
}

function dto(over: Partial<MessageDTO> & { publicID: string; role: string }): MessageDTO {
  return {
    id: 1, parentMessageID: null, parentPublicID: "", sourcePublicID: "", runID: "", contentType: "text", content: "", branchReason: "default",
    inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 0, reasoningTokens: 0, reasoningEffort: null, latencyMS: 0, status: "success",
    errorCode: "", errorMessage: "", errorRequestID: "", attachments: "[]", platformModelName: "m", myFeedback: "", thumbsUpCount: 0, thumbsDownCount: 0,
    editedAt: null, createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z", ...over,
  };
}

interface Harness {
  engine: ChatEngine;
  notices: Notice[];
  server: Map<string, MessageDTO[]>;
  convs: Map<string, ConversationDTO>;
  post: ReturnType<typeof vi.fn>;
  cancel: ReturnType<typeof vi.fn>;
  runs: RunRegistry;
  created: string[];
}

function harness(post: (path: string, body: Record<string, unknown>, signal: AbortSignal, h: RunStreamHandlers) => Promise<StreamOutcome>, hasToken = true): Harness {
  const server = new Map<string, MessageDTO[]>();
  const convs = new Map<string, ConversationDTO>();
  const created: string[] = [];
  const runs = new RunRegistry();
  const postFn = vi.fn(post);
  const cancel = vi.fn(async () => ({ canceled: true }));
  let nextConv = 1;
  const deps: EngineDeps = {
    conversations: {
      create: async () => {
        const c = conv(`conv${nextConv++}`);
        convs.set(c.publicID, c);
        created.push(c.publicID);
        return c;
      },
      find: (id) => convs.get(id) ?? null,
      patch: (id, patch) => {
        const c = convs.get(id);
        if (c) convs.set(id, { ...c, ...patch });
      },
      fetch: async (id) => convs.get(id) ?? conv(id),
      rename: async (id, title) => {
        const c = { ...(convs.get(id) ?? conv(id)), title };
        convs.set(id, c);
        return c;
      },
    },
    messages: {
      list: async (id) => ({ total: (server.get(id) ?? []).length, results: server.get(id) ?? [] }),
      cancelRun: cancel,
      runStatuses: async () => [],
      listRuns: async () => ({ total: 0, results: [] }),
    },
    streams: { post: (path, body, signal, h) => postFn(path, body as Record<string, unknown>, signal, h), resume: async () => ({ kind: "ended" }) },
    runs,
    hasToken: () => hasToken,
    now: () => Date.now(),
    revokeUrl: () => undefined,
    collectUiStates: () => [],
    isDocumentVisible: () => true,
  };
  const engine = new ChatEngine(deps);
  const notices: Notice[] = [];
  engine.on("notice", (n) => notices.push(n));
  return { engine, notices, server, convs, post: postFn, cancel, runs, created };
}

function params(over: Partial<SendParams> = {}): SendParams {
  return {
    key: "c1", text: "hello", attachments: [], model: "m", modelFacts: { kinds: new Set(["chat"]), supportsVideoExtension: false }, options: {},
    selections: EMPTY_SELECTIONS, parentId: null, sourceId: null, branchReason: "default", fromComposer: true, attachmentOnlyText: "see attachments",
    maxMessageFiles: 10, autoGenerateLabels: true, restoreOnFailure: true, ...over,
  };
}

function result(runId: string, over: { asst?: Partial<MessageDTO> } = {}): SendResultDTO {
  return {
    userMessage: dto({ id: 1, publicID: "U1", role: "user", runID: runId, content: "hello" }),
    assistantMessage: dto({ id: 2, publicID: "A1", role: "assistant", runID: runId, parentPublicID: "U1", content: "Hello", ...over.asst }),
  };
}

async function settleTimers() {
  await vi.advanceTimersByTimeAsync(100);
}

beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
});

describe("send", () => {
  it("shows an optimistic turn, streams text and settles to the server ids", async () => {
    let release!: (o: StreamOutcome) => void;
    let handlers!: RunStreamHandlers;
    const h = harness((_p, _b, _s, hd) => {
      handlers = hd;
      return new Promise<StreamOutcome>((r) => (release = r));
    });
    h.engine.setVisible("c1");
    await h.engine.load("c1");
    const out = await h.engine.send(params());
    expect(out.status).toBe("sent");
    const runId = out.status === "sent" ? out.runId : "";

    let v = h.engine.view("c1");
    expect(v.path.map((m) => m.role)).toEqual(["user", "assistant"]);
    expect(v.path[1]?.status).toBe("pending");
    expect(v.busy).toBe(true);
    expect(h.runs.isRunning("c1")).toBe(true);

    await settleTimers();
    handlers.onDelta?.("Hel");
    handlers.onDelta?.("lo");
    await vi.advanceTimersByTimeAsync(60);
    v = h.engine.view("c1");
    expect(v.path[1]?.content).toBe("Hello");

    h.server.set("c1", [
      dto({ id: 1, publicID: "U1", role: "user", runID: runId, content: "hello" }),
      dto({ id: 2, publicID: "A1", role: "assistant", runID: runId, parentPublicID: "U1", content: "Hello" }),
    ]);
    release({ kind: "completed", data: result(runId) });
    await vi.advanceTimersByTimeAsync(10);
    v = h.engine.view("c1");
    expect(v.path.map((m) => m.id)).toEqual(["U1", "A1"]);
    expect(v.path[1]?.status).toBe("success");
    expect(v.busy).toBe(false);
    expect(h.runs.isRunning("c1")).toBe(false);
    expect(h.post.mock.calls[0]?.[0]).toBe("/conversations/c1/messages/stream");
    expect(h.post.mock.calls[0]?.[1]).toMatchObject({ content: "hello", contentType: "text", model: "m", clientRunID: runId, branchReason: "default", knowledgeBaseIDs: [] });
  });

  it("creates the conversation for a draft, re-homes the turn and patches the title", async () => {
    const h = harness(async (_p, _b, _s, _h) => ({ kind: "completed", data: result("x") }));
    const createdEvents: string[] = [];
    h.engine.on("conversationCreated", (e) => createdEvents.push(`${e.fromKey}->${e.conversation.publicID}`));
    h.engine.setVisible(DRAFT_KEY);
    await h.engine.send(params({ key: DRAFT_KEY, text: '  "Hello   world, this is a very long message"  ' }));
    await vi.advanceTimersByTimeAsync(10);
    expect(createdEvents).toEqual(["draft->conv1"]);
    expect(h.post.mock.calls[0]?.[0]).toBe("/conversations/conv1/messages/stream");
    expect(h.convs.get("conv1")?.title).toBe("Hello world, thi");
    expect(h.engine.view(DRAFT_KEY).path).toHaveLength(0);
    expect(h.engine.view("conv1").path.length).toBeGreaterThan(0);
  });

  it("does nothing for empty input and asks for a model", async () => {
    const h = harness(async () => ({ kind: "completed", data: result("x") }));
    expect(await h.engine.send(params({ text: "   " }))).toEqual({ status: "not_sent", reason: "empty" });
    expect(await h.engine.send(params({ model: "" }))).toEqual({ status: "not_sent", reason: "model" });
    expect(h.notices.map((n) => n.code)).toEqual(["noModel"]);
    expect(h.post).not.toHaveBeenCalled();
  });

  it("refuses blocked task combinations with the block code", async () => {
    const h = harness(async () => ({ kind: "completed", data: result("x") }));
    const out = await h.engine.send(params({ modelFacts: { kinds: new Set(["image_edit"]), supportsVideoExtension: false } }));
    expect(out).toEqual({ status: "not_sent", reason: "blocked" });
    expect(h.notices[0]).toMatchObject({ code: "blocked", block: "B8" });
  });

  it("allows five generations at once and refuses the sixth", async () => {
    const pending: (() => void)[] = [];
    const h = harness(() => new Promise<StreamOutcome>((r) => pending.push(() => r({ kind: "ended" }))));
    for (let i = 0; i < 5; i++) {
      const out = await h.engine.send(params({ key: `c${i}`, branchReason: "retry" }));
      expect(out.status).toBe("sent");
    }
    const sixth = await h.engine.send(params({ key: "c9", branchReason: "retry" }));
    expect(sixth).toEqual({ status: "not_sent", reason: "cap" });
    expect(h.notices.at(-1)).toMatchObject({ code: "concurrency", count: 5 });
    pending.forEach((f) => f());
  });

  it("blocks a second default send on a branch that is streaming", async () => {
    const h = harness(() => new Promise<StreamOutcome>(() => undefined));
    await h.engine.send(params());
    expect(await h.engine.send(params({ text: "again" }))).toEqual({ status: "not_sent", reason: "busy" });
  });

  it("turns a failed request into an error reply, restores the draft and raises a notice", async () => {
    const h = harness(async () => {
      throw new ApiError({ message: "upstream down", status: 502, requestId: "req-1" });
    });
    const restore = vi.fn(() => true);
    h.engine.setVisible("c1");
    await h.engine.send(params({ restore }));
    await vi.advanceTimersByTimeAsync(10);
    const v = h.engine.view("c1");
    const reply = v.path[1];
    expect(reply?.status).toBe("error");
    expect(reply?.alert).toMatchObject({ kind: "interrupted", errorId: "req-1" });
    expect(restore).toHaveBeenCalled();
    expect(h.notices.at(-1)).toMatchObject({ code: "sendFailed" });
    expect(h.runs.isRunning("c1")).toBe(true); // orphaned: the server may still be running it
  });

  it("does not restore when the setting is off", async () => {
    const h = harness(async () => {
      throw new ApiError({ message: "x", status: 500 });
    });
    const restore = vi.fn(() => true);
    await h.engine.send(params({ restore, restoreOnFailure: false }));
    await vi.advanceTimersByTimeAsync(10);
    expect(restore).not.toHaveBeenCalled();
  });

  it("shows an interrupted reply when the error event carries the persisted pair", async () => {
    const data = result("r", { asst: { status: "interrupted", errorMessage: "connection lost", errorCode: "conversation_run.stream_interrupted", errorRequestID: "rq9" } });
    const h = harness(async () => ({ kind: "interrupted", data, error: { message: "cut", requestId: "evt" } }));
    h.engine.setVisible("c1");
    await h.engine.send(params());
    await vi.advanceTimersByTimeAsync(10);
    const reply = h.engine.view("c1").path[1];
    expect(reply?.status).toBe("interrupted");
    expect(reply?.alert).toMatchObject({ kind: "interrupted", errorId: "rq9" });
  });

  it("marks a moderation block and empties the reply", async () => {
    const data = result("r", { asst: { status: "blocked", content: "secret" } });
    const h = harness(async () => ({ kind: "blocked", event: { eventID: "e1", categories: ["violence"], data } }));
    const restore = vi.fn(() => true);
    h.engine.setVisible("c1");
    await h.engine.send(params({ restore }));
    await vi.advanceTimersByTimeAsync(10);
    const reply = h.engine.view("c1").path[1];
    expect(reply?.status).toBe("blocked");
    expect(reply?.content).toBe("");
    expect(h.notices.map((n) => n.code)).toContain("moderation");
    expect(restore).not.toHaveBeenCalled();
  });

  it("keeps zero usage events from erasing known counters", async () => {
    let handlers!: RunStreamHandlers;
    const h = harness((_p, _b, _s, hd) => {
      handlers = hd;
      return new Promise<StreamOutcome>(() => undefined);
    });
    await h.engine.send(params());
    handlers.onUsage?.({ input_tokens: 10, output_tokens: 3 });
    handlers.onUsage?.({ input_tokens: 0, output_tokens: 0 });
    expect(h.engine.view("c1").path[1]?.usage).toMatchObject({ input: 10, output: 3 });
  });

  it("applies a snapshot and continues from it", async () => {
    let handlers!: RunStreamHandlers;
    const h = harness((_p, _b, _s, hd) => {
      handlers = hd;
      return new Promise<StreamOutcome>(() => undefined);
    });
    await h.engine.send(params());
    handlers.onDelta?.("abc");
    handlers.onSnapshot?.("xyz");
    handlers.onDelta?.("!");
    await vi.advanceTimersByTimeAsync(60);
    expect(h.engine.view("c1").path[1]?.content).toBe("xyz!");
  });
});

describe("stop", () => {
  it("requests one cancel, keeps the stream, and aborts after the grace period", async () => {
    let signal!: AbortSignal;
    const h = harness((_p, _b, s) => {
      signal = s;
      return new Promise<StreamOutcome>((_res, rej) => s.addEventListener("abort", () => rej(Object.assign(new Error("aborted"), { name: "AbortError" }))));
    });
    h.engine.setVisible("c1");
    await h.engine.send(params());
    await h.engine.stop("c1");
    await h.engine.stop("c1");
    expect(h.cancel).toHaveBeenCalledTimes(1);
    expect(signal.aborted).toBe(false);
    await vi.advanceTimersByTimeAsync(25_000);
    expect(signal.aborted).toBe(true);
    await vi.advanceTimersByTimeAsync(10);
    expect(h.engine.view("c1").path[1]?.status).toBe("canceled");
  });

  it("aborts locally when the cancel request fails", async () => {
    const h = harness((_p, _b, s) => new Promise<StreamOutcome>((_res, rej) => s.addEventListener("abort", () => rej(Object.assign(new Error("a"), { name: "AbortError" })))));
    h.cancel.mockRejectedValueOnce(new Error("nope"));
    await h.engine.send(params());
    await h.engine.stop("c1");
    await vi.advanceTimersByTimeAsync(10);
    expect(h.engine.view("c1").path[1]?.status).toBe("canceled");
  });

  it("aborts at once without a token", async () => {
    const h = harness((_p, _b, s) => new Promise<StreamOutcome>((_res, rej) => s.addEventListener("abort", () => rej(Object.assign(new Error("a"), { name: "AbortError" })))), false);
    await h.engine.send(params());
    await h.engine.stop("c1");
    await vi.advanceTimersByTimeAsync(10);
    expect(h.cancel).not.toHaveBeenCalled();
    expect(h.engine.view("c1").path[1]?.status).toBe("canceled");
  });
});

describe("queue", () => {
  it("holds a message while a reply runs and sends it after the reply persisted", async () => {
    const first = { release: (_o: StreamOutcome) => undefined as void };
    let calls = 0;
    const h = harness(
      (_p, body) => {
        calls++;
        if (calls === 1) return new Promise<StreamOutcome>((r) => (first.release = r));
        return Promise.resolve({ kind: "completed", data: result(String(body.clientRunID), { asst: { content: "second" } }) });
      },
    );
    h.engine.setVisible("c1");
    h.server.set("c1", []);
    await h.engine.load("c1");
    const a = await h.engine.send(params({ text: "A" }));
    expect(h.engine.shouldQueue("c1")).toBe(true);
    const queued = h.engine.enqueue(params({ text: "B" }));
    expect(queued?.parentRunId).toBe(a.status === "sent" ? a.runId : "");
    expect(h.engine.queueStore.get()).toHaveLength(1);
    expect(calls).toBe(1);

    const runA = a.status === "sent" ? a.runId : "";
    h.server.set("c1", [
      dto({ id: 1, publicID: "U1", role: "user", runID: runA, content: "A" }),
      dto({ id: 2, publicID: "A1", role: "assistant", runID: runA, parentPublicID: "U1", content: "first" }),
    ]);
    first.release({ kind: "completed", data: result(runA, { asst: { content: "first" } }) });
    await vi.advanceTimersByTimeAsync(20);
    expect(calls).toBe(2);
    expect(h.engine.queueStore.get()).toHaveLength(0);
    expect(h.post.mock.calls[1]?.[1]).toMatchObject({ content: "B", parentMessagePublicID: "A1" });
  });

  it("cancels queued messages once when their parent fails", async () => {
    const first = { release: (_o: StreamOutcome) => undefined as void, fail: (_e: unknown) => undefined as void };
    const h = harness(() => new Promise<StreamOutcome>((res, rej) => ((first.release = res), (first.fail = rej))));
    h.engine.setVisible("c1");
    await h.engine.send(params({ text: "A" }));
    h.engine.enqueue(params({ text: "B" }));
    h.engine.enqueue(params({ text: "C" }));
    first.fail(new ApiError({ message: "boom", status: 500 }));
    await vi.advanceTimersByTimeAsync(20);
    expect(h.engine.queueStore.get()).toHaveLength(0);
    expect(h.notices.filter((n) => n.code === "queuedCanceled")).toHaveLength(2);
    expect(h.post).toHaveBeenCalledTimes(1);
  });

  it("edits and removes queued items, relinking the chain", async () => {
    const h = harness(() => new Promise<StreamOutcome>(() => undefined));
    await h.engine.send(params({ text: "A" }));
    const b = h.engine.enqueue(params({ text: "B" }));
    const c = h.engine.enqueue(params({ text: "C" }));
    expect(c?.parentRunId).toBe(b?.runId);
    h.engine.editQueued(b!.id, "   ");
    const left = h.engine.queueStore.get();
    expect(left.map((q) => q.text)).toEqual(["C"]);
    expect(left[0]?.parentRunId).toBe(b?.parentRunId);
  });
});

describe("re-attaching", () => {
  it("follows a pending reply and reloads when the stream ends", async () => {
    const h = harness(async () => ({ kind: "ended" }));
    const resume = vi.fn(async (_run: string, _after: number, _s: AbortSignal, hd: RunStreamHandlers): Promise<StreamOutcome> => {
      hd.onSnapshot?.("so far");
      hd.onDelta?.(" more");
      hd.onSeq?.(4);
      return { kind: "ended" };
    });
    (h.engine as unknown as { deps: EngineDeps }).deps.streams.resume = resume;
    h.server.set("c1", [
      dto({ id: 1, publicID: "U1", role: "user", runID: "run_a", content: "q" }),
      dto({ id: 2, publicID: "A1", role: "assistant", runID: "run_a", parentPublicID: "U1", status: "pending", content: "" }),
    ]);
    h.engine.setVisible("c1");
    await h.engine.load("c1");
    await vi.advanceTimersByTimeAsync(10);
    expect(resume).toHaveBeenCalledWith("run_a", 0, expect.anything(), expect.anything());
    // the stream ended: the attachment is cleared and the list reloaded
    expect(h.engine.view("c1").streamingHere).toBe(false);
  });
  it("does not show text twice when the replay repeats what the snapshot holds", async () => {
    const h = harness(async () => ({ kind: "ended" }));
    let release!: () => void;
    const resume = vi.fn(async (_run: string, _after: number, _s: AbortSignal, hd: RunStreamHandlers): Promise<StreamOutcome> => {
      hd.onSnapshot?.("Hello wor", { seq: 0 });
      hd.onDelta?.("Hel");
      hd.onDelta?.("lo ");
      hd.onDelta?.("wor");
      hd.onDelta?.("ld");
      await new Promise<void>((r) => (release = r));
      return { kind: "ended" };
    });
    (h.engine as unknown as { deps: EngineDeps }).deps.streams.resume = resume;
    h.server.set("c1", [
      dto({ id: 1, publicID: "U1", role: "user", runID: "run_a", content: "q" }),
      dto({ id: 2, publicID: "A1", role: "assistant", runID: "run_a", parentPublicID: "U1", status: "pending", content: "" }),
    ]);
    h.engine.setVisible("c1");
    await h.engine.load("c1");
    await vi.advanceTimersByTimeAsync(10);
    expect(h.engine.view("c1").path[1]?.content).toBe("Hello world");
    release();
    await vi.advanceTimersByTimeAsync(10);
  });

  it("keeps every delta when the snapshot already sits at the end of the replayed range", async () => {
    const h = harness(async () => ({ kind: "ended" }));
    let release!: () => void;
    const resume = vi.fn(async (_run: string, _after: number, _s: AbortSignal, hd: RunStreamHandlers): Promise<StreamOutcome> => {
      hd.onSnapshot?.("Hello", { seq: 9 });
      hd.onDelta?.("Hello");
      await new Promise<void>((r) => (release = r));
      return { kind: "ended" };
    });
    (h.engine as unknown as { deps: EngineDeps }).deps.streams.resume = resume;
    h.server.set("c1", [
      dto({ id: 1, publicID: "U1", role: "user", runID: "run_a", content: "q" }),
      dto({ id: 2, publicID: "A1", role: "assistant", runID: "run_a", parentPublicID: "U1", status: "pending", content: "" }),
    ]);
    h.engine.setVisible("c1");
    await h.engine.load("c1");
    await vi.advanceTimersByTimeAsync(10);
    expect(h.engine.view("c1").path[1]?.content).toBe("HelloHello");
    release();
    await vi.advanceTimersByTimeAsync(10);
  });
  it("drops the snapshot's thinking entry once the replay has supplied the same thinking", async () => {
    const h = harness(async () => ({ kind: "ended" }));
    let release!: () => void;
    const resume = vi.fn(async (_run: string, _after: number, _s: AbortSignal, hd: RunStreamHandlers): Promise<StreamOutcome> => {
      hd.onSnapshot?.("", { seq: 0 });
      hd.onSnapshotThink?.("abcdef");
      hd.onThink?.({ eventID: "e1", delta: "abc", status: "running" });
      expect((h.engine.liveThinking.get()["run_a"] ?? []).map((e) => e.id)).toContain("snapshot");
      hd.onThink?.({ eventID: "e1", delta: "def", status: "running" });
      await new Promise<void>((r) => (release = r));
      return { kind: "ended" };
    });
    (h.engine as unknown as { deps: EngineDeps }).deps.streams.resume = resume;
    h.server.set("c1", [
      dto({ id: 1, publicID: "U1", role: "user", runID: "run_a", content: "q" }),
      dto({ id: 2, publicID: "A1", role: "assistant", runID: "run_a", parentPublicID: "U1", status: "pending", content: "" }),
    ]);
    h.engine.setVisible("c1");
    await h.engine.load("c1");
    await vi.advanceTimersByTimeAsync(10);
    const entries = h.engine.liveThinking.get()["run_a"] ?? [];
    expect(entries.map((e) => e.id)).toEqual(["e1"]);
    expect(entries[0]?.text).toBe("abcdef");
    release();
    await vi.advanceTimersByTimeAsync(10);
  });
});
