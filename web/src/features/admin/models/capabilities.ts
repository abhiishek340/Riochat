/**
 * The capabilities JSON of a model: parsing, the rows the visual editor
 * edits, validation, and writing the rows back while keeping every key the
 * editor does not own.
 */
import { isPlainObject } from "../lib/json";
import { problem, type Problem } from "../lib/problem";
import { isImageProtocol } from "../lib/protocols";

export type Caps = Record<string, unknown>;

export type CapsParse = { ok: true; value: Caps } | { ok: false };

/** Empty text and `{}` mean no capabilities; anything that is not an object is invalid. */
export function parseCaps(text: string | null | undefined): CapsParse {
  const source = (text ?? "").trim();
  if (source === "") return { ok: true, value: {} };
  try {
    const value: unknown = JSON.parse(source);
    return isPlainObject(value) ? { ok: true, value } : { ok: false };
  } catch {
    return { ok: false };
  }
}

/** Empty objects are stored as an empty string. */
export function stringifyCaps(caps: Caps): string {
  return Object.keys(caps).length === 0 ? "" : JSON.stringify(caps);
}

// --- dotted paths ------------------------------------------------------------------------

export function validPath(path: string): boolean {
  const p = path.trim();
  return p !== "" && p.split(".").every((s) => s.trim() !== "");
}

export function getPath(obj: unknown, path: string): unknown {
  let node: unknown = obj;
  for (const part of path.split(".")) {
    if (!isPlainObject(node)) return undefined;
    node = node[part];
  }
  return node;
}

/** Sets a nested value, creating objects on the way; returns a new object. */
export function setPath(obj: Caps, path: string, value: unknown): Caps {
  const parts = path.split(".");
  const root: Caps = { ...obj };
  let node: Caps = root;
  for (let i = 0; i < parts.length - 1; i++) {
    const key = parts[i] as string;
    const next = isPlainObject(node[key]) ? { ...(node[key] as Caps) } : {};
    node[key] = next;
    node = next;
  }
  node[parts[parts.length - 1] as string] = value;
  return root;
}

/** Removes a nested value and any object left empty by it. */
export function unsetPath(obj: Caps, path: string): Caps {
  const parts = path.split(".");
  const root: Caps = { ...obj };
  const trail: Array<[Caps, string]> = [];
  let node: Caps = root;
  for (let i = 0; i < parts.length - 1; i++) {
    const key = parts[i] as string;
    if (!isPlainObject(node[key])) return root;
    const next = { ...(node[key] as Caps) };
    node[key] = next;
    trail.push([node, key]);
    node = next;
  }
  delete node[parts[parts.length - 1] as string];
  for (let i = trail.length - 1; i >= 0; i--) {
    const [parent, key] = trail[i] as [Caps, string];
    if (isPlainObject(parent[key]) && Object.keys(parent[key] as Caps).length === 0) delete parent[key];
    else break;
  }
  return root;
}

/** Text typed as a default value: JSON when it parses, otherwise the string itself. */
export function parseDefaultValue(text: string): unknown {
  const t = text.trim();
  if (t === "") return undefined;
  try {
    return JSON.parse(t);
  } catch {
    return t;
  }
}

export function showDefaultValue(value: unknown): string {
  if (value === undefined) return "";
  return typeof value === "string" ? value : JSON.stringify(value);
}

// --- option controls ------------------------------------------------------------------------

export const CONTROL_TYPES = ["text", "select", "number", "boolean"] as const;
export type ControlType = (typeof CONTROL_TYPES)[number];

export interface ControlRow {
  path: string;
  label: string;
  description: string;
  type: ControlType | "";
  /** Comma-separated options of a select. */
  options: string;
  defaultValue: string;
  locked: boolean;
}

export function splitOptions(text: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of text.split(",")) {
    const v = raw.trim();
    if (v && !seen.has(v)) {
      seen.add(v);
      out.push(v);
    }
  }
  return out;
}

