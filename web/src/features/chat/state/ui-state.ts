/**
 * Interactive block state of messages. Changes live in memory first (reads
 * prefer them over the server snapshot) and are written per message, debounced,
 * once the message can be a write target.
 */
import type { ChatMessage } from "../types";
import type { UiStateEntry } from "../lib/request";
import { putUiState } from "../api";
import { scanMessageBlocks } from "../lib/blocks/block";
import { BLOCK_ID_PATTERN } from "../lib/blocks/ids";
import { setUiStateCollector } from "./engine-instance";

export const DEBOUNCE_MS = 1000;

interface Pending {
  state: Record<string, unknown> | null;
  title?: string;
}

interface Slot {
  conversationId: string;
  messageId: string;
  /** Current local values; null = restored to defaults. */
  local: Map<string, Pending>;
  /** Block ids changed since the last successful write. */
  dirty: Set<string>;
  timer: ReturnType<typeof setTimeout> | null;
  writable: boolean;
}

const slots = new Map<string, Slot>();
const listeners = new Set<() => void>();
let version = 0;

const slotKey = (conversationId: string, messageId: string) => `${conversationId}|${messageId}`;

function emit(): void {
  version++;
  for (const l of [...listeners]) l();
}

export function subscribeUiState(cb: () => void): () => void {
  listeners.add(cb);
  return () => listeners.delete(cb);
}

export function uiStateVersion(): number {
  return version;
}

function slotFor(conversationId: string, messageId: string): Slot {
  const key = slotKey(conversationId, messageId);
  let s = slots.get(key);
  if (!s) {
    s = { conversationId, messageId, local: new Map(), dirty: new Set(), timer: null, writable: false };
    slots.set(key, s);
  }
  return s;
}

export function isPersistableId(id: string): boolean {
  return BLOCK_ID_PATTERN.test(id);
}

export function isWriteTarget(message: ChatMessage, conversationId: string, temporary: boolean): boolean {
  return !temporary && !!conversationId && message.role === "assistant" && message.persisted && message.status !== "pending" && !message.live?.streaming;
}

function isPlain(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

async function flush(slot: Slot, keepalive: boolean): Promise<void> {
  if (slot.timer) {
    clearTimeout(slot.timer);
    slot.timer = null;
  }
  if (!slot.writable || slot.dirty.size === 0) return;
  const ids = [...slot.dirty];
  const blocks: Record<string, Record<string, unknown> | null> = {};
  for (const id of ids) {
    if (!isPersistableId(id)) {
      slot.dirty.delete(id);
      continue;
    }
    const p = slot.local.get(id);
    blocks[id] = p && p.state && Object.keys(p.state).length > 0 ? p.state : null;
  }
  if (Object.keys(blocks).length === 0) return;
  try {
    await putUiState(slot.conversationId, slot.messageId, blocks, { keepalive });
    for (const id of Object.keys(blocks)) {
      // A newer change made while the request was in flight stays dirty.
      const now = slot.local.get(id);
      const sent = blocks[id];
      const same = (now?.state ?? null) === sent || JSON.stringify(now?.state ?? null) === JSON.stringify(sent);
      if (same) slot.dirty.delete(id);
    }
  } catch {
    // Silent: the next change retries.
  }
}

function schedule(slot: Slot): void {
  if (!slot.writable || slot.timer) return;
  slot.timer = setTimeout(() => {
    slot.timer = null;
    void flush(slot, false);
  }, DEBOUNCE_MS);
}

export interface WriteContext {
  conversationId: string;
  message: ChatMessage;
  temporary: boolean;
  /** Called with the new state for in-memory-only conversations (temporary chat). */
  sink?: (messageId: string, blockId: string, state: Record<string, unknown> | null) => void;
}

export function readBlockState(ctx: WriteContext, blockId: string): Record<string, unknown> | undefined {
  const slot = slots.get(slotKey(ctx.conversationId, ctx.message.id));
  const local = slot?.local.get(blockId);
  if (local) return local.state ?? undefined;
  const server = ctx.message.uiState[blockId];
  return isPlain(server) ? server : undefined;
}

export function writeBlockState(ctx: WriteContext, blockId: string, state: Record<string, unknown> | null, title?: string): void {
  const slot = slotFor(ctx.conversationId, ctx.message.id);
  slot.local.set(blockId, { state, title });
  ctx.sink?.(ctx.message.id, blockId, state);
  slot.writable = isWriteTarget(ctx.message, ctx.conversationId, ctx.temporary);
  if (isPersistableId(blockId)) {
    slot.dirty.add(blockId);
    schedule(slot);
  }
  emit();
}

/** Marks a slot writable once its message settles; pending changes then go out. */
export function syncWriteTarget(ctx: Pick<WriteContext, "conversationId" | "message" | "temporary">): void {
  const slot = slots.get(slotKey(ctx.conversationId, ctx.message.id));
  if (!slot) return;
  const writable = isWriteTarget(ctx.message, ctx.conversationId, ctx.temporary);
  slot.writable = writable;
  if (writable && slot.dirty.size > 0) schedule(slot);
}

export function flushAll(keepalive: boolean): void {
  for (const slot of slots.values()) void flush(slot, keepalive);
}

export function clearUiState(): void {
  for (const s of slots.values()) if (s.timer) clearTimeout(s.timer);
  slots.clear();
  emit();
}

/** Non-empty block states for the next request: local values over the server snapshot, newest last. */
export function collectUiStates(path: readonly ChatMessage[], titles: (m: ChatMessage, blockId: string) => string | undefined = () => undefined): UiStateEntry[] {
  const out: UiStateEntry[] = [];
  for (const m of path) {
    if (m.role !== "assistant") continue;
    const merged = new Map<string, { state: Record<string, unknown>; title?: string }>();
    for (const [id, s] of Object.entries(m.uiState)) if (isPlain(s)) merged.set(id, { state: s });
    for (const slot of slots.values()) {
      if (slot.messageId !== m.id) continue;
      for (const [id, p] of slot.local) {
        if (p.state && Object.keys(p.state).length > 0) merged.set(id, { state: p.state, title: p.title });
        else merged.delete(id);
      }
    }
    for (const [blockID, v] of merged) {
      if (Object.keys(v.state).length === 0) continue;
      const title = v.title ?? titles(m, blockID);
      out.push({ messagePublicID: m.id, blockID, ...(title ? { title } : {}), state: v.state });
    }
  }
  return out;
}

let installed = false;

/** Registers the collector and the page-hide flush once. */
export function installUiState(): void {
  if (installed || typeof window === "undefined") return;
  installed = true;
  setUiStateCollector((path) => collectUiStates(path, (m, id) => scanMessageBlocks(m.content).blocks.find((b) => b.key === id)?.title || undefined));
  const onHide = () => flushAll(true);
  window.addEventListener("pagehide", onHide);
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") onHide();
  });
}
