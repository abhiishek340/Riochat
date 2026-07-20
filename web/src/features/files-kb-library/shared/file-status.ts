/**
 * Status vocabulary shared by the files list, the content header and the
 * knowledge-base file rows. Everything here is pure so it can be tested.
 */
import type { ProcessingStatus, StatusBearing } from "./types";

export type BadgeTone = "neutral" | "info" | "success" | "warning" | "danger";

/** Keys resolve to `filesWs.status.<key>` messages. */
export type StatusKey =
  | "failed"
  | "empty"
  | "indexFailed"
  | "vectorizing"
  | "queued"
  | "indexOutdated"
  | "waiting"
  | "extracting"
  | "ocr"
  | "ready"
  | "imageReady"
  | "processing";

export interface StatusBadge {
  key: StatusKey;
  tone: BadgeTone;
  /** Server-provided failure text or code, for the failed state. */
  detail?: string;
  errorCode?: string;
}

const norm = (v: string | undefined | null): string => (v ?? "").trim().toLowerCase();

export function isIndexOutdated(file: Pick<StatusBearing, "embedStatus" | "canVectorize" | "vectorizationReason">): boolean {
  return norm(file.embedStatus) === "stale" || (file.canVectorize && norm(file.vectorizationReason) === "outdated_index");
}

/** True while the server is still working on the file. */
export function isProcessing(
  file: Pick<StatusBearing, "embedStatus" | "processingStatus"> & { processing?: boolean },
): boolean {
  if (typeof file.processing === "boolean") return file.processing;
  const embed = norm(file.embedStatus);
  if (embed === "queued" || embed === "processing") return true;
  return ["uploaded", "queued", "extracting", "embedding"].includes(norm(file.processingStatus));
}

export function processingBadge(
  file: StatusBearing & { ocrUsed?: boolean },
): StatusBadge {
  const processing = norm(file.processingStatus);
  const embed = norm(file.embedStatus);
  const extract = norm(file.extractStatus);
  const code = (file.processingErrorCode ?? "").trim();
  if (processing === "failed" || code !== "") {
    const message = (file.processingErrorMessage ?? "").trim();
    return { key: "failed", tone: "danger", detail: message || undefined, errorCode: code || undefined };
  }
  if (embed === "empty" || extract === "empty") return { key: "empty", tone: "neutral" };
  if (embed === "failed") return { key: "indexFailed", tone: "warning", detail: file.embedError || undefined };
  if (embed === "processing") return { key: "vectorizing", tone: "info" };
  if (embed === "queued") return { key: "queued", tone: "info" };
  if (isIndexOutdated(file)) return { key: "indexOutdated", tone: "warning" };
  switch (processing) {
    case "uploaded":
      return { key: "waiting", tone: "neutral" };
    case "queued":
      return { key: "queued", tone: "info" };
    case "extracting":
      return { key: file.ocrUsed ? "ocr" : "extracting", tone: "info" };
    case "embedding":
      return { key: "vectorizing", tone: "info" };
    case "ready":
      return { key: "ready", tone: "success" };
    default:
      break;
  }
  if (file.fileCategory === "image") return { key: "imageReady", tone: "success" };
  if (file.processingReady) return { key: "ready", tone: "success" };
  return { key: "processing", tone: "info" };
}

export type RetrievalKey = "disabled" | "outdated" | "empty" | "searchable" | "vectorizing" | "pending" | StatusKey;

export interface RetrievalLabel {
  key: RetrievalKey;
  tone: BadgeTone;
}

/** The label for a file inside a knowledge base (evaluated in a fixed order). */
export function retrievalLabel(file: StatusBearing): RetrievalLabel {
  const embed = norm(file.embedStatus);
  if (file.ragOptOut) return { key: "disabled", tone: "neutral" };
  if (isIndexOutdated(file)) return { key: "outdated", tone: "warning" };
  if (embed === "empty" || norm(file.extractStatus) === "empty") return { key: "empty", tone: "neutral" };
  if (file.processingReady && embed === "ready" && file.chunkCount > 0) return { key: "searchable", tone: "success" };
  if (embed === "queued" || embed === "processing") return { key: "vectorizing", tone: "info" };
  const badge = processingBadge(file);
  if (badge.tone === "danger" || badge.tone === "warning") return { key: badge.key, tone: badge.tone };
  if (file.processingReady || norm(file.processingStatus) === "ready") return { key: "pending", tone: "neutral" };
  return { key: badge.key, tone: badge.tone };
}