export function controlRows(caps: Caps): ControlRow[] {
  const controls = Array.isArray(caps.optionControls) ? caps.optionControls : [];
  const locked = new Set(Array.isArray(caps.lockedOptionPaths) ? caps.lockedOptionPaths.filter((p): p is string => typeof p === "string") : []);
  const rows: ControlRow[] = [];
  for (const c of controls) {
    if (!isPlainObject(c) || typeof c.path !== "string") continue;
    const type = typeof c.type === "string" && (CONTROL_TYPES as readonly string[]).includes(c.type) ? (c.type as ControlType) : "";
    rows.push({
      path: c.path,
      label: typeof c.label === "string" ? c.label : "",
      description: typeof c.description === "string" ? c.description : "",
      type,
      options: Array.isArray(c.options) ? c.options.filter((o): o is string => typeof o === "string").join(", ") : "",
      defaultValue: showDefaultValue(getPath(caps.defaultOptions, c.path)),
      locked: locked.has(c.path),
    });
    locked.delete(c.path);
  }
  // Locked paths without a control still show up so they can be unlocked.
  for (const path of locked) rows.push({ path, label: "", description: "", type: "", options: "", defaultValue: showDefaultValue(getPath(caps.defaultOptions, path)), locked: true });
  return rows;
}

export type ControlIssue = "path" | "type" | "options" | "locked" | "duplicate";

export function validateControls(rows: readonly ControlRow[]): Array<{ index: number; issue: ControlIssue }> {
  const issues: Array<{ index: number; issue: ControlIssue }> = [];
  const seen = new Set<string>();
  rows.forEach((row, index) => {
    if (!validPath(row.path)) issues.push({ index, issue: "path" });
    else if (seen.has(row.path.trim())) issues.push({ index, issue: "duplicate" });
    seen.add(row.path.trim());
    if (!row.type) issues.push({ index, issue: "type" });
    if (row.type === "select" && splitOptions(row.options).length === 0) issues.push({ index, issue: "options" });
    if (row.locked && row.defaultValue.trim() === "") issues.push({ index, issue: "locked" });
  });
  return issues;
}

/** Writes the rows into `defaultOptions`, `optionControls` and `lockedOptionPaths`. */
export function writeControls(caps: Caps, rows: readonly ControlRow[]): Caps {
  let out: Caps = { ...caps };
  // Paths the editor managed before are cleared from the defaults first.
  const before = controlRows(caps).map((r) => r.path);
  let defaults: Caps = isPlainObject(out.defaultOptions) ? { ...out.defaultOptions } : {};
  for (const path of before) defaults = unsetPath(defaults, path);
  const controls: Array<Record<string, unknown>> = [];
  const locked: string[] = [];
  for (const row of rows) {
    const path = row.path.trim();
    if (!path) continue;
    const value = parseDefaultValue(row.defaultValue);
    if (value !== undefined) defaults = setPath(defaults, path, value);
    if (row.locked) locked.push(path);
    if (row.type) {
      const control: Record<string, unknown> = { path, type: row.type };
      if (row.label.trim()) control.label = row.label.trim();
      if (row.description.trim()) control.description = row.description.trim();
      if (row.type === "select") control.options = splitOptions(row.options);
      controls.push(control);
    }
  }
  if (Object.keys(defaults).length > 0) out.defaultOptions = defaults;
  else delete out.defaultOptions;
  if (controls.length > 0) out.optionControls = controls;
  else delete out.optionControls;
  if (locked.length > 0) out.lockedOptionPaths = locked;
  else delete out.lockedOptionPaths;
  out = { ...out };
  return out;
}

// --- native tools ---------------------------------------------------------------------------------

export interface NativeToolRow {
  key: string;
  protocols: string[];
  type: string;
  label: string;
  description: string;
  provider: string;
  enabled: boolean;
  defaultEnabled: boolean;
  /** JSON object text. */
  payload: string;
}

/** Protocol names that are matched under their tool-catalog spelling. */
export function toolProtocol(protocol: string): string {
  return protocol === "google_generate_content" ? "gemini_generate_content" : protocol;
}

