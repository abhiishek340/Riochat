/**
 * A small, dependency-free Markdown renderer for read-only snapshots
 * (shared chats, announcements, search previews). It builds React elements
 * only - never raw HTML - and refuses URLs outside http(s)/mailto/same-origin.
 *
 * Supported: headings, paragraphs, fenced code, block quotes, bullet and
 * numbered lists, tables, rules, and inline code, bold, italic, strike-through,
 * links, images and bare http(s) links.
 */
import { Fragment, type ReactNode } from "react";
import { absolutizeApiPath } from "@/lib/api/base-url";
import { cn } from "@/lib/utils/cn";

export type Block =
  | { type: "heading"; level: number; text: string }
  | { type: "paragraph"; text: string }
  | { type: "code"; lang: string; text: string }
  | { type: "quote"; blocks: Block[] }
  | { type: "list"; ordered: boolean; items: string[] }
  | { type: "table"; head: string[]; rows: string[][] }
  | { type: "rule" };

const FENCE = /^\s{0,3}(`{3,}|~{3,})\s*([\w+#.-]*)\s*$/;
const HEADING = /^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$/;
const RULE = /^\s{0,3}([-*_])(?:\s*\1){2,}\s*$/;
const BULLET = /^\s{0,3}[-*+]\s+(.*)$/;
const NUMBERED = /^\s{0,3}\d{1,9}[.)]\s+(.*)$/;
const QUOTE = /^\s{0,3}>\s?(.*)$/;
const TABLE_SEP = /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/;

function splitRow(line: string): string[] {
  let body = line.trim();
  if (body.startsWith("|")) body = body.slice(1);
  if (body.endsWith("|")) body = body.slice(0, -1);
  return body.split("|").map((c) => c.trim());
}

export function parseBlocks(source: string): Block[] {
  const lines = source.replace(/\r\n?/g, "\n").split("\n");
  const blocks: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i] ?? "";
    if (!line.trim()) {
      i++;
      continue;
    }
    const fence = FENCE.exec(line);
    if (fence) {
      const marker = fence[1] ?? "```";
      const body: string[] = [];
      i++;
      while (i < lines.length && !(lines[i] ?? "").trim().startsWith(marker)) body.push(lines[i++] ?? "");
      i++; // closing fence (an unterminated block runs to the end)
      blocks.push({ type: "code", lang: fence[2] ?? "", text: body.join("\n") });
      continue;
    }
    const heading = HEADING.exec(line);
    if (heading) {
      blocks.push({ type: "heading", level: (heading[1] ?? "#").length, text: heading[2] ?? "" });
      i++;
      continue;
    }
    if (RULE.test(line)) {
      blocks.push({ type: "rule" });
      i++;
      continue;
    }
    if (QUOTE.test(line)) {
      const inner: string[] = [];
      while (i < lines.length && QUOTE.test(lines[i] ?? "")) inner.push(QUOTE.exec(lines[i++] ?? "")?.[1] ?? "");
      blocks.push({ type: "quote", blocks: parseBlocks(inner.join("\n")) });
      continue;
    }
    const bullet = BULLET.test(line);
    if (bullet || NUMBERED.test(line)) {
      const pattern = bullet ? BULLET : NUMBERED;
      const items: string[] = [];
      while (i < lines.length && pattern.test(lines[i] ?? "")) items.push(pattern.exec(lines[i++] ?? "")?.[1] ?? "");
      blocks.push({ type: "list", ordered: !bullet, items });
      continue;
    }
    if (line.includes("|") && TABLE_SEP.test(lines[i + 1] ?? "")) {
      const head = splitRow(line);
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && (lines[i] ?? "").includes("|") && (lines[i] ?? "").trim()) rows.push(splitRow(lines[i++] ?? ""));
      blocks.push({ type: "table", head, rows });
      continue;
    }
    const para: string[] = [];
    while (i < lines.length) {
      const next = lines[i] ?? "";
      if (!next.trim() || FENCE.test(next) || HEADING.test(next) || QUOTE.test(next) || BULLET.test(next) || NUMBERED.test(next) || RULE.test(next)) break;
      para.push(next);
      i++;
    }
    if (para.length === 0) {
      para.push(line);
      i++;
    }
    blocks.push({ type: "paragraph", text: para.join("\n") });
  }
  return blocks;
}

export type Inline =
  | { type: "text"; text: string }
  | { type: "code"; text: string }
  | { type: "strong"; children: Inline[] }
  | { type: "em"; children: Inline[] }
  | { type: "del"; children: Inline[] }
  | { type: "link"; href: string; children: Inline[] }
  | { type: "image"; src: string; alt: string }
  | { type: "br" };

/** http(s), mailto and same-origin paths only; everything else (javascript:, data:, //host) is dropped. */
export function safeUrl(raw: string): string {
  const url = raw.trim();
  // Browsers ignore tabs and line breaks inside a URL, so "/<tab>/host" would become "//host".
  if (/[\u0000-\u001f\u007f]/.test(url)) return "";
  if (/^(https?:\/\/|mailto:)/i.test(url)) return url;
  if (url.startsWith("/") && !url.startsWith("//") && !url.includes("\\")) return url;
  if (url.startsWith("#")) return url;
  return "";
}

const INLINE = /(`+)([^`]|[^`][\s\S]*?[^`])\1(?!`)|!\[([^\]]*)\]\(([^()\s]*(?:\([^()\s]*\)[^()\s]*)*)(?:\s+"[^"]*")?\)|\[([^\]]+)\]\(([^()\s]*(?:\([^()\s]*\)[^()\s]*)*)(?:\s+"[^"]*")?\)|\*\*([^*]+)\*\*|__([^_]+)__|~~([^~]+)~~|\*([^*\s][^*]*)\*|(?<![\w])_([^_\s][^_]*)_(?![\w])|(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])|\n/g;
