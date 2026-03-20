/**
 * The message tree. The server sends a flat list; messages point at their
 * parent. Siblings under one parent are alternative branches and exactly one
 * of them is "selected" at each parent, which yields the visible path.
 */
import type { ChatMessage } from "../types";

export const ROOT = "";

/** parent key -> id of the selected child */
export type Selection = Record<string, string>;

export interface Navigator {
  parentKey: string;
  /** 1-based position among siblings, in list order. */
  index: number;
  total: number;
  canPrevious: boolean;
  canNext: boolean;
}

/** Key of the sibling group a message belongs to. */
export function parentKeyOf(message: ChatMessage, ids: ReadonlySet<string>): string {
  const parent = message.parentId;
  if (!parent) return ROOT;
  return ids.has(parent) ? parent : `missing:${parent}`;
}

export function groupChildren(messages: readonly ChatMessage[]): Map<string, ChatMessage[]> {
  const ids = new Set(messages.map((m) => m.id));
  const groups = new Map<string, ChatMessage[]>();
  for (const m of messages) {
    const key = parentKeyOf(m, ids);
    const list = groups.get(key);
    if (list) list.push(m);
    else groups.set(key, [m]);
  }
  return groups;
}

function topAncestor(last: ChatMessage, byId: Map<string, ChatMessage>): ChatMessage {
  const seen = new Set<string>();
  let node = last;
  while (!seen.has(node.id)) {
    seen.add(node.id);
    const parent = node.parentId ? byId.get(node.parentId) : undefined;
    if (!parent) break;
    node = parent;
  }
  return node;
}

/**
 * Keeps earlier choices that still point at an existing sibling, and forces the
 * ancestry of the newest message to be selected so it is always visible.
 */
export function reconcileSelection(messages: readonly ChatMessage[], previous: Selection): Selection {
  const next: Selection = {};
  if (messages.length === 0) return next;
  const groups = groupChildren(messages);
  const ids = new Set(messages.map((m) => m.id));
  const byId = new Map(messages.map((m) => [m.id, m] as const));

  for (const [key, siblings] of groups) {
    const prev = previous[key];
    next[key] = prev && siblings.some((s) => s.id === prev) ? prev : (siblings[siblings.length - 1] as ChatMessage).id;
  }

  const seen = new Set<string>();
  let node: ChatMessage | undefined = messages[messages.length - 1];
  while (node && !seen.has(node.id)) {
    seen.add(node.id);
    next[parentKeyOf(node, ids)] = node.id;
    node = node.parentId ? byId.get(node.parentId) : undefined;
  }
  return next;
}

/** Messages along the selected branch, root first. */
export function visiblePath(messages: readonly ChatMessage[], selection: Selection): ChatMessage[] {
  if (messages.length === 0) return [];
  const groups = groupChildren(messages);
  const byId = new Map(messages.map((m) => [m.id, m] as const));

  const walk = (startKey: string): ChatMessage[] => {
    const out: ChatMessage[] = [];
    const seen = new Set<string>();
    let key = startKey;
    for (;;) {
      const siblings = groups.get(key);
      if (!siblings || siblings.length === 0) break;
      const chosen = siblings.find((s) => s.id === selection[key]) ?? (siblings[siblings.length - 1] as ChatMessage);
      if (seen.has(chosen.id)) break;
      seen.add(chosen.id);
      out.push(chosen);
      key = chosen.id;
    }
    return out;
  };

  const fromRoot = walk(ROOT);
  if (fromRoot.length > 0) return fromRoot;

  // Orphaned data (older pages not loaded, or broken parents): start where the
  // newest message's ancestry ends.
  const last = messages[messages.length - 1] as ChatMessage;
  const top = topAncestor(last, byId);
  const ids = new Set(messages.map((m) => m.id));
  const fromTop = walk(parentKeyOf(top, ids));
  if (fromTop.length > 0) return fromTop;

  const chain: ChatMessage[] = [];
  const seen = new Set<string>();
  let node: ChatMessage | undefined = last;
  while (node && !seen.has(node.id)) {
    seen.add(node.id);
    chain.unshift(node);
    node = node.parentId ? byId.get(node.parentId) : undefined;
  }
  return chain;
}

