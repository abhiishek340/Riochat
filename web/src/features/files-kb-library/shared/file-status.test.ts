import { describe, expect, it } from "vitest";
import { embedLabelKey, extractLabelKey, isIndexOutdated, isProcessing, markQueued, MissingTracker, mergeStatuses, processingBadge, retrievalLabel } from "./file-status";
import type { KnowledgeBaseFile, ProcessingStatus } from "./types";

const file = (over: Partial<KnowledgeBaseFile> = {}): KnowledgeBaseFile => ({
  fileID: "f1",
  fileName: "a.pdf",
  mimeType: "application/pdf",
  detectedMIME: "application/pdf",
  fileCategory: "pdf",
  sizeBytes: 1,
  processingStatus: "ready",
  processingReady: true,
  extractStatus: "ready",
  embedStatus: "none",
  embedError: "",
  chunkCount: 0,
  canVectorize: false,
  vectorizationReason: "",
  ragOptOut: false,
  createdAt: "",
  updatedAt: "t0",
  ...over,
});

describe("retrievalLabel (scenario 27)", () => {
  it("is searchable when ready, embedded and chunked", () => {
    expect(retrievalLabel(file({ embedStatus: "ready", chunkCount: 12 })).key).toBe("searchable");
  });
  it("reports an opt-out before anything else", () => {
    expect(retrievalLabel(file({ embedStatus: "ready", chunkCount: 12, ragOptOut: true })).key).toBe("disabled");
  });
  it("shows vectorizing while the embedding is queued", () => {
    expect(retrievalLabel(file({ embedStatus: "queued" })).key).toBe("vectorizing");
  });
  it("shows index pending when processing is done but nothing is embedded", () => {
    expect(retrievalLabel(file()).key).toBe("pending");
  });
  it("flags outdated indexes and empty text", () => {
    expect(retrievalLabel(file({ embedStatus: "stale" })).key).toBe("outdated");
    expect(retrievalLabel(file({ canVectorize: true, vectorizationReason: "outdated_index" })).key).toBe("outdated");
    expect(retrievalLabel(file({ extractStatus: "empty" })).key).toBe("empty");
  });
  it("prefers a danger or warning processing badge over pending", () => {
    expect(retrievalLabel(file({ processingStatus: "failed", processingReady: false })).key).toBe("failed");
    expect(retrievalLabel(file({ embedStatus: "failed" })).key).toBe("indexFailed");
  });
  it("falls back to the processing badge while still working", () => {
    expect(retrievalLabel(file({ processingStatus: "extracting", processingReady: false })).key).toBe("extracting");
  });
});

describe("processingBadge", () => {
  it("covers the documented states", () => {
    expect(processingBadge(file({ processingStatus: "failed" }))).toMatchObject({ key: "failed", tone: "danger" });
    expect(processingBadge(file({ processingErrorCode: "x.y", processingErrorMessage: "boom" }))).toMatchObject({ key: "failed", detail: "boom", errorCode: "x.y" });
    expect(processingBadge(file({ embedStatus: "processing" })).key).toBe("vectorizing");
    expect(processingBadge(file({ processingStatus: "uploaded", processingReady: false })).key).toBe("waiting");
    expect(processingBadge(file({ processingStatus: "extracting", processingReady: false, ocrUsed: true } as never)).key).toBe("ocr");
    expect(processingBadge(file({ processingStatus: "", processingReady: false, fileCategory: "image" })).key).toBe("imageReady");
    expect(processingBadge(file({ processingStatus: "weird", processingReady: true })).key).toBe("ready");
    expect(processingBadge(file({ processingStatus: "weird", processingReady: false })).key).toBe("processing");
  });
});

describe("isProcessing", () => {
  it("honours the server flag, then embed and processing status", () => {
    expect(isProcessing(file({ processing: false, embedStatus: "queued" }))).toBe(false);
    expect(isProcessing(file({ embedStatus: "queued" }))).toBe(true);
    expect(isProcessing(file({ processingStatus: "embedding" }))).toBe(true);
    expect(isProcessing(file())).toBe(false);
  });
});

