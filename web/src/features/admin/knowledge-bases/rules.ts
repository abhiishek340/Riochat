/** Pure rules of the knowledge base screens. */

export const MAX_UPLOAD_BATCH = 100;
export const DELETE_CHUNK = 10;

export type FileState = "ready" | "processing" | "failed" | "waiting";

/** One word for a file's state, from the fields the server reports. */
export function fileState(file: { processing: boolean; processingReady: boolean; embedError?: string; extractStatus?: string; embedStatus?: string }): FileState {
  if (file.embedError || file.extractStatus === "failed" || file.embedStatus === "failed") return "failed";
  if (file.processingReady) return "ready";
  if (file.processing) return "processing";
  return "waiting";
}

export function readyRatio(kb: { readyFileCount: number; fileCount: number }): string {
  return `${kb.readyFileCount} / ${kb.fileCount}`;
}

/** A batch above 100 files is refused before any request is made. */
export function batchTooLarge(count: number): boolean {
  return count > MAX_UPLOAD_BATCH;
}

export interface UploadOutcome {
  ok: number;
  failed: number;
  firstError?: unknown;
}

export function summarizeUploads(results: ReadonlyArray<PromiseSettledResult<unknown>>): UploadOutcome {
  const out: UploadOutcome = { ok: 0, failed: 0 };
  for (const r of results) {
    if (r.status === "fulfilled") out.ok++;
    else {
      out.failed++;
      if (out.firstError === undefined) out.firstError = r.reason;
    }
  }
  return out;
}

/** Rows of a following page that are not already shown (by file id). */
export function mergeFilePages<T extends { fileID: string }>(current: readonly T[], next: readonly T[]): { items: T[]; added: number } {
  const seen = new Set(current.map((f) => f.fileID));
  const fresh = next.filter((f) => {
    if (seen.has(f.fileID)) return false;
    seen.add(f.fileID);
    return true;
  });
  return { items: [...current, ...fresh], added: fresh.length };
}

/** Whether any file of a base still needs polling for its processing state. */
export function anyProcessing(files: ReadonlyArray<{ processing: boolean; processingReady: boolean }>): boolean {
  return files.some((f) => f.processing && !f.processingReady);
}

export function applyStatuses<T extends { fileID: string }>(files: readonly T[], statuses: ReadonlyArray<Partial<T> & { fileID: string }>): T[] {
  const by = new Map(statuses.map((s) => [s.fileID, s]));
  return files.map((f) => {
    const s = by.get(f.fileID);
    return s ? { ...f, ...s } : f;
  });
}
