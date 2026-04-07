/**
 * Messages queued while a reply is running. A queue is a chain: each item names
 * the run it waits for (`parentRunId`), the head waits for the active reply.
 * All operations return new arrays; dispatch is a pure decision.
 */
import type { ChatAttachment } from "../types";
import type { Options } from "./options";
import type { Selections } from "./request";

export interface QueuedMessage {
  id: string;
  /** Client run id the message will use when it is sent. */
  runId: string;
  conversationKey: string;
  text: string;
  attachments: ChatAttachment[];
  model: string;
  options: Options;
  selections: Selections;
  parentRunId: string | null;
  parentMessageId: string | null;
  sendAfterCurrent?: boolean;
  createdAt: number;
}

export const SUCCESS_STATUSES = new Set(["success", "interrupted"]);
export const FAILED_STATUSES = new Set(["error", "canceled", "cancelled", "blocked", "unavailable"]);
export const TERMINAL_STATUSES = new Set([...SUCCESS_STATUSES, ...FAILED_STATUSES]);

export function isFailedStatus(status: string): boolean {
  return FAILED_STATUSES.has(status.toLowerCase());
}

export function itemsOf(queue: readonly QueuedMessage[], key: string): QueuedMessage[] {
  return queue.filter((q) => q.conversationKey === key);
}

/** The run a new item at the end of this conversation's queue waits for. */
export function parentRunForNew(queue: readonly QueuedMessage[], key: string, activeRunId: string | null): string | null {
  const items = itemsOf(queue, key);
  return items.length > 0 ? (items[items.length - 1] as QueuedMessage).runId : activeRunId;
}

export function enqueue(queue: readonly QueuedMessage[], item: QueuedMessage): QueuedMessage[] {
  return [...queue, item];
}

/** Removes one item and re-attaches its followers to its own parent. */
export function removeItem(queue: readonly QueuedMessage[], id: string): { queue: QueuedMessage[]; removed: QueuedMessage | null } {
  const removed = queue.find((q) => q.id === id) ?? null;
  if (!removed) return { queue: [...queue], removed: null };
  const next = queue
    .filter((q) => q.id !== id)
    .map((q) =>
      q.parentRunId === removed.runId ? { ...q, parentRunId: removed.parentRunId, parentMessageId: removed.parentMessageId } : q,
    );
  return { queue: next, removed };
}

/** Saves edited text; an emptied item without attachments is deleted. */
export function editItem(queue: readonly QueuedMessage[], id: string, text: string): { queue: QueuedMessage[]; removed: QueuedMessage | null } {
  const item = queue.find((q) => q.id === id);
  if (!item) return { queue: [...queue], removed: null };
  const trimmed = text.trim();
  if (trimmed === "" && item.attachments.length === 0) return removeItem(queue, id);
  return { queue: queue.map((q) => (q.id === id ? { ...q, text: trimmed } : q)), removed: null };
}

/**
 * Moves an item to the front of its conversation's queue so it is sent next.
 * It then waits for the active run; the old head waits for it.
 */
export function guideToFront(queue: readonly QueuedMessage[], id: string, activeRunId: string | null): QueuedMessage[] {
  const item = queue.find((q) => q.id === id);
  if (!item) return [...queue];
  const same = itemsOf(queue, item.conversationKey);
  if (same[0]?.id === id) return queue.map((q) => (q.id === id ? { ...q, parentRunId: activeRunId, parentMessageId: null } : q));
  // detach: followers of the moved item attach to its former parent
  const detached = removeItem(queue, id).queue;
  const head = itemsOf(detached, item.conversationKey)[0];
  const moved: QueuedMessage = { ...item, parentRunId: activeRunId, parentMessageId: null };
  const out: QueuedMessage[] = [];
  let placed = false;
  for (const q of detached) {
    if (!placed && head && q.id === head.id) {
      out.push(moved);
      placed = true;
      out.push({ ...q, parentRunId: moved.runId, parentMessageId: null });
      continue;
    }
    out.push(q);
  }
  if (!placed) out.push(moved);
  return out;
}

/** Items that wait, directly or not, for `runId`. */
export function descendantsOf(queue: readonly QueuedMessage[], runId: string): QueuedMessage[] {
  const found: QueuedMessage[] = [];
  const waiting = new Set([runId]);
  let grew = true;
  while (grew) {
    grew = false;
    for (const q of queue) {
      if (q.parentRunId && waiting.has(q.parentRunId) && !waiting.has(q.runId)) {
        waiting.add(q.runId);
        found.push(q);
        grew = true;
      }
    }
  }
  return found;
}

