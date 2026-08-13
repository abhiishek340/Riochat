/** Pure list transformations for the files workspace. */
import { uniqueBy } from "../shared/async";
import { matchesFilters, matchesQuery } from "../shared/file-kind";
import type { FileObject, StorageQuota, UploadResponse } from "../shared/types";

export const PAGE_SIZE = 100;

/** Keeps the preferred ID, else the current one, else the first item, else nothing. */
export function pickSelection(preferred: string | null, current: string | null, items: readonly { fileID: string }[]): string | null {
  const has = (id: string | null) => id !== null && items.some((f) => f.fileID === id);
  if (has(preferred)) return preferred;
  if (has(current)) return current;
  return items[0]?.fileID ?? null;
}

/** After removing `deletedID`: the item that slid into its slot, else the one before, else the first. */
export function selectionAfterDelete(items: readonly { fileID: string }[], deletedID: string, selectedID: string | null): string | null {
  const remaining = items.filter((f) => f.fileID !== deletedID);
  if (selectedID !== deletedID) return pickSelection(null, selectedID, remaining);
  const index = items.findIndex((f) => f.fileID === deletedID);
  return remaining[index]?.fileID ?? remaining[index - 1]?.fileID ?? remaining[0]?.fileID ?? null;
}

export function appendPage<T extends { fileID: string }>(existing: readonly T[], incoming: readonly T[]): T[] {
  return uniqueBy([...existing, ...incoming], (f) => f.fileID);
}

/** Checked IDs survive only while the file is still loaded. */
export function pruneChecked(checked: ReadonlySet<string>, items: readonly { fileID: string }[]): Set<string> {
  const loaded = new Set(items.map((f) => f.fileID));
  const next = new Set([...checked].filter((id) => loaded.has(id)));
  return next.size === checked.size ? (checked as Set<string>) : next;
}

export interface UploadMergeInput {
  items: readonly FileObject[];
  total: number;
  query: string;
  filters: readonly string[];
  results: readonly UploadResponse[];
}

export interface UploadMerge {
  items: FileObject[];
  total: number;
  /** The newest upload that was actually shown, if any. */
  selectID: string | null;
  quota: StorageQuota | null;
}

/**
 * Shows fresh uploads right away: an upload is inserted at the top only if it
 * is not listed yet and still matches the active search and filters.
 */
export function mergeUploads({ items, total, query, filters, results }: UploadMergeInput): UploadMerge {
  let next = [...items];
  let inserted = 0;
  let selectID: string | null = null;
  let quota: StorageQuota | null = null;
  for (const result of results) {
    if (result.quota) quota = result.quota;
    const file = result.file;
    if (next.some((f) => f.fileID === file.fileID)) continue;
    if (!matchesQuery(file, query) || !matchesFilters(file, filters)) continue;
    next = [file, ...next];
    inserted += 1;
    selectID = file.fileID;
  }
  return { items: next, total: total + inserted, selectID, quota };
}

export function toggleFilter(filters: readonly string[], key: string): string[] {
  return filters.includes(key) ? filters.filter((k) => k !== key) : [...filters, key];
}
