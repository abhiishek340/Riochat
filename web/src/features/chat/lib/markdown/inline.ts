/**
 * Inline Markdown: emphasis (with CJK-friendly flanking), code spans, links,
 * images, autolinks, strikethrough, math and footnote references. When the text
 * is still streaming, openers that have not closed yet are closed for display.
 */
import type { Inline } from "./ast";

export interface InlineOptions {
  streaming?: boolean;
  /** Footnote ids in order of first use. */
  footnotes?: string[];
}

const PUNCT = /[!-/:-@[-`{-~ -⁯\u3000-\u303f\uff00-\uff0f\uff1a-\uff20\uff3b-\uff40\uff5b-\uff65¡-¿]/;
const CJK = /[\u2e80-\u9fff\uf900-\ufaff\uac00-\ud7af\u3040-\u30ff]/;
const WS = /\s/;

function isWs(c: string | undefined): boolean {
  return c === undefined || WS.test(c);
}
function isPunct(c: string | undefined): boolean {
  return c !== undefined && PUNCT.test(c);
}
function isCjk(c: string | undefined): boolean {
  return c !== undefined && CJK.test(c);
}

interface Delim {
  char: string;
  count: number;
  canOpen: boolean;
  canClose: boolean;
  node: number; // index in nodes
}

type Node = Inline | { type: "delim"; ref: Delim };

const SAFE_SCHEME = /^(https?:|mailto:|tel:|#|\/(?!\/)|\.{0,2}\/|[a-z0-9][^:]*$)/i;

export function safeHref(href: string): string {
  const h = href.trim();
  if (/^(javascript|data|vbscript):/i.test(h)) return "";
  if (/^\/\//.test(h)) return `https:${h}`;
  if (/^[a-z][a-z0-9+.-]*:/i.test(h) && !/^(https?:|mailto:|tel:)/i.test(h)) return "";
  if (!SAFE_SCHEME.test(h)) return "";
  return h;
}

function flanking(before: string | undefined, after: string | undefined, char: string): { open: boolean; close: boolean } {
  const wsBefore = isWs(before);
  const wsAfter = isWs(after);
  const pBefore = isPunct(before) && !isCjk(before);
  const pAfter = isPunct(after) && !isCjk(after);
  const left = !wsAfter && (!pAfter || wsBefore || pBefore || isCjk(before));
  const right = !wsBefore && (!pBefore || wsAfter || pAfter || isCjk(after));
  if (char === "_") {
    // Underscores do not open or close inside words (CJK neighbours do not count as word characters).
    const wordBefore = before !== undefined && !wsBefore && !pBefore && !isCjk(before);
    const wordAfter = after !== undefined && !wsAfter && !pAfter && !isCjk(after);
    return { open: left && (!right || pBefore || wsBefore) && !wordBefore, close: right && (!left || pAfter || wsAfter) && !wordAfter };
  }
  return { open: left, close: right };
}

// --- helpers for streaming ----------------------------------------------------------------

/** Closes emphasis, strike and code markers still open at the end of a streaming paragraph. */
export function closeOpenMarkers(src: string): string {
  const stack: string[] = [];
  let i = 0;
  let codeTicks = 0;
  while (i < src.length) {
    const c = src[i] as string;
    if (c === "\\") {
      i += 2;
      continue;
    }
    if (c === "`") {
      let n = 0;
      while (src[i + n] === "`") n++;
      if (codeTicks === 0) codeTicks = n;
      else if (codeTicks === n) codeTicks = 0;
      i += n;
      continue;
    }
    if (codeTicks > 0) {
      i++;
      continue;
    }
    if (c === "*" || c === "_" || c === "~") {
      let n = 0;
      while (src[i + n] === c) n++;
      const before = src[i - 1];
      const after = src[i + n];
      const f = flanking(before, after, c);
      const marker = c === "~" ? (n >= 2 ? "~~" : "") : c.repeat(Math.min(n, 3));
      if (marker) {
        const top = stack[stack.length - 1];
        if (f.close && top === marker) stack.pop();
        else if (f.open) stack.push(marker);
      }
      i += n;
      continue;
    }
    i++;
  }
  let out = src;
  if (codeTicks > 0) out += "`".repeat(codeTicks);
  while (stack.length) out += stack.pop();
  return out;
}

