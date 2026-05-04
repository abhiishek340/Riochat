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

export function parseInline(source: string, options: InlineOptions = {}): Inline[] {
  const src = options.streaming ? closeOpenMarkers(source) : source;
  const nodes: Node[] = [];
  let text = "";
  const flush = () => {
    if (text) nodes.push({ type: "text", value: text });
    text = "";
  };
  let i = 0;
  while (i < src.length) {
    const c = src[i] as string;

    if (c === "\\") {
      const n = src[i + 1];
      if (n === "\n") {
        flush();
        nodes.push({ type: "br" });
        i += 2;
        continue;
      }
      if (n !== undefined && ESCAPABLE.test(n)) {
        text += n;
        i += 2;
        continue;
      }
      text += c;
      i++;
      continue;
    }

    if (c === "`") {
      const m = /^`+/.exec(src.slice(i));
      const tick = (m as RegExpExecArray)[0];
      let end = -1;
      for (let j = i + tick.length; j < src.length; j++) {
        if (src[j] === "`") {
          let n = 0;
          while (src[j + n] === "`") n++;
          if (n === tick.length) {
            end = j;
            break;
          }
          j += n - 1;
        }
      }
      if (end >= 0) {
        flush();
        let code = src.slice(i + tick.length, end).replace(/\n/g, " ");
        if (code.length > 2 && code.startsWith(" ") && code.endsWith(" ") && code.trim() !== "") code = code.slice(1, -1);
        nodes.push({ type: "code", value: code });
        i = end + tick.length;
        continue;
      }
      text += tick;
      i += tick.length;
      continue;
    }

    if (c === "$") {
      const math = matchInlineMath(src, i);
      if (math) {
        flush();
        nodes.push({ type: "math", tex: math.tex });
        i = math.end;
        continue;
      }
      text += c;
      i++;
      continue;
    }

    if (c === "!" && src[i + 1] === "[") {
      const close = findClosingBracket(src, i + 1);
      if (close > 0 && src[close + 1] === "(") {
        const tail = parseLinkTail(src, close + 1);
        if (tail) {
          flush();
          nodes.push({ type: "image", src: tail.href, alt: src.slice(i + 2, close), title: tail.title });
          i = tail.end;
          continue;
        }
      }
      text += c;
      i++;
      continue;
    }

    if (c === "[") {
      if (src[i + 1] === "^") {
        const m = /^\[\^([^\]\s]+)\]/.exec(src.slice(i));
        if (m) {
          flush();
          const id = m[1] as string;
          const list = options.footnotes ?? [];
          let index = list.indexOf(id);
          if (index < 0) {
            list.push(id);
            index = list.length - 1;
          }
          nodes.push({ type: "footnoteRef", id, index: index + 1 });
          i += m[0].length;
          continue;
        }
      }
      const close = findClosingBracket(src, i);
      if (close > 0 && src[close + 1] === "(") {
        const tail = parseLinkTail(src, close + 1);
        if (tail) {
          flush();
          const href = safeHref(tail.href);
          const children = parseInline(src.slice(i + 1, close), { streaming: false, footnotes: options.footnotes });
          if (href) nodes.push({ type: "link", href, title: tail.title, children });
          else nodes.push(...children);
          i = tail.end;
          continue;
        }
      }
      text += c;
      i++;
      continue;
    }

    if (c === "<") {
      const auto = /^<(https?:\/\/[^\s<>]+|mailto:[^\s<>]+)>/i.exec(src.slice(i));
      if (auto) {
        flush();
        const href = safeHref(auto[1] as string);
        nodes.push(href ? { type: "link", href, title: "", children: [{ type: "text", value: auto[1] as string }] } : { type: "text", value: auto[0] });
        i += auto[0].length;
        continue;
      }
      const br = /^<br\s*\/?>/i.exec(src.slice(i));
      if (br) {
        flush();
        nodes.push({ type: "br" });
        i += br[0].length;
        continue;
      }
      text += c;
      i++;
      continue;
    }

    if ((c === "h" || c === "w") && (i === 0 || !/[A-Za-z0-9]/.test(src[i - 1] as string))) {
      const m = URL_RE.exec(src.slice(i));
      if (m) {
        const url = trimUrl(m[0]);
        if (url.length > 8) {
          flush();
          const href = safeHref(url.startsWith("www.") ? `https://${url}` : url);
          nodes.push(href ? { type: "link", href, title: "", children: [{ type: "text", value: url }] } : { type: "text", value: url });
          i += url.length;
          continue;
        }
      }
    }

    if (c === "*" || c === "_" || c === "~") {
      let n = 0;
      while (src[i + n] === c) n++;
      if (c === "~" && n < 2) {
        text += c.repeat(n);
        i += n;
        continue;
      }
      const f = flanking(src[i - 1], src[i + n], c);
      flush();
      const d: Delim = { char: c, count: c === "~" ? 2 : n, canOpen: f.open, canClose: f.close, node: nodes.length };
      if (c === "~" && n > 2) text += c.repeat(n - 2);
      nodes.push({ type: "delim", ref: d });
      i += n;
      continue;
    }

    if (c === "\n") {
      // Two trailing spaces make a hard break; otherwise it is a soft break.
      if (/ {2,}$/.test(text)) {
        text = text.replace(/ +$/, "");
        flush();
        nodes.push({ type: "br" });
      } else {
        text = text.replace(/ +$/, "") + "\n";
      }
      i++;
      while (src[i] === " ") i++;
      continue;
    }

    text += c;
    i++;
  }
  flush();
  return build(nodes);
}

