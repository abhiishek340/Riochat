/**
 * Runs a multi-entry import one entry at a time. The server limits requests
 * per user and each entry costs two (probe + create), so a 429 pauses the run
 * for the advertised time and retries the same request. All side effects go
 * through `BatchDeps`, which keeps this testable without a network.
 */
import type { AvailableModel, PersonalProvider } from "./api";
import { PRESELECT_LIMIT } from "./selection";
import type { LinkEntry } from "./link-parser";

export type EntryStatus =
  | { kind: "pending" }
  | { kind: "probing" }
  | { kind: "saving" }
  | { kind: "throttled"; seconds: number }
  | { kind: "imported"; count: number }
  | { kind: "noModels" }
  | { kind: "needsSelection"; count: number; models: AvailableModel[] }
  | { kind: "failed"; message: string }
  | { kind: "overLimit" };

export const RETRY_AFTER_DEFAULT = 60;
export const RETRY_AFTER_MAX = 60;

/** Seconds to wait after a 429: rounded up, at least 1, at most 60; 60 when unknown. */
export function throttleSeconds(retryAfter: number | undefined): number {
  if (retryAfter === undefined || !Number.isFinite(retryAfter)) return RETRY_AFTER_DEFAULT;
  return Math.min(RETRY_AFTER_MAX, Math.max(1, Math.ceil(retryAfter)));
}

export interface BatchDeps {
  probe: (entry: LinkEntry) => Promise<AvailableModel[]>;
  create: (entry: LinkEntry, models: AvailableModel[]) => Promise<PersonalProvider>;
  sleep: (ms: number) => Promise<void>;
  cancelled: () => boolean;
  describe: (error: unknown) => string;
  /** Status of the entry at `index` in the original list. */
  report: (index: number, status: EntryStatus) => void;
  /** 429 detection: returns the Retry-After seconds, or null when not a 429. */
  throttle: (error: unknown) => { retryAfter: number | undefined } | null;
}

/** Retries `action` through 429 responses, counting the wait down second by second. */
export async function withThrottle<T>(action: () => Promise<T>, index: number, deps: BatchDeps): Promise<T> {
  for (;;) {
    try {
      return await action();
    } catch (error) {
      const limited = deps.throttle(error);
      if (!limited) throw error;
      for (let left = throttleSeconds(limited.retryAfter); left > 0; left--) {
        if (deps.cancelled()) throw new CancelledError();
        deps.report(index, { kind: "throttled", seconds: left });
        await deps.sleep(1000);
      }
      if (deps.cancelled()) throw new CancelledError();
    }
  }
}

export class CancelledError extends Error {
  constructor() {
    super("cancelled");
    this.name = "CancelledError";
  }
}

export interface BatchOutcome {
  imported: number;
  freeSlots: number;
}

/**
 * Processes the given entries (indices into `entries`) in order.
 * `freeSlots` is the number of providers the user may still add.
 */
export async function runBatch(
  entries: readonly LinkEntry[],
  indices: readonly number[],
  freeSlots: number,
  deps: BatchDeps,
): Promise<BatchOutcome> {
  let slots = freeSlots;
  let imported = 0;
  for (const index of indices) {
    if (deps.cancelled()) break;
    const entry = entries[index];
    if (!entry) continue;
    if (slots <= 0) {
      deps.report(index, { kind: "overLimit" });
      continue;
    }
    try {
      deps.report(index, { kind: "probing" });
      const models = await withThrottle(() => deps.probe(entry), index, deps);
      if (models.length === 0) {
        deps.report(index, { kind: "noModels" });
        continue;
      }
      if (models.length > PRESELECT_LIMIT) {
        deps.report(index, { kind: "needsSelection", count: models.length, models });
        continue;
      }
      deps.report(index, { kind: "saving" });
      await withThrottle(() => deps.create(entry, models), index, deps);
      slots--;
      imported++;
      deps.report(index, { kind: "imported", count: models.length });
    } catch (error) {
      if (error instanceof CancelledError) break;
      deps.report(index, { kind: "failed", message: deps.describe(error) });
    }
  }
  return { imported, freeSlots: slots };
}
