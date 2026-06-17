/**
 * Client-side pre-checks for attachments, mirroring the server's chat-file
 * policy so obviously invalid files never get uploaded.
 */
import type { FilePolicyDTO } from "../types";

export const DEFAULT_MAX_MESSAGE_FILES = 10;

const ACTIVE_EXT = new Set(["html", "htm", "css", "js", "jsx", "mjs", "ts", "tsx", "xml", "xhtml", "svg"]);
const ACTIVE_MIME = new Set([
  "text/html", "text/css", "text/javascript", "text/xml", "application/javascript", "application/ecmascript",
  "application/x-javascript", "application/typescript", "application/xml", "application/xhtml+xml", "image/svg+xml",
]);

const EXT_MIME: Record<string, string> = {
  pdf: "application/pdf",
  docx: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  doc: "application/msword",
  xlsx: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
  xls: "application/vnd.ms-excel",
  csv: "text/csv",
  md: "text/markdown",
  markdown: "text/markdown",
  json: "application/json",
  yaml: "text/yaml",
  yml: "text/yaml",
  toml: "application/toml",
};

const TEXT_EXT = new Set([
  "txt", "js", "ts", "tsx", "jsx", "py", "go", "rs", "java", "c", "cpp", "h", "hpp", "cs", "php", "rb", "swift", "kt",
  "bash", "zsh", "sql", "yaml", "yml", "toml", "sh", "html", "htm", "css", "ini", "conf",
]);

export function extensionOf(name: string): string {
  const i = name.lastIndexOf(".");
  return i < 0 || i === name.length - 1 ? "" : name.slice(i + 1).toLowerCase();
}

export function stripMimeParams(mime: string): string {
  return mime.split(";")[0]?.trim().toLowerCase() ?? "";
}

/** Active content is always treated as plain text; extensions decide known types. */
export function normalizeMime(name: string, mime: string): string {
  const base = stripMimeParams(mime);
  const ext = extensionOf(name);
  if (ACTIVE_EXT.has(ext) || ACTIVE_MIME.has(base)) return "text/plain";
  if (base.startsWith("image/")) return base;
  if (ext in EXT_MIME) return EXT_MIME[ext] as string;
  if (TEXT_EXT.has(ext)) return "text/plain";
  return base;
}

export type FileKind = "image" | "audio" | "video" | "pdf" | "word" | "excel" | "text" | "unknown";

export function inferKind(name: string, normalizedMime: string): FileKind {
  if (normalizedMime.startsWith("image/")) return "image";
  if (normalizedMime.startsWith("audio/")) return "audio";
  if (normalizedMime.startsWith("video/")) return "video";
  const ext = extensionOf(name);
  if (normalizedMime === "application/pdf" || ext === "pdf") return "pdf";
  if (ext === "doc" || ext === "docx" || normalizedMime.includes("wordprocessingml") || normalizedMime === "application/msword") return "word";
  if (ext === "xls" || ext === "xlsx" || ext === "csv" || normalizedMime.includes("spreadsheetml") || normalizedMime === "application/vnd.ms-excel" || normalizedMime === "text/csv") return "excel";
  if (normalizedMime.startsWith("text/") || normalizedMime === "application/json" || normalizedMime === "application/toml") return "text";
  return "unknown";
}

function positive(n: number | undefined): number {
  return typeof n === "number" && n > 0 ? n : 0;
}

export function effectiveLimit(policy: FilePolicyDTO, kind: FileKind): number {
  const global = positive(policy.maxUploadFileBytes);
  if (kind === "image") return positive(policy.effectiveImageMaxBytes) || positive(policy.imageMaxBytes) || global;
  if (kind === "audio" || kind === "video") return global;
  return positive(policy.effectiveDocMaxBytes) || positive(policy.docMaxBytes) || global;
}

export type FileRejection =
  | { reason: "type" }
  | { reason: "size"; limit: number }
  | { reason: "fullContextOnly"; limit: number };

export function checkFile(policy: FilePolicyDTO | null | undefined, file: { name: string; type: string; size: number }): FileRejection | null {
  if (!policy) return null;
  const mime = normalizeMime(file.name, file.type);
  const allowed = (policy.allowedMIMETypes ?? []).map((m) => m.trim().toLowerCase()).filter(Boolean);
  if (allowed.length > 0 && !allowed.includes(mime)) return { reason: "type" };
  const kind = inferKind(file.name, mime);
  const limit = effectiveLimit(policy, kind);
  if (limit > 0 && file.size > limit) {
    const fullOnly = policy.capabilityMode === "full_context_only" && kind !== "image" && kind !== "audio" && kind !== "video";
    return fullOnly ? { reason: "fullContextOnly", limit } : { reason: "size", limit };
  }
  return null;
}

export function messageFileCap(policy: FilePolicyDTO | null | undefined): number {
  return policy && policy.maxMessageFiles > 0 ? policy.maxMessageFiles : DEFAULT_MAX_MESSAGE_FILES;
}

/** Splits dropped files into the ones that fit the free slots and the overflow. */
export function fitToSlots<T>(files: readonly T[], freeSlots: number): { accepted: T[]; ignored: number } {
  const room = Math.max(0, freeSlots);
  return { accepted: files.slice(0, room), ignored: Math.max(0, files.length - room) };
}

export const PROCESSING_STATES = new Set(["uploaded", "queued", "extracting", "embedding"]);

export function isProcessing(status: string | undefined, embedStatus?: string): boolean {
  return PROCESSING_STATES.has((status ?? "").toLowerCase()) || ["queued", "processing"].includes((embedStatus ?? "").toLowerCase());
}

/** Temporary chat takes images and documents only. */
export function isAllowedInTemporary(file: { name: string; type: string }): boolean {
  const kind = inferKind(file.name, normalizeMime(file.name, file.type));
  return kind !== "audio" && kind !== "video";
}

export function pastedName(kind: "image" | "file", n: number, ext: string, now = Date.now()): string {
  const e = ext ? (ext.startsWith(".") ? ext : `.${ext}`) : "";
  return `pasted-${kind}-${now}-${n}${e}`;
}