export function navigatorsFor(path: readonly ChatMessage[], messages: readonly ChatMessage[]): Map<string, Navigator> {
  const groups = groupChildren(messages);
  const ids = new Set(messages.map((m) => m.id));
  const out = new Map<string, Navigator>();
  for (const m of path) {
    if (m.role === "system") continue;
    const key = parentKeyOf(m, ids);
    const siblings = groups.get(key) ?? [];
    if (siblings.length < 2) continue;
    const index = siblings.findIndex((s) => s.id === m.id) + 1;
    out.set(m.id, { parentKey: key, index, total: siblings.length, canPrevious: index > 1, canNext: index < siblings.length });
  }
  return out;
}

/** Selection after moving `delta` siblings from `messageId`; clamps, never wraps. */
export function cycleSelection(
  messages: readonly ChatMessage[],
  selection: Selection,
  messageId: string,
  delta: -1 | 1,
): Selection {
  const target = messages.find((m) => m.id === messageId);
  if (!target) return selection;
  const groups = groupChildren(messages);
  const ids = new Set(messages.map((m) => m.id));
  const key = parentKeyOf(target, ids);
  const siblings = groups.get(key) ?? [];
  const at = siblings.findIndex((s) => s.id === messageId);
  const to = Math.min(siblings.length - 1, Math.max(0, at + delta));
  if (to === at) return selection;
  return { ...selection, [key]: (siblings[to] as ChatMessage).id };
}

/** Rewrites local ids to server ids in keys and values of a selection. */
export function rewriteSelection(selection: Selection, idMap: Record<string, string>): Selection {
  const out: Selection = {};
  for (const [k, v] of Object.entries(selection)) {
    out[idMap[k] ?? k] = idMap[v] ?? v;
  }
  return out;
}

/** A change that requires the selection to be re-reconciled. */
export function treeSignature(messages: readonly ChatMessage[]): string {
  return messages.map((m) => `${m.id}|${m.parentId ?? ""}|${m.role}`).join("\n");
}

/**
 * Assistant replies directly below a user message show that message's input
 * side counters when their own are zero (retries keep their own values).
 */
const inheritCache = new WeakMap<ChatMessage, { from: ChatMessage["usage"]; out: ChatMessage }>();

export function inheritUsage(path: readonly ChatMessage[]): ChatMessage[] {
  return path.map((m, i) => {
    if (m.role !== "assistant" || i === 0) return m;
    const prev = path[i - 1] as ChatMessage;
    if (prev.role !== "user" || m.parentId !== prev.id) return m;
    if (m.branchReason === "retry" && m.sourceId) return m;
    const usage = { ...m.usage };
    let changed = false;
    if (!usage.input && prev.usage.input) {
      usage.input = prev.usage.input;
      changed = true;
    }
    if (!usage.cacheRead && prev.usage.cacheRead) {
      usage.cacheRead = prev.usage.cacheRead;
      changed = true;
    }
    if (!usage.cacheWrite && prev.usage.cacheWrite) {
      usage.cacheWrite = prev.usage.cacheWrite;
      changed = true;
    }
    if (!changed) return m;
    // Same inputs give the same object, so unchanged rows are not re-rendered.
    const hit = inheritCache.get(m);
    if (hit && hit.from === prev.usage) return hit.out;
    const out = { ...m, usage };
    inheritCache.set(m, { from: prev.usage, out });
    return out;
  });
}

/** Last message on the path that can serve as a parent for the next send. */
export function lastPersistedLeaf(path: readonly ChatMessage[]): ChatMessage | null {
  const leaf = path[path.length - 1];
  const usable = (m: ChatMessage) =>
    m.persisted && m.status !== "pending" && (m.status === "success" || (m.role === "assistant" && m.status === "interrupted"));
  if (leaf && usable(leaf)) return leaf;
  for (let i = path.length - 1; i >= 0; i--) {
    const m = path[i] as ChatMessage;
    if (m.role === "assistant" && usable(m)) return m;
  }
  for (let i = path.length - 1; i >= 0; i--) {
    const m = path[i] as ChatMessage;
    if (m.role === "user" && usable(m)) return m;
  }
  return null;
}
