/** Bulk-operation helpers: chunking, per-item settling, batch-delete result merging. */

export function chunk<T>(items: readonly T[], size: number): T[][] {
  const n = Math.max(1, Math.floor(size));
  const out: T[][] = [];
  for (let i = 0; i < items.length; i += n) out.push(items.slice(i, i + n));
  return out;
}

export interface BulkOutcome<T> {
  succeeded: T[];
  failed: Array<{ item: T; error: unknown }>;
}

export interface BulkOptions {
  chunkSize?: number;
  /** Run the items of a chunk one after another instead of concurrently. */
  sequential?: boolean;
  onProgress?: (processed: number, total: number) => void;
}

/** Runs `run` for each item, settling every one individually, chunk by chunk. */
export async function runBulk<T>(items: readonly T[], run: (item: T) => Promise<unknown>, options: BulkOptions = {}): Promise<BulkOutcome<T>> {
  const outcome: BulkOutcome<T> = { succeeded: [], failed: [] };
  let processed = 0;
  for (const group of chunk(items, options.chunkSize ?? 100)) {
    if (options.sequential) {
      for (const item of group) {
        try {
          await run(item);
          outcome.succeeded.push(item);
        } catch (error) {
          outcome.failed.push({ item, error });
        }
        processed++;
        options.onProgress?.(processed, items.length);
      }
    } else {
      const settled = await Promise.allSettled(group.map((item) => run(item)));
      settled.forEach((result, index) => {
        const item = group[index] as T;
        if (result.status === "fulfilled") outcome.succeeded.push(item);
        else outcome.failed.push({ item, error: result.reason });
      });
      processed += group.length;
      options.onProgress?.(processed, items.length);
    }
  }
  return outcome;
}

/** True when more than one chunk is needed, which is when progress is worth showing. */
export function needsProgress(count: number, chunkSize = 100): boolean {
  return count > chunkSize;
}

/** Items whose current value already equals the requested one are left out. */
export function pendingTargets<T, V>(items: readonly T[], current: (item: T) => V, wanted: V): T[] {
  return items.filter((item) => current(item) !== wanted);
}

export interface BatchDeleteResult {
  total: number;
  successCount: number;
  notFoundCount: number;
  failedCount: number;
  results: Array<{ id: number; status: "deleted" | "not_found" | "failed" | (string & {}); error?: string }>;
}

export const EMPTY_BATCH_RESULT: BatchDeleteResult = { total: 0, successCount: 0, notFoundCount: 0, failedCount: 0, results: [] };

/** Chunk results merge by summing counts and concatenating the per-id results. */
export function mergeBatchResults(parts: readonly Partial<BatchDeleteResult>[]): BatchDeleteResult {
  const merged: BatchDeleteResult = { total: 0, successCount: 0, notFoundCount: 0, failedCount: 0, results: [] };
  for (const part of parts) {
    merged.total += part.total ?? 0;
    merged.successCount += part.successCount ?? 0;
    merged.notFoundCount += part.notFoundCount ?? 0;
    merged.failedCount += part.failedCount ?? 0;
    merged.results.push(...(part.results ?? []));
  }
  return merged;
}

/** Ids that are gone after a batch delete: deleted or already missing. */
export function removedIds(result: BatchDeleteResult): Set<number> {
  const gone = new Set<number>();
  for (const row of result.results) if (row.status === "deleted" || row.status === "not_found") gone.add(row.id);
  return gone;
}

/** Keeps only the selected ids that are still among the visible rows. */
export function pruneSelection<K>(selected: ReadonlySet<K>, visible: readonly K[]): Set<K> {
  const visibleSet = new Set(visible);
  const next = new Set<K>();
  for (const id of selected) if (visibleSet.has(id)) next.add(id);
  return next;
}

/**
 * Sends `ids` to a batch-delete endpoint chunk by chunk and merges the answers.
 * A chunk that throws aborts the run; the chunks before it already took effect.
 */
export async function batchDeleteInChunks(ids: readonly number[], call: (chunkIds: number[]) => Promise<Partial<BatchDeleteResult>>, size = 100): Promise<BatchDeleteResult> {
  const parts: Partial<BatchDeleteResult>[] = [];
  for (const group of chunk(ids, size)) parts.push(await call(group));
  return mergeBatchResults(parts);
}

/** Ids the server reported as failed in a batch delete. */
export function failedIds(result: BatchDeleteResult): Set<number> {
  const bad = new Set<number>();
  for (const row of result.results) if (row.status === "failed") bad.add(row.id);
  return bad;
}

/** Which of the requested ids left (deleted or already gone) and which the server kept. */
export function splitBatch(ids: readonly number[], result: BatchDeleteResult): { gone: number[]; kept: number[] } {
  const bad = failedIds(result);
  return { gone: ids.filter((id) => !bad.has(id)), kept: ids.filter((id) => bad.has(id)) };
}
