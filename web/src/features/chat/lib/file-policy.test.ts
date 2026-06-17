import { describe, expect, it } from "vitest";
import type { FilePolicyDTO } from "../types";
import { checkFile, fitToSlots, inferKind, isAllowedInTemporary, isProcessing, messageFileCap, normalizeMime } from "./file-policy";

const policy = (p: Partial<FilePolicyDTO> = {}): FilePolicyDTO => ({
  maxMessageFiles: 0,
  maxUploadFileBytes: 0,
  allowedMIMETypes: null,
  imageMaxBytes: 0,
  docMaxBytes: 0,
  effectiveImageMaxBytes: 0,
  effectiveDocMaxBytes: 0,
  fullContextMaxBytes: 0,
  fullContextMaxTokens: 0,
  fullContextPDFMaxPages: 0,
  ragAvailable: true,
  ragAvailabilityReason: "",
  capabilityMode: "full_context_and_rag",
  fileMode: "auto",
  ...p,
});

describe("normalizeMime", () => {
  it("treats active content as plain text", () => {
    expect(normalizeMime("a.html", "text/html")).toBe("text/plain");
    expect(normalizeMime("a.svg", "image/svg+xml")).toBe("text/plain");
    expect(normalizeMime("x", "application/xml; charset=utf-8")).toBe("text/plain");
  });
  it("derives known types from the extension", () => {
    expect(normalizeMime("r.PDF", "")).toBe("application/pdf");
    expect(normalizeMime("t.yml", "application/octet-stream")).toBe("text/yaml");
    expect(normalizeMime("main.go", "")).toBe("text/plain");
    expect(normalizeMime("p.png", "image/PNG; x=1")).toBe("image/png");
    expect(normalizeMime("v.mp4", "video/mp4")).toBe("video/mp4");
  });
});

describe("checkFile", () => {
  it("passes everything without a policy", () => {
    expect(checkFile(null, { name: "a.png", type: "image/png", size: 1e12 })).toBeNull();
  });
  it("rejects types outside the allowlist", () => {
    const p = policy({ allowedMIMETypes: ["application/pdf"] });
    expect(checkFile(p, { name: "a.png", type: "image/png", size: 1 })).toEqual({ reason: "type" });
    expect(checkFile(p, { name: "a.pdf", type: "application/pdf", size: 1 })).toBeNull();
  });
  it("applies the per-category limit and reports full-context-only mode", () => {
    const p = policy({ maxUploadFileBytes: 1000, effectiveImageMaxBytes: 5000, effectiveDocMaxBytes: 100, capabilityMode: "full_context_only" });
    expect(checkFile(p, { name: "a.png", type: "image/png", size: 4000 })).toBeNull();
    expect(checkFile(p, { name: "a.pdf", type: "application/pdf", size: 101 })).toEqual({ reason: "fullContextOnly", limit: 100 });
    expect(checkFile(p, { name: "a.mp3", type: "audio/mpeg", size: 1001 })).toEqual({ reason: "size", limit: 1000 });
    expect(checkFile(policy({ maxUploadFileBytes: 10 }), { name: "a.txt", type: "text/plain", size: 11 })).toEqual({ reason: "size", limit: 10 });
  });
});

describe("misc", () => {
  it("caps and splits", () => {
    expect(messageFileCap(policy({ maxMessageFiles: 3 }))).toBe(3);
    expect(messageFileCap(null)).toBe(10);
    expect(fitToSlots([1, 2, 3, 4, 5], 3)).toEqual({ accepted: [1, 2, 3], ignored: 2 });
    expect(fitToSlots([1], 0)).toEqual({ accepted: [], ignored: 1 });
  });
  it("detects processing states", () => {
    expect(isProcessing("queued")).toBe(true);
    expect(isProcessing("ready", "processing")).toBe(true);
    expect(isProcessing("ready", "done")).toBe(false);
  });
  it("kinds", () => {
    expect(inferKind("a.csv", "text/csv")).toBe("excel");
    expect(inferKind("a.docx", "x")).toBe("word");
    expect(isAllowedInTemporary({ name: "a.mp3", type: "audio/mpeg" })).toBe(false);
    expect(isAllowedInTemporary({ name: "a.pdf", type: "application/pdf" })).toBe(true);
  });
});
