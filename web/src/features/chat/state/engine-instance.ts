"use client";

/** The one engine of the app, wired to the real API. */
import { api } from "@/lib/api/client";
import { sessionStore } from "@/lib/api/session-store";
import { seg } from "@/lib/api/http";
import { cancelRun, getConversation, listMessages, listRuns, renameConversation, runStatuses } from "../api";
import { RunRegistry } from "../lib/run-registry";
import { consumeRunStream } from "../lib/stream-consume";
import type { ChatMessage } from "../types";
import type { UiStateEntry } from "../lib/request";
import { ChatEngine } from "./chat-engine";
import { createInList, findConversation, patchConversation } from "./conversation-store";
import type { EngineDeps } from "./engine-types";

export const runRegistry = new RunRegistry();

let uiStateCollector: (path: readonly ChatMessage[]) => UiStateEntry[] = () => [];

/** The block-state store registers how to collect states for the next turn. */
export function setUiStateCollector(fn: (path: readonly ChatMessage[]) => UiStateEntry[]): void {
  uiStateCollector = fn;
}

const NDJSON = { Accept: "application/x-ndjson" };

const deps: EngineDeps = {
  conversations: {
    create: (input) => createInList({ model: input.model, projectID: input.projectID }),
    find: findConversation,
    patch: patchConversation,
    fetch: (id) => getConversation(id),
    rename: async (id, title) => {
      const updated = await renameConversation(id, title);
      patchConversation(id, { title: updated.title });
      return updated;
    },
  },
  messages: { list: listMessages, cancelRun, runStatuses, listRuns },
  streams: {
    post: async (path, body, signal, handlers) => {
      const response = await api.raw(path, { method: "POST", body, signal, headers: NDJSON });
      return consumeRunStream(response, handlers);
    },
    resume: async (runId, after, signal, handlers) => {
      const response = await api.raw(`/conversation-runs/${seg(runId)}/stream`, {
        signal,
        headers: NDJSON,
        query: { snapshot: "true", after: after > 0 ? Math.floor(after) : undefined },
      });
      return consumeRunStream(response, handlers, { resume: true });
    },
  },
  runs: runRegistry,
  hasToken: () => sessionStore.token() !== "",
  now: () => Date.now(),
  revokeUrl: (url) => {
    try {
      URL.revokeObjectURL(url);
    } catch {
      /* not a blob url */
    }
  },
  collectUiStates: (path) => uiStateCollector(path),
  isDocumentVisible: () => typeof document === "undefined" || document.visibilityState !== "hidden",
};

export const chatEngine = new ChatEngine(deps);
