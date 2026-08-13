import { api } from "@/lib/api/client";
import { seg } from "@/lib/api/http";
import type {
  DeleteFileResponse,
  EmbeddingResponse,
  FileExtract,
  FileListResponse,
  FileObject,
  ProcessingStatus,
  UploadResponse,
} from "../shared/types";

export interface FileListParams {
  page: number;
  pageSize: number;
  q: string;
  kinds: readonly string[];
  sort: string;
}

/** Query object for `GET /files`; empty search and an empty filter set are left out. */
export function listQuery(params: FileListParams): Record<string, string | number> {
  const query: Record<string, string | number> = { page: params.page, page_size: params.pageSize, sort: params.sort };
  const q = params.q.trim();
  if (q) query.q = q;
  if (params.kinds.length > 0) query.kind = params.kinds.join(",");
  return query;
}

export function listFiles(params: FileListParams, signal?: AbortSignal): Promise<FileListResponse> {
  return api.get<FileListResponse>("/files", { query: listQuery(params), signal });
}

export function uploadFile(file: File, signal?: AbortSignal): Promise<UploadResponse> {
  const form = new FormData();
  form.append("file", file);
  return api.post<UploadResponse>("/files", form, { signal });
}

export function deleteFile(fileID: string): Promise<DeleteFileResponse> {
  return api.delete<DeleteFileResponse>(`/files/${seg(fileID)}`);
}

export function patchFile(fileID: string, patch: { fileName?: string; ragOptOut?: boolean }): Promise<FileObject> {
  return api.patch<FileObject>(`/files/${seg(fileID)}`, patch);
}

export function submitEmbeddings(fileIDs: string[]): Promise<EmbeddingResponse> {
  return api.post<EmbeddingResponse>("/files/embeddings", { fileIDs });
}

export function fetchStatuses(fileIDs: string[], signal?: AbortSignal): Promise<ProcessingStatus[]> {
  return api.post<ProcessingStatus[]>("/files/processing/statuses", { fileIDs }, { signal });
}

export function fetchContent(fileID: string, signal?: AbortSignal): Promise<Response> {
  return api.raw(`/files/${seg(fileID)}/content`, { signal });
}

export function fetchExtract(fileID: string, signal?: AbortSignal): Promise<FileExtract> {
  return api.get<FileExtract>(`/files/${seg(fileID)}/extract`, { signal });
}

/** How far a deep link searches past the first page before giving up. */
const LOCATE_MAX_PAGES = 10;

/**
 * Finds one file by ID. The list endpoint only searches names, so this asks
 * with the ID first (cheap, and works if names ever include IDs) and then
 * walks the plain list until the file turns up.
 */
export async function locateFile(fileID: string, pageSize: number, signal?: AbortSignal): Promise<FileObject | null> {
  const byId = await listFiles({ page: 1, pageSize, q: fileID, kinds: [], sort: "created" }, signal);
  const direct = byId.results.find((f) => f.fileID === fileID);
  if (direct) return direct;
  for (let page = 1; page <= LOCATE_MAX_PAGES; page += 1) {
    const res = await listFiles({ page, pageSize, q: "", kinds: [], sort: "created" }, signal);
    const hit = res.results.find((f) => f.fileID === fileID);
    if (hit) return hit;
    if (page * pageSize >= res.total) break;
  }
  return null;
}
