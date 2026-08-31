/**
 * Tab-scoped stash of an import link, so a sign-in redirect (which drops the
 * fragment) does not lose it. Reading never deletes; entries expire.
 */
import { readStorage, removeStorage, writeStorage } from "@/lib/utils/storage";
import { isRecord } from "@/lib/utils/guards";

export const STASH_KEY = "riochat:import-link";
export const STASH_TTL_MS = 10 * 60 * 1000;

export function saveStash(fragment: string, now: number = Date.now()): void {
  writeStorage(STASH_KEY, JSON.stringify({ fragment, savedAt: now }), "session");
}

/** The stashed fragment, or "" when missing, malformed or older than ten minutes. */
export function readStash(now: number = Date.now()): string {
  const raw = readStorage(STASH_KEY, "session");
  if (!raw) return "";
  try {
    const data: unknown = JSON.parse(raw);
    if (!isRecord(data) || typeof data.fragment !== "string" || typeof data.savedAt !== "number") return "";
    if (now - data.savedAt > STASH_TTL_MS || now < data.savedAt - 1000) return "";
    return data.fragment;
  } catch {
    return "";
  }
}

export function clearStash(): void {
  removeStorage(STASH_KEY, "session");
}
