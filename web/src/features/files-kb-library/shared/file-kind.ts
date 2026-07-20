/**
 * Client-side classification of files: category (icons, local filtering),
 * preview kind (which viewer to use) and the readable-text check that guards
 * the text viewers.
 */

export const FILE_CATEGORIES = ["image", "pdf", "audio", "video", "spreadsheet", "presentation", "document", "code"] as const;
export type FileCategory = (typeof FILE_CATEGORIES)[number];

/** Filter keys offered in the list toolbar (OR semantics). */
export const FILTER_KEYS = ["image", "audio", "video", "document", "spreadsheet", "presentation", "pdf", "code"] as const;
export type FilterKey = (typeof FILTER_KEYS)[number];

export const SORT_KEYS = ["size", "name", "created", "last_used"] as const;
export type SortKey = (typeof SORT_KEYS)[number];

const IMAGE_EXT = new Set(["png", "jpg", "jpeg", "gif", "webp", "bmp", "heic", "heif"]);
const SHEET_EXT = new Set(["xls", "xlsx", "csv", "ods", "numbers"]);
const SLIDE_EXT = new Set(["ppt", "pptx", "odp", "key"]);
const DOC_EXT = new Set(["doc", "docx", "rtf", "odt", "pages"]);
const MARKDOWN_EXT = new Set(["md", "mdx", "markdown"]);
const CODE_EXT = new Set([
  "c", "cc", "cpp", "css", "go", "h", "html", "java", "js", "json", "jsx", "mjs", "py", "rb", "rs", "sh", "sql", "svg", "toml", "ts", "tsx", "xml", "yaml", "yml",
]);

/** Lower-case text after the last dot; empty when the name has no extension. */
export function extensionOf(fileName: string): string {
  const name = fileName.trim();
  const dot = name.lastIndexOf(".");
  if (dot < 0 || dot === name.length - 1) return "";
  return name.slice(dot + 1).toLowerCase();
}

function mimeHas(mime: string, ...needles: string[]): boolean {
  return needles.some((n) => mime.includes(n));
}

function isCodeMime(mime: string): boolean {
  return mime.startsWith("text/") || mimeHas(mime, "json", "javascript", "typescript", "xml", "html", "css", "yaml", "toml", "sql", "markdown");
}

export function categoryOf(fileName: string, mimeType: string): FileCategory {
  const ext = extensionOf(fileName);
  const mime = mimeType.trim().toLowerCase();
  if (mime.startsWith("image/") || IMAGE_EXT.has(ext)) return "image";
  if (mime === "application/pdf" || ext === "pdf") return "pdf";
  if (mime.startsWith("audio/")) return "audio";
  if (mime.startsWith("video/")) return "video";
  if (SHEET_EXT.has(ext) || mimeHas(mime, "spreadsheet", "excel", "csv")) return "spreadsheet";
  if (SLIDE_EXT.has(ext) || mimeHas(mime, "presentation", "powerpoint")) return "presentation";
  if (DOC_EXT.has(ext) || mimeHas(mime, "word", "rtf", "opendocument.text")) return "document";
  if (MARKDOWN_EXT.has(ext) || CODE_EXT.has(ext) || isCodeMime(mime)) return "code";
  return "document";
}

/** Does a freshly uploaded file belong in a list filtered by `filters`? Empty filters match everything. */
export function matchesFilters(file: { fileName: string; mimeType: string }, filters: readonly string[]): boolean {
  if (filters.length === 0) return true;
  return filters.includes(categoryOf(file.fileName, file.mimeType));
}

export function matchesQuery(file: { fileName: string }, query: string): boolean {
  const q = query.trim().toLowerCase();
  return q === "" || file.fileName.toLowerCase().includes(q);
}

export type PreviewKind =
  | "docx"
  | "spreadsheet"
  | "native"
  | "image"
  | "pdf"
  | "audio"
  | "video"
  | "markdown"
  | "code"
  | "text"
  | "unsupported";

