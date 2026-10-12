import { api } from "@/lib/api/client";
import { seg } from "@/lib/api/http";
import { readPage, type PageResult } from "../lib/paging";

export interface KnowledgeBase {
  publicID: string;
  scope: string;
  name: string;
  description: string;
  enabled: boolean;
  sortOrder: number;
  revision: number;
  fileCount: number;
  readyFileCount: number;
  processingFileCount: number;
  createdAt: string;
  updatedAt: string;
}

export interface KbFile {
  fileID: string;
  fileName: string;
  mimeType: string;
  detectedMIME: string;
  fileCategory: string;
  sizeBytes: number;
  processingStatus: string;
  processing: boolean;
  processingReady: boolean;
  extractStatus: string;
  embedStatus: string;
  embedError: string;
  chunkCount: number;
  ragOptOut: boolean;
  canVectorize: boolean;
  vectorizationReason: string;
  createdAt: string;
  updatedAt: string;
}

export type KbSort = "" | "updated" | "created" | "name" | "files";

export interface KbQuery {
  page: number;
  pageSize: number;
  q: string;
  sort: KbSort;
}

const BASE = "/admin/knowledge-bases";

export async function listKnowledgeBases(query: KbQuery, signal?: AbortSignal): Promise<PageResult<KnowledgeBase>> {
  const raw = await api.get<unknown>(BASE, { signal, query: { page: query.page, page_size: query.pageSize, q: query.q.trim(), sort: query.sort } });
  return readPage<KnowledgeBase>(raw);
}

export interface KbInput {
  name: string;
  description: string;
  enabled: boolean;
}

export async function createKnowledgeBase(input: KbInput): Promise<KnowledgeBase> {
  const out = await api.post<{ knowledgeBase: KnowledgeBase }>(BASE, input);
  return out.knowledgeBase;
}

export async function updateKnowledgeBase(id: string, patch: Partial<KbInput>): Promise<KnowledgeBase> {
  const out = await api.patch<{ knowledgeBase: KnowledgeBase }>(`${BASE}/${seg(id)}`, patch);
  return out.knowledgeBase;
}

export async function deleteKnowledgeBase(id: string, deleteFiles: boolean): Promise<void> {
  await api.delete(`${BASE}/${seg(id)}`, { query: deleteFiles ? { delete_files: true } : undefined });
}

export async function listKbFiles(id: string, page: number, pageSize: number, signal?: AbortSignal): Promise<PageResult<KbFile>> {
  return readPage<KbFile>(await api.get<unknown>(`${BASE}/${seg(id)}/files`, { signal, query: { page, page_size: pageSize } }));
}

export async function listAvailableFiles(id: string, q: string, page: number, pageSize: number, signal?: AbortSignal): Promise<PageResult<KbFile>> {
  return readPage<KbFile>(await api.get<unknown>(`${BASE}/${seg(id)}/available-files`, { signal, query: { q: q.trim(), page, page_size: pageSize } }));
}

export async function addKbFiles(id: string, fileIds: string[]): Promise<void> {
  await api.post(`${BASE}/${seg(id)}/files`, { fileIDs: fileIds });
}

export async function removeKbFile(id: string, fileId: string): Promise<void> {
  await api.delete(`${BASE}/${seg(id)}/files/${seg(fileId)}`);
}

export interface FileStatus {
  fileID: string;
  processingStatus: string;
  processing: boolean;
  processingReady: boolean;
  extractStatus: string;
  embedStatus: string;
  embedError: string;
  chunkCount: number;
}

export async function fileStatuses(id: string, fileIds: string[], signal?: AbortSignal): Promise<FileStatus[]> {
  const out = await api.post<unknown>(`${BASE}/${seg(id)}/files/processing/statuses`, { fileIDs: fileIds }, { signal });
  if (Array.isArray(out)) return out as FileStatus[];
  const list = (out as { statuses?: FileStatus[] } | null)?.statuses;
  return list ?? [];
}

export async function poolFiles(q: string, page: number, pageSize: number, signal?: AbortSignal): Promise<PageResult<KbFile>> {
  return readPage<KbFile>(await api.get<unknown>(`${BASE}/files`, { signal, query: { q: q.trim(), page, page_size: pageSize } }));
}

export async function uploadPoolFile(file: File): Promise<KbFile> {
  const body = new FormData();
  body.append("file", file, file.name);
  const out = await api.post<{ file: KbFile }>(`${BASE}/files`, body);
  return out.file;
}

export async function deletePoolFile(fileId: string): Promise<void> {
  await api.delete(`${BASE}/files/${seg(fileId)}`);
}

export async function submitEmbeddings(fileIds: string[]): Promise<{ submittedFileIDs: string[]; skipped: Array<{ fileID: string; reason: string }> }> {
  const out = await api.post<{ submittedFileIDs?: string[]; skipped?: Array<{ fileID: string; reason: string }> }>(`${BASE}/files/embeddings`, { fileIDs: fileIds });
  return { submittedFileIDs: out?.submittedFileIDs ?? [], skipped: out?.skipped ?? [] };
}

export async function poolFileContent(fileId: string, signal?: AbortSignal): Promise<Blob> {
  const response = await api.raw(`${BASE}/files/${seg(fileId)}/content`, { signal });
  return response.blob();
}

/** The global switch lives in settings namespace `knowledgebase`. */
export async function readKbEnabled(signal?: AbortSignal): Promise<boolean> {
  const items = await api.get<Array<{ key: string; value: string }> | null>("/admin/settings/knowledgebase", { signal });
  const item = (items ?? []).find((i) => i.key === "enabled");
  return item?.value !== "false";
}

export async function writeKbEnabled(enabled: boolean): Promise<void> {
  await api.patch("/admin/settings", { items: [{ namespace: "knowledgebase", key: "enabled", value: enabled ? "true" : "false" }] });
}

export interface KbSnapshot {
  knowledgeBase: KnowledgeBase;
  statuses: FileStatus[];
}

/** Fresh counters plus the processing state of the named files. */
export async function kbSnapshot(id: string, fileIds: string[], signal?: AbortSignal): Promise<KbSnapshot> {
  const out = await api.post<Partial<KbSnapshot>>(`${BASE}/${seg(id)}/files/processing/snapshot`, { fileIDs: fileIds }, { signal });
  return { knowledgeBase: out.knowledgeBase as KnowledgeBase, statuses: out.statuses ?? [] };
}