export function cancelDescendants(queue: readonly QueuedMessage[], failedRunId: string): { queue: QueuedMessage[]; cancelled: QueuedMessage[] } {
  const cancelled = descendantsOf(queue, failedRunId);
  const ids = new Set(cancelled.map((c) => c.id));
  return { queue: queue.filter((q) => !ids.has(q.id)), cancelled };
}

export type ParentOutcome =
  | { state: "success"; messageId: string }
  | { state: "failed" }
  | { state: "running" }
  | { state: "unknown" };

export interface DispatchContext {
  activeStreams: number;
  cap: number;
  /** An active stream, a pending server generation or an attached resume. */
  branchBusy: (key: string) => boolean;
  /** A default-branch exchange that has not been settled yet. */
  unresolvedExchange: (key: string) => boolean;
  parentOutcome: (runId: string) => ParentOutcome;
}

export interface Dispatch {
  item: QueuedMessage;
  parentMessageId: string | null;
}

/** The first dispatchable head, or null. */
export function pickDispatchable(queue: readonly QueuedMessage[], ctx: DispatchContext): Dispatch | null {
  if (ctx.activeStreams >= ctx.cap) return null;
  const seen = new Set<string>();
  for (const item of queue) {
    if (seen.has(item.conversationKey)) continue; // only heads
    seen.add(item.conversationKey);
    if (ctx.branchBusy(item.conversationKey)) continue;
    if (!item.sendAfterCurrent && ctx.unresolvedExchange(item.conversationKey)) continue;
    if (!item.parentRunId) return { item, parentMessageId: item.parentMessageId };
    const outcome = ctx.parentOutcome(item.parentRunId);
    if (outcome.state === "success") return { item, parentMessageId: outcome.messageId };
  }
  return null;
}

/** Items whose parent run ended badly, with everything that waits behind them. */
export function failedChains(queue: readonly QueuedMessage[], outcome: (runId: string) => ParentOutcome): { queue: QueuedMessage[]; cancelled: QueuedMessage[] } {
  let current = [...queue];
  const cancelled: QueuedMessage[] = [];
  for (const item of queue) {
    if (!item.parentRunId) continue;
    if (!current.some((q) => q.id === item.id)) continue;
    if (outcome(item.parentRunId).state !== "failed") continue;
    const own = current.find((q) => q.id === item.id) as QueuedMessage;
    const res = cancelDescendants(current, own.parentRunId as string);
    current = res.queue;
    cancelled.push(...res.cancelled);
  }
  return { queue: current, cancelled };
}

// --- run status book ---------------------------------------------------------------------

export const MISSING_AFTER_MS = 60_000;

/** Statuses polled for runs this tab no longer owns. */
export class RunStatusBook {
  private status = new Map<string, string>();
  private firstAsked = new Map<string, number>();
  private lastSeen = new Map<string, number>();

  ask(runId: string, now: number): void {
    if (!this.firstAsked.has(runId)) this.firstAsked.set(runId, now);
  }

  apply(asked: readonly string[], answers: readonly { runID: string; status: string }[], now: number): void {
    const answered = new Set<string>();
    for (const a of answers) {
      this.status.set(a.runID, a.status.toLowerCase());
      this.lastSeen.set(a.runID, now);
      answered.add(a.runID);
    }
    for (const id of asked) if (!answered.has(id) && !this.firstAsked.has(id)) this.firstAsked.set(id, now);
  }

  /** Latest status; a run absent from responses for 60 s counts as unavailable. */
  get(runId: string, now: number): string | null {
    const known = this.status.get(runId);
    if (known && TERMINAL_STATUSES.has(known)) return known;
    const reference = this.lastSeen.get(runId) ?? this.firstAsked.get(runId);
    if (reference !== undefined && now - reference >= MISSING_AFTER_MS && !this.answeredSince(runId, now)) return "unavailable";
    return known ?? null;
  }

  private answeredSince(runId: string, now: number): boolean {
    const seen = this.lastSeen.get(runId);
    return seen !== undefined && now - seen < MISSING_AFTER_MS;
  }

  forget(runId: string): void {
    this.status.delete(runId);
    this.firstAsked.delete(runId);
    this.lastSeen.delete(runId);
  }
}

/** Poll delay: 1.5 s times 2^failures, capped at times four. */
export function pollDelay(failures: number): number {
  return 1500 * Math.min(4, 2 ** Math.max(0, failures));
}
