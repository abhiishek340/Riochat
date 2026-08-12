/**
 * Parsing the `riochat-ui` fences of a message: from raw (possibly still
 * streaming) text to a block description, plus the identities blocks and
 * widgets carry.
 */
import { scanFences } from "../fences";
import { LenientJsonError, readLenient } from "./lenient-json";

export const BLOCK_FORMAT_VERSION = 2;
export const MAX_JSON_DEPTH = 64;
export const WIDGET_MAX_BYTES = 200_000;

const STRUCTURE_KEYS = new Set(["component", "id", "version", "state", "computed", "children", "visible", "bind", "props"]);

export interface BlockNode {
  component: string;
  props: Record<string, unknown>;
  children: BlockNode[];
  bind: string | null;
  /** Raw `visible`: a boolean, an expression string or something invalid. */
  visible: unknown;
  /** The node's closing brace was read (it is complete while streaming). */
  closed: boolean;
}

export interface Block {
  component: string;
  id: string;
  key: string;
  version: number;
  state: Record<string, unknown> | undefined;
  computed: Record<string, unknown> | undefined;
  root: BlockNode;
  /** The whole document was read. */
  complete: boolean;
  /** Has `state`, `computed` or a primitive root: rendered node by node. */
  title: string;
}

export type BlockParse = { kind: "incomplete"; component: string } | { kind: "invalid"; message: string } | { kind: "ok"; block: Block };

