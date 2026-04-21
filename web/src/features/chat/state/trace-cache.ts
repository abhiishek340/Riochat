"use client";

import { fetchMessageTrace } from "../api";
import type { ProcessTraceDTO } from "../types";
import { createStore } from "./store";

export interface TraceEntry {
  status: "loading" | "ready" | "error";
  trace: ProcessTraceDTO | null;
}

/** Full traces of finished messages, loaded on demand and cached for the page's lifetime. */
export const traceCache = createStore<Record<string, TraceEntry>>({});
const inflight = new Map<string, Promise<void>>();

export function loadFullTrace(messageId: string): Promise<void> {
  const cur = traceCache.get()[messageId];
  if (cur?.status === "ready") return Promise.resolve();
  const pending = inflight.get(messageId);
  if (pending) return pending;
  traceCache.set((s) => ({ ...s, [messageId]: { status: "loading", trace: cur?.trace ?? null } }));
  const p = fetchMessageTrace(messageId)
    .then(
      (trace) => traceCache.set((s) => ({ ...s, [messageId]: { status: "ready", trace } })),
      () => traceCache.set((s) => ({ ...s, [messageId]: { status: "error", trace: null } })),
    )
    .finally(() => inflight.delete(messageId));
  inflight.set(messageId, p);
  return p;
}
