import { describe, expect, it, vi } from "vitest";
import { ApiError } from "./errors";
import { consumeConversationStream, JsonDocumentSplitter, parseActiveRunEvent, readSseData } from "./stream";

function responseOf(chunks: string[], status = 200): Response {
  const enc = new TextEncoder();
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      for (const chunk of chunks) c.enqueue(enc.encode(chunk));
      c.close();
    },
  });
  return new Response(body, { status });
}

describe("JsonDocumentSplitter", () => {
  it("reassembles documents split across chunks and ignores braces in strings", () => {
    const s = new JsonDocumentSplitter();
    expect(s.push('{"a":"}{"}\n{"b":')).toEqual(['{"a":"}{"}']);
    expect(s.pending).toBe(true);
    expect(s.push('{"c":1}}')).toEqual(['{"b":{"c":1}}']);
    expect(s.pending).toBe(false);
  });
  it("rejects stray leading characters", () => {
    expect(() => new JsonDocumentSplitter().push("data: {}")).toThrow();
  });
});

describe("consumeConversationStream", () => {
  it("delivers deltas and seq, then returns the completed payload", async () => {
    const onDelta = vi.fn();
    const onSeq = vi.fn();
    const res = responseOf([
      '{"type":"delta","delta":"Hel","seq":1}{"type":"del',
      'ta","delta":"lo","seq":2}{"type":"completed","seq":3,"data":{"ok":true}}',
    ]);
    const data = await consumeConversationStream(res, { onDelta, onSeq });
    expect(data).toEqual({ ok: true });
    expect(onDelta.mock.calls.map((c) => c[0])).toEqual(["Hel", "lo"]);
    expect(onSeq).toHaveBeenLastCalledWith(3);
  });

  it("treats replace deltas as snapshots", async () => {
    const onSnapshot = vi.fn();
    await consumeConversationStream(responseOf(['{"type":"delta","delta":"all","replace":true}{"type":"completed","data":1}']), { onSnapshot });
    expect(onSnapshot).toHaveBeenCalledWith("all");
  });

  it("fails when no completion arrives, unless resuming", async () => {
    await expect(consumeConversationStream(responseOf(['{"type":"delta","delta":"x"}']))).rejects.toThrow(/final payload/);
    await expect(consumeConversationStream(responseOf(['{"type":"delta","delta":"x"}']), {}, { resume: true })).resolves.toBeUndefined();
  });

  it("maps moderation_blocked to a coded API error", async () => {
    const res = responseOf(['{"type":"moderation_blocked","eventID":"e1","direction":"input","categories":["x"]}']);
    await expect(consumeConversationStream(res)).rejects.toMatchObject({
      errorCode: "content_moderation.blocked",
      details: { eventID: "e1", direction: "input", categories: ["x"] },
    });
  });

  it("uses error events: interrupted callback with data, ApiError without", async () => {
    const onInterrupted = vi.fn();
    const withData = responseOf(['{"type":"error","message":"cut","data":{"id":1}}']);
    expect(await consumeConversationStream(withData, { onInterrupted })).toEqual({ id: 1 });
    expect(onInterrupted).toHaveBeenCalled();

    const without = responseOf(['{"type":"error","status":502,"message":"bad","errorCode":"upstream.unavailable","requestId":"r1"}']);
    await expect(consumeConversationStream(without)).rejects.toMatchObject({ status: 502, errorCode: "upstream.unavailable", requestId: "r1" });
  });

  it("fails on unknown event types", async () => {
    await expect(consumeConversationStream(responseOf(['{"type":"mystery"}']))).rejects.toBeInstanceOf(ApiError);
  });
});

describe("SSE", () => {
  it("joins data lines per frame and skips comments and empty frames", async () => {
    const res = responseOf([': keepalive\n\ndata: {"a":1}\r\n\r\ndata: x\ndata:  y\n\n', "data: tail"]);
    const out: string[] = [];
    for await (const d of readSseData(res)) out.push(d);
    expect(out).toEqual(['{"a":1}', "x\ny", "tail"]);
  });

  it("parses active-run events defensively", () => {
    expect(parseActiveRunEvent('{"type":"snapshot","runs":[{"runID":"a","conversationPublicID":"c"}]}')).toEqual({
      type: "snapshot",
      runs: [{ runID: "a", conversationPublicID: "c" }],
    });
    expect(parseActiveRunEvent('{"type":"snapshot","runs":[{"conversationPublicID":"c"}]}')).toBeNull();
    expect(parseActiveRunEvent("garbage")).toBeNull();
    expect(parseActiveRunEvent('{"type":"started","runID":"r"}')).toMatchObject({ type: "started", runID: "r" });
    expect(parseActiveRunEvent('{"type":"other"}')).toBeNull();
  });
});
