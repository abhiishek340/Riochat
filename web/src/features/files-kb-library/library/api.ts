import { api } from "@/lib/api/client";
import { seg } from "@/lib/api/http";
import { isRecord, num, str } from "@/lib/utils/guards";
import type { LibraryItem, LibraryKind } from "./model";

const ROOT: Record<LibraryKind, string> = { skills: "/skills", prompts: "/prompt-presets" };
const BODY_FIELD: Record<LibraryKind, string> = { skills: "markdown", prompts: "content" };
const ONE_KEY: Record<LibraryKind, string> = { skills: "skill", prompts: "promptPreset" };

export const LIBRARY_PAGE_SIZE = 100;

/** Reads one wire object (skill or prompt preset) into the shared item shape. */
export function toItem(kind: LibraryKind, raw: unknown): LibraryItem | null {
  if (!isRecord(raw) || typeof raw.id !== "number") return null;
  const body = raw[BODY_FIELD[kind]];
  return {
    id: raw.id,
    scope: raw.scope === "builtin" ? "builtin" : "user",
    title: str(raw.title),
    trigger: str(raw.trigger),
    description: str(raw.description),
    body: typeof body === "string" ? body : undefined,
    enabled: raw.enabled !== false,
    sortOrder: num(raw.sortOrder),
    createdAt: str(raw.createdAt),
    updatedAt: str(raw.updatedAt),
  };
}

export interface ItemPage {
  items: LibraryItem[];
  total: number;
}

async function list(path: string, kind: LibraryKind, query: Record<string, string | number | undefined>, signal?: AbortSignal): Promise<ItemPage> {
  const res = await api.get<unknown>(path, { query, signal });
  const rec = isRecord(res) ? res : {};
  const rows = Array.isArray(rec.results) ? rec.results : [];
  return { items: rows.flatMap((r) => toItem(kind, r) ?? []), total: num(rec.total) };
}

export interface ListParams {
  page: number;
  pageSize: number;
  q?: string;
}

export function listMine(kind: LibraryKind, p: ListParams, signal?: AbortSignal): Promise<ItemPage> {
  return list(`${ROOT[kind]}/mine`, kind, { page: p.page, page_size: p.pageSize, q: p.q?.trim() || undefined }, signal);
}

export function listVisible(kind: LibraryKind, p: ListParams, signal?: AbortSignal): Promise<ItemPage> {
  return list(ROOT[kind], kind, { page: p.page, page_size: p.pageSize, q: p.q?.trim() || undefined }, signal);
}

function unwrap(kind: LibraryKind, res: unknown): LibraryItem {
  const item = isRecord(res) ? toItem(kind, res[ONE_KEY[kind]]) : null;
  if (!item) throw new Error("errors.unknown");
  return item;
}

/** The full object, including the body (list summaries may leave it out). */
export async function getItem(kind: LibraryKind, id: number, signal?: AbortSignal): Promise<LibraryItem> {
  return unwrap(kind, await api.get<unknown>(`${ROOT[kind]}/${seg(id)}`, { signal }));
}

export interface WriteBody {
  name: string;
  description: string;
  body: string;
  enabled: boolean;
}

function wire(kind: LibraryKind, w: WriteBody): Record<string, unknown> {
  return { title: w.name, trigger: w.name, description: w.description, [BODY_FIELD[kind]]: w.body, enabled: w.enabled };
}

export async function createItem(kind: LibraryKind, w: WriteBody): Promise<LibraryItem> {
  return unwrap(kind, await api.post<unknown>(`${ROOT[kind]}/mine`, { ...wire(kind, w), sortOrder: 0 }));
}

export async function updateItem(kind: LibraryKind, id: number, w: WriteBody): Promise<LibraryItem> {
  return unwrap(kind, await api.patch<unknown>(`${ROOT[kind]}/mine/${seg(id)}`, wire(kind, w)));
}

export async function setItemEnabled(kind: LibraryKind, id: number, enabled: boolean): Promise<LibraryItem> {
  return unwrap(kind, await api.patch<unknown>(`${ROOT[kind]}/mine/${seg(id)}`, { enabled }));
}

export async function deleteItem(kind: LibraryKind, id: number): Promise<void> {
  await api.delete(`${ROOT[kind]}/mine/${seg(id)}`);
}
