"use client";

/**
 * Keeps the run registry in step with the server: the SSE feed of active runs,
 * a per-user broadcast channel between tabs, and the visibility/online rules
 * for when to be connected.
 */
import { useEffect, useSyncExternalStore } from "react";
import { followActiveRuns, type ActiveRunEvent } from "@/lib/api/stream";
import { isAbortError } from "@/lib/api/errors";
import { chatEngine, runRegistry } from "./engine-instance";
import { createStore } from "./store";

export const runningIds = createStore<ReadonlySet<string>>(new Set());

runRegistry.subscribe(() => {
  runningIds.set(new Set(runRegistry.runningConversations()));
});

export function useConversationRunning(id: string | null | undefined): boolean {
  const set = useSyncExternalStore(runningIds.subscribe, runningIds.get, runningIds.get);
  return !!id && set.has(id);
}

export function useRunningConversationIds(): ReadonlySet<string> {
  return useSyncExternalStore(runningIds.subscribe, runningIds.get, runningIds.get);
}

const BASE_DELAY = 1000;
const MAX_DELAY = 30_000;

export function backoffDelay(attempt: number, random: () => number = Math.random): number {
  const base = Math.min(MAX_DELAY, BASE_DELAY * 2 ** Math.max(0, attempt));
  const jitter = 1 + (random() * 0.4 - 0.2);
  return Math.round(base * jitter);
}

interface ChannelMessage {
  type: "started" | "finished";
  runId: string;
  conversationId: string;
}

function openChannel(userKey: string): BroadcastChannel | null {
  try {
    return typeof BroadcastChannel === "undefined" ? null : new BroadcastChannel(`riochat:runs:${userKey}`);
  } catch {
    return null;
  }
}

/**
 * Starts following the feed for this user. Returns a stop function. Call it
 * from a component that lives as long as the signed-in app does.
 */
export function startRunFeed(userKey: string): () => void {
  let stopped = false;
  let controller: AbortController | null = null;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let attempt = 0;
  const channel = openChannel(userKey);

  const canConnect = () => !stopped && document.visibilityState !== "hidden" && navigator.onLine !== false;

  const handle = (event: ActiveRunEvent) => {
    attempt = 0;
    if (event.type === "snapshot") runRegistry.applySnapshot(event.runs, Date.now());
    else if (event.type === "started") runRegistry.noticeStarted(event.runID, event.conversationPublicID);
    else runRegistry.noticeFinished(event.runID, true);
  };

  const connect = () => {
    if (!canConnect() || controller) return;
    const ctl = new AbortController();
    controller = ctl;
    void followActiveRuns(handle, ctl.signal)
      .catch((error: unknown) => {
        if (!isAbortError(error)) return;
      })
      .finally(() => {
        if (controller === ctl) controller = null;
        if (stopped || ctl.signal.aborted) return;
        // The server closed it (or it failed): reconnect with backoff.
        const wait = backoffDelay(attempt++);
        timer = setTimeout(connect, wait);
      });
  };

  const disconnect = () => {
    if (timer) clearTimeout(timer);
    timer = null;
    controller?.abort();
    controller = null;
  };

  const onWake = () => {
    if (!canConnect()) return;
    if (timer) clearTimeout(timer);
    timer = null;
    connect();
  };
  const onSleep = () => {
    if (document.visibilityState === "hidden" || navigator.onLine === false) disconnect();
  };
  const onVisibility = () => {
    if (document.visibilityState === "hidden") onSleep();
    else {
      onWake();
      chatEngine.onVisible();
    }
  };

  document.addEventListener("visibilitychange", onVisibility);
  window.addEventListener("focus", onWake);
  window.addEventListener("online", onWake);
  window.addEventListener("offline", onSleep);

  channel?.addEventListener("message", (event: MessageEvent<ChannelMessage>) => {
    const m = event.data;
    if (!m || typeof m.runId !== "string") return;
    if (m.type === "started") runRegistry.noticeStarted(m.runId, m.conversationId);
    else if (m.type === "finished") runRegistry.noticeFinished(m.runId, false);
  });
  const offStarted = chatEngine.on("runStarted", (e) => channel?.postMessage({ type: "started", runId: e.runId, conversationId: e.conversationId } satisfies ChannelMessage));
  const offFinished = chatEngine.on("runFinished", (e) => channel?.postMessage({ type: "finished", runId: e.runId, conversationId: e.conversationId } satisfies ChannelMessage));
  const sweep = setInterval(() => runRegistry.sweep(Date.now()), 5000);

  connect();

  return () => {
    stopped = true;
    disconnect();
    clearInterval(sweep);
    document.removeEventListener("visibilitychange", onVisibility);
    window.removeEventListener("focus", onWake);
    window.removeEventListener("online", onWake);
    window.removeEventListener("offline", onSleep);
    offStarted();
    offFinished();
    channel?.close();
    runRegistry.clear();
  };
}

export function useRunFeed(userKey: string | null | undefined): void {
  useEffect(() => {
    if (!userKey) return;
    return startRunFeed(userKey);
  }, [userKey]);
}
