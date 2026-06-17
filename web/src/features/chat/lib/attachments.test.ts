import { describe, expect, it } from "vitest";
import type { FileDTO } from "../types";
import { attachmentFromFile, localAttachment, mergeAttachments } from "./attachments";

const dto = (over: Partial<FileDTO> = {}): FileDTO => ({
  fileID: "f1", fileName: "a.png", mimeType: "image/png", detectedMIME: "image/png", fileCategory: "image", sizeBytes: 5, status: "active",
  processingStatus: "ready", processingReady: true, processingErrorCode: "", processingErrorMessage: "", extractStatus: "", embedStatus: "", embedError: "",
  chunkCount: 0, ragOptOut: false, canVectorize: true, vectorizationReason: "", ...over,
});

describe("attachments", () => {
  it("keeps previews only for images", () => {
    expect(attachmentFromFile(dto(), "blob:x").previewUrl).toBe("blob:x");
    expect(attachmentFromFile(dto({ fileName: "a.pdf", mimeType: "application/pdf", fileCategory: "pdf" }), "blob:x").previewUrl).toBeUndefined();
    expect(attachmentFromFile(dto()).kind).toBe("image");
  });

  it("never attaches a file twice and respects the cap", () => {
    const a = attachmentFromFile(dto({ fileID: "a" }));
    const b = attachmentFromFile(dto({ fileID: "b" }));
    const c = attachmentFromFile(dto({ fileID: "c" }));
    const res = mergeAttachments([a], [a, b, c], 2);
    expect(res.list.map((x) => x.fileId)).toEqual(["a", "b"]);
    expect(res).toMatchObject({ duplicates: 1, dropped: 1 });
    expect(res.added.map((x) => x.fileId)).toEqual(["b"]);
  });

  it("creates local temporary attachments", () => {
    const file = new File(["x"], "t.png", { type: "image/png" });
    const att = localAttachment(file, "blob:y");
    expect(att.fileId.startsWith("local-file-")).toBe(true);
    expect(att.file).toBe(file);
    expect(att.kind).toBe("image");
  });
});
