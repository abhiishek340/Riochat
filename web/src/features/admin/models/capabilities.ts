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
