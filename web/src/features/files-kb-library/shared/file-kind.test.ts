import { describe, expect, it } from "vitest";
import {
  isActiveContentType,
  categoryOf,
  cleanContentType,
  extensionOf,
  isReadableText,
  isReadyLikeStatus,
  matchesFilters,
  matchesQuery,
  previewKindOf,
  rawTypeOf,
} from "./file-kind";

describe("categoryOf", () => {
  it("resolves by MIME and extension, first match wins", () => {
    expect(categoryOf("a.bin", "image/png")).toBe("image");
    expect(categoryOf("photo.HEIC", "")).toBe("image");
    expect(categoryOf("x", "application/pdf")).toBe("pdf");
    expect(categoryOf("song.mp3", "audio/mpeg")).toBe("audio");
    expect(categoryOf("clip.mp4", "video/mp4")).toBe("video");
    expect(categoryOf("t.xlsx", "")).toBe("spreadsheet");
    expect(categoryOf("t", "text/csv")).toBe("spreadsheet");
    expect(categoryOf("deck.pptx", "")).toBe("presentation");
    expect(categoryOf("memo.docx", "")).toBe("document");
    expect(categoryOf("main.go", "")).toBe("code");
    expect(categoryOf("README.md", "")).toBe("code");
    expect(categoryOf("notes", "text/plain")).toBe("code");
    expect(categoryOf("blob", "application/zip")).toBe("document");
  });
});

describe("previewKindOf", () => {
  const kind = (fileName: string, contentType: string, fallbackMime?: string) => previewKindOf({ fileName, contentType, fallbackMime });
  it("picks office and sheet viewers before categories", () => {
    expect(kind("a.docx", "application/octet-stream")).toBe("docx");
    expect(kind("data.csv", "text/csv; charset=utf-8")).toBe("spreadsheet");
    expect(kind("deck.pptx", "")).toBe("native");
    expect(kind("legacy.doc", "application/msword")).toBe("native");
  });
  it("falls through categories", () => {
    expect(kind("p.png", "image/png")).toBe("image");
    expect(kind("p.pdf", "application/pdf")).toBe("pdf");
    expect(kind("a.mp3", "audio/mpeg")).toBe("audio");
    expect(kind("a.mp4", "video/mp4")).toBe("video");
    expect(kind("README.md", "text/markdown")).toBe("markdown");
    expect(kind("main.ts", "text/plain")).toBe("code");
    expect(kind("notes.txt", "text/plain")).toBe("text");
    expect(kind("x.xyz", "application/octet-stream")).toBe("unsupported");
  });
  it("uses the declared MIME when the response has none", () => {
    expect(kind("blob", "", "image/webp")).toBe("image");
  });
});

describe("isReadableText", () => {
  it("accepts normal text and the empty sample", () => {
    expect(isReadableText("")).toBe(true);
    expect(isReadableText("Hello\tworld\r\nline two")).toBe(true);
  });
  it("rejects replacement characters at 8% and control characters at 2%", () => {
    expect(isReadableText("�".repeat(8) + "a".repeat(92))).toBe(false);
    expect(isReadableText("�".repeat(7) + "a".repeat(93))).toBe(true);
    expect(isReadableText("\u0001".repeat(2) + "a".repeat(98))).toBe(false);
    expect(isReadableText("\u0001" + "a".repeat(99))).toBe(true);
  });
  it("only looks at the first 4000 characters", () => {
    expect(isReadableText("a".repeat(4000) + "\u0000".repeat(500))).toBe(true);
  });
});

describe("isActiveContentType", () => {
  it("flags types a browser would run as a page", () => {
    for (const type of ["text/html", "text/html; charset=utf-8", "image/svg+xml", "application/xhtml+xml", "application/xml", "text/xml", "application/atom+xml", "text/javascript", "application/javascript"]) {
      expect(isActiveContentType(type), type).toBe(true);
    }
    for (const type of ["text/plain", "application/pdf", "image/png", "application/json", "", null, undefined]) {
      expect(isActiveContentType(type), String(type)).toBe(false);
    }
  });
});

describe("small helpers", () => {
  it("extracts extensions", () => {
    expect(extensionOf("Archive.TAR.GZ")).toBe("gz");
    expect(extensionOf("noext")).toBe("");
    expect(extensionOf("trailing.")).toBe("");
  });
  it("cleans content types", () => {
    expect(cleanContentType("Text/Plain; charset=utf-8")).toBe("text/plain");
    expect(cleanContentType(null)).toBe("");
  });
  it("knows ready-like statuses", () => {
    for (const s of ["active", " READY ", "available", "success"]) expect(isReadyLikeStatus(s)).toBe(true);
    for (const s of ["failed", "processing", "", undefined]) expect(isReadyLikeStatus(s)).toBe(false);
  });
  it("filters freshly uploaded files", () => {
    expect(matchesFilters({ fileName: "a.png", mimeType: "image/png" }, ["pdf"])).toBe(false);
    expect(matchesFilters({ fileName: "a.png", mimeType: "image/png" }, ["pdf", "image"])).toBe(true);
    expect(matchesFilters({ fileName: "a.png", mimeType: "image/png" }, [])).toBe(true);
    expect(matchesQuery({ fileName: "Quarterly.PDF" }, " quarter ")).toBe(true);
  });
  it("labels the raw type", () => {
    expect(rawTypeOf({ mimeType: "application/pdf", fileName: "a.pdf" })).toBe("application/pdf");
    expect(rawTypeOf({ mimeType: "application/octet-stream", fileName: "a.bin" })).toBe("bin");
    expect(rawTypeOf({ mimeType: "", fileName: "noext" })).toBe("");
  });
});
