/**
 * Content preparation before parsing: coercion to text, math delimiter
 * normalisation, nested fences and the user-message line-break rule.
 */
import { isRecord } from "@/lib/utils/guards";

export type Variant = "default" | "thinking" | "user";

/** Loosely typed content as text. */
export function coerceText(value: unknown): string {
  if (typeof value === "string") return value;
  if (value === null || value === undefined) return "";
  if (typeof value === "number" || typeof value === "boolean" || typeof value === "bigint") return String(value);
  if (Array.isArray(value)) {
    return value
      .map(coerceText)
      .filter((s) => s !== "")
      .join("\n");
  }
  if (isRecord(value)) {
    for (const k of ["content", "text", "message"]) if (typeof value[k] === "string") return value[k] as string;
    try {
      return JSON.stringify(value, null, 2);
    } catch {
      return "";
    }
  }
  return "";
}

// --- fences ------------------------------------------------------------------------------

const FENCE_OPEN = /^( {0,3})(`{3,}|~{3,})(.*)$/;

interface FenceInfo {
  indent: string;
  marker: string;
  info: string;
}

function openFence(line: string): FenceInfo | null {
  const m = FENCE_OPEN.exec(line);
  if (!m) return null;
  const marker = m[2] as string;
  const info = (m[3] ?? "").trim();
  if (marker[0] === "`" && info.includes("`")) return null;
  return { indent: m[1] ?? "", marker, info };
}

function closesFence(line: string, open: FenceInfo): boolean {
  const m = /^ {0,3}(`{3,}|~{3,})\s*$/.exec(line);
  return !!m && (m[1] as string)[0] === open.marker[0] && (m[1] as string).length >= open.marker.length;
}

/**
 * A tagged fence (for example a markdown one) that contains tagged fences of the
 * same character and closes after them: lengthen the outer markers so the
 * nesting parses as it was written.
 */
export function fixNestedFences(text: string): string {
  const lines = text.split("\n");
  type Frame = { at: number; open: FenceInfo; inner: { at: number; info: FenceInfo }[]; closeAt: number };
  const frames: Frame[] = [];
  let current: Frame | null = null;
  // Open tagged inner fences: a bare fence line closes the innermost one before it can close the outer fence.
  let depth = 0;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] as string;
    if (current) {
      const sameKind = closesFence(line, current.open);
      if (sameKind && depth > 0) {
        depth--;
        current.inner.push({ at: i, info: { indent: "", marker: line.trim(), info: "" } });
        continue;
      }
      if (sameKind) {
        current.closeAt = i;
        frames.push(current);
        current = null;
        continue;
      }
      const inner = openFence(line);
      if (inner && inner.marker[0] === current.open.marker[0]) {
        current.inner.push({ at: i, info: inner });
        if (inner.info !== "") depth++;
      }
      continue;
    }
    const open = openFence(line);
    if (open) {
      current = { at: i, open, inner: [], closeAt: -1 };
      depth = 0;
    }
  }
  if (current) frames.push(current);

  for (const f of frames) {
    const tagged = f.inner.filter((x) => x.info.info !== "");
    if (f.open.info === "" || tagged.length === 0) continue;
    // An outer fence still open with an unclosed inner one has nothing to lengthen yet.
    if (f.closeAt < 0 && f.inner.filter((x) => x.info.info !== "").length > f.inner.filter((x) => x.info.info === "").length) continue;
    const longest = Math.max(...f.inner.map((x) => x.info.marker.length));
    if (f.open.marker.length > longest) continue;
    const marker = f.open.marker[0]!.repeat(longest + 1);
    lines[f.at] = `${f.open.indent}${marker}${f.open.info}`;
    if (f.closeAt >= 0) lines[f.closeAt] = marker;
  }
  return lines.join("\n");
}

// --- math delimiters ------------------------------------------------------------------------------

/** Splits text into code (fences and spans) and prose so rewriting skips code. */
function mapProse(text: string, fn: (prose: string) => string): string {
  const lines = text.split("\n");
  const out: string[] = [];
  let fence: FenceInfo | null = null;
  let buffer: string[] = [];
  const flush = () => {
    if (buffer.length) out.push(fn(buffer.join("\n")));
    buffer = [];
  };
  for (const line of lines) {
    if (fence) {
      out.push(line);
      if (closesFence(line, fence)) fence = null;
      continue;
    }
    const open = openFence(line);
    if (open) {
      flush();
      fence = open;
      out.push(line);
      continue;
    }
    buffer.push(line);
  }
  flush();
  return out.join("\n");
}

function mapOutsideInlineCode(prose: string, fn: (s: string) => string): string {
  const parts = prose.split(/(`+[^`\n]*?`+)/g);
  return parts.map((p, i) => (i % 2 === 1 ? p : fn(p))).join("");
}

/** `\( \)` becomes `$ $`; `\[ \]` becomes a `$$` block. Inside math, `|` is written as `\vert{}`. */
export function normalizeMathDelimiters(text: string): string {
  return mapProse(text, (prose) =>
    mapOutsideInlineCode(prose, (s) => {
      let out = s.replace(/\\\[([\s\S]+?)\\\]/g, (_m, body: string) => `\n$$\n${body.trim()}\n$$\n`);
      out = out.replace(/\\\(([\s\S]+?)\\\)/g, (_m, body: string) => `$${body.replace(/\s*\n\s*/g, " ").trim()}$`);
      return out;
    }),
  );
}

const ARROWS: Record<string, string> = { "→": "\\to ", "←": "\\leftarrow ", "⇒": "\\Rightarrow ", "⇐": "\\Leftarrow ", "↔": "\\leftrightarrow ", "⇔": "\\Leftrightarrow " };

/** Arrows typed as characters inside math become commands. */
export function replaceArrowsInMath(tex: string): string {
  return tex.replace(/[→←⇒⇐↔⇔]/g, (c) => ARROWS[c] ?? c);
}

// --- user text ---------------------------------------------------------------------------------------

const BLOCKISH = /^(\s*([-*+]|\d+[.)])\s|\s*>|\s*#{1,6}\s|\s*\||\s*```|\s*~~~)/;

/** A lone line break between two plain lines becomes a paragraph break. */
export function promoteLineBreaks(text: string): string {
  const lines = text.split("\n");
  const out: string[] = [];
  let fenced = false;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] as string;
    if (/^\s*(```|~~~)/.test(line)) fenced = !fenced;
    out.push(line);
    const next = lines[i + 1];
    if (fenced || next === undefined) continue;
    if (line.trim() === "" || next.trim() === "") continue;
    if (BLOCKISH.test(line) || BLOCKISH.test(next)) continue;
    out.push("");
  }
  return out.join("\n");
}

export interface NormalizeOptions {
  variant?: Variant;
}

export function normalizeContent(value: unknown, options: NormalizeOptions = {}): string {
  let text = coerceText(value).replace(/\r\n?/g, "\n");
  text = normalizeMathDelimiters(text);
  text = fixNestedFences(text);
  if (options.variant === "user") text = promoteLineBreaks(text);
  return text;
}
