/** Small text helpers (code-point aware). */

export function truncateCodePoints(text: string, max: number): string {
  const points = Array.from(text);
  return points.length <= max ? text : points.slice(0, max).join("");
}

export function codePointLength(text: string): number {
  return Array.from(text).length;
}

const QUOTES = new Set(["\"", "'", "`", "“", "”", "‘", "’", "\u300c", "\u300d", "\u300e", "\u300f", " "]);

function trimQuotes(text: string): string {
  const chars = Array.from(text);
  let start = 0;
  let end = chars.length;
  while (start < end && QUOTES.has(chars[start] as string)) start++;
  while (end > start && QUOTES.has(chars[end - 1] as string)) end--;
  return chars.slice(start, end).join("");
}

const PLACEHOLDER_TITLES = new Set(["new chat", "untitled chat"]);

export function isPlaceholderTitle(title: string | null | undefined): boolean {
  const t = (title ?? "").trim().toLowerCase();
  return t === "" || PLACEHOLDER_TITLES.has(t);
}

/**
 * Title shown right after the first send: whitespace collapsed, quotes
 * stripped, at most 16 code points. An empty result means "do not patch".
 */
export function optimisticTitle(text: string): string {
  const collapsed = text.replace(/\s+/g, " ").trim();
  const unquoted = trimQuotes(collapsed);
  return truncateCodePoints(unquoted, 16).trim();
}

/** `<title> (Fork)` where the whole result stays within 255 code points. */
export function forkTitle(title: string, suffix: string, max = 255): string {
  const room = Math.max(0, max - codePointLength(suffix));
  return `${truncateCodePoints(title, room)}${suffix}`;
}

export function pickLastLines(text: string, count: number): string {
  const lines = text.split(/\r?\n/);
  return lines.slice(-count).join("\n");
}

/** Strips markdown/code decoration for one-line previews. */
export function plainPreview(markdown: string, max: number): string {
  const noCode = markdown.replace(/```[\s\S]*?```/g, " ").replace(/`([^`]*)`/g, "$1");
  const noMd = noCode
    .replace(/!\[[^\]]*\]\([^)]*\)/g, " ")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/^[>#\-*+\d.\s]+/gm, "")
    .replace(/[*_~|]/g, "");
  return truncateCodePoints(noMd.replace(/\s+/g, " ").trim(), max);
}
