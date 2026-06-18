/**
 * Per-user, time-limited records kept in browser storage: composer drafts and
 * the tool/skill/knowledge-base selection per conversation. Both are maps from
 * a conversation key to a small entry; entries older than 30 days are ignored
 * and at most 50 of the newest are kept. Everything tolerates missing storage.
 */
import { isRecord } from "@/lib/utils/guards";
import { readStorage, removeStorage, writeStorage } from "@/lib/utils/storage";
import type { ChatAttachment, SkillSummaryDTO } from "../types";

export interface StorageIO {
  read(key: string): string | null;
  write(key: string, value: string): void;
  remove(key: string): void;
}

export const browserIO: StorageIO = { read: (k) => readStorage(k), write: (k, v) => writeStorage(k, v), remove: (k) => removeStorage(k) };

export function memoryIO(): StorageIO & { data: Map<string, string> } {
  const data = new Map<string, string>();
  return { data, read: (k) => data.get(k) ?? null, write: (k, v) => void data.set(k, v), remove: (k) => void data.delete(k) };
}

export const MAX_AGE_MS = 30 * 24 * 60 * 60 * 1000;
export const MAX_ENTRIES = 50;
export const NEW_CHAT_KEY = "__new__";

export interface Stamped {
  updatedAt: number;
}

export function readMap<T extends Stamped>(io: StorageIO, storageKey: string, parse: (raw: unknown) => T | null, now: number): Record<string, T> {
  const raw = io.read(storageKey);
  if (!raw) return {};
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return {};
  }
  if (!isRecord(parsed)) return {};
  const out: Record<string, T> = {};
  for (const [key, value] of Object.entries(parsed)) {
    const entry = parse(value);
    if (entry && now - entry.updatedAt <= MAX_AGE_MS) out[key] = entry;
  }
  return out;
}

export function trimMap<T extends Stamped>(map: Record<string, T>): Record<string, T> {
  const keys = Object.keys(map).sort((a, b) => (map[b] as T).updatedAt - (map[a] as T).updatedAt);
  const out: Record<string, T> = {};
  for (const k of keys.slice(0, MAX_ENTRIES)) out[k] = map[k] as T;
  return out;
}

export function writeMap<T extends Stamped>(io: StorageIO, storageKey: string, map: Record<string, T>): void {
  const trimmed = trimMap(map);
  if (Object.keys(trimmed).length === 0) io.remove(storageKey);
  else io.write(storageKey, JSON.stringify(trimmed));
}

// --- drafts ------------------------------------------------------------------------------

export interface DraftEntry extends Stamped {
  text: string;
  attachments: ChatAttachment[];
}

const ATTACH_STRING_FIELDS = ["fileId", "fileName", "mimeType", "detectedMime", "fileCategory", "processingStatus", "processingErrorCode", "processingErrorMessage"] as const;

/** Metadata only: never preview URLs or file handles. */
export function persistableAttachment(a: ChatAttachment): ChatAttachment {
  const { previewUrl: _preview, ...rest } = a;
  void _preview;
  return rest;
}

export function parseAttachment(raw: unknown): ChatAttachment | null {
  if (!isRecord(raw)) return null;
  for (const f of ATTACH_STRING_FIELDS) if (raw[f] !== undefined && typeof raw[f] !== "string") return null;
  if (typeof raw.fileId !== "string" || !raw.fileId || typeof raw.fileName !== "string" || !raw.fileName) return null;
  const n = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? v : 0);
  return {
    fileId: raw.fileId,
    fileName: raw.fileName,
    mimeType: String(raw.mimeType ?? ""),
    detectedMime: String(raw.detectedMime ?? ""),
    fileCategory: String(raw.fileCategory ?? ""),
    fileSize: n(raw.fileSize),
    durationSeconds: n(raw.durationSeconds),
    kind: raw.kind === "image" ? "image" : "file",
    processingStatus: String(raw.processingStatus ?? ""),
    processingReady: raw.processingReady === true,
    processingErrorCode: String(raw.processingErrorCode ?? ""),
    processingErrorMessage: String(raw.processingErrorMessage ?? ""),
  };
}

export function parseDraft(raw: unknown): DraftEntry | null {
  if (!isRecord(raw) || typeof raw.updatedAt !== "number") return null;
  const attachments = Array.isArray(raw.attachments) ? raw.attachments.map(parseAttachment).filter((a): a is ChatAttachment => a !== null) : [];
  return { text: typeof raw.text === "string" ? raw.text : "", attachments, updatedAt: raw.updatedAt };
}

export const draftsStorageKey = (userId: string) => `riochat:drafts:${userId}`;

export function loadDraft(io: StorageIO, userId: string, key: string, now: number): DraftEntry | null {
  return readMap(io, draftsStorageKey(userId), parseDraft, now)[key] ?? null;
}

/** Saves (or, when empty, deletes) one draft. Missing text and attachments delete the entry. */
export function saveDraft(io: StorageIO, userId: string, key: string, draft: { text: string; attachments: readonly ChatAttachment[] }, now: number): void {
  const storageKey = draftsStorageKey(userId);
  const map = readMap(io, storageKey, parseDraft, now);
  if (draft.text === "" && draft.attachments.length === 0) delete map[key];
  else map[key] = { text: draft.text, attachments: draft.attachments.map(persistableAttachment), updatedAt: now };
  writeMap(io, storageKey, map);
}

export function removeDraft(io: StorageIO, userId: string, key: string, now: number): void {
  saveDraft(io, userId, key, { text: "", attachments: [] }, now);
}