describe("labels and local state", () => {
  it("maps embed and extract statuses", () => {
    expect(embedLabelKey("READY")).toBe("ready");
    expect(embedLabelKey("")).toBe("pending");
    expect(extractLabelKey("processing")).toBe("processing");
    expect(extractLabelKey("???")).toBe("pending");
  });
  it("marks queued files as not vectorizable", () => {
    expect(markQueued(file({ canVectorize: true, embedError: "old" }))).toMatchObject({
      embedStatus: "queued",
      embedError: "",
      canVectorize: false,
      vectorizationReason: "processing",
    });
  });
  it("raises the server's processing flag only when the payload carries one", () => {
    // A knowledge-base file reported processing:false must still be polled once queued.
    const kbFile = markQueued(file({ canVectorize: true, processing: false }));
    expect(kbFile.processing).toBe(true);
    expect(isProcessing(kbFile)).toBe(true);
    // A library file has no such field; adding one would never be cleared by the status endpoint.
    expect("processing" in markQueued(file({ canVectorize: true }))).toBe(false);
  });
  it("detects outdated indexes", () => {
    expect(isIndexOutdated({ embedStatus: "stale", canVectorize: false, vectorizationReason: "" })).toBe(true);
    expect(isIndexOutdated({ embedStatus: "ready", canVectorize: false, vectorizationReason: "outdated_index" })).toBe(false);
  });
});

describe("mergeStatuses", () => {
  it("applies changes and keeps identity when nothing differs (scenario 12)", () => {
    const files = [file({ embedStatus: "queued" }), file({ fileID: "f2" })];
    const status: ProcessingStatus = { fileID: "f1", embedStatus: "ready", chunkCount: 3, updatedAt: "t1", errorCode: "", errorMessage: "" };
    const first = mergeStatuses(files, [status]);
    expect(first.changed).toBe(true);
    expect(first.files[0]).toMatchObject({ embedStatus: "ready", chunkCount: 3, updatedAt: "t1" });
    expect(first.files[1]).toBe(files[1]);
    const again = mergeStatuses(first.files, [status]);
    expect(again.changed).toBe(false);
    expect(again.files).toBe(first.files);
  });
  it("maps error fields to the processing error fields", () => {
    const out = mergeStatuses([file()], [{ fileID: "f1", errorCode: "boom", errorMessage: "broke" }]);
    expect(out.files[0]).toMatchObject({ processingErrorCode: "boom", processingErrorMessage: "broke" });
  });
});

describe("MissingTracker", () => {
  it("removes a file only after two polls and at least three seconds", () => {
    const tracker = new MissingTracker(2000);
    expect(tracker.observe(["a"], [], 0)).toEqual([]);
    expect(tracker.observe(["a"], [], 2000)).toEqual([]); // second poll but only 2 s
    expect(tracker.observe(["a"], [], 4000)).toEqual(["a"]);
  });
  it("forgets a file that shows up again", () => {
    const tracker = new MissingTracker(2000);
    tracker.observe(["a"], [], 0);
    tracker.observe(["a"], ["a"], 2000);
    expect(tracker.observe(["a"], [], 4000)).toEqual([]);
    expect(tracker.observe(["a"], [], 8000)).toEqual(["a"]);
  });
});

describe("processingBadge for images", () => {
  it("calls an image ready even when its status word is not one we know", () => {
    const badge = processingBadge(file({ fileCategory: "image", processingStatus: "stored", processingReady: false }));
    expect(badge.key).toBe("imageReady");
  });
  it("still lets a known status win over the image fallback", () => {
    expect(processingBadge(file({ fileCategory: "image", processingStatus: "extracting", processingReady: false })).key).toBe("extracting");
  });
});