export function nativeRows(caps: Caps): NativeToolRow[] {
  const list = Array.isArray(caps.nativeTools) ? caps.nativeTools : [];
  const rows: NativeToolRow[] = list.flatMap((t): NativeToolRow[] => {
    if (!isPlainObject(t)) return [];
    return [
      {
        key: typeof t.key === "string" ? t.key : "",
        protocols: Array.isArray(t.protocols) ? t.protocols.filter((p): p is string => typeof p === "string") : [],
        type: typeof t.type === "string" ? t.type : "",
        label: typeof t.label === "string" ? t.label : "",
        description: typeof t.description === "string" ? t.description : "",
        provider: typeof t.provider === "string" ? t.provider : "",
        enabled: t.enabled !== false,
        defaultEnabled: t.defaultEnabled === true,
        payload: isPlainObject(t.payload) ? JSON.stringify(t.payload, null, 2) : "{}",
      },
    ];
  });
  // The legacy list of keys becomes rows (migrated on save).
  const legacy = Array.isArray(caps.nativeToolKeys) ? caps.nativeToolKeys.filter((k): k is string => typeof k === "string") : [];
  for (const key of legacy) if (!rows.some((r) => r.key === key)) rows.push({ key, protocols: [], type: "", label: key, description: "", provider: "", enabled: true, defaultEnabled: false, payload: "{}" });
  return rows;
}

export type NativeIssue = "key" | "protocols" | "payload" | "type";

export function validateNative(rows: readonly NativeToolRow[]): Array<{ index: number; issue: NativeIssue }> {
  const issues: Array<{ index: number; issue: NativeIssue }> = [];
  rows.forEach((row, index) => {
    if (!row.enabled) return;
    if (!row.key.trim()) issues.push({ index, issue: "key" });
    if (row.protocols.length === 0) issues.push({ index, issue: "protocols" });
    let payload: unknown;
    try {
      payload = JSON.parse(row.payload || "{}");
    } catch {
      issues.push({ index, issue: "payload" });
      return;
    }
    if (!isPlainObject(payload)) {
      issues.push({ index, issue: "payload" });
      return;
    }
    if (!row.type.trim() && typeof payload.type !== "string") issues.push({ index, issue: "type" });
  });
  return issues;
}

export function writeNative(caps: Caps, rows: readonly NativeToolRow[]): Caps {
  const out: Caps = { ...caps };
  delete out.nativeToolKeys;
  const tools = rows.map((row) => {
    let payload: unknown = {};
    try {
      payload = JSON.parse(row.payload || "{}");
    } catch {
      payload = {};
    }
    const type = row.type.trim() || (isPlainObject(payload) && typeof payload.type === "string" ? payload.type : "");
    const tool: Record<string, unknown> = { key: row.key.trim(), protocols: row.protocols, label: row.label.trim() || row.key.trim(), enabled: row.enabled, defaultEnabled: row.defaultEnabled, payload };
    if (type) tool.type = type;
    if (row.description.trim()) tool.description = row.description.trim();
    if (row.provider.trim()) tool.provider = row.provider.trim();
    return tool;
  });
  if (tools.length > 0) out.nativeTools = tools;
  else delete out.nativeTools;
  return out;
}

/** A catalog entry (from the model-option policy) turned into a row. */
export interface CatalogTool {
  toolKey: string;
  type: string;
  provider: string;
  label: string;
  description: string;
  protocol: string;
  payload: unknown;
  defaultEnabled?: boolean;
}

export function rowFromCatalog(tool: CatalogTool): NativeToolRow {
  return {
    key: tool.toolKey,
    protocols: [tool.protocol].filter(Boolean),
    type: tool.type,
    label: tool.label,
    description: tool.description,
    provider: tool.provider,
    enabled: true,
    defaultEnabled: tool.defaultEnabled === true,
    payload: isPlainObject(tool.payload) ? JSON.stringify(tool.payload, null, 2) : "{}",
  };
}

/** Adds a catalog tool; one that is already present only gains the protocol. */
export function addCatalogTool(rows: readonly NativeToolRow[], tool: CatalogTool): NativeToolRow[] {
  const at = rows.findIndex((r) => r.key === tool.toolKey);
  if (at < 0) return [...rows, rowFromCatalog(tool)];
  const row = rows[at] as NativeToolRow;
  if (!tool.protocol || row.protocols.includes(tool.protocol)) return [...rows];
  return rows.map((r, i) => (i === at ? { ...r, protocols: [...r.protocols, tool.protocol] } : r));
}

