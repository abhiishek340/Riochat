/**
 * Request-option helpers: dotted-path access, native-tool payload handling and
 * the reserved keys that never travel as options.
 */
import { isRecord } from "@/lib/utils/guards";

export type Options = Record<string, unknown>;

export const RESERVED_OPTION_KEYS: readonly string[] = [
  "contents", "instructions", "input", "messages", "model", "prompt", "stream", "system", "systemInstruction",
];

export function stripReserved(options: Options | null | undefined): Options {
  const out: Options = {};
  if (!options) return out;
  for (const [k, v] of Object.entries(options)) if (!RESERVED_OPTION_KEYS.includes(k)) out[k] = v;
  return out;
}

export function isEmptyObject(value: unknown): boolean {
  return isRecord(value) && Object.keys(value).length === 0;
}

export function cloneJson<T>(value: T): T {
  return value === undefined ? value : (JSON.parse(JSON.stringify(value)) as T);
}

export function getAtPath(root: unknown, path: string): unknown {
  let node: unknown = root;
  for (const part of path.split(".")) {
    if (!isRecord(node)) return undefined;
    node = node[part];
  }
  return node;
}

const FORBIDDEN = new Set(["__proto__", "constructor", "prototype"]);

/** Returns a copy with `value` written at the dotted path (creating objects). */
export function setAtPath(root: Options, path: string, value: unknown): Options {
  const parts = path.split(".");
  if (parts.some((p) => !p || FORBIDDEN.has(p))) return root;
  const out: Options = { ...root };
  let node: Options = out;
  for (let i = 0; i < parts.length - 1; i++) {
    const key = parts[i] as string;
    const next = node[key];
    const copy: Options = isRecord(next) ? { ...next } : {};
    node[key] = copy;
    node = copy;
  }
  node[parts[parts.length - 1] as string] = value;
  return out;
}

/** Returns a copy without the path; parents left empty are removed too. */
export function removeAtPath(root: Options, path: string): Options {
  const parts = path.split(".");
  if (parts.some((p) => !p || FORBIDDEN.has(p))) return root;
  const rec = (node: Options, depth: number): Options => {
    const key = parts[depth] as string;
    if (!(key in node)) return node;
    const out: Options = { ...node };
    if (depth === parts.length - 1) {
      delete out[key];
      return out;
    }
    const child = node[key];
    if (!isRecord(child)) return node;
    const next = rec(child, depth + 1);
    if (Object.keys(next).length === 0) delete out[key];
    else out[key] = next;
    return out;
  };
  return rec(root, 0);
}

/** Leaves of an options object as dotted paths (arrays are leaves). */
export function leafPaths(root: unknown, prefix = "", out: { path: string; value: unknown }[] = []): { path: string; value: unknown }[] {
  if (!isRecord(root)) return out;
  for (const [k, v] of Object.entries(root)) {
    const path = prefix ? `${prefix}.${k}` : k;
    if (isRecord(v) && Object.keys(v).length > 0) leafPaths(v, path, out);
    else out.push({ path, value: v });
  }
  return out;
}

// --- native tools ----------------------------------------------------------------

/** Stable signature of a tool payload so enabling twice never duplicates it. */
export function payloadSignature(payload: unknown): string {
  const norm = (v: unknown): unknown => {
    if (Array.isArray(v)) return v.map(norm);
    if (isRecord(v)) {
      const out: Record<string, unknown> = {};
      for (const k of Object.keys(v).sort()) out[k] = norm(v[k]);
      return out;
    }
    return v;
  };
  return JSON.stringify(norm(payload));
}

export function toolsOf(options: Options): unknown[] {
  return Array.isArray(options.tools) ? (options.tools as unknown[]) : [];
}

export function isToolPayloadEnabled(options: Options, payload: Record<string, unknown>): boolean {
  const sig = payloadSignature(payload);
  return toolsOf(options).some((t) => payloadSignature(t) === sig);
}

/** Adds the payload (once) to `options.tools`. */
export function enableToolPayload(options: Options, payload: Record<string, unknown>): Options {
  if (isToolPayloadEnabled(options, payload)) return options;
  return { ...options, tools: [...toolsOf(options), cloneJson(payload)] };
}

/** Removes every matching entry; drops `tools` when it becomes empty. */
export function disableToolPayload(options: Options, payload: Record<string, unknown>): Options {
  const sig = payloadSignature(payload);
  const remaining = toolsOf(options).filter((t) => payloadSignature(t) !== sig);
  if (remaining.length === toolsOf(options).length) return options;
  const out: Options = { ...options };
  if (remaining.length === 0) delete out.tools;
  else out.tools = remaining;
  return out;
}

export function sameJson(a: unknown, b: unknown): boolean {
  return payloadSignature(a) === payloadSignature(b);
}
