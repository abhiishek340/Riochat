import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ModelFacts } from "../lib/task-decision";
import { EMPTY_SELECTIONS } from "../lib/request";
import type { Notice } from "./engine-types";

const state = vi.hoisted(() => ({
  token: "tok" as string,
  raw: undefined as undefined | ((path: string, init: { body: unknown; signal: AbortSignal }) => Promise<Response>),
}));

vi.mock("@/lib/api/client", () => ({
  api: { raw: (path: string, init: { body: unknown; signal: AbortSignal }) => state.raw!(path, init) },
  resolveAccessToken: async () => state.token,
}));

import { TempEngine, type TempSendParams } from "./temp-engine";

const facts: ModelFacts = { kinds: new Set(["chat"]), supportsVideoExtension: false };

function ndjson(...events: unknown[]): Response {
  const enc = new TextEncoder();
  return new Response(
    new ReadableStream<Uint8Array>({
      start(c) {
        c.enqueue(enc.encode(events.map((e) => JSON.stringify(e)).join("\n") + "\n"));
        c.close();
      },
    }),
    { status: 200 },
  );
}

function params(over: Partial<TempSendParams> = {}): TempSendParams {
  return {
    text: "hello",
    attachments: [],
    model: "m",
    modelFacts: facts,
    options: {},
    selections: EMPTY_SELECTIONS,
    attachmentOnlyText: "look at the files",
    omittedNote: "older turns omitted",
    attachmentNote: (names) => `omitted: ${names.join(",")}`,
    sessionExpiredText: "session expired",
    stoppedText: "generation stopped",
    ...over,
  };
}

describe("TempEngine", () => {
  beforeEach(() => {
    state.token = "tok";
    state.raw = async () => ndjson({ type: "completed", data: { assistantMessage: { content: "hi", status: "success", inputTokens: 1, outputTokens: 2 } } });
  });

  it("settles a reply and keeps it in the local history", async () => {
    const engine = new TempEngine();
    await engine.send(params());
    const [user, reply] = engine.view().path;
    expect(user?.content).toBe("hello");
    expect(reply?.content).toBe("hi");
    expect(reply?.status).toBe("success");
    expect(engine.running).toBe(false);
  });

  it("settles a completed event that carries server identity fields, an empty branch reason, empty attachments and no seq", async () => {
    const msg = (role: string, content: string) => ({
      publicID: `pub-${role}`, runID: "run-1", userID: 1, role, contentType: role === "user" ? "text" : "markdown", content,
      branchReason: "", attachments: "", status: "success", platformModelName: "m", upstreamModelName: "up",
    });
    state.raw = async () => ndjson({ type: "completed", data: { userMessage: msg("user", "hello"), assistantMessage: msg("assistant", "hi there") } });
    const engine = new TempEngine();
    await engine.send(params());
    const [user, reply] = engine.view().path;
    expect(user?.content).toBe("hello");
    expect(reply?.content).toBe("hi there");
    expect(reply?.status).toBe("success");
    expect(reply?.branchReason).toBe("default");
    expect(reply?.attachments ?? []).toHaveLength(0);
    expect(engine.running).toBe(false);
  });

  it("fails with the session-expired text and does not call the server when there is no token", async () => {
    state.token = "";
    const raw = vi.fn();
    state.raw = raw;
    const engine = new TempEngine();
    const notices: Notice[] = [];
    engine.onNotice((n) => notices.push(n));
    const restore = vi.fn(() => true);
    await engine.send(params({ restore }));
    expect(raw).not.toHaveBeenCalled();
    const reply = engine.view().path[1];
    expect(reply?.status).toBe("error");
    expect(reply?.alert?.message).toBe("session expired");
    expect(notices[0]?.code).toBe("sendFailed");
    expect(restore).toHaveBeenCalled();
  });

  it("says the generation was stopped when the user stops before any text", async () => {
    state.raw = (_path, init) =>
      new Promise<Response>((_resolve, reject) => {
        init.signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
      });
    const engine = new TempEngine();
    const sending = engine.send(params());
    await new Promise((r) => setTimeout(r, 0));
    engine.stop();
    await sending;
    const reply = engine.view().path[1];
    expect(reply?.status).toBe("canceled");
    expect(reply?.alert?.message).toBe("generation stopped");
  });

  it("keeps partial text without an alert when stopped mid-reply", async () => {
    state.raw = (_path, init) => {
      const enc = new TextEncoder();
      return Promise.resolve(
        new Response(
          new ReadableStream<Uint8Array>({
            start(c) {
              c.enqueue(enc.encode(JSON.stringify({ type: "delta", delta: "partial" }) + "\n"));
              init.signal.addEventListener("abort", () => c.error(new DOMException("aborted", "AbortError")));
            },
          }),
        ),
      );
    };
    const engine = new TempEngine();
    const sending = engine.send(params());
    await new Promise((r) => setTimeout(r, 120));
    engine.stop();
    await sending;
    const reply = engine.view().path[1];
    expect(reply?.content).toBe("partial");
    expect(reply?.alert).toBeNull();
  });

  it("discard forgets everything", async () => {
    const engine = new TempEngine();
    await engine.send(params());
    engine.discard();
    expect(engine.hasMessages).toBe(false);
  });
});
