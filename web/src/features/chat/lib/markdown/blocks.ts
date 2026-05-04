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

function parseLines(lines: string[], base: number, ctx: Ctx, top: boolean): Block[] {
  const out: Block[] = [];
  let i = 0;
  const n = lines.length;

  const raw = (from: number, to: number) => lines.slice(from, to).join("\n");

  while (i < n) {
    const line = lines[i] as string;
    if (isBlank(line)) {
      i++;
      continue;
    }

    // fenced code
    const fence = FENCE.exec(line);
    if (fence && !(fence[2]![0] === "`" && (fence[3] ?? "").includes("`"))) {
      const indent = (fence[1] ?? "").length;
      const marker = fence[2] as string;
      const info = (fence[3] ?? "").trim();
      const start = i;
      const body: string[] = [];
      i++;
      let closed = false;
      while (i < n) {
        const l = lines[i] as string;
        const close = /^ {0,3}(`{3,}|~{3,})\s*$/.exec(l);
        if (close && (close[1] as string)[0] === marker[0] && (close[1] as string).length >= marker.length) {
          closed = true;
          i++;
          break;
        }
        body.push(stripIndent(l, indent));
        i++;
      }
      const lang = (info.split(/\s+/)[0] ?? "").replace(/^\{?\.?/, "").replace(/\}$/, "").toLowerCase();
      out.push({ type: "code", lang, info, code: body.join("\n"), closed, line: base + start, raw: raw(start, i) });
      continue;
    }

    // display math
    if (/^\s*\$\$/.test(line)) {
      const start = i;
      const first = line.trim().slice(2);
      if (first.includes("$$")) {
        const tex = first.slice(0, first.indexOf("$$"));
        out.push({ type: "math", tex: tex.trim(), closed: true, line: base + start, raw: line });
        i++;
        continue;
      }
      const body: string[] = first.trim() ? [first] : [];
      i++;
      let closed = false;
      while (i < n) {
        const l = lines[i] as string;
        const at = l.indexOf("$$");
        if (at >= 0) {
          if (l.slice(0, at).trim()) body.push(l.slice(0, at));
          closed = true;
          i++;
          break;
        }
        body.push(l);
        i++;
      }
      out.push({ type: "math", tex: body.join("\n").trim(), closed, line: base + start, raw: raw(start, i) });
      continue;
    }

    // ATX heading
    const atx = ATX.exec(line);
    if (atx && (atx[2] !== undefined || /^ {0,3}#{1,6}[ \t]*$/.test(line))) {
      const text = (atx[2] ?? "").trim();
      const children = parseInline(text, { streaming: false, footnotes: ctx.footnotes });
      const plain = inlineText(children);
      const slug = slugify(plain);
      const seen = ctx.headingIds.get(slug) ?? 0;
      ctx.headingIds.set(slug, seen + 1);
      out.push({ type: "heading", level: (atx[1] as string).length, children, id: seen ? `${slug}-${seen}` : slug, text: plain, line: base + i, raw: line });
      i++;
      continue;
    }

    // thematic break
    if (HR.test(line)) {
      out.push({ type: "hr", line: base + i, raw: line });
      i++;
      continue;
    }

    // footnote definition
    const fn = top ? FOOTNOTE_DEF.exec(line) : null;
    if (fn) {
      const id = fn[1] as string;
      const body = [fn[2] ?? ""];
      i++;
      while (i < n) {
        const l = lines[i] as string;
        if (isBlank(l)) {
          const next = lines[i + 1];
          if (next !== undefined && leadingSpaces(next) >= 2 && !isBlank(next)) {
            body.push("");
            i++;
            continue;
          }
          break;
        }
        if (leadingSpaces(l) >= 2) {
          body.push(stripIndent(l, 2));
          i++;
          continue;
        }
        if (!startsBlock(l) && !FOOTNOTE_DEF.test(l)) {
          body.push(l);
          i++;
          continue;
        }
        break;
      }
      ctx.footnoteDefs.set(id, body);
      continue;
    }

    // block quote
    if (QUOTE.test(line)) {
      const start = i;
      const inner: string[] = [];
      while (i < n) {
        const l = lines[i] as string;
        if (QUOTE.test(l)) {
          inner.push(l.replace(/^ {0,3}> ?/, ""));
          i++;
        } else if (!isBlank(l) && inner.length > 0 && !isBlank(inner[inner.length - 1] as string) && !startsBlock(l)) {
          inner.push(l); // lazy continuation
          i++;
        } else break;
      }
      out.push({ type: "quote", children: parseLines(inner, base + start, ctx, false), line: base + start, raw: raw(start, i) });
      continue;
    }

    // list
    const bullet = BULLET.exec(line);
    const ordered = ORDERED.exec(line);
    if (bullet || ordered) {
      const start = i;
      const isOrdered = !!ordered;
      const marker = bullet ? (bullet[2] as string) : (ordered![3] as string);
      const firstNumber = ordered ? Number(ordered[2]) : 1;
      const items: ListItem[] = [];
      let tight = true;
      while (i < n) {
        const l = lines[i] as string;
        const bm = BULLET.exec(l);
        const om = ORDERED.exec(l);
        const m = isOrdered ? om : bm;
        if (!m) break;
        if ((isOrdered ? (om![3] as string) : (bm![2] as string)) !== marker) break;
        if (HR.test(l)) break;
        const markerWidth = m[0].length;
        const indent = (m[1] ?? "").length;
        const afterMarker = l.slice(markerWidth);
        const gap = (m[isOrdered ? 4 : 3] ?? "").length;
        const contentIndent = gap > 4 || afterMarker.trim() === "" ? indent + (isOrdered ? (m[2] as string).length + 1 : 1) + 1 : markerWidth;
        const bodyLines: string[] = [afterMarker];
        i++;
        let sawBlank = false;
        while (i < n) {
          const nl = lines[i] as string;
          if (isBlank(nl)) {
            bodyLines.push("");
            sawBlank = true;
            i++;
            continue;
          }
          const ind = leadingSpaces(nl);
          if (ind >= contentIndent) {
            bodyLines.push(stripIndent(nl, contentIndent));
            sawBlank = false;
            i++;
            continue;
          }
          if (sawBlank) break;
          if (startsBlock(nl) || ORDERED.test(nl)) break;
          bodyLines.push(nl.trim());
          i++;
        }
        while (bodyLines.length && isBlank(bodyLines[bodyLines.length - 1] as string)) bodyLines.pop();
        // A blank line between items makes the list loose.
        const next = lines[i];
        if (sawBlank && next !== undefined && (isOrdered ? ORDERED.test(next) : BULLET.test(next))) tight = false;
        if (bodyLines.some((b, idx) => b === "" && idx > 0 && idx < bodyLines.length - 1)) {
          // blank lines inside the item between blocks also loosen it
          const blocksInside = bodyLines.join("\n").split(/\n\s*\n/).length > 1;
          if (blocksInside) tight = false;
        }
        let checked: boolean | null = null;
        const task = /^\[([ xX])\]\s+/.exec(bodyLines[0] as string);
        if (task) {
          checked = (task[1] as string).toLowerCase() === "x";
          bodyLines[0] = (bodyLines[0] as string).slice(task[0].length);
        }
        items.push({ checked, children: parseLines(bodyLines, base + start, ctx, false) });
      }
      out.push({ type: "list", ordered: isOrdered, start: firstNumber, tight, items, line: base + start, raw: raw(start, i) });
      continue;
    }

    // table
    if (line.includes("|") && i + 1 < n && TABLE_DELIM.test(lines[i + 1] as string) && (lines[i + 1] as string).includes("-")) {
      const head = splitRow(line);
      const delim = splitRow(lines[i + 1] as string);
      if (head.length === delim.length) {
        const start = i;
        i += 2;
        const rows: TableCell[][] = [];
        while (i < n) {
          const l = lines[i] as string;
          if (isBlank(l) || startsBlock(l)) break;
          if (!l.includes("|") && !ctx.streaming) break;
          if (!l.includes("|")) break;
          const cells = splitRow(l);
          rows.push(Array.from({ length: head.length }, (_, k) => cellOf(cells[k] ?? "", ctx)));
          i++;
        }
        out.push({
          type: "table",
          header: head.map((h) => cellOf(h, ctx)),
          align: delim.map(alignOf),
          rows,
          line: base + start,
          raw: raw(start, i),
          complete: !(ctx.streaming && i >= n),
        });
        continue;
      }
    }

    // indented code
    if (leadingSpaces(line) >= 4 && top) {
      const start = i;
      const body: string[] = [];
      while (i < n && (leadingSpaces(lines[i] as string) >= 4 || isBlank(lines[i] as string))) {
        body.push(stripIndent(lines[i] as string, 4));
        i++;
      }
      while (body.length && isBlank(body[body.length - 1] as string)) body.pop();
      out.push({ type: "code", lang: "", info: "", code: body.join("\n"), closed: true, line: base + start, raw: raw(start, i) });
      continue;
    }

    // paragraph (with setext headings)
    const start = i;
    const para: string[] = [line];
    i++;
    let setext: number | null = null;
    while (i < n) {
      const l = lines[i] as string;
      if (isBlank(l)) break;
      if (SETEXT.test(l)) {
        setext = 1;
        i++;
        break;
      }
      if (startsBlock(l)) break;
      if (l.includes("|") && i + 1 < n && TABLE_DELIM.test(lines[i + 1] as string) && (lines[i + 1] as string).includes("-")) break;
      para.push(l);
      i++;
    }
    const text = para.map((l, k) => (k === para.length - 1 ? l.trimEnd() : l)).join("\n").trim();
    const streaming = ctx.streaming && i >= n;
    if (setext) {
      const children = parseInline(text, { streaming: false, footnotes: ctx.footnotes });
      const plain = inlineText(children);
      const slug = slugify(plain);
      const seen = ctx.headingIds.get(slug) ?? 0;
      ctx.headingIds.set(slug, seen + 1);
      out.push({ type: "heading", level: 1, children, id: seen ? `${slug}-${seen}` : slug, text: plain, line: base + start, raw: raw(start, i) });
    } else {
      out.push({ type: "paragraph", children: parseInline(text, { streaming, footnotes: ctx.footnotes }), line: base + start, raw: raw(start, i) });
    }
  }
  return out;
}

export type { Inline };
