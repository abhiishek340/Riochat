/** Rules for submitting files to the embedding queue (same for library, knowledge-base and platform sources). */
import { cleanIDs } from "./async";
import type { EmbeddingResponse } from "./types";

export const MAX_VECTORIZE = 100;

export interface VectorizePlan {
  ids: string[];
  tooMany: boolean;
}

/** Trims and de-duplicates; flags requests above the server limit. */
export function planVectorize(ids: readonly string[]): VectorizePlan {
  const clean = cleanIDs(ids);
  return { ids: clean, tooMany: clean.length > MAX_VECTORIZE };
}

export type EmbeddingOutcome =
  | { kind: "partial"; submitted: number; failed: number }
  | { kind: "nothing" }
  | { kind: "success"; count: number; skipped: number };

const QUEUE_FAILURES = new Set(["queue_busy", "submit_failed"]);

/** Turns the server's answer into the toast the user should see. */
export function summarizeEmbedding(res: Pick<EmbeddingResponse, "submittedFileIDs" | "skipped">): EmbeddingOutcome {
  const submitted = res.submittedFileIDs?.length ?? 0;
  const skipped = res.skipped ?? [];
  const failed = skipped.filter((s) => QUEUE_FAILURES.has(s.reason)).length;
  if (failed > 0) return { kind: "partial", submitted, failed };
  if (submitted === 0) return { kind: "nothing" };
  return { kind: "success", count: submitted, skipped: skipped.length };
}