/**
 * Builds the tree by a second left-to-right pass using a stack: a closer that
 * finds a compatible opener wraps what lies between them.
 */
function build(nodes: Node[]): Inline[] {
  interface Frame {
    d: Delim | null;
    left: number; // unmatched count remaining on the opener
    kids: Inline[];
  }
  const root: Frame = { d: null, left: 0, kids: [] };
  const stack: Frame[] = [root];
  const top = () => stack[stack.length - 1] as Frame;
  const literal = (d: Delim, n: number): Inline => ({ type: "text", value: d.char.repeat(n) });

  const collapse = (toIndex: number) => {
    // Unmatched openers above toIndex become literal text.
    while (stack.length - 1 > toIndex) {
      const f = stack.pop() as Frame;
      const parent = top();
      parent.kids.push(literal(f.d as Delim, f.left), ...f.kids);
    }
  };

  for (const n of nodes) {
    if (n.type !== "delim") {
      top().kids.push(n);
      continue;
    }
    const d = n.ref;
    let remaining = d.count;
    if (d.canClose) {
      while (remaining > 0) {
        let idx = -1;
        for (let k = stack.length - 1; k >= 1; k--) {
          const f = stack[k] as Frame;
          const o = f.d as Delim;
          if (o.char !== d.char) continue;
          if ((o.canClose || d.canOpen) && (f.left + remaining) % 3 === 0 && (f.left % 3 !== 0 || remaining % 3 !== 0) && d.char !== "~") continue;
          idx = k;
          break;
        }
        if (idx < 0) break;
        collapse(idx);
        const f = stack[idx] as Frame;
        const take = d.char === "~" ? 2 : Math.min(f.left, remaining, f.left >= 3 && remaining >= 3 ? 3 : 2);
        const kind: "em" | "strong" | "del" = d.char === "~" ? "del" : take === 1 ? "em" : "strong";
        const wrapped: Inline = { type: kind, children: f.kids } as Inline;
        let node: Inline = wrapped;
        if (take === 3) node = { type: "em", children: [{ type: "strong", children: f.kids }] };
        f.left -= take;
        remaining -= take;
        if (f.left === 0) {
          stack.pop();
          top().kids.push(node);
        } else {
          // keep the opener open for outer markers; result nests inside
          f.kids = [node];
        }
        if (d.char === "~") break;
      }
    }
    if (remaining > 0) {
      if (d.canOpen) stack.push({ d, left: remaining, kids: [] });
      else top().kids.push(literal(d, remaining));
    }
  }
  collapse(0);
  return merge(root.kids);
}

function merge(list: Inline[]): Inline[] {
  const out: Inline[] = [];
  for (const n of list) {
    const last = out[out.length - 1];
    if (n.type === "text" && last && last.type === "text") out[out.length - 1] = { type: "text", value: last.value + n.value };
    else out.push(n);
  }
  return out;
}
