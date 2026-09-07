/**
 * Code artifacts: previewable HTML, CSS, JavaScript and SVG found in an
 * assistant reply, with identities that survive the switch from the optimistic
 * message to the persisted one.
 */
import { scanFences } from "./fences";

export type ArtifactKind = "html" | "css" | "javascript" | "svg";

export interface Artifact {
  /** Owning run (else message) plus the position among the message's artifacts. */
  id: string;
  owner: string;
  index: number;
  kind: ArtifactKind;
  code: string;
  complete: boolean;
  streaming: boolean;
}

const NEUTRAL = new Set(["", "text", "txt", "plaintext", "plain", "markdown"]);
const XML_LANGS = new Set(["xml", "text/xml", "application/xml"]);

export function kindOfLanguage(lang: string): ArtifactKind | null {
  switch (lang) {
    case "html":
    case "htm":
    case "xhtml":
      return "html";
    case "css":
    case "scss":
    case "sass":
    case "less":
      return "css";
    case "js":
    case "javascript":
    case "mjs":
    case "cjs":
      return "javascript";
    case "svg":
    case "svg+xml":
    case "image/svg+xml":
      return "svg";
    default:
      return null;
  }
}

/** An SVG document root after optional whitespace, comments, declarations and a DOCTYPE. */
export function looksLikeSvg(code: string): boolean {
  let s = code.replace(/^﻿/, "");
  for (;;) {
    s = s.replace(/^\s+/, "");
    if (s.startsWith("<!--")) {
      const end = s.indexOf("-->");
      if (end < 0) return false;
      s = s.slice(end + 3);
    } else if (s.startsWith("<?")) {
      const end = s.indexOf("?>");
      if (end < 0) return false;
      s = s.slice(end + 2);
    } else if (/^<!doctype\s+svg/i.test(s)) {
      const end = s.indexOf(">");
      if (end < 0) return false;
      s = s.slice(end + 1);
    } else break;
  }
  return /^<(?:[A-Za-z][\w-]*:)?svg[\s>/]/i.test(s);
}

export function looksLikeHtml(code: string): boolean {
  const s = code.replace(/^﻿/, "").trimStart().toLowerCase();
  return /^<!doctype html/.test(s) || /^<(?:html|head|body)[\s>]/.test(s) || /^<(?:article|canvas|div|main|section|style|script)[\s>]/.test(s);
}

/** Preview kind of a block, sniffing the content for neutral or XML languages. */
export function detectKind(lang: string, code: string): ArtifactKind | null {
  const direct = kindOfLanguage(lang);
  if (direct) return direct;
  if (NEUTRAL.has(lang)) {
    if (looksLikeSvg(code)) return "svg";
    return looksLikeHtml(code) ? "html" : null;
  }
  if (XML_LANGS.has(lang) && looksLikeSvg(code)) return "svg";
  return null;
}

export interface ExtractOptions {
  /** Run id of the reply, else its message id. */
  owner: string;
  streaming: boolean;
}

export function extractArtifacts(content: string, options: ExtractOptions): Artifact[] {
  const fences = scanFences(content);
  const out: Artifact[] = [];
  for (const f of fences) {
    if (!f.closed && !options.streaming) continue;
    if (f.body.trim() === "") continue;
    const kind = detectKind(f.lang, f.body);
    if (!kind) continue;
    const index = out.length;
    out.push({ id: `${options.owner}:${index}`, owner: options.owner, index, kind, code: f.body, complete: f.closed, streaming: options.streaming && !f.closed });
  }
  if (fences.length === 0) {
    const kind = detectKind("", content);
    if ((kind === "html" || kind === "svg") && content.trim() !== "") {
      out.push({ id: `${options.owner}:0`, owner: options.owner, index: 0, kind, code: content, complete: !options.streaming, streaming: options.streaming });
    }
  }
  return out;
}

/** Index of the artifact an explicit "open preview" refers to: same kind and code, else same kind, else the last. */
export function pickArtifact(list: readonly Artifact[], kind: ArtifactKind, code: string): number {
  if (list.length === 0) return -1;
  const same = list.findIndex((a) => a.kind === kind && a.code === code);
  if (same >= 0) return same;
  const sameKind = list.findIndex((a) => a.kind === kind);
  return sameKind >= 0 ? sameKind : list.length - 1;
}

export interface ArtifactRef {
  id: string;
  owner: string;
  kind: ArtifactKind;
  index: number;
  code: string;
  streaming: boolean;
  messageId?: string;
}

/**
 * Finds the artifact that replaces a previously selected one after ids changed:
 * same id; same kind+index and run; same kind+index and message; same
 * kind+index with related code (equal or one a prefix of the other); or, for a
 * streaming selection, the latest of that kind+index.
 */
export function replacementFor(previous: ArtifactRef, candidates: readonly (Artifact & { messageId?: string })[]): Artifact | null {
  const exact = candidates.find((c) => c.id === previous.id);
  if (exact) return exact;
  const sameSlot = candidates.filter((c) => c.kind === previous.kind && c.index === previous.index);
  const byOwner = sameSlot.find((c) => c.owner === previous.owner);
  if (byOwner) return byOwner;
  const byMessage = previous.messageId ? sameSlot.find((c) => c.messageId === previous.messageId) : undefined;
  if (byMessage) return byMessage;
  const related = sameSlot.find((c) => c.code === previous.code || c.code.startsWith(previous.code) || previous.code.startsWith(c.code));
  if (related) return related;
  if (previous.streaming && sameSlot.length > 0) return sameSlot[sameSlot.length - 1] as Artifact;
  return null;
}

export function artifactLabel(kind: ArtifactKind, position: number): string {
  const name = kind === "javascript" ? "JS" : kind.toUpperCase();
  return `${name} #${position}`;
}

export function downloadName(kind: ArtifactKind): string {
  switch (kind) {
    case "html":
      return "artifact-preview.html";
    case "css":
      return "artifact-css-preview.html";
    case "javascript":
      return "artifact-js-preview.html";
    case "svg":
      return "artifact.svg";
  }
}
