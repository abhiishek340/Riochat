/**
 * Paste handling for the composer: rich HTML becomes Markdown, code copied from
 * an editor becomes a fenced block, long plain text becomes an attachment.
 */

export const LONG_PASTE_CHARS = 2000;

export function isLongPaste(text: string): boolean {
  return text.length >= LONG_PASTE_CHARS;
}

// --- editor code ------------------------------------------------------------------------

const MODE_ALIASES: Record<string, string> = { javascriptreact: "jsx", typescriptreact: "tsx", shellscript: "bash" };
const UNFENCED_MODES = new Set(["markdown", "mdx", "plaintext"]);
const SAFE_MODE = /^[A-Za-z0-9_+#.-]{1,32}$/;

/** A fence one backtick longer than the longest run in the code, at least three. */
export function fenceFor(code: string): string {
  let longest = 0;
  for (const m of code.matchAll(/`+/g)) longest = Math.max(longest, m[0].length);
  return "`".repeat(Math.max(3, longest + 1));
}

/** Fenced block for text copied from VS Code (`vscode-editor-data` version 1), else null. */
export function editorPasteToMarkdown(editorData: string | undefined, text: string): string | null {
  if (!editorData) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(editorData);
  } catch {
    return null;
  }
  if (typeof parsed !== "object" || parsed === null) return null;
  const rec = parsed as Record<string, unknown>;
  if (rec.version !== 1) return null;
  const mode = typeof rec.mode === "string" ? rec.mode.trim() : "";
  if (UNFENCED_MODES.has(mode.toLowerCase())) return null;
  const lang = mode ? (MODE_ALIASES[mode] ?? (SAFE_MODE.test(mode) ? mode : "")) : "";
  const code = text.replace(/\n+$/, "");
  const fence = fenceFor(code);
  return `${fence}${lang}\n${code}\n${fence}`;
}

// --- html -> markdown --------------------------------------------------------------------------

const FORMATTING = new Set(["A", "B", "STRONG", "I", "EM", "H1", "H2", "H3", "H4", "H5", "H6", "UL", "OL", "LI", "CODE", "PRE", "TABLE", "IMG", "BLOCKQUOTE", "HR", "DEL", "S", "STRIKE"]);

/** Escapes what would change the meaning of plain text inside Markdown. */
export function escapeText(text: string): string {
  return text
    .replace(/\\/g, "\\\\")
    .replace(/([`*[\]])/g, "\\$1")
    .replace(/(^|[^\p{L}\p{N}])_|_([^\p{L}\p{N}]|$)/gu, (m) => m.replace("_", "\\_"))
    .replace(/<(?=[A-Za-z/!])/g, "\\<")
    .replace(/^(\s*)([#>])/gm, "$1\\$2")
    .replace(/^(\s*)([-+])(\s)/gm, "$1\\$2$3")
    .replace(/^(\s*\d+)([.)])(\s)/gm, "$1\\$2$3");
}

interface Ctx {
  listDepth: number;
  inPre: boolean;
}

function childrenMd(node: Node, ctx: Ctx): string {
  let out = "";
  node.childNodes.forEach((child) => {
    out += nodeMd(child, ctx);
  });
  return out;
}

function inlineWrap(marker: string, content: string): string {
  const trimmed = content.trim();
  if (!trimmed) return content;
  const lead = content.slice(0, content.length - content.trimStart().length);
  const tail = content.slice(content.trimEnd().length);
  return `${lead}${marker}${trimmed}${marker}${tail}`;
}

function tableMd(table: Element, ctx: Ctx): string {
  const rows = Array.from(table.querySelectorAll("tr"));
  if (rows.length === 0) return "";
  const cells = rows.map((tr) =>
    Array.from(tr.children)
      .filter((c) => c.tagName === "TD" || c.tagName === "TH")
      .map((c) => childrenMd(c, ctx).replace(/\s*\n\s*/g, " ").replace(/\|/g, "\\|").trim()),
  );
  const width = Math.max(...cells.map((r) => r.length));
  if (width === 0) return "";
  const pad = (r: string[]) => [...r, ...Array(width - r.length).fill("")];
  const line = (r: string[]) => `| ${pad(r).join(" | ")} |`;
  const [head, ...body] = cells as [string[], ...string[][]];
  return [line(head), `| ${Array(width).fill("---").join(" | ")} |`, ...body.map(line)].join("\n");
}

function nodeMd(node: Node, ctx: Ctx): string {
  if (node.nodeType === 3) {
    const raw = node.textContent ?? "";
    if (ctx.inPre) return raw;
    return escapeText(raw.replace(/\s+/g, " "));
  }
  if (node.nodeType !== 1) return "";
  const el = node as Element;
  const tag = el.tagName;
  switch (tag) {
    case "SCRIPT":
    case "STYLE":
    case "HEAD":
    case "META":
    case "TITLE":
      return "";
    case "BR":
      return "\n";
    case "HR":
      return "\n\n---\n\n";
    case "STRONG":
    case "B":
      return inlineWrap("**", childrenMd(el, ctx));
    case "EM":
    case "I":
      return inlineWrap("*", childrenMd(el, ctx));
    case "DEL":
    case "S":
    case "STRIKE":
      return inlineWrap("~~", childrenMd(el, ctx));
    case "CODE": {
      if (ctx.inPre) return el.textContent ?? "";
      const code = el.textContent ?? "";
      const fence = "`".repeat(Math.max(1, ...Array.from(code.matchAll(/`+/g)).map((m) => m[0].length + 1)));
      return code ? `${fence}${code.startsWith("`") || code.endsWith("`") ? ` ${code} ` : code}${fence}` : "";
    }
    case "PRE": {
      const code = (el.textContent ?? "").replace(/\n+$/, "");
      const lang = /language-([\w+#.-]+)/.exec(el.querySelector("code")?.className ?? el.className)?.[1] ?? "";
      const fence = fenceFor(code);
      return `\n\n${fence}${lang}\n${code}\n${fence}\n\n`;
    }
    case "A": {
      const href = el.getAttribute("href") ?? "";
      const text = childrenMd(el, ctx).trim();
      if (!href || href.startsWith("javascript:")) return text;
      return text ? `[${text}](${href})` : "";
    }
    case "IMG": {
      const src = el.getAttribute("src") ?? "";
      return src ? `![${escapeText(el.getAttribute("alt") ?? "")}](${src})` : "";
    }
    case "H1":
    case "H2":
    case "H3":
    case "H4":
    case "H5":
    case "H6":
      return `\n\n${"#".repeat(Number(tag[1]))} ${childrenMd(el, ctx).replace(/\s+/g, " ").trim()}\n\n`;
    case "BLOCKQUOTE": {
      const inner = childrenMd(el, ctx).trim().split("\n").map((l) => `> ${l}`.trimEnd()).join("\n");
      return `\n\n${inner}\n\n`;
    }
    case "UL":
    case "OL": {
      const ordered = tag === "OL";
      let n = Number(el.getAttribute("start") ?? 1) || 1;
      const lines: string[] = [];
      const indent = "  ".repeat(ctx.listDepth);
      for (const li of Array.from(el.children)) {
        if (li.tagName !== "LI") continue;
        const inner = childrenMd(li, { ...ctx, listDepth: ctx.listDepth + 1 }).trim().replace(/\n{3,}/g, "\n\n");
        const marker = ordered ? `${n++}.` : "-";
        const [first = "", ...rest] = inner.split("\n");
        lines.push(`${indent}${marker} ${first}${rest.length ? "\n" + rest.map((l) => (l.trim() ? `${indent}  ${l}` : l)).join("\n") : ""}`);
      }
      return ctx.listDepth > 0 ? `\n${lines.join("\n")}\n` : `\n\n${lines.join("\n")}\n\n`;
    }
    case "TABLE":
      return `\n\n${tableMd(el, ctx)}\n\n`;
    case "P":
    case "DIV":
    case "SECTION":
    case "ARTICLE":
    case "HEADER":
    case "FOOTER":
    case "MAIN":
    case "ASIDE":
    case "FIGURE":
    case "FIGCAPTION":
    case "DETAILS":
    case "SUMMARY":
      return `\n\n${childrenMd(el, ctx).trim()}\n\n`;
    default:
      return childrenMd(el, ctx);
  }
}

function hasFormatting(root: Element): boolean {
  if (FORMATTING.has(root.tagName)) return true;
  for (const el of Array.from(root.querySelectorAll("*"))) if (FORMATTING.has(el.tagName)) return true;
  return false;
}

function flow(text: string): string {
  return text.replace(/\s+/g, " ").trim();
}

function normalizeMarkdown(md: string): string {
  return md.replace(/[ \t]+\n/g, "\n").replace(/\n{3,}/g, "\n\n").trim();
}

/**
 * Markdown for a rich paste, or null when the browser should paste natively:
 * no formatting, nothing gained, or line breaks would be lost.
 */
export function htmlToMarkdown(html: string, plain: string): string | null {
  if (typeof DOMParser === "undefined" || !html.trim()) return null;
  const doc = new DOMParser().parseFromString(html, "text/html");
  const body = doc.body;
  if (!body || !hasFormatting(body)) return null;
  const md = normalizeMarkdown(childrenMd(body, { listDepth: 0, inPre: false }));
  if (!md) return null;
  const plainTrim = plain.trim();
  if (md === plainTrim || flow(md) === flow(plainTrim) || flow(escapeText(plainTrim)) === flow(md)) return null;
  const nl = (s: string) => (s.match(/\n/g) ?? []).length;
  if (nl(md) < nl(plainTrim)) return null;
  return md;
}

/** Inserts `insert` at the selection, keeping block content on its own lines. */
export function insertBlock(text: string, start: number, end: number, insert: string): { text: string; caret: number } {
  const before = text.slice(0, start);
  const after = text.slice(end);
  const isBlock = /\n/.test(insert) || /^(#{1,6} |[-*] |\d+\. |> |```|\|)/.test(insert);
  const lead = isBlock && before && !before.endsWith("\n") ? "\n" : "";
  const trail = isBlock && after && !after.startsWith("\n") ? "\n" : "";
  const next = `${before}${lead}${insert}${trail}${after}`;
  return { text: next, caret: before.length + lead.length + insert.length };
}
