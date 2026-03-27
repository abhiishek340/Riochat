import { describe, expect, it } from "vitest";
import { ApiError } from "@/lib/api/errors";
import { consumeRunStream, type RunStreamHandlers } from "./stream-consume";

function response(chunks: string[], status = 200): Response {
  const enc = new TextEncoder();
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const c of chunks) controller.enqueue(enc.encode(c));
      controller.close();
    },
  });
  return new Response(body, { status });
}

const lines = (...events: unknown[]) => events.map((e) => JSON.stringify(e)).join("\n") + "\n";

function recorder() {
  const log: string[] = [];
  const handlers: RunStreamHandlers = {
    onDelta: (t) => log.push(`delta:${t}`),
    onSnapshot: (t) => log.push(`snap:${t}`),
    onSnapshotThink: (t) => log.push(`think:${t}`),
    onSeq: (s) => log.push(`seq:${s}`),
    onUsage: (u) => log.push(`usage:${u.output_tokens}`),
    onFileProc: (m) => log.push(`file:${m}`),
  };
  return { log, handlers };
}

describe("consumeRunStream", () => {
  it("applies deltas, snapshots and usage, then returns the completed result", async () => {
    const { log, handlers } = recorder();
    const result = { userMessage: null, assistantMessage: { publicID: "a" } };
    const outcome = await consumeRunStream(
      response([
        lines({ type: "file_proc", message: "x", seq: 1 }, { type: "delta", delta: "Hel", seq: 2 }),
        lines({ type: "delta", delta: "abc", replace: true, seq: 3 }, { type: "delta", delta: "!", seq: 4 }, { type: "usage", output_tokens: 5 }),
        lines({ type: "completed", data: result, seq: 5 }),
      ]),
      handlers,
    );
    expect(outcome).toEqual({ kind: "completed", data: result });
    expect(log).toEqual(["seq:1", "file:x", "seq:2", "delta:Hel", "seq:3", "snap:abc", "seq:4", "delta:!", "usage:5", "seq:5"]);
  });

  it("copes with events split across chunks", async () => {
    const { log, handlers } = recorder();
    const text = lines({ type: "delta", delta: "{brace} \"q\"" }, { type: "completed", data: {} });
    const outcome = await consumeRunStream(response([text.slice(0, 7), text.slice(7, 30), text.slice(30)]), handlers);
    expect(outcome.kind).toBe("completed");
    expect(log).toEqual(['delta:{brace} "q"']);
  });

  it("reads the resume snapshot event", async () => {
    const { log, handlers } = recorder();
    const outcome = await consumeRunStream(response([lines({ type: "snapshot", seq: 7, content: "so far", think: "hmm" }, { type: "delta", delta: " more" })]), handlers, { resume: true });
    expect(outcome).toEqual({ kind: "ended" });
    expect(log).toEqual(["seq:7", "snap:so far", "think:hmm", "delta: more"]);
  });

  it("fails on an unknown event with its own status and message", async () => {
    const { handlers } = recorder();
    await expect(consumeRunStream(response([lines({ type: "mystery", message: "boom", status: 418 })]), handlers)).rejects.toMatchObject({
      status: 418,
      message: "boom",
    });
  });

  it("resolves an error event that carries the persisted pair as interrupted", async () => {
    const { handlers } = recorder();
    const outcome = await consumeRunStream(
      response([lines({ type: "error", status: 503, message: "cut", errorCode: "conversation_run.stream_interrupted", requestId: "r1", data: { userMessage: null, assistantMessage: null } })]),
      handlers,
    );
    expect(outcome.kind).toBe("interrupted");
    if (outcome.kind === "interrupted") expect(outcome.error.requestId).toBe("r1");
  });

  it("throws an error event without a result", async () => {
    const { handlers } = recorder();
    const err = await consumeRunStream(response([lines({ type: "error", status: 429, message: "slow down", errorCode: "x.y", requestId: "rq" })]), handlers).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 429, errorCode: "x.y", requestId: "rq" });
  });

  it("reports a moderation block", async () => {
    const { handlers } = recorder();
    const outcome = await consumeRunStream(response([lines({ type: "moderation_blocked", eventID: "e", categories: ["violence"], billedReason: "moderation_blocked_upstream_usage" })]), handlers);
    expect(outcome).toMatchObject({ kind: "blocked", event: { eventID: "e", categories: ["violence"] } });
  });

  it("requires a terminal result unless resuming", async () => {
    const { handlers } = recorder();
    await expect(consumeRunStream(response([lines({ type: "delta", delta: "x" })]), handlers)).rejects.toBeInstanceOf(ApiError);
    await expect(consumeRunStream(new Response(null, { status: 200 }), handlers)).rejects.toBeInstanceOf(ApiError);
    await expect(consumeRunStream(new Response(null, { status: 200 }), handlers, { resume: true })).resolves.toEqual({ kind: "ended" });
  });
  it("hands the snapshot its sequence number", async () => {
    const seen: { text: string; seq: number }[] = [];
    await consumeRunStream(
      response([lines({ type: "snapshot", content: "abc", think: "", seq: 4 }, { type: "delta", delta: "x", replace: true, seq: 9 }, { type: "completed", data: {} })]),
      { onSnapshot: (text, meta) => seen.push({ text, seq: meta?.seq ?? -1 }) },
    );
    expect(seen).toEqual([
      { text: "abc", seq: 4 },
      { text: "x", seq: 9 },
    ]);
  });
});