/** Append finished uploads to a stored draft (the user switched conversations meanwhile), merged by file id. */
export function appendToDraft(io: StorageIO, userId: string, key: string, files: readonly ChatAttachment[], now: number): void {
  const current = loadDraft(io, userId, key, now);
  const have = new Set((current?.attachments ?? []).map((a) => a.fileId));
  const merged = [...(current?.attachments ?? []), ...files.filter((f) => !have.has(f.fileId))];
  saveDraft(io, userId, key, { text: current?.text ?? "", attachments: merged }, now);
}

// --- selections ---------------------------------------------------------------------------

export interface SelectionEntry extends Stamped {
  toolIds: number[];
  skills: SkillSummaryDTO[];
  knowledgeBaseIds: string[];
}

export function parseSkillSummary(raw: unknown): SkillSummaryDTO | null {
  if (!isRecord(raw)) return null;
  if (typeof raw.id !== "number" || !Number.isInteger(raw.id) || raw.id <= 0) return null;
  if (typeof raw.title !== "string") return null;
  return {
    id: raw.id,
    scope: raw.scope === "builtin" ? "builtin" : "user",
    title: raw.title,
    trigger: typeof raw.trigger === "string" ? raw.trigger : "",
    description: typeof raw.description === "string" ? raw.description : "",
    enabled: raw.enabled !== false,
    sortOrder: typeof raw.sortOrder === "number" ? raw.sortOrder : 0,
  };
}

export function parseSelection(raw: unknown): SelectionEntry | null {
  if (!isRecord(raw) || typeof raw.updatedAt !== "number") return null;
  const toolIds = Array.isArray(raw.toolIds) ? raw.toolIds.filter((x): x is number => typeof x === "number" && Number.isInteger(x) && x > 0) : [];
  const skills = Array.isArray(raw.skills) ? raw.skills.map(parseSkillSummary).filter((s): s is SkillSummaryDTO => s !== null) : [];
  const kbs = Array.isArray(raw.knowledgeBaseIds) ? raw.knowledgeBaseIds.filter((x): x is string => typeof x === "string" && x !== "").slice(0, 8) : [];
  return { toolIds, skills, knowledgeBaseIds: kbs, updatedAt: raw.updatedAt };
}

export const selectionStorageKey = (userId: string) => `riochat:selections:${userId}`;

export function loadSelection(io: StorageIO, userId: string, key: string, now: number): SelectionEntry | null {
  return readMap(io, selectionStorageKey(userId), parseSelection, now)[key] ?? null;
}

export function saveSelection(io: StorageIO, userId: string, key: string, sel: { toolIds: number[]; skills: SkillSummaryDTO[]; knowledgeBaseIds: string[] }, now: number): void {
  const storageKey = selectionStorageKey(userId);
  const map = readMap(io, storageKey, parseSelection, now);
  if (sel.toolIds.length === 0 && sel.skills.length === 0 && sel.knowledgeBaseIds.length === 0) delete map[key];
  else map[key] = { toolIds: sel.toolIds, skills: sel.skills, knowledgeBaseIds: sel.knowledgeBaseIds.slice(0, 8), updatedAt: now };
  writeMap(io, storageKey, map);
}

/** A draft chat became a real conversation: its selection moves along. */
export function moveSelection(io: StorageIO, userId: string, from: string, to: string, now: number): void {
  const storageKey = selectionStorageKey(userId);
  const map = readMap(io, storageKey, parseSelection, now);
  const entry = map[from];
  if (!entry) return;
  delete map[from];
  map[to] = { ...entry, updatedAt: now };
  writeMap(io, storageKey, map);
}

// --- per-model option and control caches ---------------------------------------------------

export const optionsStorageKey = (model: string) => `riochat:model-options:${model}`;
export const controlsStorageKey = (model: string) => `riochat:model-controls:${model}`;

export function readJsonRecord(io: StorageIO, key: string): Record<string, unknown> | null {
  const raw = io.read(key);
  if (!raw) return null;
  try {
    const parsed: unknown = JSON.parse(raw);
    return isRecord(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

export function writeJsonRecord(io: StorageIO, key: string, value: Record<string, unknown> | null): void {
  if (!value || Object.keys(value).length === 0) io.remove(key);
  else io.write(key, JSON.stringify(value));
}

// --- recents ------------------------------------------------------------------------------

export type RecentKind = "model" | "file" | "knowledge" | "tool" | "skill" | "prompt";
export interface RecentItem {
  id: string;
  usedAt: number;
}
export const RECENTS_KEY = "riochat:mention-recents";
export const RECENTS_MAX = 20;

export function readRecents(io: StorageIO): Record<RecentKind, RecentItem[]> {
  const out: Record<RecentKind, RecentItem[]> = { model: [], file: [], knowledge: [], tool: [], skill: [], prompt: [] };
  const raw = io.read(RECENTS_KEY);
  if (!raw) return out;
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!isRecord(parsed)) return out;
    for (const kind of Object.keys(out) as RecentKind[]) {
      const list = parsed[kind];
      if (!Array.isArray(list)) continue;
      const seen = new Set<string>();
      for (const item of list) {
        if (isRecord(item) && typeof item.id === "string" && typeof item.usedAt === "number" && !seen.has(item.id)) {
          seen.add(item.id);
          out[kind].push({ id: item.id, usedAt: item.usedAt });
        }
      }
      out[kind] = out[kind].sort((a, b) => b.usedAt - a.usedAt).slice(0, RECENTS_MAX);
    }
  } catch {
    /* ignore */
  }
  return out;
}

export function recordRecent(io: StorageIO, kind: RecentKind, id: string, now: number): void {
  const all = readRecents(io);
  all[kind] = [{ id, usedAt: now }, ...all[kind].filter((r) => r.id !== id)].slice(0, RECENTS_MAX);
  io.write(RECENTS_KEY, JSON.stringify(all));
}