export type EmbedLabelKey = "ready" | "processing" | "queued" | "failed" | "empty" | "pending";

export function embedLabelKey(embedStatus: string): EmbedLabelKey {
  const v = norm(embedStatus);
  if (v === "ready" || v === "processing" || v === "queued" || v === "failed" || v === "empty") return v;
  return "pending";
}

export type ExtractLabelKey = "ready" | "processing" | "failed" | "pending";

export function extractLabelKey(extractStatus: string): ExtractLabelKey {
  const v = norm(extractStatus);
  if (v === "ready" || v === "processing" || v === "failed") return v;
  return "pending";
}

/**
 * Locally marks files as waiting for the embedding queue. When the payload
 * carries the server's own `processing` flag (knowledge-base files), it is
 * raised too: `isProcessing` trusts that flag over the status words, so a stale
 * `false` would keep the poll from ever picking the file up.
 */
export function markQueued<T extends StatusBearing>(file: T): T {
  const next: T = { ...file, embedStatus: "queued", embedError: "", canVectorize: false, vectorizationReason: "processing" };
  if (typeof file.processing === "boolean") next.processing = true;
  return next;
}

/** Folds poll results into a file; returns the same object when nothing changed. */
export function mergeStatus<T extends StatusBearing & { updatedAt: string }>(file: T, status: ProcessingStatus): T {
  const patch: Record<string, unknown> = {
    detectedMIME: status.detectedMIME,
    fileCategory: status.fileCategory,
    processingStatus: status.processingStatus,
    processingReady: status.processingReady,
    extractStatus: status.extractStatus,
    embedStatus: status.embedStatus,
    embedError: status.embedError,
    chunkCount: status.chunkCount,
    canVectorize: status.canVectorize,
    vectorizationReason: status.vectorizationReason,
    ragOptOut: status.ragOptOut,
    updatedAt: status.updatedAt,
    processing: status.processing,
    processingErrorCode: status.errorCode,
    processingErrorMessage: status.errorMessage,
  };
  const current = file as unknown as Record<string, unknown>;
  let changed = false;
  for (const [key, value] of Object.entries(patch)) {
    if (value === undefined) {
      delete patch[key];
    } else if (current[key] !== value) {
      changed = true;
    }
  }
  return changed ? { ...file, ...patch } : file;
}

/**
 * Applies a batch of statuses to a list. The result keeps object identity for
 * untouched rows and for the list itself when nothing changed.
 */
export function mergeStatuses<T extends StatusBearing & { updatedAt: string }>(
  files: readonly T[],
  statuses: readonly ProcessingStatus[],
): { files: T[]; changed: boolean } {
  const byID = new Map(statuses.map((s) => [s.fileID, s]));
  let changed = false;
  const next = files.map((file) => {
    const status = byID.get(file.fileID);
    if (!status) return file;
    const merged = mergeStatus(file, status);
    if (merged !== file) changed = true;
    return merged;
  });
  return { files: changed ? next : (files as T[]), changed };
}

/**
 * Decides when a file that stopped appearing in poll results is really gone:
 * it must be absent from at least two consecutive polls and for at least
 * max(2 x interval, 3 s) since it was first seen missing.
 */
export class MissingTracker {
  private readonly firstMissing = new Map<string, { at: number; polls: number }>();

  constructor(private readonly intervalMs: number) {}

  /** `asked` = IDs sent in the poll, `answered` = IDs the server returned. Returns IDs now considered removed. */
  observe(asked: readonly string[], answered: readonly string[], now: number): string[] {
    const present = new Set(answered);
    const removed: string[] = [];
    const askedSet = new Set(asked);
    for (const id of asked) {
      if (present.has(id)) {
        this.firstMissing.delete(id);
        continue;
      }
      const entry = this.firstMissing.get(id) ?? { at: now, polls: 0 };
      entry.polls += 1;
      this.firstMissing.set(id, entry);
      if (entry.polls >= 2 && now - entry.at >= Math.max(this.intervalMs * 2, 3000)) {
        removed.push(id);
        this.firstMissing.delete(id);
      }
    }
    for (const id of [...this.firstMissing.keys()]) if (!askedSet.has(id)) this.firstMissing.delete(id);
    return removed;
  }

  reset(): void {
    this.firstMissing.clear();
  }
}
