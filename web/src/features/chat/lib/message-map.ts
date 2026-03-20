/** Server message -> view message, plus the helpers the mapping needs. */
import { isRecord } from "@/lib/utils/guards";
import type {
  BranchReason,
  ChatAttachment,
  ChatMessage,
  DebugSnapshot,
  KnowledgeSource,
  MessageAlert,
  MessageDTO,
  MessageRole,
  MessageStatus,
  ProcessTraceDTO,
  UsageCounters,
} from "../types";

const STATUSES: readonly MessageStatus[] = ["pending", "success", "error", "interrupted", "canceled", "blocked"];
const EFFORTS = ["none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"] as const;

export const EMPTY_USAGE: UsageCounters = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, reasoning: 0 };

export function mapRole(value: string): MessageRole {
  return value === "assistant" ? "assistant" : value === "system" ? "system" : "user";
}

export function mapStatus(value: string | undefined): MessageStatus {
  const v = (value ?? "").trim().toLowerCase();
  if (v === "cancelled") return "canceled";
  return (STATUSES as readonly string[]).includes(v) ? (v as MessageStatus) : "success";
}

export function normalizeEffort(value: string | null | undefined): string | null {
  const v = (value ?? "").trim().toLowerCase();
  return (EFFORTS as readonly string[]).includes(v) ? v : null;
}

function numberOr(value: unknown, fallback = 0): number {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

/** Attachments arrive as a JSON string of an array; bad data yields none. */
export function parseAttachments(raw: unknown): ChatAttachment[] {
  let list: unknown = raw;
  if (typeof raw === "string") {
    if (!raw.trim()) return [];
    try {
      list = JSON.parse(raw);
    } catch {
      return [];
    }
  }
  if (!Array.isArray(list)) return [];
  const out: ChatAttachment[] = [];
  for (const item of list) {
    if (!isRecord(item)) continue;
    const fileId = typeof item.file_id === "string" ? item.file_id : typeof item.fileID === "string" ? item.fileID : "";
    const fileName = typeof item.file_name === "string" ? item.file_name : typeof item.fileName === "string" ? item.fileName : "";
    if (!fileId || !fileName) continue;
    const duration = numberOr(item.duration_seconds ?? item.durationSeconds);
    out.push({
      fileId,
      fileName,
      mimeType: String(item.mime_type ?? item.mimeType ?? ""),
      detectedMime: String(item.detected_mime ?? item.detectedMIME ?? ""),
      fileCategory: String(item.file_category ?? item.fileCategory ?? ""),
      fileSize: numberOr(item.file_size ?? item.sizeBytes),
      durationSeconds: duration > 0 ? Math.ceil(duration) : 0,
      kind: item.kind === "image" ? "image" : "file",
      processingStatus: String(item.processing_status ?? item.processingStatus ?? ""),
      processingReady: item.processing_ready === true || item.processingReady === true,
      processingErrorCode: String(item.processing_error_code ?? item.processingErrorCode ?? ""),
      processingErrorMessage: String(item.processing_error_message ?? item.processingErrorMessage ?? ""),
    });
  }
  return out;
}

/** Keeps a saved interactive state only if it is a non-empty map of block id -> object. */
export function sanitizeUiState(value: unknown): Record<string, Record<string, unknown>> {
  if (!isRecord(value)) return {};
  const out: Record<string, Record<string, unknown>> = {};
  for (const [id, state] of Object.entries(value)) {
    if (isRecord(state) && Object.keys(state).length > 0) out[id] = state;
  }
  return out;
}

export function mapSources(list: MessageDTO["knowledgeSources"]): KnowledgeSource[] {
  if (!Array.isArray(list)) return [];
  return list.map((s) => ({
    fileName: s.fileName ?? "",
    fileId: s.fileId ?? "",
    chunkIndex: numberOr(s.chunkIndex),
    score: numberOr(s.score),
    preview: s.preview ?? "",
    modality: s.modality === "image" ? "image" : "text",
  }));
}

// --- debug snapshots ---------------------------------------------------------

function strMap(value: unknown): Record<string, string> {
  if (!isRecord(value)) return {};
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(value)) out[k] = typeof v === "string" ? v : JSON.stringify(v);
  return out;
}

function pick(record: Record<string, unknown>, ...names: string[]): unknown {
  for (const n of names) if (record[n] !== undefined) return record[n];
  return undefined;
}

/** Accepts the server's debug object in camelCase or snake_case. */
export function readDebug(value: unknown): DebugSnapshot | null {
  if (!isRecord(value)) return null;
  const requestBody = pick(value, "requestBody", "request_body");
  const responseBody = pick(value, "responseBody", "response_body");
  const snapshot: DebugSnapshot = {
    method: String(pick(value, "method") ?? ""),
    path: String(pick(value, "path", "url") ?? ""),
    status: numberOr(pick(value, "status", "statusCode", "status_code")),
    requestHeaders: strMap(pick(value, "requestHeaders", "request_headers")),
    requestBody: typeof requestBody === "string" ? requestBody : requestBody === undefined ? "" : JSON.stringify(requestBody),
    responseHeaders: strMap(pick(value, "responseHeaders", "response_headers")),
    responseBody: typeof responseBody === "string" ? responseBody : responseBody === undefined ? "" : JSON.stringify(responseBody),
  };
  const empty = !snapshot.method && !snapshot.path && !snapshot.status && !snapshot.requestBody && !snapshot.responseBody;
  return empty ? null : snapshot;
}

/** Richness weights: request body 8, response body 4, request headers 2, response headers 1. */
export function debugWeight(d: DebugSnapshot): number {
  return (
    (d.requestBody ? 8 : 0) +
    (d.responseBody ? 4 : 0) +
    (Object.keys(d.requestHeaders).length ? 2 : 0) +
    (Object.keys(d.responseHeaders).length ? 1 : 0)
  );
}
