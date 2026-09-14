/** Shared rules for skills and prompts: names, limits, ordering and search. */
import { uniqueBy } from "../shared/async";

export type LibraryKind = "skills" | "prompts";

export const LIMITS = { name: 64, description: 256, body: 10000 } as const;

/** One list entry; `body` is the SKILL.md text or the prompt content (absent on list summaries). */
export interface LibraryItem {
  id: number;
  scope: "user" | "builtin";
  title: string;
  trigger: string;
  description: string;
  body?: string;
  enabled: boolean;
  sortOrder: number;
  createdAt: string;
  updatedAt: string;
}

/** Length in characters as the server counts them (code points, not UTF-16 units). */
export function charCount(text: string): number {
  return Array.from(text).length;
}

/** Trims, removes every leading slash, trims again. */
export function normalizeName(raw: string): string {
  return raw.trim().replace(/^\/+/, "").trim();
}

export interface ItemForm {
  name: string;
  description: string;
  body: string;
  enabled: boolean;
}

export type FormCheck =
  | { ok: true; value: { name: string; description: string; body: string; enabled: boolean } }
  | { ok: false; reason: "missing" | "tooLong" };

/** Client-side validation that runs before any request is sent. */
export function checkForm(form: ItemForm): FormCheck {
  const name = normalizeName(form.name);
  const description = form.description.trim();
  const body = form.body.trim();
  if (name === "" || body === "") return { ok: false, reason: "missing" };
  if (charCount(name) > LIMITS.name || charCount(description) > LIMITS.description || charCount(body) > LIMITS.body) {
    return { ok: false, reason: "tooLong" };
  }
  return { ok: true, value: { name, description, body, enabled: form.enabled } };
}

/** What the form is prefilled with when editing. */
export function formFromItem(item: LibraryItem): ItemForm {
  return { name: item.trigger || item.title, description: item.description, body: item.body ?? "", enabled: item.enabled };
}

export const EMPTY_FORM: ItemForm = { name: "", description: "", body: "", enabled: true };

export function displayName(item: Pick<LibraryItem, "trigger" | "title">): string {
  return item.trigger || item.title;
}

export function summaryOf(item: Pick<LibraryItem, "description" | "body">): string {
  return item.description.trim() || (item.body ?? "").trim().split("\n")[0] || "";
}

/** Enabled personal items first, then enabled built-in ones, then everything disabled. */
export function rank(item: Pick<LibraryItem, "scope" | "enabled">): 0 | 1 | 2 {
  if (!item.enabled) return 2;
  return item.scope === "user" ? 0 : 1;
}

/** Skills tie-break by lower sort order, then newer (higher) ID; prompts keep arrival order. */
export function orderItems(kind: LibraryKind, items: readonly LibraryItem[]): LibraryItem[] {
  const indexed = items.map((item, index) => ({ item, index }));
  indexed.sort((a, b) => {
    const byRank = rank(a.item) - rank(b.item);
    if (byRank !== 0) return byRank;
    if (kind === "skills") {
      if (a.item.sortOrder !== b.item.sortOrder) return a.item.sortOrder - b.item.sortOrder;
      if (a.item.id !== b.item.id) return b.item.id - a.item.id;
    }
    return a.index - b.index;
  });
  return indexed.map((x) => x.item);
}

/** Case-insensitive match over title, trigger, description (and the body, for prompts). */
export function matchesSearch(kind: LibraryKind, item: LibraryItem, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (q === "") return true;
  const parts = [item.title, item.trigger, item.description];
  if (kind === "prompts") parts.push(item.body ?? "");
  return parts.join(" ").toLowerCase().includes(q);
}

/** Own items from the "mine" endpoint plus only the built-in ones from the "visible" endpoint. */
export function mergeVisible(mine: readonly LibraryItem[], visible: readonly LibraryItem[]): LibraryItem[] {
  const builtin = visible.filter((item) => item.scope === "builtin");
  return uniqueBy([...mine, ...builtin], (item) => `${item.scope}:${item.id}`);
}

/** Replaces the item with the same ID; adds it when it is new. */
export function upsert(items: readonly LibraryItem[], next: LibraryItem): LibraryItem[] {
  return items.some((i) => i.id === next.id) ? items.map((i) => (i.id === next.id ? next : i)) : [next, ...items];
}
