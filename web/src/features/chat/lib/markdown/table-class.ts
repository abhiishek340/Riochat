/**
 * Column classification for Markdown tables so each column gets a sensible
 * width and alignment: compact ids and flags, right-aligned numbers, dates,
 * ordinary text, long prose and code-like values.
 */

export type ColumnKind = "compact" | "numeric" | "date" | "normal" | "content" | "code";

/** Wide characters (CJK, emoji) count as two. */
export function visualLength(text: string): number {
  let n = 0;
  for (const ch of text) {
    const cp = ch.codePointAt(0) as number;
    n += (cp >= 0x1100 && cp <= 0x115f) || (cp >= 0x2e80 && cp <= 0xa4cf) || (cp >= 0xac00 && cp <= 0xd7a3) || (cp >= 0xf900 && cp <= 0xfaff) || (cp >= 0xfe30 && cp <= 0xfe6f) || (cp >= 0xff00 && cp <= 0xff60) || (cp >= 0xffe0 && cp <= 0xffe6) || cp >= 0x1f300 ? 2 : 1;
  }
  return n;
}

const NUMERIC = /^[+-]?[$€£¥]?\s?(?:\d{1,3}(?:[,\s]\d{3})+|\d+)(?:\.\d+)?\s?(?:%|‰|[A-Za-z]{1,3})?$/;
const DATE = /^(?:\d{4}[-/.]\d{1,2}[-/.]\d{1,2}(?:[T\s]\d{1,2}:\d{2}(?::\d{2})?(?:Z|[+-]\d{2}:?\d{2})?)?|\d{1,2}[-/.]\d{1,2}[-/.]\d{2,4}|\d{1,2}:\d{2}(?::\d{2})?|[A-Z][a-z]{2,8}\.? \d{1,2},? \d{4})$/;
const URL_LIKE = /^(?:https?:\/\/|www\.|\/[\w./-]+|~\/|\.{1,2}\/|[\w.-]+\/[\w./-]+$|[0-9a-f]{7,64}$|[A-Za-z]:\\|\$ |--?[A-Za-z][\w-]*\b)/;
const IDENTIFIER = /^[A-Za-z0-9_.:/#@-]{22,}$/;
const BOOLEAN = /^(?:true|false|yes|no|y|n|on|off|✓|✗|✔|✘|√|×)$/i;
const SENTENCE = /[.!?\u3002\uff01\uff1f;\uff1b]\s|[.!?\u3002\uff01\uff1f]$|[,\uff0c\u3001]\s?\S/;

const HEADER_HINTS: { kind: ColumnKind; re: RegExp }[] = [
  { kind: "compact", re: /^(?:id|no\.?|#|number|index|idx|rank)$/i },
  { kind: "numeric", re: /(?:amount|price|cost|rate|ratio|percent|total|count|qty|quantity|score)/i },
  { kind: "date", re: /(?:date|time|created|updated|deadline)/i },
  { kind: "content", re: /(?:description|detail|reason|note|advice|summary|comment|remark)/i },
  { kind: "code", re: /(?:url|link|path|file|hash|command|code|cmd|sha|uri)/i },
];

const RANK: Record<ColumnKind, number> = { compact: 0, numeric: 1, date: 2, normal: 3, code: 4, content: 5 };

/** Widest kind wins, so a column never narrows while a table streams in. */
export function widen(a: ColumnKind, b: ColumnKind): ColumnKind {
  return RANK[b] > RANK[a] ? b : a;
}

function hintOf(header: string): ColumnKind | null {
  const h = header.trim();
  for (const { kind, re } of HEADER_HINTS) if (re.test(h)) return kind;
  return null;
}

const share = (cells: string[], test: (c: string) => boolean) => (cells.length === 0 ? 0 : cells.filter(test).length / cells.length);

export function classifyColumn(header: string, values: readonly string[]): ColumnKind {
  const cells = values.map((v) => v.trim()).filter((v) => v !== "");
  if (cells.length === 0) return "normal";
  const hint = hintOf(header);
  const bonus = (kind: ColumnKind) => (hint === kind ? 0.2 : 0);
  const lens = cells.map(visualLength);
  const avg = lens.reduce((a, b) => a + b, 0) / cells.length;
  const max = Math.max(...lens);

  const codeShare = share(cells, (c) => URL_LIKE.test(c) || /^`.*`$/.test(c));
  const identShare = share(cells, (c) => IDENTIFIER.test(c));
  if (codeShare + bonus("code") >= 0.45 || (identShare >= 0.34 && max >= 22)) return "code";

  const outliers = (test: (c: string) => boolean) => cells.filter((c) => !test(c)).every((c) => visualLength(c) <= 16);

  if (share(cells, (c) => DATE.test(c)) + bonus("date") >= 0.6 && outliers((c) => DATE.test(c))) return "date";
  if (share(cells, (c) => NUMERIC.test(c)) + bonus("numeric") >= 0.72 && outliers((c) => NUMERIC.test(c))) return "numeric";

  const sentences = share(cells, (c) => SENTENCE.test(c) || visualLength(c) > 30);
  if (avg >= 38 || (avg >= 26 && sentences + bonus("content") >= 0.34) || (max >= 44 && sentences + bonus("content") >= 0.34)) return "content";

  const compact = share(cells, (c) => visualLength(c) <= 12 || BOOLEAN.test(c));
  const distinct = new Set(cells).size;
  if (avg <= 5 && max <= 12 && (compact + bonus("compact") >= 0.75 || (avg <= 3 && distinct <= 5))) return "compact";
  return "normal";
}

export interface ColumnState {
  kinds: ColumnKind[];
}

/** Classifies every column; with `previous`, kinds only widen. Streaming tables wait for three rows. */
export function classifyTable(header: readonly string[], rows: readonly (readonly string[])[], previous?: readonly ColumnKind[], streaming = false): ColumnKind[] {
  if (streaming && rows.length < 3) return previous ? [...previous] : header.map(() => "normal" as ColumnKind);
  return header.map((h, i) => {
    const kind = classifyColumn(h, rows.map((r) => r[i] ?? ""));
    const before = previous?.[i];
    return before ? widen(before, kind) : kind;
  });
}

/** Width limits per kind, in rem. */
export function columnWidth(kind: ColumnKind, contentColumns: number, narrow = false): { min: number; max: number } {
  switch (kind) {
    case "compact":
      return { min: 3, max: 6 };
    case "numeric":
      return { min: 4.5, max: 9 };
    case "date":
      return { min: 6.75, max: 10 };
    case "code":
      return { min: 12.5, max: 26 };
    case "content": {
      if (narrow) return contentColumns === 2 ? { min: 14, max: 14 } : contentColumns >= 3 ? { min: 11.5, max: 11.5 } : { min: 16, max: 30 };
      if (contentColumns === 2) return { min: 14.5, max: 30 };
      if (contentColumns >= 3) return { min: 12, max: 30 };
      return { min: 17, max: 30 };
    }
    default:
      return { min: 6.25, max: 13.75 };
  }
}

export function toCsv(header: readonly string[], rows: readonly (readonly string[])[]): string {
  const q = (v: string) => (/[",\n\r]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v);
  return "﻿" + [header, ...rows].map((r) => r.map(q).join(",")).join("\r\n");
}

export function toMarkdownTable(header: readonly string[], rows: readonly (readonly string[])[]): string {
  const esc = (v: string) => v.replace(/\\/g, "\\\\").replace(/\|/g, "\\|").replace(/\n/g, " ");
  const line = (r: readonly string[]) => `| ${r.map(esc).join(" | ")} |`;
  return [line(header), `| ${header.map(() => "---").join(" | ")} |`, ...rows.map(line)].join("\n");
}
