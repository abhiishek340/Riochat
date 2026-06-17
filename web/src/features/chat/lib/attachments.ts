/** Turning files and file records into composer attachments. */
import type { ChatAttachment, FileDTO } from "../types";
import { normalizeMime } from "./file-policy";

function isImageFile(name: string, mime: string, category: string): boolean {
  return category === "image" || normalizeMime(name, mime).startsWith("image/");
}

export function attachmentFromFile(dto: FileDTO, previewUrl?: string): ChatAttachment {
  const image = isImageFile(dto.fileName, dto.mimeType, dto.fileCategory);
  return {
    fileId: dto.fileID,
    fileName: dto.fileName,
    mimeType: dto.mimeType,
    detectedMime: dto.detectedMIME,
    fileCategory: dto.fileCategory,
    fileSize: dto.sizeBytes,
    durationSeconds: 0,
    kind: image ? "image" : "file",
    processingStatus: dto.processingStatus,
    processingReady: dto.processingReady,
    processingErrorCode: dto.processingErrorCode,
    processingErrorMessage: dto.processingErrorMessage,
    previewUrl: image ? previewUrl : undefined,
    embedStatus: dto.embedStatus,
    extractStatus: dto.extractStatus,
    ragOptOut: dto.ragOptOut,
  };
}

let localCounter = 0;

/** A temporary-chat attachment: the file stays in memory. */
export function localAttachment(file: File, previewUrl?: string): ChatAttachment {
  const image = file.type.startsWith("image/");
  return {
    fileId: `local-file-${Date.now().toString(36)}-${++localCounter}`,
    fileName: file.name,
    mimeType: file.type,
    detectedMime: file.type,
    fileCategory: image ? "image" : "",
    fileSize: file.size,
    durationSeconds: 0,
    kind: image ? "image" : "file",
    processingStatus: "ready",
    processingReady: true,
    processingErrorCode: "",
    processingErrorMessage: "",
    previewUrl: image ? previewUrl : undefined,
    file,
  };
}

export function isLocalAttachment(a: ChatAttachment): boolean {
  return a.fileId.startsWith("local-file-");
}

export function attachmentFailed(a: ChatAttachment): boolean {
  return a.processingStatus === "failed" || a.processingErrorCode !== "";
}

/** Merges new attachments into a list, never twice by file id, within `cap`. */
export function mergeAttachments(list: readonly ChatAttachment[], incoming: readonly ChatAttachment[], cap: number): { list: ChatAttachment[]; added: ChatAttachment[]; duplicates: number; dropped: number } {
  const have = new Set(list.map((a) => a.fileId));
  const out = [...list];
  const added: ChatAttachment[] = [];
  let duplicates = 0;
  let dropped = 0;
  for (const a of incoming) {
    if (have.has(a.fileId)) {
      duplicates++;
      continue;
    }
    if (out.length >= cap) {
      dropped++;
      continue;
    }
    have.add(a.fileId);
    out.push(a);
    added.push(a);
  }
  return { list: out, added, duplicates, dropped };
}
