/**
 * Cross-tab "something changed" announcements.
 *
 * Files: other areas (deleting a conversation or project that owned files)
 * announce through `announceFilesChanged`. Delivery uses a window event for the
 * same tab, a BroadcastChannel and a storage write as the fallback for other
 * tabs. Subscribers de-duplicate by nonce and ignore malformed payloads.
 *
 * Knowledge bases: after a mutation the page writes a small payload to
 * localStorage; other tabs observe the storage event and refresh.
 */
import { isRecord } from "@/lib/utils/guards";

// --- files -------------------------------------------------------------------

export type FilesChangeReason = "conversation-deleted" | "project-deleted";

export interface FilesChange {
  reason: FilesChangeReason;
  deletedCount: number;
  quota?: { quotaBytes: number; usedBytes: number };
  sourceID?: string;
  timestamp: number;
  nonce: string;
}

const FILES_EVENT = "riochat:files-changed";
const FILES_CHANNEL = "riochat-files-changed";
const FILES_STORAGE_KEY = "riochat:files-changed";
const REASONS: readonly string[] = ["conversation-deleted", "project-deleted"];

export function parseFilesChange(value: unknown): FilesChange | null {
  if (!isRecord(value)) return null;
  const { reason, deletedCount, nonce, timestamp, quota, sourceID } = value;
  if (typeof reason !== "string" || !REASONS.includes(reason)) return null;
  if (typeof deletedCount !== "number" || !Number.isFinite(deletedCount) || deletedCount <= 0) return null;
  if (typeof nonce !== "string" || nonce === "") return null;
  const change: FilesChange = {
    reason: reason as FilesChangeReason,
    deletedCount,
    nonce,
    timestamp: typeof timestamp === "number" ? timestamp : 0,
  };
  if (isRecord(quota) && typeof quota.quotaBytes === "number" && typeof quota.usedBytes === "number") {
    change.quota = { quotaBytes: quota.quotaBytes, usedBytes: quota.usedBytes };
  }
  if (typeof sourceID === "string" && sourceID !== "") change.sourceID = sourceID;
  return change;
}

/** Remembers the most recent nonces so a message delivered twice is handled once. */
export class NonceMemory {
  private readonly seen: string[] = [];
  constructor(private readonly capacity = 50) {}

  /** True the first time `nonce` is offered. */
  take(nonce: string): boolean {
    if (this.seen.includes(nonce)) return false;
    this.seen.push(nonce);
    if (this.seen.length > this.capacity) this.seen.shift();
    return true;
  }
}

function makeNonce(): string {
  try {
    if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  } catch {
    /* fall through */
  }
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
}

/** Announces that the file library changed. A count of zero or less is not sent. */
export function announceFilesChanged(input: {
  reason: FilesChangeReason;
  deletedCount: number;
  quota?: { quotaBytes: number; usedBytes: number };
  sourceID?: string;
}): void {
  if (!(input.deletedCount > 0) || typeof window === "undefined") return;
  const change: FilesChange = { ...input, timestamp: Date.now(), nonce: makeNonce() };
  try {
    window.dispatchEvent(new CustomEvent(FILES_EVENT, { detail: change }));
  } catch {
    /* ignore */
  }
  try {
    if (typeof BroadcastChannel !== "undefined") {
      const channel = new BroadcastChannel(FILES_CHANNEL);
      channel.postMessage(change);
      channel.close();
    }
  } catch {
    /* ignore */
  }
  try {
    window.localStorage.setItem(FILES_STORAGE_KEY, JSON.stringify(change));
  } catch {
    /* ignore */
  }
}

export function subscribeFilesChanged(handler: (change: FilesChange) => void): () => void {
  if (typeof window === "undefined") return () => undefined;
  const memory = new NonceMemory();
  const deliver = (raw: unknown) => {
    const change = parseFilesChange(raw);
    if (change && memory.take(change.nonce)) handler(change);
  };
  const onEvent = (event: Event) => deliver((event as CustomEvent<unknown>).detail);
  const onStorage = (event: StorageEvent) => {
    if (event.key !== FILES_STORAGE_KEY || !event.newValue) return;
    try {
      deliver(JSON.parse(event.newValue));
    } catch {
      /* malformed */
    }
  };
  window.addEventListener(FILES_EVENT, onEvent);
  window.addEventListener("storage", onStorage);
  let channel: BroadcastChannel | null = null;
  try {
    if (typeof BroadcastChannel !== "undefined") {
      channel = new BroadcastChannel(FILES_CHANNEL);
      channel.onmessage = (event: MessageEvent<unknown>) => deliver(event.data);
    }
  } catch {
    channel = null;
  }
  return () => {
    window.removeEventListener(FILES_EVENT, onEvent);
    window.removeEventListener("storage", onStorage);
    channel?.close();
  };
}

// --- knowledge bases ---------------------------------------------------------

export interface KbChange {
  knowledgeBaseID?: string;
  timestamp: number;
  nonce: string;
}

const KB_STORAGE_KEY = "riochat:knowledge-bases-changed";

export function parseKbChange(value: unknown): KbChange | null {
  if (!isRecord(value)) return null;
  if (typeof value.nonce !== "string" || value.nonce === "") return null;
  if (typeof value.timestamp !== "number") return null;
  const change: KbChange = { timestamp: value.timestamp, nonce: value.nonce };
  if (typeof value.knowledgeBaseID === "string" && value.knowledgeBaseID !== "") change.knowledgeBaseID = value.knowledgeBaseID;
  return change;
}

export function announceKbChanged(knowledgeBaseID?: string): void {
  try {
    if (typeof window === "undefined") return;
    const change: KbChange = { knowledgeBaseID, timestamp: Date.now(), nonce: makeNonce() };
    window.localStorage.setItem(KB_STORAGE_KEY, JSON.stringify(change));
  } catch {
    /* storage unavailable: other tabs simply refresh on focus */
  }
}

export function subscribeKbChanged(handler: (change: KbChange) => void): () => void {
  if (typeof window === "undefined") return () => undefined;
  const memory = new NonceMemory();
  const onStorage = (event: StorageEvent) => {
    if (event.key !== KB_STORAGE_KEY || !event.newValue) return;
    try {
      const change = parseKbChange(JSON.parse(event.newValue));
      if (change && memory.take(change.nonce)) handler(change);
    } catch {
      /* malformed */
    }
  };
  window.addEventListener("storage", onStorage);
  return () => window.removeEventListener("storage", onStorage);
}
