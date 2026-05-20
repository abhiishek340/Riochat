/** Pure helpers for the global conversation search dialog. */

export interface SearchItem {
  publicID: string;
  title: string;
  projectName: string;
  status: string;
  updatedAt: string;
  isStarred?: boolean;
  messageCount?: number;
  projectID?: string;
}

export type GroupKey = { kind: "today" } | { kind: "month"; year: number; month: number };

export interface SearchGroup {
  id: string;
  key: GroupKey;
  items: SearchItem[];
}

/** Trim + case-fold: the same form the sidebar's local matcher uses. */
export function normalizeQuery(raw: string): string {
  return raw.trim().toLocaleLowerCase();
}

export function sameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

/**
 * Groups by update date: today first, then calendar months, newest first.
 * Items whose date cannot be read are dropped.
 */
export function groupByUpdated(items: readonly SearchItem[], now: Date): SearchGroup[] {
  const dated = items
    .map((item) => ({ item, time: new Date(item.updatedAt) }))
    .filter((e) => !Number.isNaN(e.time.getTime()))
    .sort((a, b) => b.time.getTime() - a.time.getTime());
  const groups: SearchGroup[] = [];
  for (const { item, time } of dated) {
    const key: GroupKey = sameDay(time, now) ? { kind: "today" } : { kind: "month", year: time.getFullYear(), month: time.getMonth() };
    const id = key.kind === "today" ? "today" : `${key.year}-${key.month}`;
    const last = groups[groups.length - 1];
    if (last && last.id === id) last.items.push(item);
    else groups.push({ id, key, items: [item] });
  }
  return groups;
}

const pad2 = (n: number) => String(n).padStart(2, "0");

export function clock(time: Date): string {
  return `${pad2(time.getHours())}:${pad2(time.getMinutes())}:${pad2(time.getSeconds())}`;
}

/** Today: time only. This year: month/day + time. Otherwise: full date + time. */
export function formatUpdatedLabel(iso: string, now: Date, locale: string): string {
  const time = new Date(iso);
  if (Number.isNaN(time.getTime())) return "";
  if (sameDay(time, now)) return clock(time);
  const date =
    time.getFullYear() === now.getFullYear()
      ? new Intl.DateTimeFormat(locale, { month: "short", day: "numeric" }).format(time)
      : new Intl.DateTimeFormat(locale, { year: "numeric", month: "short", day: "numeric" }).format(time);
  return `${date} ${clock(time)}`;
}

/** Appends `more` to `current`, keeping the first occurrence of every id. */
export function mergeResults<T extends { publicID: string }>(current: readonly T[], more: readonly T[]): T[] {
  const seen = new Set(current.map((c) => c.publicID));
  const out = [...current];
  for (const item of more) {
    if (!seen.has(item.publicID)) {
      seen.add(item.publicID);
      out.push(item);
    }
  }
  return out;
}

/** Next index in a looping list; -1 means "nothing active yet". */
export function stepIndex(current: number, delta: 1 | -1, length: number): number {
  if (length <= 0) return -1;
  if (current < 0) return delta === 1 ? 0 : length - 1;
  return (current + delta + length) % length;
}

/** Small least-recently-used cache for conversation previews. */
export class LruCache<V> {
  private readonly map = new Map<string, V>();
  constructor(private readonly limit: number) {}

  get(key: string): V | undefined {
    const value = this.map.get(key);
    if (value !== undefined) {
      this.map.delete(key);
      this.map.set(key, value);
    }
    return value;
  }

  set(key: string, value: V): void {
    this.map.delete(key);
    this.map.set(key, value);
    while (this.map.size > this.limit) {
      const oldest = this.map.keys().next().value;
      if (oldest === undefined) break;
      this.map.delete(oldest);
    }
  }

  delete(key: string): void {
    this.map.delete(key);
  }

  clear(): void {
    this.map.clear();
  }

  get size(): number {
    return this.map.size;
  }
}

export const SEARCH_PAGE_SIZE = 20;
export const SEARCH_DEBOUNCE_MS = 250;
export const DEFAULT_LIST_TTL_MS = 30_000;
export const PREVIEW_DEBOUNCE_MS = 240;
export const PREVIEW_CACHE_SIZE = 24;
