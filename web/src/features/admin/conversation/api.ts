import { api } from "@/lib/api/client";
import { seg } from "@/lib/api/http";
import { readPage, type PageResult } from "../lib/paging";

export type LibraryKind = "prompts" | "skills" | "components";

const PATHS: Record<LibraryKind, string> = {
  prompts: "/admin/prompt-presets",
  skills: "/admin/skills",
  components: "/admin/ui-components",
};
const WRAP: Record<LibraryKind, string> = { prompts: "promptPreset", skills: "skill", components: "component" };

interface Common {
  id: number;
  scope: "builtin" | "user" | "platform" | (string & {});
  description: string;
  enabled: boolean;
  sortOrder: number;
  createdAt: string;
  updatedAt: string;
}

export interface PromptItem extends Common {
  title: string;
  trigger: string;
  content: string;
}

export interface SkillItem extends Common {
  title: string;
  trigger: string;
  markdown: string;
}

export interface ComponentItem extends Common {
  name: string;
  version: number;
  propsSummary: string;
  propsSchema: string;
  rendererKind: string;
  rendererSource: string;
}

export type LibraryItem = PromptItem | SkillItem | ComponentItem;

export interface LibraryQuery {
  page: number;
  pageSize: number;
  q: string;
}

export async function listLibrary<T extends LibraryItem>(kind: LibraryKind, query: LibraryQuery, signal?: AbortSignal): Promise<PageResult<T>> {
  const raw = await api.get<unknown>(PATHS[kind], { signal, query: { page: query.page, page_size: query.pageSize, q: query.q.trim() } });
  return readPage<T>(raw);
}

export async function createLibrary<T extends LibraryItem>(kind: LibraryKind, body: Record<string, unknown>): Promise<T> {
  const out = await api.post<Record<string, T>>(PATHS[kind], body);
  return out[WRAP[kind]] as T;
}

export async function updateLibrary<T extends LibraryItem>(kind: LibraryKind, id: number, body: Record<string, unknown>): Promise<T> {
  const out = await api.patch<Record<string, T>>(`${PATHS[kind]}/${seg(id)}`, body);
  return out[WRAP[kind]] as T;
}

export async function deleteLibrary(kind: LibraryKind, id: number): Promise<void> {
  await api.delete(`${PATHS[kind]}/${seg(id)}`);
}

export interface ModelRow {
  platformModelName: string;
  kindsJSON?: string;
  icon?: string;
  vendorIcon?: string;
}

/** Models that are routable right now (the same list the picker uses). */
export async function listAvailableModels(signal?: AbortSignal): Promise<ModelRow[]> {
  const out = await api.get<{ results?: ModelRow[] }>("/admin/llm/models", { signal, query: { only_available: true, page: 1, page_size: 1000, sort: "sortOrder_asc" } });
  return out?.results ?? [];
}

export interface ExportManifest {
  complete: boolean;
  exported: number;
  failed: number;
}

/** The last line of an export is `{"_type":"export_manifest",...}`; anything else means no manifest. */
export function manifestFromText(tail: string): ExportManifest | null {
  const lines = tail.split("\n").map((l) => l.trim()).filter(Boolean);
  const last = lines[lines.length - 1];
  if (!last) return null;
  try {
    const value: unknown = JSON.parse(last);
    if (typeof value !== "object" || value === null) return null;
    const m = value as { _type?: unknown; complete?: unknown; exported?: unknown; failed?: unknown };
    if (m._type !== "export_manifest") return null;
    return { complete: m.complete === true, exported: typeof m.exported === "number" ? m.exported : 0, failed: typeof m.failed === "number" ? m.failed : 0 };
  } catch {
    return null;
  }
}

export async function downloadExport(): Promise<{ blob: Blob; disposition: string | null; manifest: ExportManifest | null }> {
  const response = await api.raw("/admin/conversations/export", { headers: { Accept: "application/x-ndjson, application/json" } });
  const blob = await response.blob();
  const tail = await blob.slice(Math.max(0, blob.size - 4096)).text();
  return { blob, disposition: response.headers.get("content-disposition"), manifest: manifestFromText(tail) };
}
