import { api } from "@/lib/api/client";
import { seg } from "@/lib/api/http";
import type { EmbeddingResponse, KnowledgeBase, KnowledgeBaseFile, ProcessingStatus } from "../shared/types";

export type KbMode = "user" | "admin";

export const KB_SORTS = ["default", "updated", "created", "name", "files"] as const;
export type KbSort = (typeof KB_SORTS)[number];

interface Roots {
  /** Reads (list, get, files, content, snapshot). */
  read: string;
  /** Writes on a base (create, patch, delete, add and remove files). */
  write: string;
}

/** User mode writes under `/mine`; the administrator console uses the plain admin tree. */
export function rootsFor(mode: KbMode): Roots {
  return mode === "admin"
    ? { read: "/admin/knowledge-bases", write: "/admin/knowledge-bases" }
    : { read: "/knowledge-bases", write: "/knowledge-bases/mine" };
}

export interface Paged<T> {
  results: T[];
  total: number;
}

function paged<T>(value: Partial<Paged<T>> | null | undefined): Paged<T> {
  return { results: value?.results ?? [], total: value?.total ?? 0 };
}

export interface KbListParams {
  page: number;
  pageSize: number;
  q: string;
  sort: KbSort;
}

export function kbListQuery(params: KbListParams): Record<string, string | number> {
  const query: Record<string, string | number> = { page: params.page, page_size: params.pageSize };
  const q = params.q.trim();
  if (q) query.q = q;
  if (params.sort !== "default") query.sort = params.sort;
  return query;
}

export async function listKnowledgeBases(mode: KbMode, params: KbListParams, signal?: AbortSignal): Promise<Paged<KnowledgeBase>> {
  return paged(await api.get<Partial<Paged<KnowledgeBase>>>(rootsFor(mode).read, { query: kbListQuery(params), signal }));
}

export async function getKnowledgeBase(mode: KbMode, id: string, signal?: AbortSignal): Promise<KnowledgeBase> {
  const res = await api.get<{ knowledgeBase: KnowledgeBase }>(`${rootsFor(mode).read}/${seg(id)}`, { signal });
  return res.knowledgeBase;
}

export interface KbDraft {
  name: string;
  description: string;
}

export async function createKnowledgeBase(mode: KbMode, draft: KbDraft): Promise<KnowledgeBase> {
  const body = mode === "admin" ? { ...draft, enabled: true } : draft;
  const res = await api.post<{ knowledgeBase: KnowledgeBase }>(rootsFor(mode).write, body);
  return res.knowledgeBase;
}

export async function updateKnowledgeBase(
  mode: KbMode,
  id: string,
  patch: Partial<KbDraft & { enabled: boolean; sortOrder: number }>,
): Promise<KnowledgeBase> {
  const res = await api.patch<{ knowledgeBase: KnowledgeBase }>(`${rootsFor(mode).write}/${seg(id)}`, patch);
  return res.knowledgeBase;
}

export function deleteKnowledgeBase(mode: KbMode, id: string, deleteFiles: boolean): Promise<{ deleted: boolean; deletedFileCount?: number }> {
  return api.delete(`${rootsFor(mode).write}/${seg(id)}`, { query: deleteFiles ? { delete_files: "true" } : undefined });
}

export async function listKbFiles(mode: KbMode, id: string, page: number, pageSize: number, signal?: AbortSignal): Promise<Paged<KnowledgeBaseFile>> {
  return paged(
    await api.get<Partial<Paged<KnowledgeBaseFile>>>(`${rootsFor(mode).read}/${seg(id)}/files`, { query: { page, page_size: pageSize }, signal }),
  );
}

export async function listAvailableFiles(
  mode: KbMode,
  id: string,
  params: { page: number; pageSize: number; q: string },
  signal?: AbortSignal,
): Promise<Paged<KnowledgeBaseFile>> {
  const q = params.q.trim();
  return paged(
    await api.get<Partial<Paged<KnowledgeBaseFile>>>(`${rootsFor(mode).write}/${seg(id)}/available-files`, {
      query: { page: params.page, page_size: params.pageSize, q: q || undefined },
      signal,
    }),
  );
}

export function addFilesToKb(mode: KbMode, id: string, fileIDs: string[]): Promise<{ updated: boolean }> {
  return api.post(`${rootsFor(mode).write}/${seg(id)}/files`, { fileIDs });
}

export function removeFileFromKb(mode: KbMode, id: string, fileID: string): Promise<{ updated: boolean }> {
  return api.delete(`${rootsFor(mode).write}/${seg(id)}/files/${seg(fileID)}`);
}

export function fetchKbContent(mode: KbMode, id: string, fileID: string, signal?: AbortSignal): Promise<Response> {
  return api.raw(`${rootsFor(mode).read}/${seg(id)}/files/${seg(fileID)}/content`, { signal });
}

export interface KbSnapshot {
  knowledgeBase: KnowledgeBase;
  statuses: ProcessingStatus[];
}

export function fetchKbSnapshot(mode: KbMode, id: string, fileIDs: string[], signal?: AbortSignal): Promise<KbSnapshot> {
  return api.post<KbSnapshot>(`${rootsFor(mode).read}/${seg(id)}/files/processing/snapshot`, { fileIDs: fileIDs.slice(0, 100) }, { signal });
}

// --- platform sources (administrator console) ----------------------------------

export async function uploadPlatformFile(file: File, signal?: AbortSignal): Promise<{ fileID: string }> {
  const form = new FormData();
  form.append("file", file);
  const res = await api.post<{ file: { fileID: string } }>("/admin/knowledge-bases/files", form, { signal });
  return { fileID: res.file.fileID };
}

export function deletePlatformFile(fileID: string): Promise<{ deleted: boolean }> {
  return api.delete(`/admin/knowledge-bases/files/${seg(fileID)}`);
}

export function submitKbEmbeddings(mode: KbMode, fileIDs: string[]): Promise<EmbeddingResponse> {
  return api.post<EmbeddingResponse>(mode === "admin" ? "/admin/knowledge-bases/files/embeddings" : "/files/embeddings", { fileIDs });
}