/** FNV-1a over UTF-16 code units, unsigned, base 36. */
export function fingerprint(text: string): string {
  let h = 0x811c9dc5;
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return (h >>> 0).toString(36);
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function depthOf(v: unknown, limit: number, at = 0): number {
  if (at > limit) return at;
  if (Array.isArray(v)) return v.reduce<number>((m, x) => Math.max(m, depthOf(x, limit, at + 1)), at + 1);
  if (isObject(v)) return Object.values(v).reduce<number>((m, x) => Math.max(m, depthOf(x, limit, at + 1)), at + 1);
  return at;
}

export function readVersion(v: unknown): number {
  if (typeof v === "number" && Number.isFinite(v)) return Math.max(1, Math.trunc(v));
  if (typeof v === "string") {
    const m = /^v?(\d+)(\.\d+)?$/i.exec(v.trim());
    if (m) return Math.max(1, Number(m[1]));
  }
  return 1;
}

export function readId(v: unknown): string {
  if (typeof v === "number" && Number.isFinite(v)) return String(v);
  if (typeof v === "string") {
    const t = v.trim();
    if (/^[A-Za-z0-9_-]{1,64}$/.test(t)) return t;
  }
  return "";
}

function readNode(value: Record<string, unknown>, closed: WeakSet<object>): BlockNode {
  const props = isObject(value.props) ? { ...value.props } : Object.fromEntries(Object.entries(value).filter(([k]) => !STRUCTURE_KEYS.has(k)));
  const kids = Array.isArray(value.children) ? value.children.filter((c): c is Record<string, unknown> => isObject(c) && typeof c.component === "string" && c.component !== "") : [];
  return {
    component: String(value.component),
    props,
    children: kids.map((k) => readNode(k, closed)),
    bind: typeof value.bind === "string" && value.bind !== "" ? value.bind : null,
    visible: value.visible,
    closed: closed.has(value),
  };
}

export function parseBlock(source: string, streaming: boolean): BlockParse {
  let read;
  try {
    read = readLenient(source);
  } catch (e) {
    return { kind: "invalid", message: e instanceof LenientJsonError ? e.message : "invalid JSON" };
  }
  const value = read.value;
  const componentRaw = isObject(value) ? value.component : undefined;
  if (!isObject(value) || typeof componentRaw !== "string" || componentRaw === "") {
    if (streaming && !read.complete) return { kind: "incomplete", component: "" };
    return { kind: "invalid", message: "an object with a component is expected" };
  }
  if (!read.complete && !streaming) return { kind: "invalid", message: "the JSON is incomplete" };
  if (read.cut.get(value) === "component") return { kind: "incomplete", component: componentRaw };
  if (depthOf(value, MAX_JSON_DEPTH) > MAX_JSON_DEPTH) return { kind: "invalid", message: "the JSON is nested too deeply" };

  const id = readId(value.id);
  const key = id || (read.complete ? `auto-${fingerprint(JSON.stringify(value))}` : "");
  const block: Block = {
    component: componentRaw,
    id,
    key,
    version: readVersion(value.version),
    state: isObject(value.state) ? value.state : undefined,
    computed: isObject(value.computed) ? value.computed : undefined,
    root: readNode(value, read.closed),
    complete: read.complete,
    title: typeof value.title === "string" ? value.title.trim() : "",
  };
  return { kind: "ok", block };
}

/** The component name read from raw text as soon as it appears, for choosing a skeleton. */
export function sniffComponent(raw: string): string {
  const m = /"component"\s*:\s*"([A-Za-z0-9_-]+)"/.exec(raw);
  return m ? (m[1] as string) : "";
}

/** How many objects have begun in the first array of the document (skeleton rows). */
export function sniffItemCount(raw: string): number {
  const open = raw.indexOf("[");
  if (open < 0) return 0;
  let depth = 0;
  let count = 0;
  let inString = false;
  for (let i = open; i < raw.length; i++) {
    const c = raw[i] as string;
    if (inString) {
      if (c === "\\") i++;
      else if (c === '"') inString = false;
      continue;
    }
    if (c === '"') inString = true;
    else if (c === "[" || c === "{") {
      depth++;
      if (c === "{" && depth === 2) count++;
    } else if (c === "]" || c === "}") {
      depth--;
      if (depth <= 0) break;
    }
  }
  return count;
}

// --- widgets ----------------------------------------------------------------------------------------------------

const ENTITIES: Record<string, string> = { "&amp;": "&", "&lt;": "<", "&gt;": ">", "&quot;": '"', "&#39;": "'", "&nbsp;": " " };

function plainText(html: string): string {
  let out = "";
  let inTag = false;
  for (let i = 0; i < html.length; i++) {
    const c = html[i] as string;
    if (c === "<") inTag = true;
    else if (c === ">") inTag = false;
    else if (!inTag) out += c;
  }
  return out.replace(/&(?:amp|lt|gt|quot|#39|nbsp);/g, (m) => ENTITIES[m] ?? m).replace(/\s+/g, " ").trim();
}

function firstElementText(source: string, tag: string): string {
  const lower = source.toLowerCase();
  const open = lower.indexOf(`<${tag}`);
  if (open < 0) return "";
  const gt = lower.indexOf(">", open);
  if (gt < 0) return "";
  const close = lower.indexOf(`</${tag}`, gt);
  if (close < 0) return "";
  return plainText(source.slice(gt + 1, close));
}

/** Title of a widget: its <title>, else the first h1, h2 or h3; at most 40 code points. */
export function widgetTitle(source: string): string {
  const text = firstElementText(source, "title") || firstElementText(source, "h1") || firstElementText(source, "h2") || firstElementText(source, "h3");
  return Array.from(text).slice(0, 40).join("");
}

export function widgetKey(source: string): string {
  return `widget-${fingerprint(source.trim())}`;
}

export interface MessageBlockRef {
  key: string;
  title: string;
  kind: "ui" | "widget";
}

export interface MessageBlocks {
  blocks: MessageBlockRef[];
  /** Keys that occur more than once: such blocks do not persist state. */
  duplicates: Set<string>;
}

export function scanMessageBlocks(content: string): MessageBlocks {
  const blocks: MessageBlockRef[] = [];
  const seen = new Set<string>();
  const duplicates = new Set<string>();
  const add = (ref: MessageBlockRef) => {
    if (seen.has(ref.key)) duplicates.add(ref.key);
    seen.add(ref.key);
    blocks.push(ref);
  };
  for (const f of scanFences(content)) {
    if (f.lang === "riochat-ui") {
      const parsed = parseBlock(f.body, !f.closed);
      if (parsed.kind === "ok" && parsed.block.key) add({ key: parsed.block.key, title: parsed.block.title, kind: "ui" });
    } else if (f.lang === "riochat-widget" && f.closed && new TextEncoder().encode(f.body).length <= WIDGET_MAX_BYTES) {
      add({ key: widgetKey(f.body), title: widgetTitle(f.body), kind: "widget" });
    }
  }
  return { blocks, duplicates };
}
