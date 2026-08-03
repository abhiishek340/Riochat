import { describe, expect, it } from "vitest";
import { MAX_VECTORIZE, planVectorize, summarizeEmbedding } from "./vectorize";
import { summarizeUploads } from "./upload-outcome";
import type { UploadResponse } from "./types";

describe("planVectorize (scenario 11)", () => {
  it("trims and de-duplicates", () => {
    expect(planVectorize([" a", "a", "", "b "])).toEqual({ ids: ["a", "b"], tooMany: false });
  });
  it("flags more than 100 IDs", () => {
    const ids = Array.from({ length: MAX_VECTORIZE + 1 }, (_, i) => `f${i}`);
    expect(planVectorize(ids).tooMany).toBe(true);
    expect(planVectorize(ids.slice(0, MAX_VECTORIZE)).tooMany).toBe(false);
  });
});

describe("summarizeEmbedding", () => {
  it("warns when the queue refused some files", () => {
    expect(summarizeEmbedding({ submittedFileIDs: ["a"], skipped: [{ fileID: "b", reason: "queue_busy" }] })).toEqual({
      kind: "partial",
      submitted: 1,
      failed: 1,
    });
    expect(summarizeEmbedding({ submittedFileIDs: [], skipped: [{ fileID: "b", reason: "submit_failed" }] }).kind).toBe("partial");
  });
  it("reports nothing to do", () => {
    expect(summarizeEmbedding({ submittedFileIDs: [], skipped: [{ fileID: "b", reason: "up_to_date" }] })).toEqual({ kind: "nothing" });
  });
  it("counts submitted and skipped files on success", () => {
    expect(summarizeEmbedding({ submittedFileIDs: ["a", "b"], skipped: [{ fileID: "c", reason: "unsupported" }] })).toEqual({
      kind: "success",
      count: 2,
      skipped: 1,
    });
  });
});

const ok = (reused: boolean) => ({ ok: true as const, value: { reused, file: {}, quota: null } as unknown as UploadResponse });
const bad = (error: unknown = new Error("nope")) => ({ ok: false as const, error });

describe("summarizeUploads (scenarios 5 and 6)", () => {
  it("covers duplicates, mixes and clean uploads", () => {
    expect(summarizeUploads([ok(true)])).toEqual({ kind: "duplicate-only" });
    expect(summarizeUploads([ok(true), ok(false)])).toEqual({ kind: "mixed", newCount: 1 });
    expect(summarizeUploads([ok(false), ok(false)])).toEqual({ kind: "clean", count: 2 });
  });
  it("separates total and partial failure", () => {
    const first = new Error("first");
    expect(summarizeUploads([bad(first), bad()])).toEqual({ kind: "all-failed", error: first });
    expect(summarizeUploads([ok(false), ok(false), bad(first)])).toEqual({ kind: "partial", success: 2, failed: 1, error: first });
  });
  it("does not count cancelled uploads as failures", () => {
    const abort = new DOMException("x", "AbortError");
    expect(summarizeUploads([bad(abort)])).toEqual({ kind: "none" });
    expect(summarizeUploads([ok(false), bad(abort)])).toEqual({ kind: "clean", count: 1 });
  });
});
