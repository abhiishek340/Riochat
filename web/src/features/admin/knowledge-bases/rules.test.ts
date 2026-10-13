import { describe, expect, it } from "vitest";
import { anyProcessing, applyStatuses, batchTooLarge, fileState, mergeFilePages, readyRatio, summarizeUploads } from "./rules";

describe("knowledge base rules", () => {
  it("derives a single state per file", () => {
    expect(fileState({ processing: false, processingReady: true })).toBe("ready");
    expect(fileState({ processing: true, processingReady: false })).toBe("processing");
    expect(fileState({ processing: false, processingReady: false })).toBe("waiting");
    expect(fileState({ processing: false, processingReady: true, embedError: "boom" })).toBe("failed");
    expect(fileState({ processing: true, processingReady: false, extractStatus: "failed" })).toBe("failed");
  });
  it("formats the ready ratio", () => {
    expect(readyRatio({ readyFileCount: 3, fileCount: 8 })).toBe("3 / 8");
  });
  it("refuses more than 100 files per batch", () => {
    expect(batchTooLarge(100)).toBe(false);
    expect(batchTooLarge(101)).toBe(true);
  });
  it("counts successes and keeps the first failure", () => {
    const out = summarizeUploads([{ status: "fulfilled", value: 1 }, { status: "rejected", reason: "a" }, { status: "rejected", reason: "b" }]);
    expect(out).toEqual({ ok: 1, failed: 2, firstError: "a" });
  });
  it("merges pages without duplicates by file id", () => {
    const merged = mergeFilePages([{ fileID: "a" }, { fileID: "b" }], [{ fileID: "b" }, { fileID: "c" }]);
    expect(merged.items.map((f) => f.fileID)).toEqual(["a", "b", "c"]);
    expect(merged.added).toBe(1);
  });
  it("polls while something is still processing", () => {
    expect(anyProcessing([{ processing: false, processingReady: true }])).toBe(false);
    expect(anyProcessing([{ processing: true, processingReady: false }])).toBe(true);
  });
  it("overlays status updates on the matching files only", () => {
    const next = applyStatuses([{ fileID: "a", processing: true }, { fileID: "b", processing: true }], [{ fileID: "a", processing: false }]);
    expect(next).toEqual([{ fileID: "a", processing: false }, { fileID: "b", processing: true }]);
  });
});
