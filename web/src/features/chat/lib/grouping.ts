/** Grouping conversations for the sidebar. */
import type { ConversationDTO } from "../types";

export type GroupId = "starred" | "today" | "yesterday" | "previous7" | "previous30" | "older";

export interface ConversationGroup {
  id: GroupId;
  items: ConversationDTO[];
}

const ORDER: GroupId[] = ["starred", "today", "yesterday", "previous7", "previous30", "older"];

function startOfDay(d: Date): number {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}

export function groupOf(c: ConversationDTO, now: Date): GroupId {
  if (c.isStarred) return "starred";
  const updated = Date.parse(c.updatedAt);
  if (!Number.isFinite(updated)) return "older";
  const today = startOfDay(now);
  const day = 24 * 60 * 60 * 1000;
  if (updated >= today) return "today";
  if (updated >= today - day) return "yesterday";
  if (updated >= today - 7 * day) return "previous7";
  if (updated >= today - 30 * day) return "previous30";
  return "older";
}

/** Conversations outside projects, newest first inside each group; empty groups are omitted. */
export function groupConversations(list: readonly ConversationDTO[], now: Date): ConversationGroup[] {
  const buckets = new Map<GroupId, ConversationDTO[]>();
  for (const c of list) {
    const id = groupOf(c, now);
    const bucket = buckets.get(id);
    if (bucket) bucket.push(c);
    else buckets.set(id, [c]);
  }
  const byRecent = (a: ConversationDTO, b: ConversationDTO) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt);
  return ORDER.filter((id) => buckets.has(id)).map((id) => ({ id, items: (buckets.get(id) as ConversationDTO[]).sort(byRecent) }));
}
