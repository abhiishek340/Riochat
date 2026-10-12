import { api } from "@/lib/api/client";
import { seg } from "@/lib/api/http";
import { readPage, type PageResult } from "../lib/paging";
import type { EmbeddingStatus, TaskBucket } from "./rules";

export type RuntimeName = "tika" | "docling" | "mineru" | "tesseract" | "rapidocr" | "embedding";

export interface RuntimeState {
  source: "external" | "managed" | (string & {});
  baseURL: string;
  containerName: string;
  image: string;
  network: string;
  status: string;
  reachable: boolean;
  message: string;
}

export function getRuntime(name: RuntimeName, signal?: AbortSignal): Promise<RuntimeState> {
  return api.get<RuntimeState>(`/admin/settings/${seg(name)}/runtime`, { signal });
}

export function getEmbeddingStatus(signal?: AbortSignal): Promise<EmbeddingStatus> {
  return api.get<EmbeddingStatus>("/admin/settings/embedding/status", { signal });
}

export async function reindex(includeEmpty: boolean): Promise<number> {
  const out = await api.post<{ submitted?: number }>("/admin/settings/embedding/reindex", undefined, { query: includeEmpty ? { include_empty: true } : undefined });
  return out?.submitted ?? 0;
}

export interface EmbeddingTask {
  fileID: string;
  fileName: string;
  mimeType: string;
  sizeBytes: number;
  embedStatus: string;
  embedError: string;
  stalled: boolean;
  retryable: boolean;
  retryBlockedReason: string;
  userID: number;
  userLabel: string;
  updatedAt: string;
}

export async function listTasks(args: { bucket: TaskBucket; page: number; pageSize: number; query: string }, signal?: AbortSignal): Promise<PageResult<EmbeddingTask>> {
  const raw = await api.get<unknown>("/admin/settings/embedding/tasks", { signal, query: { bucket: args.bucket, page: args.page, page_size: args.pageSize, query: args.query.trim() } });
  return readPage<EmbeddingTask>(raw);
}

export interface RetryResult {
  submittedFileIDs: string[];
  skipped: Array<{ fileID: string; reason: string }>;
}

export async function retryTasks(fileIds: string[]): Promise<RetryResult> {
  const out = await api.post<Partial<RetryResult>>("/admin/settings/embedding/tasks/retry", { fileIDs: fileIds });
  return { submittedFileIDs: out?.submittedFileIDs ?? [], skipped: out?.skipped ?? [] };
}
