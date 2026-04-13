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