/** Catalog tools whose protocol the model's routes use, sorted by label. */
export function catalogForProtocols(catalog: readonly CatalogTool[], routeProtocols: readonly string[]): CatalogTool[] {
  const wanted = new Set(routeProtocols.map(toolProtocol));
  return catalog.filter((t) => wanted.size === 0 || wanted.has(toolProtocol(t.protocol))).sort((a, b) => a.label.localeCompare(b.label));
}

// --- prompt cache and image stream -----------------------------------------------------------------------

export type CacheAvailability = "auto" | "enabled" | "disabled";

export interface PromptCacheDraft {
  availability: CacheAvailability;
  explicit: boolean;
}

export function readPromptCache(caps: Caps): PromptCacheDraft {
  const pc = isPlainObject(caps.promptCache) ? caps.promptCache : {};
  const availability: CacheAvailability = pc.enabled === true ? "enabled" : pc.enabled === false ? "disabled" : "auto";
  return { availability, explicit: pc.mode === "explicit" && availability !== "disabled" };
}

/** Auto omits `enabled`; explicit mode adds mode and ttl; disabled drops mode and ttl; legacy `retention` always goes. */
export function writePromptCache(caps: Caps, draft: PromptCacheDraft): Caps {
  const out: Caps = { ...caps };
  const next: Record<string, unknown> = {};
  if (draft.availability === "enabled") next.enabled = true;
  if (draft.availability === "disabled") next.enabled = false;
  if (draft.availability !== "disabled" && draft.explicit) {
    next.mode = "explicit";
    next.ttl = "30m";
  }
  if (Object.keys(next).length > 0) out.promptCache = next;
  else delete out.promptCache;
  return out;
}

export function cacheEditable(routeProtocols: readonly string[]): boolean {
  return routeProtocols.some((p) => p === "openai_chat_completions" || p === "openai_responses");
}

export function imageToggleShown(routeProtocols: readonly string[]): boolean {
  return routeProtocols.some(isImageProtocol);
}

export function readImageStream(caps: Caps): boolean {
  const image = isPlainObject(caps.image) ? caps.image : {};
  return image.stream !== false;
}

export function writeImageStream(caps: Caps, stream: boolean): Caps {
  const out: Caps = { ...caps };
  const image = isPlainObject(out.image) ? { ...out.image } : {};
  if (stream) delete image.stream;
  else image.stream = false;
  if (Object.keys(image).length > 0) out.image = image;
  else delete out.image;
  return out;
}

export function readModalities(caps: Caps): { mode: "auto" | "custom"; chosen: string[] } {
  const list = caps.inputModalities;
  if (!Array.isArray(list)) return { mode: "auto", chosen: [] };
  return { mode: "custom", chosen: list.filter((m): m is string => typeof m === "string") };
}

export function writeModalities(caps: Caps, mode: "auto" | "custom", chosen: readonly string[]): Caps {
  const out: Caps = { ...caps };
  if (mode === "auto") {
    delete out.inputModalities;
    return out;
  }
  out.inputModalities = ["text", ...["image", "pdf", "audio", "video"].filter((m) => chosen.includes(m))];
  return out;
}

export function writeReasoning(caps: Caps, value: Record<string, unknown> | null): Caps {
  const out: Caps = { ...caps };
  if (value === null) delete out.reasoning;
  else out.reasoning = value;
  return out;
}

export function allIssues(rows: { controls: readonly ControlRow[]; native: readonly NativeToolRow[] }): Problem | null {
  const c = validateControls(rows.controls)[0];
  if (c) return problem(`adminModels.caps.issues.${c.issue}`, { row: c.index + 1 });
  const n = validateNative(rows.native)[0];
  if (n) return problem(`adminModels.caps.nativeIssues.${n.issue}`, { row: n.index + 1 });
  return null;
}
