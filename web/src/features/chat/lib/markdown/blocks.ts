/**
 * Block-level Markdown. Line based, tolerant of input that is still arriving:
 * an unclosed fence is reported as open, a table keeps growing row by row.
 */
import { inlineText, type Align, type Block, type Inline, type ListItem, type TableCell } from "./ast";
import { parseInline } from "./inline";

export interface ParseOptions {
  streaming?: boolean;
}

interface Ctx {
  streaming: boolean;
  footnotes: string[];
  footnoteDefs: Map<string, string[]>;
  headingIds: Map<string, number>;
}

const FENCE = /^( {0,3})(`{3,}|~{3,})\s*(.*)$/;
const ATX = /^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$/;
const HR = /^ {0,3}([-*_])(?:[ \t]*\1){2,}[ \t]*$/;
const BULLET = /^( {0,3})([-+*])([ \t]+|$)/;
const ORDERED = /^( {0,3})(\d{1,9})([.)])([ \t]+|$)/;
const QUOTE = /^ {0,3}>/;
const TABLE_DELIM = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/;
const FOOTNOTE_DEF = /^ {0,3}\[\^([^\]\s]+)\]:\s?(.*)$/;
const SETEXT = /^ {0,3}=+\s*$/;

function leadingSpaces(line: string): number {
  let n = 0;
  for (const c of line) {
    if (c === " ") n++;
    else if (c === "\t") n += 4 - (n % 4);
    else break;
  }
  return n;
}

function stripIndent(line: string, count: number): string {
  let removed = 0;
  let i = 0;
  while (i < line.length && removed < count) {
    const c = line[i];
    if (c === " ") removed++;
    else if (c === "\t") removed += 4 - (removed % 4);
    else break;
    i++;
  }
  return line.slice(i);
}

function isBlank(line: string): boolean {
  return line.trim() === "";
}

export function slugify(text: string): string {
  const s = text
    .trim()
    .toLowerCase()
    .replace(/[^\p{L}\p{N}\s-]/gu, "")
    .replace(/\s+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "");
  return s || "section";
}

function splitRow(line: string): string[] {
  let s = line.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|") && !s.endsWith("\\|")) s = s.slice(0, -1);
  const cells: string[] = [];
  let cur = "";
  let inCode = false;
  for (let i = 0; i < s.length; i++) {
    const c = s[i] as string;
    if (c === "\\" && s[i + 1] === "|") {
      cur += "|";
      i++;
      continue;
    }
    if (c === "`") inCode = !inCode;
    if (c === "|" && !inCode) {
      cells.push(cur.trim());
      cur = "";
      continue;
    }
    cur += c;
  }
  cells.push(cur.trim());
  return cells;
}

function alignOf(cell: string): Align {
  const t = cell.trim();
  const l = t.startsWith(":");
  const r = t.endsWith(":");
  return l && r ? "center" : r ? "right" : l ? "left" : null;
}

function startsBlock(line: string): boolean {
  return FENCE.test(line) || ATX.test(line) || HR.test(line) || QUOTE.test(line) || BULLET.test(line) || /^ {0,3}1[.)](\s|$)/.test(line) || /^\s*\$\$/.test(line);
}

function cellOf(raw: string, ctx: Ctx): TableCell {
  const children = parseInline(raw, { streaming: false, footnotes: ctx.footnotes });
  return { children, text: inlineText(children) };
}

export function parseBlocks(source: string, options: ParseOptions = {}): Block[] {
  const ctx: Ctx = { streaming: !!options.streaming, footnotes: [], footnoteDefs: new Map(), headingIds: new Map() };
  const lines = source.replace(/\r\n?/g, "\n").split("\n");
  const blocks = parseLines(lines, 0, ctx, true);
  if (ctx.footnoteDefs.size > 0 && ctx.footnotes.length > 0) {
    const items = ctx.footnotes
      .filter((id) => ctx.footnoteDefs.has(id))
      .map((id) => ({
        id,
        index: ctx.footnotes.indexOf(id) + 1,
        children: parseLines(ctx.footnoteDefs.get(id) as string[], 0, { ...ctx, footnoteDefs: new Map() }, false),
      }));
    if (items.length > 0) blocks.push({ type: "footnotes", items, line: lines.length, raw: "" });
  }
  return blocks;
}
