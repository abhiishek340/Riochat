import { api } from "@/lib/api/client";
import { seg } from "@/lib/api/http";
import { isRecord, num, str } from "@/lib/utils/guards";

export const MEMORY_SCOPES = ["profile", "preference", "custom"] as const;
export type MemoryScope = (typeof MEMORY_SCOPES)[number];

export interface MemoryEntry {
  id: number;
  memoryKey: string;
  value: string;
  scope: string;
  updatedBy: string;
  createdAt: string;
  updatedAt: string;
}

export const MEMORY_KEY_MAX = 128;
export const MEMORY_VALUE_MAX = 10000;
/** Soft cap of the preference list shown beside the chat settings. */
export const PREFERENCE_LIMIT = 20;

export function normalizeEntries(raw: unknown): MemoryEntry[] {
  if (!Array.isArray(raw)) return [];
  const out: MemoryEntry[] = [];
  for (const item of raw) {
    if (!isRecord(item)) continue;
    const key = str(item.memoryKey);
    if (!key) continue;
    out.push({
      id: num(item.id),
      memoryKey: key,
      value: str(item.value),
      scope: str(item.scope),
      updatedBy: str(item.updatedBy),
      createdAt: str(item.createdAt),
      updatedAt: str(item.updatedAt),
    });
  }
  return out;
}

export async function listMemories(signal?: AbortSignal): Promise<MemoryEntry[]> {
  return normalizeEntries(await api.get<unknown>("/memories/profile", { signal }));
}

export async function saveMemory(entry: { memoryKey: string; value: string; scope: MemoryScope }): Promise<void> {
  await api.put("/memories/profile", entry);
}

export async function deleteMemory(memoryKey: string): Promise<void> {
  await api.delete(`/memories/profile/${seg(memoryKey)}`);
}

/**
 * Upsert into a list the way the server does: an existing key keeps its place
 * and gets the new value, a new key goes first.
 */
export function upsertEntry(list: readonly MemoryEntry[], entry: MemoryEntry): MemoryEntry[] {
  const at = list.findIndex((e) => e.memoryKey === entry.memoryKey);
  if (at < 0) return [entry, ...list];
  return list.map((e, i) => (i === at ? { ...e, value: entry.value, scope: entry.scope, updatedAt: entry.updatedAt } : e));
}

export function localEntry(memoryKey: string, value: string, scope: MemoryScope): MemoryEntry {
  const now = new Date().toISOString();
  return { id: 0, memoryKey, value, scope, updatedBy: "user", createdAt: now, updatedAt: now };
}
