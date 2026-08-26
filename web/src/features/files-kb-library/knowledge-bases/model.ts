/** Pure rules for the knowledge-base workspace. */
import { uniqueBy } from "../shared/async";
import type { KnowledgeBase, KnowledgeBaseFile } from "../shared/types";
import type { KbMode } from "./api";

export const LIST_PAGE_SIZE = 50;
export const FILES_PAGE_SIZE = 100;
export const AVAILABLE_PAGE_SIZE = 50;
export const MAX_FILES_PER_CALL = 100;

/** Admin mode may edit every base; users only their personal ones. */
export function isEditable(kb: Pick<KnowledgeBase, "scope">, mode: KbMode): boolean {
  return mode === "admin" || kb.scope === "user";
}

export type Health =
  | { kind: "none" }
  | { kind: "all"; count: number }
  | { kind: "some"; ready: number; total: number }
  | { kind: "notReady"; count: number };

/** The one-line summary under a base's name. */
export function healthOf(kb: Pick<KnowledgeBase, "fileCount" | "readyFileCount">): Health {
  if (kb.fileCount <= 0) return { kind: "none" };
  if (kb.readyFileCount >= kb.fileCount) return { kind: "all", count: kb.fileCount };
  if (kb.readyFileCount > 0) return { kind: "some", ready: kb.readyFileCount, total: kb.fileCount };
  return { kind: "notReady", count: kb.fileCount };
}

/** True when the snapshot's summary says something changed that the held row does not know. */
export function summaryChanged(held: KnowledgeBase, fresh: KnowledgeBase): boolean {
  return (
    held.revision !== fresh.revision ||
    held.fileCount !== fresh.fileCount ||
    held.readyFileCount !== fresh.readyFileCount ||
    held.processingFileCount !== fresh.processingFileCount
  );
}

export function selectableIDs(items: readonly KnowledgeBase[], mode: KbMode): string[] {
  return items.filter((kb) => isEditable(kb, mode)).map((kb) => kb.publicID);
}

export function pruneSelection(checked: ReadonlySet<string>, items: readonly KnowledgeBase[], mode: KbMode): Set<string> {
  const allowed = new Set(selectableIDs(items, mode));
  const next = new Set([...checked].filter((id) => allowed.has(id)));
  return next.size === checked.size ? (checked as Set<string>) : next;
}

export function mergeKbPage(existing: readonly KnowledgeBase[], incoming: readonly KnowledgeBase[]): KnowledgeBase[] {
  return uniqueBy([...existing, ...incoming], (kb) => kb.publicID);
}

/** Pages of up to 100 needed to re-read `loaded` rows. */
export function refreshPages(loaded: number): number[] {
  const pages = Math.max(1, Math.ceil(Math.max(loaded, 1) / 100));
  return Array.from({ length: pages }, (_, i) => i + 1);
}

/** Adds an ID to a capped selection; refuses (returns the same set) at the cap. */
export function toggleCapped(selection: ReadonlySet<string>, id: string, cap: number): Set<string> {
  const next = new Set(selection);
  if (next.delete(id)) return next;
  if (next.size >= cap) return selection as Set<string>;
  next.add(id);
  return next;
}

/** Files that can be vectorized, among the loaded ones. */
export function vectorizableIDs(files: readonly Pick<KnowledgeBaseFile, "fileID" | "canVectorize">[]): string[] {
  return files.filter((f) => f.canVectorize).map((f) => f.fileID);
}

/** Header checkbox state for the vectorize selection. */
export function headerCheckState(checked: ReadonlySet<string>, files: readonly Pick<KnowledgeBaseFile, "fileID" | "canVectorize">[]): "none" | "some" | "all" {
  const eligible = vectorizableIDs(files);
  const picked = eligible.filter((id) => checked.has(id)).length;
  if (picked === 0) return "none";
  return picked >= Math.min(eligible.length, MAX_FILES_PER_CALL) ? "all" : "some";
}

/** Matches a name against the dialog's search box (case-insensitive). */
export function nameMatches(name: string, query: string): boolean {
  const q = query.trim().toLowerCase();
  return q === "" || name.toLowerCase().includes(q);
}

/** Splits an upload batch's results into the IDs to add and the failure count. */
export function uniqueIDs(ids: readonly string[]): string[] {
  return uniqueBy(ids, (id) => id);
}
