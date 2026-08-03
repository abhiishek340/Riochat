/** Decides which toast follows a batch of uploads. Pure, so the wording rules stay testable. */
import { isAbortError } from "@/lib/api/errors";
import type { Settled } from "./async";
import type { UploadResponse } from "./types";

export type UploadOutcome =
  | { kind: "none" }
  | { kind: "duplicate-only" }
  | { kind: "mixed"; newCount: number }
  | { kind: "clean"; count: number }
  | { kind: "all-failed"; error: unknown }
  | { kind: "partial"; success: number; failed: number; error: unknown };

export function summarizeUploads(results: readonly Settled<UploadResponse>[]): UploadOutcome {
  const successes = results.flatMap((r) => (r.ok ? [r.value] : []));
  // Uploads cancelled by a newer pick are not failures the user caused.
  const failures = results.flatMap((r) => (!r.ok && !isAbortError(r.error) ? [r.error] : []));
  if (successes.length === 0) return failures.length > 0 ? { kind: "all-failed", error: failures[0] } : { kind: "none" };
  if (failures.length > 0) return { kind: "partial", success: successes.length, failed: failures.length, error: failures[0] };
  const reused = successes.filter((s) => s.reused).length;
  if (reused === successes.length) return { kind: "duplicate-only" };
  if (reused > 0) return { kind: "mixed", newCount: successes.length - reused };
  return { kind: "clean", count: successes.length };
}
