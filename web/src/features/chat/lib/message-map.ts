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

function findDebugIn(value: unknown, depth = 0): DebugSnapshot[] {
  if (depth > 6 || !isRecord(value)) return [];
  const found: DebugSnapshot[] = [];
  for (const key of ["debug", "upstream_debug", "upstreamDebug"]) {
    const snap = readDebug(value[key]);
    if (snap) found.push(snap);
  }
  for (const child of Object.values(value)) {
    if (isRecord(child)) found.push(...findDebugIn(child, depth + 1));
    else if (Array.isArray(child)) for (const item of child.slice(0, 50)) found.push(...findDebugIn(item, depth + 1));
  }
  return found;
}

function parsePayload(text: string | undefined): unknown {
  if (!text) return undefined;
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

/** Best upstream debug snapshot found in the payloads of a trace. */
export function bestDebug(trace: ProcessTraceDTO | null | undefined): DebugSnapshot | null {
  if (!trace) return null;
  const payloads = [trace.process, trace.tools, trace.upstreamThink, ...(trace.events ?? [])].map((b) => parsePayload(b?.payloadJSON));
  let best: DebugSnapshot | null = null;
  for (const p of payloads) {
    for (const snap of findDebugIn(p)) {
      if (!best || debugWeight(snap) > debugWeight(best)) best = snap;
    }
  }
  return best;
}

// --- alerts --------------------------------------------------------------------

export const BLOCKED_BILLED_REASON = "moderation_blocked_upstream_usage";

function billedReason(billing: MessageDTO["billingCost"]): string {
  if (!billing?.pricingSnapshotJSON) return "";
  const parsed = parsePayload(billing.pricingSnapshotJSON);
  return isRecord(parsed) && typeof parsed.billed_reason === "string" ? parsed.billed_reason : "";
}

export function buildAlert(dto: Pick<MessageDTO, "status" | "errorCode" | "errorMessage" | "errorRequestID" | "moderation" | "billingCost" | "processTrace">): MessageAlert | null {
  const status = mapStatus(dto.status);
  if (status === "blocked" || dto.errorCode === "content_moderation.blocked") {
    const lines: MessageAlert["lines"] = [{ key: "description" }];
    if (dto.moderation?.eventID) lines.push({ key: "eventId", value: dto.moderation.eventID });
    if (dto.moderation?.categories?.length) lines.push({ key: "categories", value: dto.moderation.categories.join(", ") });
    if (billedReason(dto.billingCost) === BLOCKED_BILLED_REASON) lines.push({ key: "billed" });
    return { kind: "blocked", title: "blocked", lines };
  }
  if ((status === "error" || status === "interrupted") && dto.errorMessage.trim()) {
    return {
      kind: "interrupted",
      title: "interrupted",
      lines: [],
      message: dto.errorMessage.trim(),
      errorCode: dto.errorCode || undefined,
      errorId: dto.errorRequestID || undefined,
      debug: bestDebug(dto.processTrace),
    };
  }
  return null;
}

// --- main mapping --------------------------------------------------------------

export function mapMessage(dto: MessageDTO): ChatMessage {
  const role = mapRole(dto.role);
  const parentId = dto.parentPublicID?.trim() || null;
  const sourceId = dto.sourcePublicID?.trim() || null;
  const branch = dto.branchReason === "retry" || dto.branchReason === "edit" ? (dto.branchReason as BranchReason) : "default";
  const status = mapStatus(dto.status);
  const feedback = dto.myFeedback === "up" || dto.myFeedback === "down" ? dto.myFeedback : "";
  return {
    key: messageKey(role, dto.runID, dto.publicID),
    id: dto.publicID,
    numericId: typeof dto.id === "number" && dto.id > 0 ? dto.id : null,
    persisted: true,
    role,
    parentId,
    sourceId,
    branchReason: branch,
    runId: dto.runID ?? "",
    content: dto.content ?? "",
    contentType: dto.contentType || "text",
    status,
    attachments: parseAttachments(dto.attachments),
    usage: {
      input: numberOr(dto.inputTokens),
      output: numberOr(dto.outputTokens),
      cacheRead: numberOr(dto.cacheReadTokens),
      cacheWrite: numberOr(dto.cacheWriteTokens),
      reasoning: numberOr(dto.reasoningTokens),
    },
    reasoningEffort: normalizeEffort(dto.reasoningEffort),
    latencyMs: numberOr(dto.latencyMS),
    model: dto.platformModelName ?? "",
    errorCode: dto.errorCode ?? "",
    errorMessage: dto.errorMessage ?? "",
    errorRequestId: dto.errorRequestID ?? "",
    feedback,
    editedAt: dto.editedAt ?? null,
    createdAt: dto.createdAt,
    updatedAt: dto.updatedAt,
    billing: dto.billingCost ?? null,
    sources: mapSources(dto.knowledgeSources),
    trace: dto.processTrace ?? null,
    moderation: dto.moderation ?? null,
    uiState: sanitizeUiState(dto.uiState),
    alert: buildAlert(dto),
  };
}

/**
 * Identity key: non-system messages that have a run share a key between the
 * optimistic and persisted copy of a turn; everything else keys on the id.
 */
export function messageKey(role: MessageRole, runId: string | undefined, id: string): string {
  if (role !== "system" && runId) return `${role}:${runId}`;
  return `id:${id}`;
}

export function isSettled(message: ChatMessage): boolean {
  return message.status !== "pending";
}

/** A message usable as a parent/source id: persisted, settled and successful. */
export function isUsableParent(message: ChatMessage): boolean {
  if (!message.persisted) return false;
  if (message.status === "success") return true;
  return message.role === "assistant" && message.status === "interrupted";
}