// --- parser ---------------------------------------------------------------------------------

const URL_RE = /^(https?:\/\/[^\s<>"'`]+|www\.[^\s<>"'`]+\.[a-z]{2,}[^\s<>"'`]*)/i;

function trimUrl(url: string): string {
  let u = url;
  while (u && /[.,;:!?)\]}'"*_~\uff0c\u3002\uff1b\uff1a\uff01\uff1f\u3001\uff09\u3011\u300b]$/.test(u)) {
    const last = u[u.length - 1] as string;
    if (last === ")" && (u.match(/\(/g) ?? []).length >= (u.match(/\)/g) ?? []).length) break;
    u = u.slice(0, -1);
  }
  return u;
}

function parseLinkTail(src: string, from: number): { href: string; title: string; end: number } | null {
  // src[from] === "("
  let i = from + 1;
  while (i < src.length && /[ \t\n]/.test(src[i] as string)) i++;
  let href = "";
  if (src[i] === "<") {
    const close = src.indexOf(">", i);
    if (close < 0) return null;
    href = src.slice(i + 1, close);
    i = close + 1;
  } else {
    let depth = 0;
    const start = i;
    while (i < src.length) {
      const c = src[i] as string;
      if (c === "\\") {
        i += 2;
        continue;
      }
      if (c === "(") depth++;
      else if (c === ")") {
        if (depth === 0) break;
        depth--;
      } else if (/\s/.test(c)) break;
      i++;
    }
    href = src.slice(start, i).replace(/\\(.)/g, "$1");
  }
  while (i < src.length && /[ \t\n]/.test(src[i] as string)) i++;
  let title = "";
  const q = src[i];
  if (q === '"' || q === "'" || q === "(") {
    const closeQ = q === "(" ? ")" : q;
    const end = src.indexOf(closeQ, i + 1);
    if (end < 0) return null;
    title = src.slice(i + 1, end);
    i = end + 1;
    while (i < src.length && /[ \t\n]/.test(src[i] as string)) i++;
  }
  if (src[i] !== ")") return null;
  return { href, title, end: i + 1 };
}

function findClosingBracket(src: string, from: number): number {
  let depth = 0;
  for (let i = from; i < src.length; i++) {
    const c = src[i] as string;
    if (c === "\\") {
      i++;
      continue;
    }
    if (c === "`") {
      const m = /^`+/.exec(src.slice(i));
      const tick = m ? m[0] : "`";
      const end = src.indexOf(tick, i + tick.length);
      if (end > 0) i = end + tick.length - 1;
      continue;
    }
    if (c === "[") depth++;
    else if (c === "]") {
      depth--;
      if (depth === 0) return i;
    }
  }
  return -1;
}

/** `$...$` per pandoc-like rules: no space inside the delimiters, closing not before a digit. */
function matchInlineMath(src: string, i: number): { tex: string; end: number } | null {
  if (src[i + 1] === "$") return null;
  const next = src[i + 1];
  if (next === undefined || WS.test(next)) return null;
  for (let j = i + 1; j < src.length; j++) {
    const c = src[j] as string;
    if (c === "\\") {
      j++;
      continue;
    }
    if (c === "\n") return null;
    if (c === "$") {
      if (src[j - 1] !== undefined && WS.test(src[j - 1] as string)) return null;
      if (/\d/.test(src[j + 1] ?? "")) return null;
      if (j === i + 1) return null;
      return { tex: src.slice(i + 1, j), end: j + 1 };
    }
  }
  return null;
}

const ESCAPABLE = /[!-/:-@[-`{-~]/;
