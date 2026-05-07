/**
 * Scanning a whole message for fenced code blocks. Used for artifacts and for
 * the interactive-block fences.
 */

export interface Fence {
  /** First word of the info string, lower-cased, decoration stripped. */
  lang: string;
  info: string;
  body: string;
  /** False when the message ended before the closing fence. */
  closed: boolean;
  /** Zero-based line of the opening fence and of the closing one (or the last line). */
  startLine: number;
  endLine: number;
  marker: string;
}

const OPEN = /^( {0,3})(`{3,}|~{3,})(.*)$/;

export function languageOf(info: string): string {
  const first = info.trim().split(/\s+/)[0] ?? "";
  return first.replace(/^\{+/, "").replace(/\}+$/, "").replace(/^\./, "").toLowerCase();
}

export function scanFences(text: string): Fence[] {
  const lines = text.split(/\r?\n/);
  const out: Fence[] = [];
  let i = 0;
  while (i < lines.length) {
    const m = OPEN.exec(lines[i] as string);
    if (!m) {
      i++;
      continue;
    }
    const marker = m[2] as string;
    const info = (m[3] ?? "").trim();
    if (marker[0] === "`" && info.includes("`")) {
      i++;
      continue;
    }
    const indent = (m[1] ?? "").length;
    const start = i;
    const body: string[] = [];
    i++;
    let closed = false;
    while (i < lines.length) {
      const l = lines[i] as string;
      const c = /^ {0,3}(`{3,}|~{3,})\s*$/.exec(l);
      if (c && (c[1] as string)[0] === marker[0] && (c[1] as string).length >= marker.length) {
        closed = true;
        break;
      }
      body.push(indent ? l.replace(new RegExp(`^ {0,${indent}}`), "") : l);
      i++;
    }
    out.push({ lang: languageOf(info), info, body: body.join("\n"), closed, startLine: start, endLine: closed ? i : Math.max(start, lines.length - 1), marker });
    if (closed) i++;
  }
  return out;
}

/** Replaces whole fences (opening line to closing line) by text; `null` keeps the fence. */
export function replaceFences(text: string, decide: (fence: Fence, index: number) => string | null): string {
  const fences = scanFences(text);
  if (fences.length === 0) return text;
  const lines = text.split(/\r?\n/);
  const out: string[] = [];
  let cursor = 0;
  fences.forEach((f, index) => {
    const replacement = decide(f, index);
    if (replacement === null) return;
    out.push(...lines.slice(cursor, f.startLine));
    out.push(replacement);
    cursor = f.endLine + 1;
  });
  out.push(...lines.slice(cursor));
  return out.join("\n");
}