const WORD_MIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document";

/**
 * Types a browser would run as a page when opened from a blob URL (which
 * inherits this app's origin). Such files are handed to "open" and "download"
 * as plain text so a hostile upload can never execute here.
 */
export function isActiveContentType(contentType: string | null | undefined): boolean {
  const mime = (contentType ?? "").split(";")[0]?.trim().toLowerCase() ?? "";
  return (
    mime === "text/html" ||
    mime === "application/xhtml+xml" ||
    mime === "image/svg+xml" ||
    mime.endsWith("/xml") ||
    mime.endsWith("+xml") ||
    mime.includes("javascript") ||
    mime.includes("ecmascript")
  );
}

/** Header value without parameters ("text/plain; charset=utf-8" -> "text/plain"). */
export function cleanContentType(value: string | null | undefined): string {
  return (value ?? "").split(";")[0]?.trim().toLowerCase() ?? "";
}

export function previewKindOf(input: { fileName: string; contentType: string; fallbackMime?: string }): PreviewKind {
  const ext = extensionOf(input.fileName);
  const mime = cleanContentType(input.contentType) || cleanContentType(input.fallbackMime);
  if (ext === "docx" || mime === WORD_MIME) return "docx";
  if (ext === "csv" || mime === "text/csv") return "spreadsheet";
  if (
    ["doc", "xls", "xlsx", "ppt", "pptx", "odt", "ods", "odp", "rtf", "pages", "numbers", "key"].includes(ext) ||
    mimeHas(mime, "msword", "ms-excel", "ms-powerpoint", "officedocument", "opendocument", "application/rtf", "text/rtf")
  ) {
    return "native";
  }
  const category = categoryOf(input.fileName, mime);
  if (category === "image") return "image";
  if (category === "pdf") return "pdf";
  if (category === "audio") return "audio";
  if (category === "video") return "video";
  if (MARKDOWN_EXT.has(ext) || mime.includes("markdown")) return "markdown";
  if (CODE_EXT.has(ext) || isCodeMime(mime) && !mime.startsWith("text/plain")) return "code";
  if (mime.startsWith("text/") || ["txt", "log", "csv"].includes(ext)) return "text";
  return "unsupported";
}

/**
 * Looks at the first 4000 characters: text is readable when replacement
 * characters stay under 8% and control characters under 2%.
 */
export function isReadableText(text: string): boolean {
  const sample = text.slice(0, 4000);
  if (sample.length === 0) return true;
  let replacement = 0;
  let control = 0;
  for (let i = 0; i < sample.length; i += 1) {
    const code = sample.charCodeAt(i);
    if (code === 0xfffd) replacement += 1;
    else if ((code < 32 && code !== 9 && code !== 10 && code !== 13) || code === 127) control += 1;
  }
  return replacement / sample.length < 0.08 && control / sample.length < 0.02;
}

/** Decides the final viewer once the bytes are known (pure; the readability rules live here). */
export function settleKind(kind: PreviewKind, sample: string): PreviewKind {
  const textual = kind === "markdown" || kind === "code" || kind === "text" || kind === "spreadsheet";
  if (textual) return isReadableText(sample) ? kind : "unsupported";
  if (kind === "unsupported") return isReadableText(sample) && sample.length > 0 ? "text" : "unsupported";
  return kind;
}

const READY_LIKE = new Set(["active", "ready", "available", "success"]);

export function isReadyLikeStatus(status: string | null | undefined): boolean {
  return READY_LIKE.has((status ?? "").trim().toLowerCase());
}

/** The label shown as "type": MIME unless blank/octet-stream, else extension, else "". */
export function rawTypeOf(file: { mimeType: string; fileName: string }): string {
  const mime = file.mimeType.trim();
  if (mime && mime.toLowerCase() !== "application/octet-stream") return mime;
  return extensionOf(file.fileName);
}
